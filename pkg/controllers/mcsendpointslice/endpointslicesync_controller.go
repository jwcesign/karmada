/*
Copyright 2023 The Karmada Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package mcsendpointslice

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	networkingv1alpha1 "github.com/karmada-io/karmada/pkg/apis/networking/v1alpha1"
	workv1alpha1 "github.com/karmada-io/karmada/pkg/apis/work/v1alpha1"
	"github.com/karmada-io/karmada/pkg/events"
	"github.com/karmada-io/karmada/pkg/util"
	"github.com/karmada-io/karmada/pkg/util/fedinformer/genericmanager"
	"github.com/karmada-io/karmada/pkg/util/helper"
	"github.com/karmada-io/karmada/pkg/util/names"
)

// EndpointsliceSyncControllerName is the controller name that will be used when reporting events.
const EndpointsliceSyncControllerName = "endpointslice-sync-controller"

type EndpointsliceSyncController struct {
	client.Client
	EventRecorder   record.EventRecorder
	RESTMapper      meta.RESTMapper
	InformerManager genericmanager.MultiClusterInformerManager
}

// Reconcile performs a full reconciliation for the object referred to by the Request.
func (c *EndpointsliceSyncController) Reconcile(ctx context.Context, req controllerruntime.Request) (controllerruntime.Result, error) {
	klog.V(4).Infof("Reconciling Work %s", req.NamespacedName.String())

	work := &workv1alpha1.Work{}
	if err := c.Client.Get(ctx, req.NamespacedName, work); err != nil {
		if apierrors.IsNotFound(err) {
			return controllerruntime.Result{}, nil
		}
		return controllerruntime.Result{Requeue: true}, err
	}

	if !work.DeletionTimestamp.IsZero() {
		if err := c.cleanupEndpointSliceFromConsumerClusters(ctx, work); err != nil {
			klog.Errorf("Failed to cleanup EndpointSlice from consumer clusters for work %s/%s", work.Namespace, work.Name)
			return controllerruntime.Result{Requeue: true}, err
		}
		return controllerruntime.Result{}, nil
	}

	mcsName := util.GetLabelValue(work.Labels, util.ServiceNameLabel)
	mcsNS := util.GetLabelValue(work.Labels, util.ServiceNamespaceLabel)
	mcs := &networkingv1alpha1.MultiClusterService{}
	if err := c.Client.Get(ctx, types.NamespacedName{Namespace: mcsNS, Name: mcsName}, mcs); err != nil {
		if apierrors.IsNotFound(err) {
			klog.Warningf("MultiClusterService %s/%s is not found", mcsNS, mcsName)
			return controllerruntime.Result{}, nil
		}
		return controllerruntime.Result{Requeue: true}, err
	}

	var err error
	defer func() {
		if err != nil {
			_ = c.updateEndpointSliceSynced(mcs, metav1.ConditionFalse, "EndpointSliceSyncFailed", err.Error())
			c.EventRecorder.Eventf(mcs, corev1.EventTypeWarning, events.EventReasonScheduleBindingFailed, err.Error())
		}
		_ = c.updateEndpointSliceSynced(mcs, metav1.ConditionFalse, "EndpointSliceSyncSucceed", "EndpointSlice are synced successfully")
		c.EventRecorder.Eventf(mcs, corev1.EventTypeWarning, events.EventReasonScheduleBindingSucceed, "EndpointSlice are synced successfully")
	}()

	// TDB: When cluster range changes in mcs, we should delete/create the corresponding work
	if err = c.syncEndpointSlice(ctx, work.DeepCopy(), mcs); err != nil {
		return controllerruntime.Result{Requeue: true}, err
	}

	return controllerruntime.Result{}, nil
}

func (c *EndpointsliceSyncController) updateEndpointSliceSynced(mcs *networkingv1alpha1.MultiClusterService, status metav1.ConditionStatus, reason, message string) error {
	EndpointSliceCollected := metav1.Condition{
		Type:               workv1alpha1.WorkApplied,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() (err error) {
		meta.SetStatusCondition(&mcs.Status.Conditions, EndpointSliceCollected)
		updateErr := c.Status().Update(context.TODO(), mcs)
		if updateErr == nil {
			return nil
		}
		updated := &networkingv1alpha1.MultiClusterService{}
		if err = c.Get(context.TODO(), client.ObjectKey{Namespace: mcs.Namespace, Name: mcs.Name}, updated); err == nil {
			mcs = updated
		} else {
			klog.Errorf("Failed to get updated MultiClusterService %s/%s: %v", mcs.Namespace, mcs.Name, err)
		}
		return updateErr
	})
}

// SetupWithManager creates a controller and register to controller manager.
func (c *EndpointsliceSyncController) SetupWithManager(mgr controllerruntime.Manager) error {
	workPredicateFun := predicate.Funcs{
		CreateFunc: func(createEvent event.CreateEvent) bool {
			return util.GetLabelValue(createEvent.Object.GetLabels(), util.ServiceNameLabel) != ""
		},
		UpdateFunc: func(updateEvent event.UpdateEvent) bool {
			return util.GetLabelValue(updateEvent.ObjectNew.GetLabels(), util.ServiceNameLabel) != ""
		},
		DeleteFunc: func(deleteEvent event.DeleteEvent) bool {
			return util.GetLabelValue(deleteEvent.Object.GetLabels(), util.ServiceNameLabel) != ""
		},
		GenericFunc: func(genericEvent event.GenericEvent) bool {
			return false
		},
	}
	return controllerruntime.NewControllerManagedBy(mgr).For(&workv1alpha1.Work{}, builder.WithPredicates(workPredicateFun)).Complete(c)
}

func (c *EndpointsliceSyncController) syncEndpointSlice(ctx context.Context, work *workv1alpha1.Work, mcs *networkingv1alpha1.MultiClusterService) error {
	epsSourceCluster, err := names.GetClusterName(work.Namespace)
	if err != nil {
		klog.Errorf("Failed to get EndpointSlice source cluster name for work %s/%s", work.Namespace, work.Name)
		return err
	}

	consumptionClusters := sets.New[string](mcs.Spec.ServiceConsumptionClusters...)
	if len(consumptionClusters) == 0 {
		consumptionClusters, err = util.GetClusterSet(c.Client)
		if err != nil {
			klog.Errorf("Failed to get cluster set, error is: %v", err)
			return err
		}
	}
	for clusterName := range consumptionClusters {
		if clusterName == epsSourceCluster {
			continue
		}

		// It couldn't happen here
		if len(work.Spec.Workload.Manifests) == 0 {
			continue
		}

		// There should be only one manifest in the work, let's use the first one.
		manifest := work.Spec.Workload.Manifests[0]
		unstructuredObj := &unstructured.Unstructured{}
		if err := unstructuredObj.UnmarshalJSON(manifest.Raw); err != nil {
			klog.Errorf("Failed to unmarshal work manifest, error is: %v", err)
			return err
		}

		endpointSlice := &discoveryv1.EndpointSlice{}
		if err := helper.ConvertToTypedObject(unstructuredObj, endpointSlice); err != nil {
			klog.Errorf("Failed to convert unstructured object to typed object, error is: %v", err)
			return err
		}

		// Use this name to avoid naming conflicts and locate the EPS source cluster.
		endpointSlice.Name = epsSourceCluster + "-" + endpointSlice.Name
		clusterNamespace := names.GenerateExecutionSpaceName(clusterName)
		endpointSlice.Labels = map[string]string{
			discoveryv1.LabelServiceName:    mcs.Name,
			workv1alpha1.WorkNamespaceLabel: clusterNamespace,
			workv1alpha1.WorkNameLabel:      work.Name,
			util.ManagedByKarmadaLabel:      util.ManagedByKarmadaLabelValue,
			discoveryv1.LabelManagedBy:      util.EndpointSliceControllerLabelValue,
		}

		workMeta := metav1.ObjectMeta{
			Name:       work.Name,
			Namespace:  clusterNamespace,
			Finalizers: []string{util.ExecutionControllerFinalizer},
			Annotations: map[string]string{
				util.EndpointSliceProvisionClusterLabel: epsSourceCluster,
			},
			Labels: map[string]string{
				util.ManagedByKarmadaLabel: util.ManagedByKarmadaLabelValue,
			},
		}
		unstructuredEPS, err := helper.ToUnstructured(endpointSlice)
		if err != nil {
			klog.Errorf("Failed to convert typed object to unstructured object, error is: %v", err)
			return err
		}
		if err := helper.CreateOrUpdateWork(c.Client, workMeta, unstructuredEPS); err != nil {
			klog.Errorf("Failed to sync EndpointSlice %s/%s from %s to cluster %s:%v",
				work.GetNamespace(), work.GetName(), epsSourceCluster, clusterName, err)
			return err
		}
	}

	if !controllerutil.ContainsFinalizer(work, util.MCSEndpointSliceSyncControllerFinalizer) {
		controllerutil.AddFinalizer(work, util.MCSEndpointSliceSyncControllerFinalizer)
		if err := c.Client.Update(ctx, work); err != nil {
			klog.Errorf("Failed to add finalizer %s for work %s/%s, error: %v", util.MCSEndpointSliceSyncControllerFinalizer, work.Namespace, work.Name, err)
			return err
		}
	}

	return nil
}

func (c *EndpointsliceSyncController) cleanupEndpointSliceFromConsumerClusters(ctx context.Context, work *workv1alpha1.Work) error {
	// TBD: There may be a better way without listing all works.
	workList := &workv1alpha1.WorkList{}
	err := c.Client.List(ctx, workList)
	if err != nil {
		klog.Errorf("Failed to list works serror: %v", err)
		return err
	}

	epsSourceCluster, err := names.GetClusterName(work.Namespace)
	if err != nil {
		klog.Errorf("Failed to get EndpointSlice provision cluster name for work %s/%s", work.Namespace, work.Name)
		return err
	}
	for _, item := range workList.Items {
		if item.Name != work.Name || util.GetAnnotationValue(item.Annotations, util.EndpointSliceProvisionClusterLabel) != epsSourceCluster {
			continue
		}
		if err := c.Client.Delete(ctx, item.DeepCopy()); err != nil {
			return err
		}
	}

	if controllerutil.ContainsFinalizer(work, util.MCSEndpointSliceSyncControllerFinalizer) {
		controllerutil.RemoveFinalizer(work, util.MCSEndpointSliceSyncControllerFinalizer)
		if err := c.Client.Update(ctx, work); err != nil {
			klog.Errorf("Failed to remove %s for work %s/%s, error: %v", util.MCSEndpointSliceSyncControllerFinalizer, work.Namespace, work.Name, err)
			return err
		}
	}

	return nil
}
