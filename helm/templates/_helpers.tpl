{{- define "llmbridge.fullname" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "llmbridge.labels" -}}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{ include "llmbridge.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "llmbridge.selectorLabels" -}}
app.kubernetes.io/name: llmbridge
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
