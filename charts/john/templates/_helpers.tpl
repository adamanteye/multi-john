{{/*
Expand the chart name.
*/}}
{{- define "john.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a fully qualified app name.
*/}}
{{- define "john.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Create chart label value.
*/}}
{{- define "john.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels.
*/}}
{{- define "john.labels" -}}
helm.sh/chart: {{ include "john.chart" . }}
{{ include "john.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: {{ include "john.name" . }}
{{- end -}}

{{/*
Selector labels.
*/}}
{{- define "john.selectorLabels" -}}
app.kubernetes.io/name: {{ include "john.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Component resource names.
*/}}
{{- define "john.howdyName" -}}
{{- printf "%s-howdy" (include "john.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "john.etcdName" -}}
{{- printf "%s-etcd" (include "john.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "john.controllerName" -}}
{{- printf "%s-controller" (include "john.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "john.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "john.controllerName" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "john.workPVCName" -}}
{{- default (printf "%s-work" (include "john.fullname" .)) .Values.john.work.name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Derived etcd URLs. Values can override these when an external etcd is used.
*/}}
{{- define "john.etcdClientURL" -}}
{{- default (printf "http://%s:%v" (include "john.etcdName" .) (int .Values.etcd.port)) .Values.etcd.clientURL -}}
{{- end -}}

{{- define "john.etcdAdvertiseClientURLs" -}}
{{- default (include "john.etcdClientURL" .) .Values.etcd.advertiseClientURLs -}}
{{- end -}}

{{- define "john.etcdInitialAdvertisePeerURLs" -}}
{{- default (printf "http://%s:%v" (include "john.etcdName" .) (int .Values.etcd.peerPort)) .Values.etcd.initialAdvertisePeerURLs -}}
{{- end -}}

{{- define "john.etcdInitialCluster" -}}
{{- default (printf "etcd=%s" (include "john.etcdInitialAdvertisePeerURLs" .)) .Values.etcd.initialCluster -}}
{{- end -}}
