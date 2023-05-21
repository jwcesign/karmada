package metricsadapter

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	v1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/metrics/pkg/apis/metrics"
	"sigs.k8s.io/metrics-server/pkg/api"
)

type resourceProvider struct {
}

func NewResourceMetricsProvider() (api.MetricsGetter, error) {
	return &resourceProvider{}, nil
}

func (r *resourceProvider) GetPodMetrics(pods ...*metav1.PartialObjectMetadata) ([]metrics.PodMetrics, error) {
	klog.Infof("Query pods metrics: %v", pods)
	return []metrics.PodMetrics{}, nil
}

func (r *resourceProvider) GetNodeMetrics(nodes ...*corev1.Node) ([]metrics.NodeMetrics, error) {
	klog.Infof("karmada-metrics-adapter does not support query node metrics yet")
	return []metrics.NodeMetrics{}, nil
}

type ResourceLister struct {
	PodLister       cache.GenericLister
	NamespaceLister cache.GenericNamespaceLister
	NodeLister      v1.NodeLister
}

func NewResourceLister() *ResourceLister {
	r := &ResourceLister{
		NamespaceLister: NewNamespaceLister(),
		NodeLister:      NewNodeLister(),
	}
	r.PodLister = NewPodLister(r.NamespaceLister)

	return r
}

type PodLister struct {
	NamespaceLister cache.GenericNamespaceLister
}

func NewPodLister(lister cache.GenericNamespaceLister) cache.GenericLister {
	return &PodLister{lister}
}

func (p PodLister) List(selector labels.Selector) (ret []runtime.Object, err error) {
	klog.Infof("List query pods with selector: %v", selector)
	return []runtime.Object{}, nil
}

func (p PodLister) Get(name string) (runtime.Object, error) {
	klog.Infof("Get query pods metrics with name: %s", name)
	return &corev1.Pod{}, nil
}

func (p PodLister) ByNamespace(namespace string) cache.GenericNamespaceLister {
	klog.Infof("Namespace query with name: %s", namespace)
	return p.NamespaceLister
}

type NamespaceLister struct {
}

func NewNamespaceLister() cache.GenericNamespaceLister {
	return &NamespaceLister{}
}

func (n NamespaceLister) List(selector labels.Selector) (ret []runtime.Object, err error) {
	klog.Infof("List query namespace with selector: %v", selector)
	return []runtime.Object{}, nil
}

func (n NamespaceLister) Get(name string) (runtime.Object, error) {
	klog.Infof("Get query namespace with name: %s", name)
	return nil, nil
}

type NodeLister struct {
}

func NewNodeLister() v1.NodeLister {
	return &NodeLister{}
}

func (n NodeLister) List(selector labels.Selector) (ret []*corev1.Node, err error) {
	klog.Warning("karmada-metrics-adapter does not support list node metrics yet")
	return []*corev1.Node{}, nil
}

func (n NodeLister) Get(name string) (*corev1.Node, error) {
	klog.Warning("karmada-metrics-adapter does not support node metrics get yet")
	return &corev1.Node{}, nil
}
