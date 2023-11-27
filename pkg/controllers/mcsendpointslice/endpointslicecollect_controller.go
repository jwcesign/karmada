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
	"fmt"
	"reflect"
	"sync"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	clusterv1alpha1 "github.com/karmada-io/karmada/pkg/apis/cluster/v1alpha1"
	networkingv1alpha1 "github.com/karmada-io/karmada/pkg/apis/networking/v1alpha1"
	workv1alpha1 "github.com/karmada-io/karmada/pkg/apis/work/v1alpha1"
	"github.com/karmada-io/karmada/pkg/events"
	"github.com/karmada-io/karmada/pkg/util"
	"github.com/karmada-io/karmada/pkg/util/fedinformer"
	"github.com/karmada-io/karmada/pkg/util/fedinformer/genericmanager"
	"github.com/karmada-io/karmada/pkg/util/fedinformer/keys"
	"github.com/karmada-io/karmada/pkg/util/helper"
	"github.com/karmada-io/karmada/pkg/util/names"
)

// EndpointSliceCollectControllerName is the controller name that will be used when reporting events.
const EndpointSliceCollectControllerName = "endpointslice-collect-controller"

type EndpointSliceCollectController struct {
	client.Client
	EventRecorder               record.EventRecorder
	RESTMapper                  meta.RESTMapper
	StopChan                    <-chan struct{}
	InformerManager             genericmanager.MultiClusterInformerManager
	WorkerNumber                int // WorkerNumber is the number of worker goroutines
	ClusterDynamicClientSetFunc func(clusterName string, client client.Client) (*util.DynamicClusterClient, error)
	// eventHandlers holds the handlers which used to handle events reported from member clusters.
	// Each handler takes the cluster name as key and takes the handler function as the value, e.g.
	// "member1": instance of ResourceEventHandler
	eventHandlers sync.Map
	worker        util.AsyncWorker // worker process resources periodic from rateLimitingQueue.

	ClusterCacheSyncTimeout metav1.Duration
}

var (
	endpointSliceGVR = discoveryv1.SchemeGroupVersion.WithResource("endpointslices")
)

// Reconcile performs a full reconciliation for the object referred to by the Request.
func (c *EndpointSliceCollectController) Reconcile(ctx context.Context, req controllerruntime.Request) (controllerruntime.Result, error) {
	klog.V(4).Infof("Reconciling MultiClusterService %s", req.NamespacedName.String())

	mcs := &networkingv1alpha1.MultiClusterService{}
	if err := c.Client.Get(ctx, req.NamespacedName, mcs); err != nil {
		if apierrors.IsNotFound(err) {
			return controllerruntime.Result{}, nil
		}
		return controllerruntime.Result{Requeue: true}, err
	}

	if !helper.IsServiceApplied(&mcs.Status) {
		return controllerruntime.Result{}, nil
	}

	if !mcs.DeletionTimestamp.IsZero() {
		if err := c.cleanworkWithMCSDelete(mcs); err != nil {
			return controllerruntime.Result{Requeue: true}, err
		}
		return controllerruntime.Result{}, nil
	}

	var err error
	defer func() {
		if err != nil {
			_ = c.updateEndpointSliceCollected(mcs, metav1.ConditionFalse, "EndpointSliceCollectedFailed", err.Error())
			c.EventRecorder.Eventf(mcs, corev1.EventTypeWarning, events.EventReasonCollectEndpointSliceFailed, err.Error())
			return
		}
		_ = c.updateEndpointSliceCollected(mcs, metav1.ConditionTrue, "EndpointSliceCollectedSucceed", "EndpointSlice are collected successfully")
		c.EventRecorder.Eventf(mcs, corev1.EventTypeNormal, events.EventReasonCollectEndpointSliceSucceed, "EndpointSlice are collected successfully")
	}()

	// TDB: When cluster range changes in mcs, we should delete/create the corresponding work
	if err = c.buildResourceInformers(ctx, mcs); err != nil {
		return controllerruntime.Result{Requeue: true}, err
	}

	if err = c.cleanorphanEndpointSliceWork(ctx, mcs); err != nil {
		return controllerruntime.Result{Requeue: true}, err
	}

	if err = c.collectTargetEndpointSlice(mcs); err != nil {
		return controllerruntime.Result{Requeue: true}, err
	}

	return controllerruntime.Result{}, nil
}

// SetupWithManager creates a controller and register to controller manager.
func (c *EndpointSliceCollectController) SetupWithManager(mgr controllerruntime.Manager) error {
	return controllerruntime.NewControllerManagedBy(mgr).For(&networkingv1alpha1.MultiClusterService{}).Complete(c)
}

func (c *EndpointSliceCollectController) cleanworkWithMCSDelete(mcs *networkingv1alpha1.MultiClusterService) error {
	for _, clusterName := range mcs.Spec.ServiceProvisionClusters {
		executionSpace := names.GenerateExecutionSpaceName(clusterName)

		workList := &workv1alpha1.WorkList{}
		if err := c.List(context.TODO(), workList, &client.ListOptions{
			Namespace: executionSpace,
			LabelSelector: labels.SelectorFromSet(labels.Set{
				networkingv1alpha1.MultiClusterServicePermanentIDLabel: util.GetLabelValue(mcs.Labels, networkingv1alpha1.MultiClusterServicePermanentIDLabel),
			}),
		}); err != nil {
			klog.Errorf("Failed to list workList reported by MultiClusterService(%s/%s) in executionSpace(%s), Error: %v",
				mcs.Namespace, mcs.Name, executionSpace, err)
			return err
		}

		var errs []error
		for _, work := range workList.Items {
			if err := c.Delete(context.TODO(), work.DeepCopy()); err != nil {
				klog.Errorf("Failed to delete work(%s/%s), Error: %v", work.Namespace, work.Name, err)
				errs = append(errs, err)
			}
		}
		if len(errs) != 0 {
			return utilerrors.NewAggregate(errs)
		}
	}

	if controllerutil.ContainsFinalizer(mcs, util.MCSEndpointSliceCollectControllerFinalizer) {
		controllerutil.RemoveFinalizer(mcs, util.MCSEndpointSliceCollectControllerFinalizer)
		if err := c.Client.Update(context.Background(), mcs); err != nil {
			klog.Errorf("Failed to remove finalizer %s for mcs %s/%s, error: %v",
				util.MCSEndpointSliceCollectControllerFinalizer, mcs.Namespace, mcs.Name, err)
			return err
		}
	}

	return nil
}

// RunWorkQueue initializes worker and run it, worker will process resource asynchronously.
func (c *EndpointSliceCollectController) RunWorkQueue() {
	workerOptions := util.Options{
		Name:          "endpointslice-collect",
		KeyFunc:       nil,
		ReconcileFunc: c.syncEndpointSlice,
	}
	c.worker = util.NewAsyncWorker(workerOptions)
	c.worker.Run(c.WorkerNumber, c.StopChan)
}

func (c *EndpointSliceCollectController) syncEndpointSlice(key util.QueueKey) error {
	fedKey, ok := key.(keys.FederatedKey)
	if !ok {
		klog.Errorf("Failed to sync endpointslice as invalid key: %v", key)
		return fmt.Errorf("invalid key")
	}

	klog.V(4).Infof("Begin to sync %s %s.", fedKey.Kind, fedKey.NamespaceKey())
	if err := c.handleEndpointSliceEvent(fedKey); err != nil {
		klog.Errorf("Failed to handle endpointSlice(%s) event, Error: %v",
			fedKey.NamespaceKey(), err)
		return err
	}

	return nil
}

func (c *EndpointSliceCollectController) buildResourceInformers(ctx context.Context, mcs *networkingv1alpha1.MultiClusterService) error {
	provisionClusters := sets.New[string](mcs.Spec.ServiceProvisionClusters...)
	if len(provisionClusters) == 0 {
		var err error
		provisionClusters, err = util.GetClusterSet(c.Client)
		if err != nil {
			klog.Errorf("Failed to get cluster set for MultiClusterService(%s/%s), Error: %v", mcs.Namespace, mcs.Name, err)
			return err
		}
	}
	for clusterName := range provisionClusters {
		cluster, err := util.GetCluster(c.Client, clusterName)
		if err != nil {
			klog.Errorf("Failed to get the given member cluster %s", clusterName)
			return err
		}

		if !util.IsClusterReady(&cluster.Status) {
			klog.Errorf("Stop sync mcs(%s/%s) for cluster(%s) as cluster not ready.", mcs.Namespace, mcs.Name, cluster.Name)
			return fmt.Errorf("cluster(%s) not ready", cluster.Name)
		}

		if err := c.registerInformersAndStart(cluster); err != nil {
			klog.Errorf("Failed to register informer for Cluster %s. Error: %v.", cluster.Name, err)
			return err
		}
	}

	if !controllerutil.ContainsFinalizer(mcs, util.MCSEndpointSliceCollectControllerFinalizer) {
		controllerutil.AddFinalizer(mcs, util.MCSEndpointSliceCollectControllerFinalizer)
		if err := c.Client.Update(ctx, mcs); err != nil {
			klog.Errorf("Failed to add finalizer %s for mcs %s/%s, error: %v", util.MCSEndpointSliceCollectControllerFinalizer, mcs.Namespace, mcs.Name, err)
			return err
		}
	}

	return nil
}

// registerInformersAndStart builds informer manager for cluster if it doesn't exist, then constructs informers for gvr
// and start it.
func (c *EndpointSliceCollectController) registerInformersAndStart(cluster *clusterv1alpha1.Cluster) error {
	singleClusterInformerManager := c.InformerManager.GetSingleClusterManager(cluster.Name)
	if singleClusterInformerManager == nil {
		dynamicClusterClient, err := c.ClusterDynamicClientSetFunc(cluster.Name, c.Client)
		if err != nil {
			klog.Errorf("Failed to build dynamic cluster client for cluster %s.", cluster.Name)
			return err
		}
		singleClusterInformerManager = c.InformerManager.ForCluster(dynamicClusterClient.ClusterName, dynamicClusterClient.DynamicClientSet, 0)
	}

	gvrTargets := []schema.GroupVersionResource{
		endpointSliceGVR,
	}

	allSynced := true
	for _, gvr := range gvrTargets {
		if !singleClusterInformerManager.IsInformerSynced(gvr) || !singleClusterInformerManager.IsHandlerExist(gvr, c.getEventHandler(cluster.Name)) {
			allSynced = false
			singleClusterInformerManager.ForResource(gvr, c.getEventHandler(cluster.Name))
		}
	}
	if allSynced {
		return nil
	}

	c.InformerManager.Start(cluster.Name)

	if err := func() error {
		synced := c.InformerManager.WaitForCacheSyncWithTimeout(cluster.Name, c.ClusterCacheSyncTimeout.Duration)
		if synced == nil {
			return fmt.Errorf("no informerFactory for cluster %s exist", cluster.Name)
		}
		for _, gvr := range gvrTargets {
			if !synced[gvr] {
				return fmt.Errorf("informer for %s hasn't synced", gvr)
			}
		}
		return nil
	}(); err != nil {
		klog.Errorf("Failed to sync cache for cluster: %s, error: %v", cluster.Name, err)
		c.InformerManager.Stop(cluster.Name)
		return err
	}

	return nil
}

// getEventHandler return callback function that knows how to handle events from the member cluster.
func (c *EndpointSliceCollectController) getEventHandler(clusterName string) cache.ResourceEventHandler {
	if value, exists := c.eventHandlers.Load(clusterName); exists {
		return value.(cache.ResourceEventHandler)
	}

	eventHandler := fedinformer.NewHandlerOnEvents(c.genHandlerAddFunc(clusterName), c.genHandlerUpdateFunc(clusterName),
		c.genHandlerDeleteFunc(clusterName))
	c.eventHandlers.Store(clusterName, eventHandler)
	return eventHandler
}

func (c *EndpointSliceCollectController) genHandlerAddFunc(clusterName string) func(obj interface{}) {
	return func(obj interface{}) {
		curObj := obj.(runtime.Object)
		key, err := keys.FederatedKeyFunc(clusterName, curObj)
		if err != nil {
			klog.Warningf("Failed to generate key for obj: %s", curObj.GetObjectKind().GroupVersionKind())
			return
		}
		c.worker.Add(key)
	}
}

func (c *EndpointSliceCollectController) genHandlerUpdateFunc(clusterName string) func(oldObj, newObj interface{}) {
	return func(oldObj, newObj interface{}) {
		curObj := newObj.(runtime.Object)
		if !reflect.DeepEqual(oldObj, newObj) {
			key, err := keys.FederatedKeyFunc(clusterName, curObj)
			if err != nil {
				klog.Warningf("Failed to generate key for obj: %s", curObj.GetObjectKind().GroupVersionKind())
				return
			}
			c.worker.Add(key)
		}
	}
}

func (c *EndpointSliceCollectController) genHandlerDeleteFunc(clusterName string) func(obj interface{}) {
	return func(obj interface{}) {
		if deleted, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			// This object might be stale but ok for our current usage.
			obj = deleted.Obj
			if obj == nil {
				return
			}
		}
		oldObj := obj.(runtime.Object)
		key, err := keys.FederatedKeyFunc(clusterName, oldObj)
		if err != nil {
			klog.Warningf("Failed to generate key for obj: %s", oldObj.GetObjectKind().GroupVersionKind())
			return
		}
		c.worker.Add(key)
	}
}

// handleEndpointSliceEvent syncs EndPointSlice objects to control-plane according to EndpointSlice event.
// For EndpointSlice create or update event, reports the EndpointSlice when referencing service has been exported.
// For EndpointSlice delete event, cleanup the previously reported EndpointSlice.
func (c *EndpointSliceCollectController) handleEndpointSliceEvent(endpointSliceKey keys.FederatedKey) error {
	endpointSliceObj, err := helper.GetObjectFromCache(c.RESTMapper, c.InformerManager, endpointSliceKey)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return cleanupWorkWithEndpointSliceDelete(c.Client, endpointSliceKey)
		}
		return err
	}

	if err = c.reportEndpointSliceWithEndpointSliceCreateOrUpdate(endpointSliceKey.Cluster, endpointSliceObj); err != nil {
		klog.Errorf("Failed to handle endpointSlice(%s) event, Error: %v",
			endpointSliceKey.NamespaceKey(), err)
		return err
	}

	return nil
}

func (c *EndpointSliceCollectController) cleanorphanEndpointSliceWork(ctx context.Context, mcs *networkingv1alpha1.MultiClusterService) error {
	workList := &workv1alpha1.WorkList{}
	if err := c.List(context.TODO(), workList, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{
			networkingv1alpha1.MultiClusterServicePermanentIDLabel: util.GetLabelValue(mcs.Labels, networkingv1alpha1.MultiClusterServicePermanentIDLabel),
		}),
	}); err != nil {
		klog.Errorf("Failed to list workList reported by MultiClusterService(%s/%s) error: %v", mcs.Namespace, mcs.Name, err)
		return err
	}

	clustersSet := sets.New[string](mcs.Spec.ServiceProvisionClusters...)
	if len(clustersSet) == 0 {
		return nil
	}
	for _, item := range workList.Items {
		clusterNamespace, err := names.GetClusterName(item.Namespace)
		if err != nil {
			klog.Errorf("Failed to get cluster name for work %s/%s", item.Namespace, item.Name)
			return err
		}
		if clustersSet.Has(clusterNamespace) {
			continue
		}
		if err := c.Client.Delete(ctx, item.DeepCopy()); err != nil {
			klog.Errorf("Failed to delete work %s/%s", item.Namespace, item.Name)
			return err
		}
	}

	return nil
}

func (c *EndpointSliceCollectController) collectTargetEndpointSlice(mcs *networkingv1alpha1.MultiClusterService) error {
	provisionClusters := sets.New[string](mcs.Spec.ServiceProvisionClusters...)
	if len(provisionClusters) == 0 {
		var err error
		provisionClusters, err = util.GetClusterSet(c.Client)
		if err != nil {
			klog.Errorf("Failed to get cluster set for MultiClusterService(%s/%s), Error: %v", mcs.Namespace, mcs.Name, err)
			return err
		}
	}
	for clusterName := range provisionClusters {
		manager := c.InformerManager.GetSingleClusterManager(clusterName)
		if manager == nil {
			err := fmt.Errorf("failed to get informer manager for cluster %s", clusterName)
			klog.Errorf("%v", err)
			return err
		}

		selector := labels.SelectorFromSet(labels.Set{
			discoveryv1.LabelServiceName: mcs.Name,
		})
		epsList, err := manager.Lister(discoveryv1.SchemeGroupVersion.WithResource("endpointslices")).ByNamespace(mcs.Namespace).List(selector)
		if err != nil {
			klog.Errorf("Failed to list EndpointSlice for MultiClusterService(%s/%s) in cluster(%s), Error: %v", mcs.Namespace, mcs.Name, clusterName, err)
			return err
		}
		for _, epsObj := range epsList {
			eps := &discoveryv1.EndpointSlice{}
			if err = helper.ConvertToTypedObject(epsObj, eps); err != nil {
				klog.Errorf("Failed to convert object to EndpointSlice, error: %v", err)
				return err
			}
			if util.GetLabelValue(eps.GetLabels(), discoveryv1.LabelManagedBy) == util.EndpointSliceControllerLabelValue {
				continue
			}
			epsUnstructured, err := helper.ToUnstructured(eps)
			if err != nil {
				klog.Errorf("Failed to convert EndpointSlice %s/%s to unstructured, error: %v", eps.GetNamespace(), eps.GetName(), err)
				return err
			}
			if err := c.reportEndpointSliceWithEndpointSliceCreateOrUpdate(clusterName, epsUnstructured); err != nil {
				return err
			}
		}
	}

	return nil
}

// reportEndpointSliceWithEndpointSliceCreateOrUpdate reports the EndpointSlice when referencing service has been exported.
func (c *EndpointSliceCollectController) reportEndpointSliceWithEndpointSliceCreateOrUpdate(clusterName string, endpointSlice *unstructured.Unstructured) error {
	relatedMCSName := endpointSlice.GetLabels()[discoveryv1.LabelServiceName]

	singleClusterManager := c.InformerManager.GetSingleClusterManager(clusterName)
	if singleClusterManager == nil {
		return nil
	}

	mcs := &networkingv1alpha1.MultiClusterService{}
	if err := c.Client.Get(singleClusterManager.Context(), types.NamespacedName{Namespace: endpointSlice.GetNamespace(), Name: relatedMCSName}, mcs); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}

		klog.Errorf("Failed to get  object %s/%s. error: %v.", endpointSlice.GetNamespace(), relatedMCSName, err)
		return err
	}

	if err := reportEndpointSlice(c.Client, endpointSlice, mcs, clusterName); err != nil {
		return fmt.Errorf("failed to report EndpointSlice(%s/%s) from cluster(%s) to control-plane",
			endpointSlice.GetNamespace(), endpointSlice.GetName(), clusterName)
	}

	return nil
}

func (c *EndpointSliceCollectController) updateEndpointSliceCollected(mcs *networkingv1alpha1.MultiClusterService, status metav1.ConditionStatus, reason, message string) error {
	EndpointSliceCollected := metav1.Condition{
		Type:               networkingv1alpha1.EndpointSliceCollected,
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

// reportEndpointSlice report EndPointSlice objects to control-plane.
func reportEndpointSlice(c client.Client, endpointSlice *unstructured.Unstructured, mcs *networkingv1alpha1.MultiClusterService, clusterName string) error {
	executionSpace := names.GenerateExecutionSpaceName(clusterName)

	workMeta := metav1.ObjectMeta{
		// Karmada will synchronize this work to other cluster namespaces and add the cluster name to prevent conflicts.
		Name:      names.GenerateMCSWorkName(endpointSlice.GetKind(), endpointSlice.GetName(), endpointSlice.GetNamespace(), clusterName),
		Namespace: executionSpace,
		Labels: map[string]string{
			util.ServiceNamespaceLabel:                             endpointSlice.GetNamespace(),
			util.ServiceNameLabel:                                  endpointSlice.GetLabels()[discoveryv1.LabelServiceName],
			networkingv1alpha1.MultiClusterServicePermanentIDLabel: util.GetLabelValue(mcs.Labels, networkingv1alpha1.MultiClusterServicePermanentIDLabel),
			// indicate the Work should be not propagated since it's collected resource.
			util.PropagationInstruction: util.PropagationInstructionSuppressed,
			util.ManagedByKarmadaLabel:  util.ManagedByKarmadaLabelValue,
		},
	}

	if err := helper.CreateOrUpdateWork(c, workMeta, endpointSlice); err != nil {
		klog.Errorf("Failed to create or update work(%s/%s), Error: %v", workMeta.Namespace, workMeta.Name, err)
		return err
	}

	return nil
}

func cleanupWorkWithEndpointSliceDelete(c client.Client, endpointSliceKey keys.FederatedKey) error {
	executionSpace := names.GenerateExecutionSpaceName(endpointSliceKey.Cluster)

	workNamespaceKey := types.NamespacedName{
		Namespace: executionSpace,
		Name:      names.GenerateWorkName(endpointSliceKey.Kind, endpointSliceKey.Name, endpointSliceKey.Namespace),
	}
	work := &workv1alpha1.Work{}
	if err := c.Get(context.TODO(), workNamespaceKey, work); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}

		klog.Errorf("Failed to get work(%s) in executionSpace(%s), Error: %v", workNamespaceKey.String(), executionSpace, err)
		return err
	}

	if err := c.Delete(context.TODO(), work); err != nil {
		klog.Errorf("Failed to delete work(%s), Error: %v", workNamespaceKey, err)
		return err
	}

	return nil
}
