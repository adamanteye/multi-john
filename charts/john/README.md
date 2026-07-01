# john chart

Installs the John the Ripper Web UI/controller and etcd. Worker Pods are created per run as Kubernetes Indexed Jobs.

```shell
kubectl create namespace <namespace>
helm install john ./charts/john -n <namespace> --values charts/john/values.yaml
kubectl port-forward -n <namespace> service/john-howdy 8080:8080
```

Open `http://localhost:8080` to submit the run YAML from the editor. The controller creates the Kubernetes objects in the editor as-is.

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

Values configure the controller, UI defaults, and worker runtime defaults. The Web UI starts each submission with generated Kubernetes YAML containing a Secret and the full worker `batch/v1` Job.

Each run is submitted as Kubernetes YAML documents:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: raw-sha256-batch-in
stringData:
  hashes: |-
    ...
---
apiVersion: batch/v1
kind: Job
metadata:
  name: raw-sha256-batch
spec:
  completions: 5
  parallelism: 5
  template:
    spec:
      containers:
        - name: worker
          image: ghcr.io/adamanteye/john:latest
          args:
            - --mode=worker
            - --johnFile=/input/hashes
            - --johnFlags=
            - --logLevel=debug
```

Worker Jobs can be customized with a strategic merge patch applied when generating the default Job YAML shown in the editor:

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
