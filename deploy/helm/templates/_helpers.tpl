{{/*
Expand the name of the chart.
*/}}
{{- define "openssm.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "openssm.fullname" -}}
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
{{- define "openssm.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "openssm.labels" -}}
helm.sh/chart: {{ include "openssm.chart" . }}
{{ include "openssm.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "openssm.selectorLabels" -}}
app.kubernetes.io/name: {{ include "openssm.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Service account name.
*/}}
{{- define "openssm.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "openssm.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Server secret value.
*/}}
{{- define "openssm.serverSecret" -}}
{{- if .Values.existingSecret -}}
{{- printf "%s" .Values.existingSecret -}}
{{- else -}}
{{- printf "%s" .Values.openssm.ssm.serverSecret -}}
{{- end -}}
{{- end }}

{{/*
Validate that a server secret is configured.
Renders to an error message if neither serverSecret nor existingSecret is set.
*/}}
{{- define "openssm.validateSecret" -}}
{{- if and (not .Values.existingSecret) (not .Values.openssm.ssm.serverSecret) -}}
{{- fail "Either openssm.ssm.serverSecret or existingSecret must be set. Refusing to install with an empty server secret (it signs agent/session tokens and wraps KMS keys)." -}}
{{- end -}}
{{- end }}
