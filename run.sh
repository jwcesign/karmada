kubectl --context=karmada-apiserver get --raw /apis/metrics.k8s.io/v1beta1/nodes/member3-control-plane
kubectl --context=karmada-apiserver get --raw /apis/metrics.k8s.io/v1beta1/nodes/member3-control-plane-none
kubectl --context=karmada-apiserver top nodes
kubectl --context=karmada-apiserver get --raw /apis/metrics.k8s.io/v1beta1/nodes\?fieldSelector\=metadata\.name=member2-control-plane

kubectl --context=karmada-apiserver top pods -A
kubectl --context=karmada-apiserver top pod nginx-748c667d99-qt2z8
kubectl --context=karmada-apiserver top pod nginx-748c667d99-qt2z8 -nkarmada-system
kubectl --context=karmada-apiserver top pods -nlocal-path-storage
kubectl --context=karmada-apiserver get --raw /apis/metrics.k8s.io/v1beta1/namespaces/karmada-system/pods?fieldSelector\=metadata\.name=karmada-agent-54dcc8846f-2mhgl

