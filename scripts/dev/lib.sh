# shellcheck shell=bash
# scripts/dev/lib.sh — scripts/dev-*.sh ortak yardımcıları. YALNIZ YEREL.
#
# Kaynak olarak yüklenir (`. scripts/dev/lib.sh`), tek başına çalışmaz.
# - Proje adı: çalışan coremetry-clickhouse container'ının
#   com.docker.compose.project etiketi → COMPOSE_PROJECT_NAME → "gotrace".
#   Aynı ad kullanılır ki ikinci bir yığın (ve ikinci volume takımı) doğmasın.
# - Oturum: POST /api/auth/login → coremetry_session çerezi
#   ${TMPDIR:-/tmp}/coremetry-dev/cookies.txt (dizin 0700, dosya 0600).
#   Kimlik bilgisi COREMETRY_ADMIN_EMAIL/_PASSWORD → COREMETRY_INITIAL_ADMIN/
#   _PASSWORD → compose varsayılanı; hiçbir zaman ekrana ya da argv'ye yazılmaz
#   (gövde python ile 0600 geçici dosyaya yazılıp curl'e @dosya verilir).
# - Koruma: localhost / 127.0.0.1 dışındaki her URL reddedilir.

DEV_ROOT="$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)" || exit 2
DEV_URL="${COREMETRY_URL:-http://localhost:8088}"
DEV_VM_URL="${COREMETRY_VM_URL:-http://localhost:8428}"
DEV_CH_CONTAINER="${COREMETRY_CH_CONTAINER:-coremetry-clickhouse}"
DEV_STATE_DIR="${TMPDIR:-/tmp}"
DEV_STATE_DIR="${DEV_STATE_DIR%/}/coremetry-dev"
DEV_JAR="$DEV_STATE_DIR/cookies.txt"
# Yığının çekirdeği: dev-up'ın beklediği container'lar (ad = container_name).
DEV_SERVICES=(clickhouse redis victoriametrics otel-collector coremetry go-demo)

dev_die() { echo "error: $*" >&2; exit 2; }

# dev_require_local <url> — yalnız http(s)://localhost|127.0.0.1[:port][/…]
dev_require_local() {
  case "$1" in
    http://localhost|http://localhost[:/]*|http://127.0.0.1|http://127.0.0.1[:/]*) ;;
    https://localhost|https://localhost[:/]*|https://127.0.0.1|https://127.0.0.1[:/]*) ;;
    *) dev_die "yalnız localhost: '$1' reddedildi" ;;
  esac
}
dev_require_local "$DEV_URL"
dev_require_local "$DEV_VM_URL"

dev_state_dir() {
  (umask 077 && mkdir -p "$DEV_STATE_DIR")
  chmod 700 "$DEV_STATE_DIR"
}

# dev_project — compose proje adı (bkz. üst not).
dev_project() {
  local p
  p="$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' "$DEV_CH_CONTAINER" 2>/dev/null || true)"
  [ -n "$p" ] && [ "$p" != "<no value>" ] || p="${COMPOSE_PROJECT_NAME:-gotrace}"
  echo "$p"
}

# dev_compose_args [--es] — "-p … -f … -f …" dizisini DEV_COMPOSE'a yazar.
dev_compose_args() {
  DEV_COMPOSE=(docker compose -p "$(dev_project)" --project-directory "$DEV_ROOT"
    -f "$DEV_ROOT/docker-compose.yml" -f "$DEV_ROOT/docker-compose.dev.yml")
  if [ "${1:-}" = "--es" ]; then
    DEV_COMPOSE+=(-f "$DEV_ROOT/scripts/dev/compose.es.yml")
  fi
  DEV_COMPOSE+=(--profile go)
}

# dev_container_state <container> → "running healthy" | "running none" | "exited none" | "missing"
dev_container_state() {
  docker inspect -f '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$1" 2>/dev/null || echo missing
}

# dev_wait_http <url> <saniye> [grep-deseni] — 2 sn arayla dener.
dev_wait_http() {
  local url="$1" limit="$2" pat="${3:-}" t0 body
  dev_require_local "$url"
  t0=$(date +%s)
  while :; do
    body="$(curl -s -m 4 "$url" 2>/dev/null || true)"
    if [ -n "$body" ] && { [ -z "$pat" ] || grep -q -- "$pat" <<<"$body"; }; then return 0; fi
    [ $(( $(date +%s) - t0 )) -ge "$limit" ] && return 1
    sleep 2
  done
}

# dev_session_ok — kavanozdaki çerez geçerli mi?
dev_session_ok() {
  [ -s "$DEV_JAR" ] || return 1
  curl -s -m 5 -b "$DEV_JAR" "$DEV_URL/api/auth/session" 2>/dev/null | grep -q '"authenticated":true'
}

# dev_login — geçerli çerez yoksa oturum açar. Kimlik bilgisi yazdırılmaz.
dev_login() {
  dev_state_dir
  dev_session_ok && return 0
  local body code
  body="$(mktemp "$DEV_STATE_DIR/login.XXXXXX")"
  chmod 600 "$body"
  DK_EMAIL="${COREMETRY_ADMIN_EMAIL:-${COREMETRY_INITIAL_ADMIN:-admin@coremetry.local}}" \
  DK_PASS="${COREMETRY_ADMIN_PASSWORD:-${COREMETRY_INITIAL_PASSWORD:-admin}}" \
    python3 -c 'import json,os;print(json.dumps({"email":os.environ["DK_EMAIL"],"password":os.environ["DK_PASS"]}))' >"$body"
  rm -f "$DEV_JAR"
  (umask 077 && : >"$DEV_JAR")
  code="$(umask 077; curl -s -m 10 -o /dev/null -w '%{http_code}' -c "$DEV_JAR" -X POST "$DEV_URL/api/auth/login" \
    -H 'content-type: application/json' --data-binary @"$body")"
  rm -f "$body"
  chmod 600 "$DEV_JAR"
  [ "$code" = "200" ] || { rm -f "$DEV_JAR"; echo "login failed (HTTP $code)" >&2; return 1; }
}

# dev_ch_query <sql> [biçim] [max_execution_time] [max_result_rows] — salt okunur
# clickhouse-client (container içinde; parola container'ın kendi ortamından, argv'de
# görünmez: CLICKHOUSE_PASSWORD istemci tarafından okunur). SQL stdin'den gider.
# Satır sınırı: Pretty biçimlerde tam; diğerlerinde blok düzeyinde (break modu).
dev_ch_query() {
  local sql="$1" fmt="${2:-PrettyCompactMonoBlock}" met="${3:-10}" rows="${4:-200}"
  printf '%s' "$sql" | docker exec -i "$DEV_CH_CONTAINER" clickhouse-client \
    --database coremetry --readonly=1 --max_execution_time="$met" \
    --max_result_rows="$rows" --result_overflow_mode=break \
    --output_format_pretty_max_rows="$rows" --format="$fmt"
}

# dev_ms — milisaniye zaman damgası (macOS date %N bilmez).
dev_ms() { python3 -c 'import time;print(int(time.time()*1000))'; }
