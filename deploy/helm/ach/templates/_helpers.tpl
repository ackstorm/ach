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

{{/*
ach.contentServiceContainer — the content-service container spec, shared by
the sidecar in the operator Pod (default) and the standalone Deployment
(contentService.standalone=true, G16 RWX split). Defined ONCE so the two
render paths cannot drift. The container is a READER of the artifact cache
(mounts `cache` readOnly at /var/cache/ach via sendfile(2)); the operator
container is the sole writer. Call with the root context and nindent 8:
  {{- include "ach.contentServiceContainer" . | nindent 8 }}
*/}}
{{- define "ach.contentServiceContainer" -}}
- name: content-service
  image: {{ include "ach.image" . }}
  imagePullPolicy: {{ include "ach.imagePullPolicy" . }}
  args: {{ toYaml .Values.contentService.args | nindent 4 }}
  ports:
    - name: cs-http
      containerPort: 8082
  env:
    - name: ACH_NAMESPACE
      valueFrom:
        fieldRef:
          fieldPath: metadata.namespace
    - name: ACH_CACHE_ROOT
      value: /var/cache/ach
    - name: CONTENT_SERVICE_HEALTH_BIND_ADDRESS
      value: ":8082"
    {{- with .Values.extraEnv }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
    # ACH_LITELLM_BASE_URL + ACH_LITELLM_MASTER_KEY derived from the
    # single litellmConnection block — do NOT also set them in extraEnv.
    {{- include "ach.litellmConnectionEnv" . | nindent 4 }}
  volumeMounts:
    - name: cache
      mountPath: /var/cache/ach
      readOnly: true
  livenessProbe:
    httpGet:
      path: /healthz
      port: 8082
    initialDelaySeconds: 15
    periodSeconds: 20
  readinessProbe:
    httpGet:
      path: /healthz
      port: 8082
    initialDelaySeconds: 5
    periodSeconds: 10
  resources: {{ toYaml .Values.contentService.resources | nindent 4 }}
  securityContext:
    allowPrivilegeEscalation: false
    runAsNonRoot: true
    readOnlyRootFilesystem: true
    capabilities:
      drop:
        - ALL
{{- end -}}
