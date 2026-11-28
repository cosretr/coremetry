#!/usr/bin/env bash
# dev-shot.sh — yerel sayfanın redhat + dark temada tam sayfa görüntüsü. YALNIZ YEREL.
#
#   scripts/dev-shot.sh "/services?range=1h" services
#   scripts/dev-shot.sh /problems --themes "dark" --allow-console-errors
#
# Çıktı e2e/shots/<ad>-<tema>.png (+ önceki .prev.png, + .json: konsol hataları,
# başarısız istekler, fark oranı). Satır başına: yol, boyut, konsol hatası,
# başarısız istek, fark oranı, süre. Çıkış 1: konsol hatası > 0 (gevşet:
# --allow-console-errors), giriş ekranına düşme ya da tema uygulanmaması.
# playwright-core: bu ağacın frontend/node_modules → ana kopyanınki →
# PLAYWRIGHT_CORE_DIR → .claude/worktrees/*/frontend/node_modules (ilk bulunan).
# Tarayıcı ~/Library/Caches/ms-playwright'tan; indirme yapılmaz.
set -euo pipefail
. "$(dirname "$0")/dev/lib.sh"

P=""; NAME=""; THEMES="redhat dark"; ALLOW=""
while [ $# -gt 0 ]; do
  case "$1" in
    --themes) THEMES="${2:?}"; shift ;;
    --allow-console-errors) ALLOW=1 ;;
    -h|--help) sed -n '2,13p' "$0"; exit 0 ;;
    /*) P="$1" ;;
    *) [ -z "$NAME" ] || dev_die "fazla argüman: $1"; NAME="$1" ;;
  esac
  shift
done
[ -n "$P" ] || dev_die "kullanım: scripts/dev-shot.sh </yol> [ad]"
[ -n "$NAME" ] || NAME="$(printf '%s' "$P" | sed 's/[?#].*//; s#^/##; s#[^A-Za-z0-9_-]#_#g')"; NAME="${NAME:-root}"
[[ "$NAME" =~ ^[A-Za-z0-9_-]+$ ]] || dev_die "ad yalnız [A-Za-z0-9_-]"

COMMON="$(dirname "$(git -C "$DEV_ROOT" rev-parse --path-format=absolute --git-common-dir)")"
pw=""
for d in "$DEV_ROOT/frontend/node_modules/playwright-core" "$COMMON/frontend/node_modules/playwright-core" \
         "${PLAYWRIGHT_CORE_DIR:-/nonexistent}" "$COMMON"/.claude/worktrees/*/frontend/node_modules/playwright-core; do
  [ -f "$d/package.json" ] && { pw="$d"; break; }
done
[ -n "$pw" ] || dev_die "playwright-core bulunamadı (frontend/node_modules; npm ci gerekir)"

dev_login
t0="$(dev_ms)"
rc=0
SHOT_PW_CORE="$pw" SHOT_BASE="$DEV_URL" SHOT_PATH="$P" SHOT_NAME="$NAME" SHOT_JAR="$DEV_JAR" \
SHOT_OUT="$DEV_ROOT/e2e/shots" SHOT_THEMES="$THEMES" SHOT_ALLOW_CONSOLE="${ALLOW:+1}" \
  node "$DEV_ROOT/e2e/shot.mjs" || rc=$?
echo "-- exit $rc, $(( $(dev_ms) - t0 ))ms, playwright-core $(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' "$pw/package.json" | head -1) ($pw)" >&2
exit "$rc"
