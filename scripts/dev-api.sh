#!/usr/bin/env bash
# dev-api.sh — yerel Coremetry API'sini oturum çereziyle çağırır. YALNIZ YEREL.
#
#   scripts/dev-api.sh [GET] </api/yol>              # varsayılan: GET, ilk 60 satır
#   scripts/dev-api.sh --lines 200 /api/services
#   scripts/dev-api.sh --write POST /api/x --data '{"a":1}'   # GET dışı YALNIZ --write ile
#
# Çıktı: "HTTP <kod> <ms>ms <bayt>B <yöntem> <yol>" + gövde (jq varsa biçimli),
# kırpılmış. Çıkış kodu: 2xx → 0, diğer → 1, kullanım hatası → 2.
# Oturum: scripts/dev/lib.sh (çerez kavanozu 0600; kimlik bilgisi basılmaz).
set -euo pipefail
. "$(dirname "$0")/dev/lib.sh"

WRITE=""; LINES=60; METHOD=""; P=""; DATA=""
while [ $# -gt 0 ]; do
  case "$1" in
    --write) WRITE=1 ;;
    --lines) LINES="${2:?}"; shift ;;
    --data) DATA="${2:?}"; shift ;;
    -h|--help) sed -n '2,10p' "$0"; exit 0 ;;
    GET|POST|PUT|PATCH|DELETE|HEAD) METHOD="$1" ;;
    /*|http://*|https://*) P="$1" ;;
    *) dev_die "bilinmeyen argüman: $1 (yol / ile başlamalı)" ;;
  esac
  shift
done
[ -n "$P" ] || dev_die "kullanım: scripts/dev-api.sh [GET] </api/yol>"
METHOD="${METHOD:-GET}"
if [ "$METHOD" != GET ] && [ -z "$WRITE" ]; then
  dev_die "$METHOD reddedildi: GET dışı yöntem yalnız açık --write ile"
fi
[ -z "$DATA" ] || [ -n "$WRITE" ] || dev_die "--data yalnız --write ile"
case "$P" in
  http*) dev_require_local "$P"; URL="$P" ;;
  *) URL="$DEV_URL$P" ;;
esac

dev_login
out="$(mktemp "$DEV_STATE_DIR/body.XXXXXX")"
trap 'rm -f "$out"' EXIT
args=(-s -m 60 -o "$out" -w '%{http_code} %{time_total} %{size_download}\n' -b "$DEV_JAR" -X "$METHOD")
[ -n "$DATA" ] && args+=(-H 'content-type: application/json' --data-binary "$DATA")
read -r code secs size < <(curl "${args[@]}" "$URL" || echo "000 0 0")
ms="$(python3 -c "print(round(float('$secs')*1000))")"
echo "HTTP $code ${ms}ms ${size}B $METHOD $P"
fmt="$out"
if command -v jq >/dev/null && jq . "$out" >"$out.fmt" 2>/dev/null; then fmt="$out.fmt"; fi
total="$(wc -l <"$fmt" | tr -d ' ')"
head -n "$LINES" "$fmt" | cut -c1-400
[ "$total" -le "$LINES" ] || echo "… ($((total - LINES)) more lines; --lines N)"
rm -f "$out.fmt"
case "$code" in 2*) exit 0 ;; *) exit 1 ;; esac
