# john chart

Installs the John the Ripper Web UI/controller and etcd. Worker Pods are created per run as Kubernetes Indexed Jobs.

```shell
kubectl create namespace <namespace>
helm install john ./charts/john -n <namespace> --values charts/john/values.yaml
kubectl port-forward -n <namespace> service/john-howdy 8080:8080
```

Open `http://localhost:8080` to submit hash input. The controller stores hashes in a Secret and creates an Indexed Job with the requested shard count, parallelism, and optional node selector.

The chart creates a shared workspace PVC by default:

```yaml
john:
  work:
    enabled: true
    mountPath: /work
    accessModes:
      - ReadWriteMany
```

The same PVC is mounted into the controller and every worker Job. Set `john.work.existingClaim` to reuse an existing PVC. By default, resource names are based on the Helm release name, so `helm install john ...` creates `john-howdy`, `john-etcd`, `john-controller`, and `john-work`.

Values configure the controller, UI defaults, and worker runtime defaults. Hashes, shard count, John flags, and node selector are submitted per run through the controller.

Worker Jobs can be customized with a strategic merge patch applied to the generated Job `spec.template`:

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
