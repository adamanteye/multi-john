# john

Kubernetes controller and Web UI for running [John the Ripper](https://github.com/openwall/john) as Indexed Jobs.

## Deploy

```shell
kubectl create namespace <namespace>
helm install john ./charts/john -n <namespace> --values charts/john/values.yaml
kubectl port-forward -n <namespace> service/john-howdy 8080:8080
```

Open `http://localhost:8080` and submit the run YAML from the editor.

Each run submits the Kubernetes objects in the editor as-is. The default YAML includes a Secret for hash input and an Indexed Job for workers. Workers run John with `--node=N/M` and write results to etcd.

The chart creates a shared `/work` PVC by default and mounts it into both the Web UI/controller and worker Jobs. Use it for dictionaries, rules, or other shared job inputs. The storage class must support the configured access mode; the default is `ReadWriteMany`.

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

Worker Pods can be customized through `john.worker.podTemplatePatch`, which is applied as a Kubernetes strategic merge patch when generating the default Job YAML shown in the editor. For example:

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
