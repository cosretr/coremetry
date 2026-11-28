#!/usr/bin/env bash
# dev-prom.sh — yerel VictoriaMetrics'e PromQL sorgusu. YALNIZ YEREL, salt okunur.
#
#   scripts/dev-prom.sh 'count(kube_pod_info)'                 # api/v1/query (anlık)
#   scripts/dev-prom.sh 'rate(x[5m])' --range 1h --step 60     # api/v1/query_range
#   scripts/dev-prom.sh '<PromQL>' --show 10                   # ilk 10 seri (varsayılan 5)
#
# Çıktı: durum, sonuç türü, seri sayısı, ilk birkaç seri (etiketler + son değer).
# Yalnız sorgu uçları çağrılır; adres COREMETRY_VM_URL (http://localhost:8428).
set -euo pipefail
. "$(dirname "$0")/dev/lib.sh"

Q=""; RANGE=""; STEP=60; SHOW=5
while [ $# -gt 0 ]; do
  case "$1" in
    --range) RANGE="${2:?}"; shift ;;
    --step) STEP="${2:?}"; shift ;;
    --show) SHOW="${2:?}"; shift ;;
    -h|--help) sed -n '2,9p' "$0"; exit 0 ;;
    *) [ -z "$Q" ] || dev_die "tek PromQL argümanı (tırnak içinde)"; Q="$1" ;;
  esac
  shift
done
[ -n "$Q" ] || dev_die "kullanım: scripts/dev-prom.sh '<PromQL>' [--range 1h] [--step 60]"
[[ "$STEP" =~ ^[0-9]+$ && "$SHOW" =~ ^[0-9]+$ ]] || dev_die "geçersiz --step/--show"

args=(-s -m 30 -G --data-urlencode "query=$Q")
if [ -n "$RANGE" ]; then
  secs="$(python3 -c '
import re,sys
m=re.fullmatch(r"(\d+)([smhd])",sys.argv[1]) or sys.exit("bad range")
print(int(m.group(1))*{"s":1,"m":60,"h":3600,"d":86400}[m.group(2)])' "$RANGE")" || dev_die "--range örn. 30m, 1h, 2d"
  now="$(date +%s)"
  ep="query_range"; args+=(--data-urlencode "start=$((now - secs))" --data-urlencode "end=$now" --data-urlencode "step=$STEP")
else
  ep="query"
fi
t0="$(dev_ms)"
body="$(curl "${args[@]}" -w '\n%{http_code}' "$DEV_VM_URL/api/v1/$ep")" || { echo "FAIL VictoriaMetrics unreachable at $DEV_VM_URL" >&2; exit 1; }
code="${body##*$'\n'}"; body="${body%$'\n'*}"
ms=$(( $(dev_ms) - t0 ))
rc=0
SHOW="$SHOW" python3 -c '
import json,os,sys
try: d=json.loads(sys.stdin.read())
except Exception as e: print("non-JSON response"); sys.exit(1)
if d.get("status")!="success":
    print("status:",d.get("status"),"error:",d.get("errorType",""),d.get("error","")); sys.exit(1)
r=d["data"]["result"]; t=d["data"]["resultType"]
print("resultType: %s  series: %d" % (t, len(r) if isinstance(r,list) else 1))
if t=="scalar" or t=="string": print("  value:",r[1]); sys.exit()
for s in r[:int(os.environ["SHOW"])]:
    m=s.get("metric",{}); name=m.pop("__name__","")
    lab=",".join("%s=\"%s\"" % kv for kv in sorted(m.items()))
    v=s["value"][1] if "value" in s else ("%s (%d points)" % (s["values"][-1][1], len(s["values"])) if s.get("values") else "-")
    print("  %s{%s} %s" % (name, lab[:200], v))
if isinstance(r,list) and len(r)>int(os.environ["SHOW"]): print("  … %d more (--show N)" % (len(r)-int(os.environ["SHOW"])))
' <<<"$body" || rc=$?
echo "-- HTTP $code ${ms}ms $ep" >&2
exit "$rc"
