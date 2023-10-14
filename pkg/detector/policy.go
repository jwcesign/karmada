package detector

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	policyv1alpha1 "github.com/karmada-io/karmada/pkg/apis/policy/v1alpha1"
	workv1alpha2 "github.com/karmada-io/karmada/pkg/apis/work/v1alpha2"
	"github.com/karmada-io/karmada/pkg/events"
	"github.com/karmada-io/karmada/pkg/metrics"
	"github.com/karmada-io/karmada/pkg/util"
	"github.com/karmada-io/karmada/pkg/util/fedinformer/keys"
	"github.com/karmada-io/karmada/pkg/util/helper"
)

func (d *ResourceDetector) propagateResourceWithPropagationPolicy(object *unstructured.Unstructured, objectKey keys.ClusterWideKey) (bool, error) {
	// 1. Check if the object has been claimed by a PropagationPolicy,
	// if so, just apply it.
	policyLabels := object.GetLabels()
	policyAnnotations := object.GetAnnotations()
	claimedNamespaceAnnotation := util.GetAnnotationValue(policyAnnotations, policyv1alpha1.PropagationPolicyNamespaceAnnotation)
	claimedNameAnnotation := util.GetAnnotationValue(policyAnnotations, policyv1alpha1.PropagationPolicyNameAnnotation)
	cliamedIDLabel := util.GetLabelValue(policyLabels, policyv1alpha1.PropagationPolicyIDLabel)
	if claimedNamespaceAnnotation != "" && claimedNameAnnotation != "" && cliamedIDLabel != "" {
		return true, d.getAndApplyPolicy(object, objectKey, cliamedIDLabel, claimedNamespaceAnnotation, claimedNameAnnotation)
	}

	// 2. attempt to match policy in its namespace.
	start := time.Now()
	propagationPolicy, err := d.LookForMatchedPolicy(object, objectKey)
	if err != nil {
		klog.Errorf("Failed to retrieve policy for object: %s, error: %v", objectKey.String(), err)
		return false, err
	}
	if propagationPolicy != nil {
		// return err when dependents not present, that we can retry at next reconcile.
		if present, err := helper.IsDependentOverridesPresent(d.Client, propagationPolicy); err != nil || !present {
			klog.Infof("Waiting for dependent overrides present for policy(%s/%s)", propagationPolicy.Namespace, propagationPolicy.Name)
			return false, fmt.Errorf("waiting for dependent overrides")
		}
		d.RemoveWaiting(objectKey)
		metrics.ObserveFindMatchedPolicyLatency(start)
		return true, d.ApplyPolicy(object, objectKey, propagationPolicy)
	}

	return false, nil
}

func (d *ResourceDetector) propagateResourceWithClusterPropagationPolicy(object *unstructured.Unstructured, objectKey keys.ClusterWideKey) (bool, error) {
	// 1. Check if the object has been claimed by a ClusterPropagationPolicy,
	// if so, just apply it.
	policyLabels := object.GetLabels()
	policyAnnotations := object.GetAnnotations()
	claimedNameAnnotation := util.GetAnnotationValue(policyAnnotations, policyv1alpha1.ClusterPropagationPolicyAnnotation)
	claimedIDLabel := util.GetLabelValue(policyLabels, policyv1alpha1.ClusterPropagationPolicyIDLabel)
	if claimedNameAnnotation != "" && claimedIDLabel != "" {
		return true, d.getAndApplyClusterPolicy(object, objectKey, claimedIDLabel, claimedNameAnnotation)
	}

	// 2. reaching here means there is no appropriate PropagationPolicy, attempt to match a ClusterPropagationPolicy.
	start := time.Now()
	clusterPolicy, err := d.LookForMatchedClusterPolicy(object, objectKey)
	if err != nil {
		klog.Errorf("Failed to retrieve cluster policy for object: %s, error: %v", objectKey.String(), err)
		return false, err
	}
	if clusterPolicy != nil {
		// return err when dependents not present, that we can retry at next reconcile.
		if present, err := helper.IsDependentClusterOverridesPresent(d.Client, clusterPolicy); err != nil || !present {
			klog.Infof("Waiting for dependent overrides present for policy(%s)", clusterPolicy.Name)
			return false, fmt.Errorf("waiting for dependent overrides")
		}
		d.RemoveWaiting(objectKey)
		metrics.ObserveFindMatchedPolicyLatency(start)
		return true, d.ApplyClusterPolicy(object, objectKey, clusterPolicy)
	}

	return false, nil
}

func (d *ResourceDetector) propagateResource(object *unstructured.Unstructured, objectKey keys.ClusterWideKey) error {
	ok, err := d.propagateResourceWithPropagationPolicy(object, objectKey)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}

	ok, err = d.propagateResourceWithClusterPropagationPolicy(object, objectKey)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}

	if d.isWaiting(objectKey) {
		// reaching here means there is no appropriate policy for the object
		d.EventRecorder.Event(object, corev1.EventTypeWarning, events.EventReasonApplyPolicyFailed, "No policy match for resource")
		return nil
	}

	// put it into waiting list and retry once in case the resource and propagation policy come at the same time
	// see https://github.com/karmada-io/karmada/issues/1195
	d.AddWaiting(objectKey)
	return fmt.Errorf("no matched propagation policy")
}

func (d *ResourceDetector) getAndApplyPolicy(object *unstructured.Unstructured, objectKey keys.ClusterWideKey,
	policyID, policyNamespace, policyName string) error {
	policyObject, err := d.propagationPolicyLister.ByNamespace(policyNamespace).Get(policyName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.V(4).Infof("PropagationPolicy(%s/%s) has been removed.", policyNamespace, policyName)
			return d.HandlePropagationPolicyDeletion(policyID, policyNamespace, policyName)
		}
		klog.Errorf("Failed to get claimed policy(%s/%s),: %v", policyNamespace, policyName, err)
		return err
	}

	matchedPropagationPolicy := &policyv1alpha1.PropagationPolicy{}
	if err = helper.ConvertToTypedObject(policyObject, matchedPropagationPolicy); err != nil {
		klog.Errorf("Failed to convert PropagationPolicy from unstructured object: %v", err)
		return err
	}

	// Some resources are available in more than one group in the same kubernetes version.
	// Therefore, the following scenarios occurs:
	// In v1.21 kubernetes cluster, Ingress are available in both networking.k8s.io and extensions groups.
	// When user creates an Ingress(networking.k8s.io/v1) and specifies a PropagationPolicy to propagate it
	// to the member clusters, the detector will listen two resource creation events:
	// Ingress(networking.k8s.io/v1) and Ingress(extensions/v1beta1). In order to prevent
	// Ingress(extensions/v1beta1) from being propagated, we need to ignore it.
	if !util.ResourceMatchSelectors(object, matchedPropagationPolicy.Spec.ResourceSelectors...) {
		return nil
	}

	// return err when dependents not present, that we can retry at next reconcile.
	if present, err := helper.IsDependentOverridesPresent(d.Client, matchedPropagationPolicy); err != nil || !present {
		klog.Infof("Waiting for dependent overrides present for policy(%s/%s)", policyNamespace, policyName)
		return fmt.Errorf("waiting for dependent overrides")
	}

	return d.ApplyPolicy(object, objectKey, matchedPropagationPolicy)
}

func (d *ResourceDetector) getAndApplyClusterPolicy(object *unstructured.Unstructured, objectKey keys.ClusterWideKey,
	policyUID, policyName string) error {
	policyObject, err := d.clusterPropagationPolicyLister.Get(policyName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.V(4).Infof("ClusterPropagationPolicy(%s) has been removed.", policyName)
			return d.HandleClusterPropagationPolicyDeletion(policyUID, policyName)
		}

		klog.Errorf("Failed to get claimed policy(%s),: %v", policyName, err)
		return err
	}

	matchedClusterPropagationPolicy := &policyv1alpha1.ClusterPropagationPolicy{}
	if err = helper.ConvertToTypedObject(policyObject, matchedClusterPropagationPolicy); err != nil {
		klog.Errorf("Failed to convert ClusterPropagationPolicy from unstructured object: %v", err)
		return err
	}

	// Some resources are available in more than one group in the same kubernetes version.
	// Therefore, the following scenarios occurs:
	// In v1.21 kubernetes cluster, Ingress are available in both networking.k8s.io and extensions groups.
	// When user creates an Ingress(networking.k8s.io/v1) and specifies a ClusterPropagationPolicy to
	// propagate it to the member clusters, the detector will listen two resource creation events:
	// Ingress(networking.k8s.io/v1) and Ingress(extensions/v1beta1). In order to prevent
	// Ingress(extensions/v1beta1) from being propagated, we need to ignore it.
	if !util.ResourceMatchSelectors(object, matchedClusterPropagationPolicy.Spec.ResourceSelectors...) {
		return nil
	}

	// return err when dependents not present, that we can retry at next reconcile.
	if present, err := helper.IsDependentClusterOverridesPresent(d.Client, matchedClusterPropagationPolicy); err != nil || !present {
		klog.Infof("Waiting for dependent overrides present for policy(%s)", policyName)
		return fmt.Errorf("waiting for dependent overrides")
	}

	return d.ApplyClusterPolicy(object, objectKey, matchedClusterPropagationPolicy)
}

func (d *ResourceDetector) cleanPPUnmatchedResourceBindings(policyObejectMeta *metav1.ObjectMeta, selectors []policyv1alpha1.ResourceSelector) error {
	bindings, err := d.listPPDerivedRB(policyObejectMeta)
	if err != nil {
		return err
	}

	removeLabels := []string{
		policyv1alpha1.PropagationPolicyIDLabel,
	}
	removeAnnotations := []string{
		policyv1alpha1.PropagationPolicyNameAnnotation,
		policyv1alpha1.PropagationPolicyNamespaceAnnotation,
	}
	return d.removeResourceBindingsLabels(bindings, selectors, removeLabels, removeAnnotations)
}

func (d *ResourceDetector) cleanCPPUnmatchedResourceBindings(policy *policyv1alpha1.ClusterPropagationPolicy) error {
	bindings, err := d.listCPPDerivedRB(string(policy.UID), policy.Name)
	if err != nil {
		return err
	}

	removeLabels := []string{
		policyv1alpha1.ClusterPropagationPolicyIDLabel,
	}
	removeAnnotations := []string{
		policyv1alpha1.ClusterPropagationPolicyAnnotation,
	}
	return d.removeResourceBindingsLabels(bindings, policy.Spec.ResourceSelectors, removeLabels, removeAnnotations)
}

func (d *ResourceDetector) cleanUnmatchedClusterResourceBinding(policy *policyv1alpha1.ClusterPropagationPolicy) error {
	bindings, err := d.listCPPDerivedCRB(string(policy.UID), policy.Name)
	if err != nil {
		return err
	}

	return d.removeClusterResourceBindingsLabels(bindings, policy.Spec.ResourceSelectors)
}

func (d *ResourceDetector) removeResourceBindingsLabels(bindings *workv1alpha2.ResourceBindingList, selectors []policyv1alpha1.ResourceSelector, removeLabels, removeAnnotations []string) error {
	var errs []error
	for _, binding := range bindings.Items {
		removed, err := d.removeResourceLabelsAnnotationsIfNotMatch(binding.Spec.Resource, selectors, removeLabels, removeAnnotations)
		if err != nil {
			klog.Errorf("Failed to remove resource labels when resource not match with policy selectors, err: %v", err)
			errs = append(errs, err)
			continue
		}
		if !removed {
			continue
		}

		bindingCopy := binding.DeepCopy()
		for _, l := range removeLabels {
			delete(bindingCopy.Labels, l)
		}
		for _, a := range removeAnnotations {
			delete(bindingCopy.Annotations, a)
		}
		err = d.Client.Update(context.TODO(), bindingCopy)
		if err != nil {
			klog.Errorf("Failed to update resourceBinding(%s/%s), err: %v", binding.Namespace, binding.Name, err)
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.NewAggregate(errs)
	}

	return nil
}

func (d *ResourceDetector) removeClusterResourceBindingsLabels(bindings *workv1alpha2.ClusterResourceBindingList, selectors []policyv1alpha1.ResourceSelector) error {
	var errs []error
	for _, binding := range bindings.Items {
		removed, err := d.removeResourceLabelsAnnotationsIfNotMatch(binding.Spec.Resource, selectors,
			[]string{policyv1alpha1.ClusterPropagationPolicyIDLabel}, []string{policyv1alpha1.ClusterPropagationPolicyAnnotation})
		if err != nil {
			klog.Errorf("Failed to remove resource labels when resource not match with policy selectors, err: %v", err)
			errs = append(errs, err)
			continue
		}
		if !removed {
			continue
		}

		bindingCopy := binding.DeepCopy()
		delete(bindingCopy.Labels, policyv1alpha1.ClusterPropagationPolicyIDLabel)
		delete(bindingCopy.Annotations, policyv1alpha1.ClusterPropagationPolicyAnnotation)
		err = d.Client.Update(context.TODO(), bindingCopy)
		if err != nil {
			klog.Errorf("Failed to update clusterResourceBinding(%s), err: %v", binding.Name, err)
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.NewAggregate(errs)
	}
	return nil
}

func (d *ResourceDetector) removeResourceLabelsAnnotationsIfNotMatch(objectReference workv1alpha2.ObjectReference, selectors []policyv1alpha1.ResourceSelector, labelKeys, annotationKeys []string) (bool, error) {
	objectKey, err := helper.ConstructClusterWideKey(objectReference)
	if err != nil {
		return false, err
	}

	object, err := d.GetUnstructuredObject(objectKey)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	if util.ResourceMatchSelectors(object, selectors...) {
		return false, nil
	}

	object = object.DeepCopy()
	util.RemoveLabels(object, labelKeys...)
	util.RemoveAnnotations(object, annotationKeys...)

	err = d.Client.Update(context.TODO(), object)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (d *ResourceDetector) listPPDerivedRB(policyObjectMeta *metav1.ObjectMeta) (*workv1alpha2.ResourceBindingList, error) {
	bindings := &workv1alpha2.ResourceBindingList{}
	policyID := util.GetLabelValue(policyObjectMeta.GetLabels(), policyv1alpha1.PropagationPolicyIDLabel)
	if policyID == "" {
		return bindings, nil
	}

	listOpt := &client.ListOptions{
		Namespace: policyObjectMeta.Namespace,
		LabelSelector: labels.SelectorFromSet(labels.Set{
			policyv1alpha1.PropagationPolicyIDLabel: policyID,
		}),
	}
	err := d.Client.List(context.TODO(), bindings, listOpt)
	if err != nil {
		klog.Errorf("Failed to list ResourceBinding with policy(%s/%s), error: %v", policyObjectMeta.Namespace, policyObjectMeta.Name, err)
		return nil, err
	}

	return bindings, nil
}

func (d *ResourceDetector) listCPPDerivedRB(policyID, policyName string) (*workv1alpha2.ResourceBindingList, error) {
	bindings := &workv1alpha2.ResourceBindingList{}
	listOpt := &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{
			policyv1alpha1.ClusterPropagationPolicyIDLabel: policyID,
		})}
	err := d.Client.List(context.TODO(), bindings, listOpt)
	if err != nil {
		klog.Errorf("Failed to list ResourceBinding with policy(%s), error: %v", policyName, err)
		return nil, err
	}

	return bindings, nil
}

func (d *ResourceDetector) listCPPDerivedCRB(policyID, policyName string) (*workv1alpha2.ClusterResourceBindingList, error) {
	bindings := &workv1alpha2.ClusterResourceBindingList{}
	listOpt := &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{
			policyv1alpha1.ClusterPropagationPolicyIDLabel: policyID,
		})}
	err := d.Client.List(context.TODO(), bindings, listOpt)
	if err != nil {
		klog.Errorf("Failed to list ClusterResourceBinding with policy(%s), error: %v", policyName, err)
		return nil, err
	}

	return bindings, nil
}

// excludeClusterPolicy excludes cluster propagation policy.
// If propagation policy was claimed, cluster propagation policy should not exists.
func excludeClusterPolicy(objLabels map[string]string) bool {
	if _, ok := objLabels[policyv1alpha1.ClusterPropagationPolicyIDLabel]; !ok {
		return false
	}
	delete(objLabels, policyv1alpha1.ClusterPropagationPolicyIDLabel)
	return true
}
