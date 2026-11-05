#!/usr/bin/env bash
# gate.sh — release kapı zinciri, token-tasarruflu çıktı (docs/claude/token-audit.md Faz 1 adım 1).
#
#   scripts/gate.sh fe   # tsc → eslint (yalnız error) → vitest → bundle:check
#   scripts/gate.sh go   # go build → go vet → go test
#   scripts/gate.sh all  # fe && go (ilk kırmızıda durur, && zinciri gibi)
#
# Çıktı: adım başına tek özet satırı; kırmızıda başarısız komut adı + hata
# satırları (en çok 40) + tam günlüğün yolu. Çıkış kodu = başarısız komutun
# GERÇEK kodu (filtre boru hattı değil, komut kendisi ölçülür; `|| echo` yok).
set -o pipefail
ROOT="$(git -C "$(dirname "$0")" rev-parse --show-toplevel)" || exit 2
LOGDIR="${TMPDIR:-/tmp}/coremetry-gate"
mkdir -p "$LOGDIR"
export NO_COLOR=1 FORCE_COLOR=0 CI=1

# step <ad> <filtre-fonksiyonu> <dizin> <komut...>
step() {
  local name="$1" filt="$2" dir="$3"; shift 3
  local log="$LOGDIR/$(echo "$name" | tr ' /:' '___').log"
  (cd "$dir" && "$@") >"$log" 2>&1
  local rc=$?
  if [ "$rc" -ne 0 ]; then
    echo "FAIL $name (exit $rc)"
    "$filt" fail <"$log" | head -40
    echo "log: $log"
    return "$rc"
  fi
  "$filt" ok <"$log"
  return 0
}

f_plain() { if [ "$1" = fail ]; then grep -v '^\s*$'; else echo "ok $name"; fi; }
f_tsc()   { if [ "$1" = fail ]; then grep -E 'error TS|error' ; else echo "ok tsc"; fi; }
f_eslint(){ if [ "$1" = fail ]; then grep -E 'error|^/' | grep -vE '^\s*$'; else echo "ok eslint"; fi; }
f_vitest(){
  # passing satırlar (✓) ad içinde × taşıyabilir — onları ele.
  grep -E 'FAIL|✗|×|^[[:space:]]*(Test Files|Tests)[[:space:]]' | grep -v '✓'
}
f_bundle(){
  if [ "$1" = fail ]; then grep -E '^bundle-budget:|error|^\|' | tail -10
  else grep -E '^\|' | tail -1; fi
}
f_gotest(){
  local okc; okc=$(grep -c '^ok' "$log" 2>/dev/null)
  echo "go test: ${okc:-0} ok"
  if [ "$1" = fail ]; then grep -E '^(--- )?FAIL|^\s+\S+_test\.go:[0-9]+:|^panic:|\[build failed\]'; fi
}

fe() {
  local d="$ROOT/frontend"
  step tsc     f_tsc    "$d" npx tsc --noEmit &&
  step eslint  f_eslint "$d" npx eslint src --quiet &&
  step vitest  f_vitest "$d" env TZ=UTC npx vitest run &&
  step bundle  f_bundle "$d" npm run bundle:check
}
gocheck() {
  step "go build" f_plain  "$ROOT" go build ./... &&
  step "go vet"   f_plain  "$ROOT" go vet ./... &&
  step "go test"  f_gotest "$ROOT" go test ./...
}

case "${1:-all}" in
  fe)  fe ;;
  go)  gocheck ;;
  all) fe && gocheck ;;
  *)   echo "usage: scripts/gate.sh [fe|go|all]" >&2; exit 2 ;;
esac
