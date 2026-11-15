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
{{/*
v0.10.1132 — Grafana MCP (grafana-mcp.yaml) yardımcıları.
*/}}
{{- define "coremetry.grafanaMcp.fullname" -}}
{{- printf "%s-grafana-mcp" (include "coremetry.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "coremetry.grafanaMcp.tokenSecretName" -}}
{{- if .Values.grafanaMcp.existingSecret -}}
{{- .Values.grafanaMcp.existingSecret -}}
{{- else -}}
{{- include "coremetry.grafanaMcp.fullname" . -}}
{{- end -}}
{{- end -}}

{{/* Ayarlar → MCP sunucuları'na girilecek kümeiçi URL. */}}
{{- define "coremetry.grafanaMcp.url" -}}
{{- printf "http://%s.%s.svc:%d/mcp" (include "coremetry.grafanaMcp.fullname" .) .Release.Namespace (int .Values.grafanaMcp.port) -}}
{{- end -}}

{{/*
coremetry.grafanaMcp.seedEnv — autoRegister açıkken Coremetry pod'larına
(monolitik / api / worker) eklenen env öğeleri; değilse boş. Token JSON'a
GİRMEZ: tohum "tokenEnv" ile COREMETRY_MCP_SEED_ önekli bir env'i işaret
eder, o env Secret'tan gelir (internal/mcpclient/seed.go).
*/}}
{{- define "coremetry.grafanaMcp.seedEnv" -}}
{{- $g := .Values.grafanaMcp -}}
{{- if and $g.enabled $g.autoRegister -}}
{{- $entry := dict "name" "grafana" "transport" "http" "url" (include "coremetry.grafanaMcp.url" .) "enabled" true -}}
{{- if $g.allowTools -}}{{- $_ := set $entry "allowTools" $g.allowTools -}}{{- end -}}
{{- if $g.callerAuth.existingSecret -}}{{- $_ := set $entry "tokenEnv" "COREMETRY_MCP_SEED_GRAFANA_TOKEN" -}}{{- end -}}
- name: COREMETRY_MCP_SEED_JSON
  value: {{ toJson (list $entry) | quote }}
{{- if $g.callerAuth.existingSecret }}
- name: COREMETRY_MCP_SEED_GRAFANA_TOKEN
  valueFrom:
    secretKeyRef:
      name: {{ $g.callerAuth.existingSecret }}
      key: {{ $g.callerAuth.existingSecretKey | default "token" }}
{{- end }}
{{- end -}}
{{- end -}}

{{/* Render-anı koruması: eksik/çelişkili Grafana MCP değerleri apply'a kalmasın. */}}
{{- define "coremetry.grafanaMcp.validate" -}}
{{- $g := .Values.grafanaMcp -}}
{{- if not $g.grafanaUrl -}}
{{- fail "coremetry: grafanaMcp.enabled=true requires grafanaMcp.grafanaUrl" -}}
{{- end -}}
{{- if and $g.existingSecret $g.serviceAccountToken -}}
{{- fail "coremetry: set only one of grafanaMcp.existingSecret / grafanaMcp.serviceAccountToken" -}}
{{- end -}}
{{- $envAuth := false -}}
{{- range $e := ($g.extraEnv | default list) -}}
{{- if has (toString $e.name) (list "GRAFANA_SERVICE_ACCOUNT_TOKEN" "GRAFANA_SERVICE_ACCOUNT_TOKEN_FILE" "GRAFANA_API_KEY" "GRAFANA_USERNAME") -}}{{- $envAuth = true -}}{{- end -}}
{{- end -}}
{{- if not (or $g.existingSecret $g.serviceAccountToken $envAuth) -}}
{{- fail "coremetry: grafanaMcp needs a Grafana token — set grafanaMcp.existingSecret (preferred) or grafanaMcp.serviceAccountToken" -}}
{{- end -}}
{{- if ne (toString $g.transport) "streamable-http" -}}
{{- fail (printf "coremetry: grafanaMcp.transport must be streamable-http (Coremetry's MCP client speaks streamable HTTP only), got %q" (toString $g.transport)) -}}
{{- end -}}
{{- range $e := (.Values.extraEnv | default list) -}}
{{- if and $g.autoRegister (hasPrefix "COREMETRY_MCP_SEED_" (toString $e.name)) -}}
{{- fail (printf "coremetry: extraEnv must not set %s while grafanaMcp.autoRegister=true — the chart manages it" (toString $e.name)) -}}
{{- end -}}
{{- end -}}
{{- end -}}

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
{{- /* v0.10.1282 — distributed'da chart sre rolünün env'lerini de yönetir. */ -}}
{{- if eq (default "monolithic" .Values.deployment.mode) "distributed" -}}
{{- $reservedEnv = concat $reservedEnv (list "COREMETRY_SRE_IDLE_EXIT" "COREMETRY_SRE_LAUNCHER") -}}
{{- end -}}
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

{{/*
  v0.10.1471 — self-obs env'leri (selfObs bloğu + rol başına selfObs.roles.<rol>).
  Argüman: (dict "root" $ "role" "<all|api|worker|sre|…>").

  Uç nokta çözümü (ilk dolu kazanır):
    1. selfObs.roles.<rol>.endpoint  (yalnız distributed rolleri)
    2. selfObs.endpoint
    3. chart varsayılanı — BU kurulumun alıcısı:
       bundled collector açıksa <fullname>-otelcol:<grpcPort> (exporter'ı
       kendi ingest'ine gider; eski varsayılan, değişmedi); collector
       kapalıysa distributed'da <fullname>-ingest:<service.grpcPort>,
       monolitikte localhost:4317 (pod'un kendi alıcısı). v0.10.1471 öncesi
       collector kapalıyken var olmayan -otelcol servisine gidiyordu.
  COREMETRY_SELF_OBS_TRACES_LOCAL (/ai "Trace" linki kapısı, v0.10.1470):
  selfObs.roles.<rol>.tracesLocal → selfObs.tracesLocal → uç nokta chart
  varsayılanıysa "true" (trace bu Coremetry'ye düşer); operatörün verdiği
  uç noktada env YAZILMAZ (binary otomatiği: loopback mı).
  COREMETRY_SELF_OBS_ALWAYS_SAMPLE: rol → global; boşsa env YAZILMAZ
  (binary varsayılanı sre.investigation; "-" = hiçbiri).
*/}}
{{- define "coremetry.selfObsEnv" -}}
{{- $root := .root -}}
{{- $so := $root.Values.selfObs -}}
{{- $rv := dict -}}
{{- if and $so.roles (ne .role "all") -}}
{{- $rv = (get $so.roles .role) | default dict -}}
{{- end -}}
{{- $ep := $rv.endpoint | default $so.endpoint | default "" -}}
{{- $chartDefault := eq $ep "" -}}
{{- if $chartDefault -}}
{{- if $root.Values.otelCollector.enabled -}}
{{- $ep = printf "%s-otelcol:%v" (include "coremetry.fullname" $root) $root.Values.otelCollector.service.grpcPort -}}
{{- else if eq .role "all" -}}
{{- $ep = "localhost:4317" -}}
{{- else -}}
{{- $ep = printf "%s-ingest:%v" (include "coremetry.fullname" $root) $root.Values.service.grpcPort -}}
{{- end -}}
{{- end -}}
- name: COREMETRY_SELF_OBS_OTLP_ENDPOINT
  value: {{ $ep | quote }}
- name: COREMETRY_SELF_OBS_SAMPLE_RATE
  value: {{ $so.sampleRate | quote }}
{{- $always := $rv.alwaysSample | default $so.alwaysSample | default "" }}
{{- with $always }}
- name: COREMETRY_SELF_OBS_ALWAYS_SAMPLE
  value: {{ . | quote }}
{{- end }}
{{- /* bool false da "false" olarak kalsın diye default yerine kindIs. */}}
{{- $local := "" }}
{{- if not (kindIs "invalid" $rv.tracesLocal) }}{{ $local = toString $rv.tracesLocal }}{{ end }}
{{- if and (eq $local "") (not (kindIs "invalid" $so.tracesLocal)) }}{{ $local = toString $so.tracesLocal }}{{ end }}
{{- if and (eq $local "") $chartDefault }}
{{- $local = "true" }}
{{- end }}
{{- with $local }}
- name: COREMETRY_SELF_OBS_TRACES_LOCAL
  value: {{ . | quote }}
{{- end }}
{{- end -}}

{{/*
coremetry.redisMaxmemory — v0.10.2227 (Redis R1, K3): bundled Redis
maxmemory = %75 × redis.resources.limits.memory (Gi / Mi tam sayı). Kap
limitine ulaşmadan tahliye başlar (OOMKill = tüm kilit/hak anahtarlarının
aynı anda kaybı). Ayrıştırılamayan limit ("1.5Gi", "1G", boş) → boş dize;
şablon bayrağı hiç basmaz. Yeni değer yok: limitten türetilir.
*/}}
{{- define "coremetry.redisMaxmemory" -}}
{{- $lim := toString (dig "limits" "memory" "" .Values.redis.resources) -}}
{{- $mb := 0 -}}
{{- if regexMatch "^[0-9]+Gi$" $lim -}}
{{- $mb = mul (trimSuffix "Gi" $lim | atoi) 1024 -}}
{{- else if regexMatch "^[0-9]+Mi$" $lim -}}
{{- $mb = trimSuffix "Mi" $lim | atoi -}}
{{- end -}}
{{- if gt (int $mb) 0 -}}{{ div (mul $mb 3) 4 }}mb{{- end -}}
{{- end -}}
