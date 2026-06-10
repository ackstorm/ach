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

{{/*
ach.litellmConnectionEnv — derives the LiteLLM coordinates that
platform-api + content-service read from env (ACH_LITELLM_BASE_URL +
ACH_LITELLM_MASTER_KEY) from the single litellmConnection values block,
so LiteLLM is configured ONCE. The same block also renders
LiteLLMConnection/default (templates/litellmconnection.yaml) for the
forwarder + operator. Emitted only when litellmConnection.enabled.
*/}}
{{/*
ach.serviceMonitorEndpointTuning — shared interval/scrapeTimeout block for
every ServiceMonitor endpoint, so the three endpoints stay in lock-step.
Both fields render only when set (empty → Prometheus default). Call with the
root context: include "ach.serviceMonitorEndpointTuning" .
*/}}
{{- define "ach.serviceMonitorEndpointTuning" -}}
{{- with .Values.metrics.serviceMonitor.interval }}
interval: {{ . | quote }}
{{- end }}
{{- with .Values.metrics.serviceMonitor.scrapeTimeout }}
scrapeTimeout: {{ . | quote }}
{{- end }}
{{- end -}}

{{- define "ach.litellmConnectionEnv" -}}
{{- if .Values.litellmConnection.enabled -}}
- name: ACH_LITELLM_BASE_URL
  value: {{ required "litellmConnection.endpoint is required when litellmConnection.enabled" .Values.litellmConnection.endpoint | quote }}
- name: ACH_LITELLM_MASTER_KEY
  valueFrom:
    secretKeyRef:
      name: {{ required "litellmConnection.masterKeySecretRef.name is required" .Values.litellmConnection.masterKeySecretRef.name | quote }}
      key: {{ required "litellmConnection.masterKeySecretRef.key is required" .Values.litellmConnection.masterKeySecretRef.key | quote }}
{{- end -}}
{{- end -}}
