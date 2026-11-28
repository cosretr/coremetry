#!/usr/bin/env bash
# dev-down.sh — yerel yığını DURDURUR (container ve volume silinmez). YALNIZ YEREL.
#
#   scripts/dev-down.sh [--es] [--dry-run]
#
# Çalıştırılan tek komut: docker compose -p <proje> … stop <servisler>.
# `down`, `down -v`, `rm`, volume ve prune komutları bu betikte YOK.
# --dry-run: komutu yalnız yazdırır.
set -euo pipefail
. "$(dirname "$0")/dev/lib.sh"

ES=""; DRY=""
for a in "$@"; do
  case "$a" in
    --es) ES="--es" ;;
    --dry-run) DRY=1 ;;
    -h|--help) sed -n '2,8p' "$0"; exit 0 ;;
    *) dev_die "bilinmeyen bayrak: $a" ;;
  esac
done
svcs=("${DEV_SERVICES[@]}")
[ -n "$ES" ] && svcs+=(elasticsearch)
dev_compose_args $ES
cmd=("${DEV_COMPOSE[@]}" stop "${svcs[@]}")
if [ -n "$DRY" ]; then
  printf 'would run:'; printf ' %q' "${cmd[@]}"; echo
  exit 0
fi
"${cmd[@]}"
echo "stopped (containers and volumes kept); start again: scripts/dev-up.sh"
