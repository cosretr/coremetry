#!/usr/bin/env bash
# dev-seed.sh — yerel yığını "veri var" durumuna getirir. YALNIZ YEREL, idempotent.
#
#   scripts/dev-seed.sh [--min N] [--timeout SN]     # N varsayılan 10, süre 180 sn
#
# 1. Demo trafiği akıyor mu: coremetry-go-demo çalışıyor mu + son 2 dk span sayısı
#    (salt okunur clickhouse-client).
# 2. Demo cluster'larını Remote Cluster olarak kaydeder: scripts/demo-k8s-clusters.sh
#    (zaten kayıtlıysa PUT atmaz).
# 3. /api/services en az N servis döndürene dek bekler ve sayıyı yazar.
set -euo pipefail
. "$(dirname "$0")/dev/lib.sh"

MIN=10; TIMEOUT=180
while [ $# -gt 0 ]; do
  case "$1" in
    --min) MIN="${2:?}"; shift ;;
    --timeout) TIMEOUT="${2:?}"; shift ;;
    -h|--help) sed -n '2,10p' "$0"; exit 0 ;;
    *) dev_die "bilinmeyen bayrak: $1" ;;
  esac
  shift
done
t0=$(date +%s)

demo="$(dev_container_state coremetry-go-demo)"
spans="$(dev_ch_query "SELECT count() FROM spans WHERE time > now() - INTERVAL 2 MINUTE" TSV 2>/dev/null || echo '?')"
echo "demo: coremetry-go-demo $demo; spans last 2m: $spans"
case "$demo" in running*) ;; *) echo "FAIL demo container not running (scripts/dev-up.sh)" >&2; exit 1 ;; esac
[ "$spans" != "?" ] && [ "$spans" -gt 0 ] || echo "  warn: no spans in the last 2 minutes yet"

COREMETRY_URL="$DEV_URL" "$(dirname "$0")/demo-k8s-clusters.sh" | sed 's/^/  /'

dev_login
n=0
while :; do
  n="$(curl -s -m 10 -b "$DEV_JAR" "$DEV_URL/api/services?limit=500" \
    | python3 -c 'import json,sys
try: print(len(json.load(sys.stdin).get("services") or []))
except Exception: print(0)')"
  [ "$n" -ge "$MIN" ] && break
  [ $(( $(date +%s) - t0 )) -ge "$TIMEOUT" ] && { echo "FAIL /api/services: $n < $MIN after ${TIMEOUT}s" >&2; exit 1; }
  sleep 5
done
echo "services: $n (>= $MIN) in $(( $(date +%s) - t0 ))s"
