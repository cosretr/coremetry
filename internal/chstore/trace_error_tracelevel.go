package chstore

// trace_error_tracelevel.go — v0.10.1010 — Errors + attribute çipi: hata
// TRACE DÜZEYİNDE aranır (çip bir span'de, hata başka span'de).
//
// Operator-reported (prod, 2026-10-01): `function_code = …` çipiyle liste dolu
// ve satırların çoğu ERROR; "Errors" işaretlenince "Trace bulunamadı".
//
// Kök neden: arama yokken çipler WHERE'de SPAN düzeyindedir ve Errors, başka
// span yüklemi varken HAVING'e taşınır (v0.10.258) — ama o HAVING, WHERE'in
// bıraktığı span'lerde sayar: fiilî anlam "çipe uyan span'in KENDİSİ hatalı".
// Prod'da fonksiyon kodunu yalnız log-yayın span'leri taşıyor ve onlar hata
// vermiyor; hata aynı trace'in başka span'inde → her trace düşüyor. v0.10.1005
// (satır) ve v0.10.1008 (Root) ile aynı sınıfın üçüncü ucu.
//
// Karar — iki basamak, deterministik (sayfadan bağımsız):
//
//	① Kapsamda çipe uyan VE hatalı bir span VARSA → eski anlam aynen
//	   ("çipe uyan hatalı span"; v0.10.812'nin kesin yolu). Mevcut dolu
//	   sonuçların HİÇBİRİ değişmez.
//	② YOKSA → trace düzeyi: "çipe uyan span'i olan VE herhangi bir span'i
//	   hatalı olan trace". Eski cevap bu durumda kesin BOŞTU; artık dolu.
//
// ②'nin maliyeti iki indeksli taramanın kesişimi: çip tarafı terfi kolonu /
// kvh indeksiyle, hata tarafı idx_status ile budanır. Küçük taraf KÜME olur
// (tavanlı sayımla seçilir), büyük taraf o kümeye göre süzülür:
//
//	SELECT trace_id FROM spans WHERE <kapsam> AND <büyük taraf>
//	  AND trace_id GLOBAL IN (SELECT trace_id FROM spans
//	      WHERE <kapsam> AND <küçük taraf> ORDER BY time DESC LIMIT M)
//	ORDER BY time DESC LIMIT bütçe
//
// Küme tavanı M (traceErrJoinSetMax satır ≈ onlarca MB) — tam pencere
// GROUP BY trace_id (v0.10.341'in kaçındığı tarama) ve sınırsız GLOBAL IN
// (v0.10.238'in 241 sınıfı) YOK. Küçük taraf M'yi aşıyorsa küme en yeni M
// satırla sınırlanır ve cevap bunu RankedWithin ile ilan eder. Dönen id'ler
// (≤ traceStage2MaxIDs, en yeni önce) listenin CandidateIDs'i olur; hata
// doğrulandığı için liste aşamalarında HasError kapatılır — yoksa HAVING
// yine çipli span'lerde hata arar.
//
// Kapsam = pencere + servis + ortam + küme (errorFirstFilter ile aynı kapsam
// yüklemleri); arama / kök / RequireServices / süre liste aşamalarında koşar.

import (
	"context"
	"fmt"
	"time"
)

const (
	// traceErrJoinSetMax — kesişimin küme tarafındaki satır tavanı.
	traceErrJoinSetMax = 300_000

	traceErrModeSpan  = "span"  // çipe uyan hatalı span var → eski anlam
	traceErrModeTrace = "trace" // yok → trace düzeyi kesişim
)

// traceLevelErrorEligible — SAF: Errors + span-düzeyi çip, aday listesi yok.
func traceLevelErrorEligible(f TraceFilter) bool {
	return f.HasError && spanScopedChips(f) &&
		f.TraceID == "" && len(f.TraceIDs) == 0 && len(f.CandidateIDs) == 0
}

// traceErrScope — SAF: kesişimin iki tarafının ORTAK kapsamı (pencere, servis,
// ortam, küme). Çipler, hata, arama, kök, süre ve id listeleri düşer.
func traceErrScope(f TraceFilter) TraceFilter {
	lf := withoutChips(f)
	lf.Search = ""
	lf.RequireServices, lf.RootOnly = nil, false
	lf.TraceIDs, lf.CandidateIDs = nil, nil
	lf.MinMs, lf.MaxMs = 0, 0
	return lf
}

// traceErrWheres — SAF: (çip tarafı, hata tarafı, ikisi aynı span'de) WHERE'leri.
func traceErrWheres(f TraceFilter, clusterExpr string) (chip, errw, both whereClause) {
	scope := traceErrScope(f)
	chipF := scope
	chipF.Filters, chipF.FilterRoot, chipF.NoPromoted = f.Filters, f.FilterRoot, f.NoPromoted
	chip = buildGetTracesWhere(chipF, clusterExpr)
	errw = buildGetTracesWhere(scope, clusterExpr)
	errw.add(traceErrSpanPredicate)
	return chip, errw, traceErrBothWhere(f, clusterExpr)
}

// traceErrBothWhere — SAF (v0.10.1082): ① basamağının WHERE'i — kapsamda çipe
// uyan VE hatalı span. Listenin probu (traceErrWheres) ve /traces hacim
// şeridinin span kipi (trace_error_histogram.go) bu TEK fonksiyonu çağırır;
// iki yüzeyin çip çevirisi / hata yüklemi / kapsamı ayrışamaz.
func traceErrBothWhere(f TraceFilter, clusterExpr string) whereClause {
	chipF := traceErrScope(f)
	chipF.Filters, chipF.FilterRoot, chipF.NoPromoted = f.Filters, f.FilterRoot, f.NoPromoted
	both := buildGetTracesWhere(chipF, clusterExpr)
	both.add(traceErrSpanPredicate)
	return both
}

// traceErrSpanPredicate — span-düzeyi hata yüklemi; listenin HAVING'i
// (traceHasErrorHaving) ile aynı kolon ve değer.
const traceErrSpanPredicate = "status_code = 'error'"

// traceErrCountSQL — SAF: tavanlı satır sayımı (taraf seçimi / varlık probu).
func traceErrCountSQL(whereSQL string) string {
	return "SELECT count() FROM (SELECT 1 FROM spans " + whereSQL + " LIMIT ?) SETTINGS max_execution_time = 5"
}

// traceErrJoinSQL — SAF: büyük taraf, küçük tarafın (en yeni M satır) trace
// kümesine göre süzülür; en yeniden eskiye, tekilleme Go'da (akışkan).
// Yer tutucu sırası: dış WHERE, iç WHERE, M, bütçe.
func traceErrJoinSQL(outerWhere, innerWhere string) string {
	return `
		SELECT trace_id
		FROM spans ` + outerWhere + `
		  AND trace_id GLOBAL IN (
			SELECT trace_id FROM spans ` + innerWhere + `
			ORDER BY time DESC
			LIMIT ?)
		ORDER BY time DESC
		LIMIT ?
		SETTINGS max_execution_time = 15,
		         distributed_product_mode = 'global'`
}

// traceErrJoinSides — SAF: küçük taraf küme olur. Dönüş: (dış, iç, küme
// tavana çarptı mı). Eşitlikte hata tarafı küme (hatalar genelde nadir).
func traceErrJoinSides(chip, errw whereClause, nChip, nErr uint64) (outer, inner whereClause, capped bool) {
	if nChip < nErr {
		return errw, chip, nChip > traceErrJoinSetMax
	}
	return chip, errw, nErr > traceErrJoinSetMax
}

func (s *Store) traceErrCount(ctx context.Context, wc whereClause, limit int) (uint64, error) {
	var n uint64
	err := s.telemetryReadConn().QueryRow(ctx, traceErrCountSQL(wc.sql()), append(append([]any{}, wc.args...), limit)...).Scan(&n)
	return n, err
}

// traceLevelErrorCandidates — (id'ler, kip, tavanlı mı, hata). Kip "span" ise
// id'ler nil'dir ve çağıran eski yolu aynen koşar.
func (s *Store) traceLevelErrorCandidates(ctx context.Context, f TraceFilter) ([]string, string, bool, error) {
	chip, errw, both := traceErrWheres(f, s.clusterExpr())
	t0 := time.Now()
	nBoth, err := s.traceErrCount(ctx, both, 1)
	f.Explain.step("err-chip-probe", traceErrCountSQL(both.sql()), both.args, t0, int(nBoth), err)
	if err != nil {
		return nil, "", false, fmt.Errorf("trace-level error probe: %w", err)
	}
	if nBoth > 0 {
		return nil, traceErrModeSpan, false, nil
	}
	nErr, err := s.traceErrCount(ctx, errw, traceErrJoinSetMax+1)
	if err != nil {
		return nil, "", false, fmt.Errorf("trace-level error count: %w", err)
	}
	if nErr == 0 {
		return []string{}, traceErrModeTrace, false, nil
	}
	nChip, err := s.traceErrCount(ctx, chip, traceErrJoinSetMax+1)
	if err != nil {
		return nil, "", false, fmt.Errorf("trace-level chip count: %w", err)
	}
	if nChip == 0 {
		return []string{}, traceErrModeTrace, false, nil
	}
	outer, inner, capped := traceErrJoinSides(chip, errw, nChip, nErr)
	query := traceErrJoinSQL(outer.sql(), inner.sql())
	args := append(append([]any{}, outer.args...), inner.args...)
	args = append(args, traceErrJoinSetMax, traceStage2MaxIDs*errorFirstOverfetch)
	t1 := time.Now()
	rows, err := s.telemetryReadConn().Query(ctx, query, args...)
	if err != nil {
		f.Explain.step("err-trace-join", query, args, t1, 0, err)
		return nil, "", false, fmt.Errorf("trace-level error join: %w", err)
	}
	defer rows.Close()
	seen := make(map[string]struct{}, 256)
	ids := make([]string, 0, 256)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, "", false, err
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
		if len(ids) >= traceStage2MaxIDs {
			break
		}
	}
	err = rows.Err()
	f.Explain.step("err-trace-join", query, args, t1, len(ids), err)
	if err != nil {
		return nil, "", false, err
	}
	return ids, traceErrModeTrace, capped || len(ids) >= traceStage2MaxIDs, nil
}
