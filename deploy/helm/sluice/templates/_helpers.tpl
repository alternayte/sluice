{{- define "sluice.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "sluice.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "sluice.labels" -}}
{{ include "sluice.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "sluice.serviceAccountName" -}}
{{- default (include "sluice.fullname" .) .Values.serviceAccount.name -}}
{{- end -}}

{{- define "sluice.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}

{{- define "sluice.internalURL" -}}
{{- default (printf "http://%s.%s.svc:%v" (include "sluice.fullname" .) .Release.Namespace .Values.service.port) .Values.internalURL -}}
{{- end -}}
