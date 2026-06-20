# john

Kubernetes controller and Web UI for running [John the Ripper](https://github.com/openwall/john) as Indexed Jobs.

## Deploy

```shell
kubectl create namespace <namespace>
helm install john ./charts/john -n <namespace> --values charts/john/values.yaml
kubectl port-forward -n <namespace> service/john-howdy 8080:8080
```

Open `http://localhost:8080`, submit hashes, flags, shard count, and optional node selector.

Each run creates a Secret for the hash input and an Indexed Job for workers. Workers run John with `--node=N/M` and write results to etcd.

The chart creates a shared `/work` PVC by default and mounts it into both the Web UI/controller and worker Jobs. Use it for dictionaries, rules, or other shared job inputs. The storage class must support the configured access mode; the default is `ReadWriteMany`.

Values configure the controller, UI defaults, and worker runtime defaults. Hashes, shard count, John flags, and node selector are submitted per run through the controller.

Worker Pods can be customized through `john.worker.podTemplatePatch`, which is applied as a Kubernetes strategic merge patch to the generated Job `spec.template`. For example:

```yaml
john:
  worker:
    podTemplatePatch:
      spec:
        topologySpreadConstraints:
          - maxSkew: 1
            topologyKey: kubernetes.io/hostname
            whenUnsatisfiable: ScheduleAnyway
            labelSelector:
              matchLabels:
                app.kubernetes.io/name: john
                app.kubernetes.io/instance: "{{ .Release.Name }}"
                app.kubernetes.io/component: worker
            matchLabelKeys:
              - john/run-id
        priorityClassName: batch
        tolerations:
          - key: dedicated
            operator: Equal
            value: cracking
            effect: NoSchedule
        affinity:
          nodeAffinity:
            requiredDuringSchedulingIgnoredDuringExecution:
              nodeSelectorTerms:
                - matchExpressions:
                    - key: nodepool
                      operator: In
                      values:
                        - cpu
```
