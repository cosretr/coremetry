#!/usr/bin/env bash
# dev-up.sh — yerel yığını kaldırır ya da (zaten ayaktaysa) yalnız durumunu yazar. YALNIZ YEREL.
#
#   scripts/dev-up.sh            # eksik/durmuş container'ları kaldır, hazır olana dek bekle
#   scripts/dev-up.sh --es       # + Elasticsearch log yolu (scripts/dev/compose.es.yml)
#   scripts/dev-up.sh --deploy   # sonra scripts/dev-deploy.sh (host'ta derle, imaja koy)
#
# Gerekli container'ların hepsi çalışıyor + sağlıklı + /api/version cevap
# veriyorsa HİÇBİR ŞEY oluşturulmaz. Değilse:
#   docker compose -p <proje> -f docker-compose.yml -f docker-compose.dev.yml \
#     --profile go up -d --no-build --no-recreate <servisler>
# --no-recreate: var olan (durmuş) container yeniden oluşturulmaz, yalnız başlar;
# --no-build: kayıt defterine gidilmez. Volume'a dokunulmaz.
set -euo pipefail
. "$(dirname "$0")/dev/lib.sh"

ES=""; DEPLOY=""
for a in "$@"; do
  case "$a" in
    --es) ES="--es" ;;
    --deploy) DEPLOY=1 ;;
    -h|--help) sed -n '2,13p' "$0"; exit 0 ;;
    *) dev_die "bilinmeyen bayrak: $a" ;;
  esac
done

t0=$(date +%s)
svcs=("${DEV_SERVICES[@]}")
[ -n "$ES" ] && svcs+=(elasticsearch)
cname() { if [ "$1" = coremetry ]; then echo coremetry; else echo "coremetry-$1"; fi; }

need=()
for s in "${svcs[@]}"; do
  st="$(dev_container_state "$(cname "$s")")"
  case "$st" in
    "running healthy"|"running none") ;;
    *) need+=("$s") ;;
  esac
done
api_ok=""; dev_wait_http "$DEV_URL/api/version" 0 '"version"' && api_ok=1

dev_compose_args $ES
if [ ${#need[@]} -eq 0 ] && [ -n "$api_ok" ]; then
  echo "stack already up (project $(dev_project)); nothing created"
else
  echo "starting: ${need[*]:-<api not answering>} (project $(dev_project))"
  "${DEV_COMPOSE[@]}" up -d --no-build --no-recreate "${svcs[@]}"
  dev_wait_http "$DEV_URL/api/version" 180 '"version"' || { echo "FAIL /api/version did not answer in 180s" >&2; exit 1; }
fi

if [ -n "$DEPLOY" ]; then
  "$(dirname "$0")/dev-deploy.sh" ${ES:+--es}
fi

# Durum: container, durum, bellek.
names=(); for s in "${svcs[@]}"; do names+=("$(cname "$s")"); done
stats="$(docker stats --no-stream --format '{{.Name}} {{.MemUsage}}' "${names[@]}" 2>/dev/null || true)"
for c in "${names[@]}"; do
  mem="$(awk -v n="$c" '$1==n{$1="";sub(/^ /,"");print}' <<<"$stats")"; mem="${mem:--}"
  printf '  %-26s %-16s %s\n' "$c" "$(dev_container_state "$c")" "$mem"
done
# Sapma uyarısı: çalışan uygulamanın log arka ucu bu yığının beklediği mi?
want=clickhouse; [ -n "$ES" ] && want=elasticsearch
have="$(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' coremetry 2>/dev/null | sed -n 's/^COREMETRY_LOGS_BACKEND=//p')"
[ "$have" = "$want" ] || echo "  note: coremetry COREMETRY_LOGS_BACKEND=${have:-?} (expected $want); scripts/dev-deploy.sh recreates it with docker-compose.dev.yml"
echo "version: $(curl -s -m 4 "$DEV_URL/api/version")"
echo "elapsed: $(( $(date +%s) - t0 ))s"
