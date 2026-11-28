#!/usr/bin/env bash
# dev-ch.sh — yerel ClickHouse'ta SALT OKUNUR sorgu. YALNIZ YEREL.
#
#   scripts/dev-ch.sh "SELECT count() FROM spans WHERE time > now() - INTERVAL 5 MINUTE"
#   scripts/dev-ch.sh --timeout 30 --rows 1000 --format TSV "<SQL>"
#
# Çalışan coremetry-clickhouse container'ında clickhouse-client --readonly=1,
# veritabanı coremetry, max_execution_time (10) ve max_result_rows (200,
# taşarsa kırpılır). Yalnız SELECT / WITH / SHOW / DESCRIBE / DESC / EXPLAIN /
# EXISTS ile başlayan TEK ifade kabul edilir; gerisi reddedilir (çıkış 2).
set -euo pipefail
. "$(dirname "$0")/dev/lib.sh"

MET=10; ROWS=200; FMT=PrettyCompactMonoBlock; SQL=""
while [ $# -gt 0 ]; do
  case "$1" in
    --timeout) MET="${2:?}"; shift ;;
    --rows) ROWS="${2:?}"; shift ;;
    --format) FMT="${2:?}"; shift ;;
    -h|--help) sed -n '2,10p' "$0"; exit 0 ;;
    *) [ -z "$SQL" ] || dev_die "tek SQL argümanı (tırnak içinde)"; SQL="$1" ;;
  esac
  shift
done
[ -n "$SQL" ] || dev_die "kullanım: scripts/dev-ch.sh \"<SELECT …>\""
[[ "$MET" =~ ^[0-9]+$ && "$ROWS" =~ ^[0-9]+$ && "$FMT" =~ ^[A-Za-z]+$ ]] || dev_die "geçersiz --timeout/--rows/--format"

# Baştaki boşluk ve yorumları at, ilk kelimeyi denetle.
first="$(python3 -c '
import re,sys
s=sys.argv[1]
s=re.sub(r"^(\s+|--[^\n]*\n|/\*.*?\*/)*","",s,flags=re.S)
body=s.rstrip().rstrip(";")
if ";" in body: print("MULTI"); sys.exit()
m=re.match(r"[A-Za-z]+",s); print((m.group(0) if m else "").upper())' "$SQL")"
case "$first" in
  SELECT|WITH|SHOW|DESCRIBE|DESC|EXPLAIN|EXISTS) ;;
  MULTI) dev_die "reddedildi: birden çok ifade" ;;
  *) dev_die "reddedildi: '${first:-?}' — yalnız SELECT/WITH/SHOW/DESCRIBE/EXPLAIN/EXISTS" ;;
esac

t0="$(dev_ms)"
set +e
dev_ch_query "$SQL" "$FMT" "$MET" "$ROWS"
rc=$?
set -e
echo "-- exit $rc, $(( $(dev_ms) - t0 ))ms (readonly=1, max_execution_time=$MET, max_result_rows=$ROWS)" >&2
exit "$rc"
