{{/*
Expand the name of the chart.
*/}}
{{- define "isrv.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "isrv.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "isrv.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "isrv.labels" -}}
helm.sh/chart: {{ include "isrv.chart" . }}
{{ include "isrv.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "isrv.selectorLabels" -}}
app.kubernetes.io/name: {{ include "isrv.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "isrv.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "isrv.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Port the isrv container listens on.
*/}}
{{- define "isrv.containerPort" -}}
{{- dig "serverPort" 8080 .Values.config -}}
{{- end }}

{{/*
Name of the Secret holding ISRV_* credentials: the user-provided one, or the
chart-managed one.
*/}}
{{- define "isrv.secretName" -}}
{{- default (include "isrv.fullname" .) .Values.secrets.existingSecret }}
{{- end }}

{{/*
Whether the chart should create a Secret from inline `secrets` values.
*/}}
{{- define "isrv.createSecret" -}}
{{- $s := .Values.secrets -}}
{{- if and (not $s.existingSecret) (or $s.adminUsername $s.adminPassword $s.adminSessionSecret $s.databasePassword $s.encryptionIdentity .Values.storage.accessKey .Values.storage.secretKey) -}}
true
{{- end }}
{{- end }}

{{/*
Public URL for generated links: config.serverUrl if set, otherwise derived
from the first ingress host (https when that host appears in ingress.tls).
Empty when neither is available, so the application default applies.
*/}}
{{- define "isrv.serverUrl" -}}
{{- if .Values.config.serverUrl }}
{{- .Values.config.serverUrl }}
{{- else if and .Values.ingress.enabled .Values.ingress.hosts }}
{{- $host := (first .Values.ingress.hosts).host }}
{{- $scheme := "http" }}
{{- range .Values.ingress.tls }}
{{- if has $host .hosts }}{{- $scheme = "https" }}{{- end }}
{{- end }}
{{- printf "%s://%s" $scheme $host }}
{{- end }}
{{- end }}

{{/*
Application config file content: the `config` block with serverUrl resolved
and the non-secret parts of the top-level `storage` block merged in.
*/}}
{{- define "isrv.configFile" -}}
{{- $cfg := deepCopy .Values.config }}
{{- $url := include "isrv.serverUrl" . }}
{{- if $url }}
{{- $_ := set $cfg "serverUrl" $url }}
{{- else }}
{{- $_ := unset $cfg "serverUrl" }}
{{- end }}
{{- $storage := omit .Values.storage "accessKey" "secretKey" "gcsCredentials" }}
{{- if and (eq $storage.type "local") (not $storage.basePath) }}
{{- $_ := set $storage "basePath" "/data/uploads" }}
{{- end }}
{{- $_ := set $cfg "storage" $storage }}
{{- toYaml $cfg }}
{{- end }}

{{/*
Guard rails, evaluated once from the Deployment template.
*/}}
{{- define "isrv.validateValues" -}}
{{- $cfg := .Values.config }}
{{- if hasKey $cfg "storage" }}
{{- fail "`config.storage` must not be set: configure the storage backend via the top-level `storage` block instead." }}
{{- end }}
{{- if or (dig "admin" "username" "" $cfg) (dig "admin" "password" "" $cfg) (dig "admin" "sessionSecret" "" $cfg) (dig "database" "password" "" $cfg) (dig "encryption" "identity" "" $cfg) }}
{{- fail "Credentials must not be set in `config` (admin.*, database.password, encryption.identity): they would be stored in a world-readable ConfigMap. Use the `secrets` block or `secrets.existingSecret` instead." }}
{{- end }}
{{- $replicas := int .Values.replicaCount }}
{{- if .Values.autoscaling.enabled }}{{- $replicas = int .Values.autoscaling.maxReplicas }}{{- end }}
{{- if gt $replicas 1 }}
{{- if eq .Values.storage.type "local" }}
{{- fail "storage.type \"local\" does not support more than one replica (each pod would have its own files). Use s3/gcs storage, or set replicaCount to 1 and disable autoscaling." }}
{{- end }}
{{- if eq (dig "database" "type" "sqlite" $cfg) "sqlite" }}
{{- fail "config.database.type \"sqlite\" does not support more than one replica. Use postgres, or set replicaCount to 1 and disable autoscaling." }}
{{- end }}
{{- end }}
{{- end }}
