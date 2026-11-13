#!/usr/bin/env bash
# cosre-contracts.sh — v0.10.2108 (CoSRE kalite döngüsü D3,
# docs/cosre/quality-loop-audit.md §3.2–3.3): modelsiz CoSRE sözleşme kapısı.
#
#   scripts/cosre-contracts.sh check   # adlı alt küme (make evalset-check)
#   scripts/cosre-contracts.sh rest    # go test ./... EKSİ bu alt küme
#
# Girdi YALNIZ repodaki dosyalar: (1) gömülü evalset vakalarının şema
# geçerliliği, (2) testdata/cosre/ui-stream-v1* fikstürleri üzerinde uistream
# altınları + stream_contract denetimi, (3) rubrik birim testleri. Prod koşu
# artefaktı OKUNMAZ. Model ve ağ YOK — internal/api/cosre_contracts_test.go
# bu betiği ve koşturduğu testleri pinler.
#
# Çift koşu yok: gate.sh ve ci.yml `go test ./...` yerine `rest`i koşar —
# iki paket bütün olarak listeden çıkar, api adları -skip ile atlanır;
# check + rest = go test ./... (eksiksiz, bir kez).
set -euo pipefail
ROOT="$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"
cd "$ROOT"
# Model replay değişkenleri bu süreçte asla görünmesin (belt-and-braces).
unset COREMETRY_EVAL_BASE_URL COREMETRY_EVAL_MODEL COREMETRY_EVAL_OUT

PKGS='./internal/ai/evalrubric/ ./internal/cosre/uistream/'
API_RUN='^(TestEvalsetFixturesValid|TestEvalPartSinkMatchesEmitterAllFixtures|TestStreamContractFixtures|TestEvalStreamContract|TestEvalStreamContractInputNA|TestCosreContractsNeverReachModel)$'
MOD="$(go list -m)"

case "${1:-check}" in
  check)
    # shellcheck disable=SC2086
    go test $PKGS
    go test ./internal/api/ -run "$API_RUN"
    ;;
  rest)
    # shellcheck disable=SC2046
    go test -skip "$API_RUN" $(go list ./... | grep -v -x -e "$MOD/internal/ai/evalrubric" -e "$MOD/internal/cosre/uistream")
    ;;
  *)
    echo "kullanım: scripts/cosre-contracts.sh check|rest" >&2
    exit 2
    ;;
esac
