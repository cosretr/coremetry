#!/usr/bin/env bash
# Stop hook — "doğrulanmadan bitti" uyarısı.
#
# Ürün kodunda commit'siz değişiklik var ve bu çalışma ağacı için /dogrula
# damgası yoksa durmayı BİR KEZ engeller (exit 2: stderr Claude'a geri gider,
# Claude ya doğrular ya da son mesajında "doğrulanmadı" der). Aynı ağaç özeti
# için ikinci kez engellemez: döngü koruması damga dosyasıyladır (belgedeki
# stop_hook_active alanı doğrulanamadı; docs/claude/capability-audit.md §C).
# Kapı koşmaz; yalnız git diff'e ve damgaya bakar (milisaniyeler).
set -uo pipefail
proj="${CLAUDE_PROJECT_DIR:-$(pwd)}"
stamp="$proj/scripts/dev/verify-stamp.sh"
[ -x "$stamp" ] || exit 0
cd "$proj" 2>/dev/null || exit 0
h=$(bash "$stamp" hash 2>/dev/null)
[ -z "$h" ] && exit 0                      # ürün kodu değişmemiş
bash "$stamp" check 2>/dev/null && exit 0  # bu ağaç doğrulanmış
dir="${TMPDIR:-/tmp}/coremetry-dev"
mkdir -p "$dir" 2>/dev/null || exit 0
[ -e "$dir/warned-$h" ] && exit 0          # bu ağaç için zaten uyarıldı
: > "$dir/warned-$h"
cat >&2 <<'MSG'
Doğrulanmadan bitti: ürün kodunda commit'siz değişiklik var ve bu çalışma ağacı için /dogrula kanıtı yok.
Ya /dogrula ile kanıt üret (kapı, API yanıtı, sorgu, ekran görüntüsü), ya da son mesajında neyin doğrulanmadığını açıkça yaz.
MSG
exit 2
