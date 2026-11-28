#!/usr/bin/env bash
# demo-k8s-clusters.sh — v0.10.2486, YALNIZ YEREL.
#
# Go demo'nun K8s metrik simülatörü (cmd/demo/k8s_metrics.go,
# DEMO_K8S_METRICS_URL) serileri yerel VictoriaMetrics'e `cluster="<ad>"`
# etiketiyle yazar. Bu betik demo'nun cluster'larını Coremetry'ye Remote
# Cluster olarak kaydeder — TEK querier önünde N cluster modeli
# (thanosLabelName=cluster, değer = ad, kaynak manual), span cluster değeri =
# ad (k8s.cluster.name / openshift.cluster.name). Uygulamanın kendi admin
# API'si (PUT /api/settings/thanos → audit `settings.thanos.update`);
# ClickHouse'a elle yazmaz. İdempotent: kayıtlar zaten istenen biçimdeyse
# PUT atmaz; başka (elle eklenmiş) cluster'lara dokunmaz.
#
# Kullanım (repo kökünden, yığın ayaktayken):
#   scripts/demo-k8s-clusters.sh            # kaydet / doğrula
#   scripts/demo-k8s-clusters.sh --remove   # demo kayıtlarını sil
# Ortam: COREMETRY_URL (http://localhost:8088), COREMETRY_ADMIN_EMAIL /
# COREMETRY_ADMIN_PASSWORD (yoksa COREMETRY_INITIAL_ADMIN / _PASSWORD,
# yoksa compose varsayılanları), DEMO_K8S_QUERY_URL (uygulamanın göreceği
# VM adresi; http://victoriametrics:8428), DEMO_K8S_CLUSTERS (boşlukla).
# Yerel olmayan bir COREMETRY_URL'yi reddeder (DEMO_K8S_ALLOW_REMOTE=1 hariç).
set -euo pipefail

URL="${COREMETRY_URL:-http://localhost:8088}"
export DK_EMAIL="${COREMETRY_ADMIN_EMAIL:-${COREMETRY_INITIAL_ADMIN:-admin@coremetry.local}}"
export DK_PASS="${COREMETRY_ADMIN_PASSWORD:-${COREMETRY_INITIAL_PASSWORD:-admin}}"
export DK_VM="${DEMO_K8S_QUERY_URL:-http://victoriametrics:8428}"
export DK_CLUSTERS="${DEMO_K8S_CLUSTERS:-prod-eu-west prod-eu-central prod-us-east prod-ap-south dr-eu-west}"
export DK_MODE="${1:-apply}"

case "$URL" in
  http://localhost:*|http://127.0.0.1:*) ;;
  *) [[ "${DEMO_K8S_ALLOW_REMOTE:-}" == "1" ]] || { echo "refusing non-local COREMETRY_URL=$URL (demo-only script)" >&2; exit 2; } ;;
esac

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

python3 -c 'import json,os;print(json.dumps({"email":os.environ["DK_EMAIL"],"password":os.environ["DK_PASS"]}))' >"$TMP/login.json"
code=$(curl -s -o "$TMP/session.json" -w '%{http_code}' -X POST "$URL/api/auth/login" \
  -H 'content-type: application/json' --data-binary @"$TMP/login.json")
[[ "$code" == "200" ]] || { echo "login failed (HTTP $code)" >&2; exit 1; }
TOKEN=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["token"])' "$TMP/session.json")
printf 'Authorization: Bearer %s\n' "$TOKEN" >"$TMP/auth.hdr"

curl -sf -H @"$TMP/auth.hdr" "$URL/api/settings/thanos" >"$TMP/cur.json"

python3 - "$TMP/cur.json" "$TMP/next.json" <<'PY'
import json, os, sys
cur = json.load(open(sys.argv[1])).get("clusters") or []
names = os.environ["DK_CLUSTERS"].split()
vm, mode = os.environ["DK_VM"], os.environ["DK_MODE"]
by = {c["name"]: c for c in cur}
want = lambda n: {"url": vm, "enabled": True, "authType": "none",
                  "thanosLabelName": "cluster", "thanosLabelValue": n,
                  "thanosLabelSource": "manual", "spanClusterValues": [n]}
changed = False
out = []
for c in cur:
    if c["name"] in names and mode == "--remove":
        changed = True
        continue
    out.append(c)
if mode != "--remove":
    for n in names:
        c = by.get(n)
        if c is None:
            c = {"name": n}
            out.append(c)
        for k, v in want(n).items():
            if c.get(k) != v:
                c[k] = v
                changed = True
json.dump({"clusters": out, "changed": changed}, open(sys.argv[2], "w"))
print(("changes: " if changed else "already up to date: ") + ", ".join(names) + (" (remove)" if mode == "--remove" else f" -> {vm}"))
PY

if python3 -c 'import json,sys;sys.exit(0 if json.load(open(sys.argv[1]))["changed"] else 1)' "$TMP/next.json"; then
  python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));d.pop("changed");print(json.dumps(d))' "$TMP/next.json" >"$TMP/put.json"
  code=$(curl -s -o "$TMP/put.out" -w '%{http_code}' -X PUT -H @"$TMP/auth.hdr" \
    -H 'content-type: application/json' --data-binary @"$TMP/put.json" "$URL/api/settings/thanos")
  [[ "$code" == "200" ]] || { echo "PUT /api/settings/thanos failed (HTTP $code): $(head -c 300 "$TMP/put.out")" >&2; exit 1; }
  echo "saved (audit: settings.thanos.update)"
fi
python3 -c '
import json,sys
for c in json.load(open(sys.argv[1])).get("clusters") or []:
    print("  %-16s enabled=%-5s url=%s label=%s=%s" % (c["name"], c.get("enabled"), c.get("url"), c.get("thanosLabelName",""), c.get("thanosLabelValue","")))
' <(curl -sf -H @"$TMP/auth.hdr" "$URL/api/settings/thanos")
