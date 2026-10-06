package chstore

// filterexpr_opgroup.go — v0.10.1115 — operasyon ŞEKLİ süzgeci (`op_group`).
//
// Operator-approved queue item (Service › Operations, Normalized kip): satıra
// tıklayınca /traces BOŞ liste (ve boş histogram şeridi) açıyordu. Normalized
// kipte satırlar operasyon ŞEKİLLERİ — spans.op_group kolonu, ingest'te
// templater.NormalizeOperation üretir (`GET /users/:id`). Pivot ise
// `name = <şekil>` çipi taşıyordu: kesin ad eşitliği, hiçbir gerçek span adı
// (`GET /users/8421`) şekle eşit değil → sıfır satır.
//
// Çözüm: `op_group` bir süzgeç anahtarı; spans süzgeç derleyicisinde kolona
// çözülür (LowCardinality, sözlük karşılaştırması):
//   - yalnız =, !=, IN, NOT IN (şekil bir KİMLİK; LIKE / regex / aralık /
//     EXISTS anlamsız → sınırda 400, db_stmt_hash emsali v0.10.1093) — kolon
//     olsun olmasın aynı kısıt;
//   - değer bağlanır (`op_group = ?`), metne gömülmez.
//
// Kolon YOKSA (dış Distributed `spans`, cluster_name boş — store.go
// hasOpGroupCol probu) anahtar `name` anahtarıyla BİREBİR aynı derlenir (aynı
// op, aynı değerler: `name = ?` / `name IN (…)`), 400 YOK; süreç başına bir kez
// INFO loglanır. Gerekçe: o kurulumda op_group değerinin TEK üreticisi
// Normalized tablosudur ve orada tablo ham span adlarını taşır
// (GetOperationSummary normalized → false düşüşü, v0.8.186; Explore/dashboard
// groupBy=op_group da groupKeyExpr'de `name`e düşer) — yani doğru kolon
// `name`. Bugün çalışan "Normalized satırı → name = <değer>" tıklaması bu
// kurulumda kırılmaz. Elle yazılmış bir ŞEKİL orada boş liste verir: daha DAR,
// asla daha GENİŞ (v0.9.269 sınıfı düşen süzgeç değil) ve kolon anılmaz
// (code 47 → 500 değil). MATERIALIZED ifade fallback'i yok: şekil Go'da
// (NormalizeOperation) üretilir, SQL'de karşılığı yok.
//
// Okuma yolları — hepsi bu derleyiciden geçer (spansSQL):
//   - /traces liste ham WHERE'i (buildGetTracesWhere) ve arama + çip
//     birlikteyken trace düzeyi HAVING (traceLevelFilterHaving);
//   - Errors şeridi / iki basamaklı hata (traceErrBothWhere, v0.10.1082);
//   - trace_summary_5m hızlı yolu: herhangi bir çip MV'yi diskalifiye eder
//     (tracesMVEligible `len(Filters) == 0`) → ham yol, span düzeyi çip
//     (spanScopedChips: satır onarımı + kök sorusu daraltılmamış kaynaktan);
//   - hacim şeridi (QuerySpanMetricMulti): op-MV kapısı (operationMVGate) ve
//     dar rollup (narrowFilterCol) op_group'u boyut olarak tanımaz → ham spans
//     (zaman sınırı + LIMIT + max_execution_time).
// metric_points yolu (SQLForMetricPoints) değişmedi: orada kolon yok.

import (
	"fmt"
	"log"
	"strings"
	"sync/atomic"
)

// OpGroupFilterKey — süzgeç anahtarı; FE lib/filterQuery.ts OP_GROUP_FILTER_KEY.
const OpGroupFilterKey = "op_group"

// opGroupColReady — spans.op_group bu kurulumda okunabilir mi (migrate,
// hasOpGroupCol probu + self-heal). FilterExpr.SQL() saf ve Store'u görmez;
// bayrak stmtHashColReady emsali paket düzeyinde.
var opGroupColReady atomic.Bool

// opGroupFallbackLogged — `name` düşüşünün INFO satırı süreç başına bir kez
// (CompareAndSwap; test sıfırlayabilsin diye sync.Once değil).
var opGroupFallbackLogged atomic.Bool

// validateOpGroup — SAF sınır doğrulaması: op ve değer sayısı. Kolon
// varlığından BAĞIMSIZ (kolonsuz kurulumda `name`e düşer, bkz. dosya başı).
// op zaten büyük harfe çevrilmiş ve allowedOps'tan geçmiş gelir.
func validateOpGroup(op string, vs []string) error {
	switch op {
	case "=", "!=":
		if len(vs) != 1 {
			return fmt.Errorf("op %s on %q needs exactly one value", op, OpGroupFilterKey)
		}
	case "IN", "NOT IN":
		if len(vs) == 0 {
			return fmt.Errorf("op %s on %q needs at least one value", op, OpGroupFilterKey)
		}
	default:
		return fmt.Errorf("key %q supports only =, !=, IN, NOT IN (got %q)", OpGroupFilterKey, op)
	}
	return nil
}

// opGroupFilterSQL — SAF (log hariç): `op_group` yan tümcesi (alias'lı
// self-join dahil). Kolon yoksa aynı op + değerlerle `name` kolonu.
func opGroupFilterSQL(alias, op string, vs []string, colReady bool) (string, []any, error) {
	if err := validateOpGroup(op, vs); err != nil {
		return "", nil, err
	}
	col := "op_group"
	if !colReady {
		col = "name"
		if opGroupFallbackLogged.CompareAndSwap(false, true) {
			log.Printf("[filter] INFO: spans.op_group column absent on this install (external Distributed spans, clickhouse.cluster_name unset) — " +
				"`op_group` filters compile as `name` (the Normalized operations table shows raw span names here); logged once per process")
		}
	}
	lhs := qualCol(alias, col)
	args := make([]any, len(vs))
	for i, v := range vs {
		args[i] = v
	}
	if op == "IN" || op == "NOT IN" {
		return lhs + " " + op + " (" + strings.TrimRight(strings.Repeat("?,", len(vs)), ",") + ")", args, nil
	}
	return lhs + " " + op + " ?", args, nil
}
