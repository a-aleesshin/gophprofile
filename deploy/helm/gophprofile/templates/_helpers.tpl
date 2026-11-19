{{- define "gophprofile.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{ define "gophprofile.fullname" -}}
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

{{- define "gophprofile.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "gophprofile.labels" -}}
helm.sh/chart: {{ include "gophprofile.chart" . }}
app.kubernetes.io/name: {{ include "gophprofile.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: gophprofile
{{- end -}}

{{- define "gophprofile.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gophprofile.name" .root }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{- define "gophprofile.componentName" -}}
{{- printf "%s-%s" (include "gophprofile.fullname" .root) .component | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "gophprofile.image" -}}
{{- $tag := required "image.tag is required: pass an immutable tag per build" .root.Values.image.tag -}}
{{- with .root.Values.image.registry }}{{ printf "%s/" . }}{{ end }}{{ .repository }}:{{ $tag }}
{{- end -}}

{{- define "gophprofile.secretName" -}}
{{- if .spec.existingSecret -}}
{{- .spec.existingSecret -}}
{{- else -}}
{{- printf "%s-%s" (include "gophprofile.fullname" .root) .service | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "gophprofile.configName" -}}
{{- printf "%s-%s" (include "gophprofile.fullname" .root) .service | trunc 63 | trimSuffix "-" -}}
{{- end -}}
