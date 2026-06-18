# multi-john

Kubernetes controller and Web UI for running [John the Ripper](https://github.com/openwall/john) as Indexed Jobs.

## Image

`ghcr.io/adamanteye/multi-john:0.1.1`

## Deploy

```shell
kubectl create namespace <namespace>
helm install multi-john ./charts/multi-john -n <namespace> --values charts/multi-john/values.yaml
kubectl port-forward -n <namespace> service/howdy 8080:8080
```

Open `http://localhost:8080`, submit hashes, flags, shard count, and optional node selector.

Each run creates a Secret for the hash input and an Indexed Job for workers. Workers run John with `--node=N/M` and write results to etcd.
