docker rmi karmada/karmada-metrics-adapter:latest

make image-karmada-metrics-adapter
docker tag karmada/karmada-metrics-adapter:v1.3.0-1041-g4fb2ee62-dirty karmada/karmada-metrics-adapter:latest

kind load docker-image karmada/karmada-metrics-adapter:latest --name karmada-host

kubectl --context=karmada-host delete -f artifacts/deploy/karmada-metrics-adapter.yaml
kubectl --context=karmada-host apply -f artifacts/deploy/karmada-metrics-adapter.yaml
kubectl --context=karmada-apiserver apply -f artifacts/deploy/karmada-metrics-adapter-apiservice.yaml
