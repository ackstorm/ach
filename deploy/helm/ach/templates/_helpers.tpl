{{/*
Shared label helpers for the ACH single-binary chart. Each per-mode
Deployment composes the common labels with its own
app.kubernetes.io/component value.
*/}}

{{- define "ach.commonLabels" -}}
app.kubernetes.io/name: ach
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/instance: {{ .Release.Name }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
{{- end }}

{{- define "ach.image" -}}
{{ .Values.image.repo }}:{{ .Values.image.tag }}
{{- end }}

{{- define "ach.imagePullPolicy" -}}
{{ default "IfNotPresent" .Values.image.pullPolicy }}
{{- end }}

{{- define "ach.extraEnv" -}}
{{- with .Values.extraEnv -}}
{{ toYaml . }}
{{- end -}}
{{- end -}}
