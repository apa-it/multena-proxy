{{/*
Expand the name of the chart.
*/}}
{{- define "multena-proxy.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "multena-proxy-rbac-collector.name" -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- print $name "-rbac-collector" | trunc 63 | trimSuffix "-" -}}
{{- end }}



{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "multena-proxy.fullname" -}}
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
{{- define "multena-proxy.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "multena-proxy.labels" -}}
helm.sh/chart: {{ include "multena-proxy.chart" . }}
{{ include "multena-proxy.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "multena-proxy.selectorLabels" -}}
app.kubernetes.io/name: {{ include "multena-proxy.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "multena-proxy-rbac-collector.selectorLabels" -}}
app.kubernetes.io/name: {{ include "multena-proxy-rbac-collector.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "multena-proxy.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "multena-proxy.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "multena-proxy.secretName" -}}
{{ .Values.proxy.db.existingSecret | default (printf "%s-db-external-secret" (include "multena-proxy.name" .)) }}
{{- end }}

{{/*
Full image repository (without tag/digest): "<registry>/<repository>".
Input (dict): registry, repository.
- If registry is empty, the repository is returned unchanged.
- If the repository already starts with a host (first path segment contains "." or ":",
  or is "localhost"), the registry is NOT prepended. This keeps values files that bake
  the host into the repository working unchanged, even though image.registry defaults to ghcr.io.
*/}}
{{- define "multena-proxy.imageRepository" -}}
{{- $registry := trimSuffix "/" (default "" .registry) -}}
{{- $repo := .repository -}}
{{- $first := index (splitList "/" $repo) 0 -}}
{{- $hasHost := and (contains "/" $repo) (or (contains "." $first) (contains ":" $first) (eq $first "localhost")) -}}
{{- if and $registry (not $hasHost) -}}
{{- printf "%s/%s" $registry $repo -}}
{{- else -}}
{{- $repo -}}
{{- end -}}
{{- end }}