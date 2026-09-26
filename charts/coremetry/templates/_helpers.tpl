{{/* vim: set filetype=mustache: */}}

{{- define "coremetry.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
fullname: Bitnami-style release-vs-chart collapse. When the user
runs `helm install coremetry charts/coremetry`, .Release.Name and
.Chart.Name are identical, and the naive "release-chart" template
would yield "coremetry-coremetry". Collapse to just .Release.Name
in that case so resources are named "coremetry", "coremetry-redis",
etc. — what every operator actually expects from this chart's
release name.

For a non-matching release name (e.g. `helm install prod
charts/coremetry`), keep the standard "<release>-<chart>" form
("prod-coremetry") so multiple instances in one namespace don't
collide.
*/}}
{{- define "coremetry.fullname" -}}
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

{{- define "coremetry.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "coremetry.labels" -}}
helm.sh/chart: {{ include "coremetry.chart" . }}
{{ include "coremetry.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "coremetry.selectorLabels" -}}
app.kubernetes.io/name: {{ include "coremetry.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "coremetry.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "coremetry.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Resolve the secret name. If the user supplied an existing secret, use that;
otherwise reference the one this chart creates.
*/}}
{{- define "coremetry.secretName" -}}
{{- if .Values.secrets.existingSecret -}}
{{- .Values.secrets.existingSecret -}}
{{- else -}}
{{- printf "%s-secret" (include "coremetry.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Redis URL: explicit external takes priority, then in-cluster service when
enabled, then empty (single-instance / no cache mode).
*/}}
{{- define "coremetry.redisURL" -}}
{{- if .Values.redis.external.url -}}
{{- .Values.redis.external.url -}}
{{- else if .Values.redis.enabled -}}
{{- printf "redis://%s-redis:6379/0" (include "coremetry.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
coremetry.image renders a fully-qualified image reference for any of the
chart's components. Resolution order for the registry portion:

  1. global.imageRegistry (when set) — wins for ALL images, useful for
     air-gapped clusters that mirror everything into one internal registry
  2. <component>.image.registry — per-image override
  3. "" — no prefix, lets the runtime pick the default (docker.io)

Tag falls back to .defaultTag (typically Chart.AppVersion for the coremetry
image, hard-coded for upstream images).

Usage:
  image: {{ include "coremetry.image" (dict "imageRoot" .Values.image
                                          "global"    .Values.global
                                          "defaultTag" .Chart.AppVersion) }}
*/}}
{{- define "coremetry.image" -}}
{{- $registry := .imageRoot.registry | default "" -}}
{{- if and .global .global.imageRegistry -}}
{{- $registry = .global.imageRegistry -}}
{{- end -}}
{{- $repo := .imageRoot.repository -}}
{{- $tag := .imageRoot.tag | toString -}}
{{- if eq $tag "" -}}{{- $tag = (.defaultTag | toString) -}}{{- end -}}
{{- if eq $tag "" -}}{{- $tag = "latest" -}}{{- end -}}
{{- if $registry -}}
{{- printf "%s/%s:%s" $registry $repo $tag -}}
{{- else -}}
{{- printf "%s:%s" $repo $tag -}}
{{- end -}}
{{- end -}}

{{/*
coremetry.imagePullSecrets renders an `imagePullSecrets` list. Merges
global.imagePullSecrets and the per-image .image.pullSecrets if any.
*/}}
{{- define "coremetry.imagePullSecrets" -}}
{{- $secrets := list -}}
{{- if and .global .global.imagePullSecrets -}}
{{- $secrets = concat $secrets .global.imagePullSecrets -}}
{{- end -}}
{{- if .imageRoot.pullSecrets -}}
{{- $secrets = concat $secrets .imageRoot.pullSecrets -}}
{{- end -}}
{{- if $secrets }}
imagePullSecrets:
{{- range $secrets }}
  - name: {{ . }}
{{- end }}
{{- end }}
{{- end -}}

{{/*
ClickHouse address (host:port): explicit external takes priority, then
in-cluster service when enabled, then a sane "clickhouse:9000" placeholder
that will fail loudly so the misconfiguration is obvious.
*/}}
{{- define "coremetry.clickhouseAddr" -}}
{{- if .Values.clickhouse.external.addr -}}
{{- .Values.clickhouse.external.addr -}}
{{- else if .Values.clickhouse.enabled -}}
{{- printf "%s-clickhouse:%d" (include "coremetry.fullname" .) (int .Values.clickhouse.service.nativePort) -}}
{{- else -}}
clickhouse:9000
{{- end -}}
{{- end -}}

{{/*
v0.10.958 — coremetry.validateExtras: extraEnv / envFrom / extraVolumes /
extraVolumeMounts için render-anı koruması (Rollouts v2 P1.6). Kubernetes bu
hataların bir kısmını ancak apply'da, bir kısmını HİÇ yakalamaz:
  - liste olmayan değer ya da eşleme olmayan öğe → geçersiz manifest;
  - chart'ın her zaman yönettiği env adının tekrarı → Kubernetes SONUNCUYU
    alır (extraEnv COREMETRY_MODE=api, distributed'da worker'ı sessizce api
    yapar) ve upgrade'in strategic-merge patch'i bozulur;
  - chart'ın hacim adı (config, tmp) ya da mount yolu (/tmp,
    /app/config.yaml) çakışması → apply reddi, rolling update yarıda kalır.
İki deployment şablonu mod bloğunun başında çağırır; çıktısı boştur, varsayılan
değerlerle manifest değişmez.
*/}}
{{- define "coremetry.validateExtras" -}}
{{- range $key := list "extraEnv" "envFrom" "extraVolumes" "extraVolumeMounts" -}}
{{- $v := index $.Values $key -}}
{{- if and $v (not (kindIs "slice" $v)) -}}
{{- fail (printf "coremetry: %s must be a YAML list (got %s) — see the extraEnv block in values.yaml" $key (kindOf $v)) -}}
{{- end -}}
{{- range $item := ($v | default list) -}}
{{- if not (kindIs "map" $item) -}}
{{- fail (printf "coremetry: every %s item must be a mapping (got %s)" $key (kindOf $item)) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- $reservedEnv := list "COREMETRY_MODE" "COREMETRY_JWT_SECRET" "COREMETRY_CH_PASSWORD" "COREMETRY_INITIAL_PASSWORD" -}}
{{- range $e := (.Values.extraEnv | default list) -}}
{{- if has (toString $e.name) $reservedEnv -}}
{{- fail (printf "coremetry: extraEnv must not set %s — the chart manages it (use deployment.mode / secrets.*)" (toString $e.name)) -}}
{{- end -}}
{{- end -}}
{{- range $vol := (.Values.extraVolumes | default list) -}}
{{- if has (toString $vol.name) (list "config" "tmp") -}}
{{- fail (printf "coremetry: extraVolumes name %q collides with a chart-owned volume (config, tmp)" (toString $vol.name)) -}}
{{- end -}}
{{- end -}}
{{- range $m := (.Values.extraVolumeMounts | default list) -}}
{{- if has (toString $m.mountPath) (list "/tmp" "/app/config.yaml") -}}
{{- fail (printf "coremetry: extraVolumeMounts mountPath %q collides with a chart-owned mount (/tmp, /app/config.yaml)" (toString $m.mountPath)) -}}
{{- end -}}
{{- end -}}
{{- end -}}
