# multi-john chart

Installs the multi-john Web UI/controller and etcd. Worker Pods are created per run as Kubernetes Indexed Jobs.

```shell
kubectl create namespace <namespace>
helm install multi-john ./charts/multi-john -n <namespace> --values charts/multi-john/values.yaml
kubectl port-forward -n <namespace> service/howdy 8080:8080
```

Default app image: `ghcr.io/adamanteye/multi-john:0.1.1`.

Open `http://localhost:8080` to submit hash input. The controller stores hashes in a Secret and creates an Indexed Job with the requested shard count, parallelism, and optional node selector.
