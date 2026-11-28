#!/usr/bin/env bash
# Çalışma ağacının "doğrulandı" damgası.
#   verify-stamp.sh hash   → ürün kodundaki commit'siz değişikliğin özetini yazar (değişiklik yoksa boş)
#   verify-stamp.sh write  → o özet için damga bırakır (/dogrula bitince çağırır)
#   verify-stamp.sh check  → damga varsa 0, yoksa 1 (değişiklik yoksa 0)
# Ürün kodu: internal/ cmd/ frontend/src/ migrations/ charts/ main.go. Damgalar
# ${TMPDIR:-/tmp}/coremetry-dev/ altında; repo içine hiçbir şey yazılmaz.
set -uo pipefail
root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
cd "$root" || exit 0
paths=(internal cmd frontend/src migrations charts main.go)
dir="${TMPDIR:-/tmp}/coremetry-dev"
tree_hash() {
  local tracked untracked
  tracked=$(git diff HEAD -- "${paths[@]}" 2>/dev/null)
  untracked=$(git ls-files --others --exclude-standard -- "${paths[@]}" 2>/dev/null | while IFS= read -r f; do
    printf '%s ' "$f"; shasum < "$f" 2>/dev/null | cut -c1-12; done)
  [ -z "$tracked$untracked" ] && return 0
  printf '%s\n%s' "$tracked" "$untracked" | shasum | cut -c1-16
}
h=$(tree_hash)
case "${1:-check}" in
  hash)  printf '%s\n' "$h" ;;
  write) [ -n "$h" ] && mkdir -p "$dir" && : > "$dir/verified-$h"; printf 'damga: %s\n' "${h:-değişiklik yok}" ;;
  check) [ -z "$h" ] && exit 0; [ -e "$dir/verified-$h" ] ;;
  *) echo "kullanım: verify-stamp.sh hash|write|check" >&2; exit 2 ;;
esac
