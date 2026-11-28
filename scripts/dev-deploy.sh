#!/usr/bin/env bash
# dev-deploy.sh — bu ağacın kodunu yerel coremetry container'ına koyar; docker build YOK. YALNIZ YEREL.
#
#   scripts/dev-deploy.sh [--es] [--version <etiket>] [--skip-fe]
#
# Adımlar (emsal: operatörün repo dışındaki local-deploy.sh'i):
#   1. frontend: `npm run build` (bu ağacın frontend/node_modules'ı gerekir; --skip-fe atlar)
#   2. host'ta linux binary: CGO_ENABLED=0 GOOS=linux GOARCH=<docker arch> go build
#   3. coremetry:latest'ten geçici container → binary'yi /app/coremetry'ye kopyala → commit
#   4. compose (dev override'lı, aynı proje adı) ile YALNIZ coremetry yeniden oluşturulur
#      (--no-deps --no-build --force-recreate); /api/version yeni sürümü verene dek bekle.
# Aynı anda tek deploy: ${TMPDIR}/coremetry-dev/deploy.lock (mkdir kilidi).
set -euo pipefail
. "$(dirname "$0")/dev/lib.sh"

ES=""; SKIP_FE=""; VER=""
while [ $# -gt 0 ]; do
  case "$1" in
    --es) ES="--es" ;;
    --skip-fe) SKIP_FE=1 ;;
    --version) VER="${2:?--version <etiket>}"; shift ;;
    -h|--help) sed -n '2,12p' "$0"; exit 0 ;;
    *) dev_die "bilinmeyen bayrak: $1" ;;
  esac
  shift
done
[ -n "$VER" ] || VER="$(git -C "$DEV_ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)-local"

dev_state_dir
LOCK="$DEV_STATE_DIR/deploy.lock"
mkdir "$LOCK" 2>/dev/null || dev_die "başka bir deploy sürüyor ($LOCK; takılı kaldıysa rmdir)"
out="$(mktemp -d)"
trap 'rm -rf "$out"; rmdir "$LOCK" 2>/dev/null; docker rm -f coremetry-swap >/dev/null 2>&1 || true' EXIT

t0=$(date +%s)
if [ -z "$SKIP_FE" ]; then
  [ -d "$DEV_ROOT/frontend/node_modules" ] || dev_die "frontend/node_modules yok ($DEV_ROOT); --skip-fe ya da npm ci"
  (cd "$DEV_ROOT/frontend" && VITE_APP_VERSION="$VER" npm run build >"$out/fe.log" 2>&1) \
    || { tail -20 "$out/fe.log"; dev_die "frontend build failed"; }
  echo "ok frontend build"
fi
arch="$(docker version --format '{{.Server.Arch}}')"
(cd "$DEV_ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -ldflags="-s -w -X main.Version=$VER" -o "$out/coremetry" .)
echo "ok go build (linux/$arch)"

docker rm -f coremetry-swap >/dev/null 2>&1 || true
docker create --name coremetry-swap coremetry:latest >/dev/null
docker cp "$out/coremetry" coremetry-swap:/app/coremetry
docker commit coremetry-swap coremetry:latest >/dev/null
docker rm coremetry-swap >/dev/null
echo "ok image coremetry:latest updated"

dev_compose_args $ES
"${DEV_COMPOSE[@]}" up -d --no-deps --no-build --force-recreate coremetry
dev_wait_http "$DEV_URL/api/version" 200 "$VER" || dev_die "/api/version did not report $VER in 200s"
echo "deployed $VER in $(( $(date +%s) - t0 ))s: $(curl -s -m 4 "$DEV_URL/api/version")"
