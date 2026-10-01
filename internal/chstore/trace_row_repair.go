package chstore

// trace_row_repair.go — v0.10.1005 — attribute çipli trace listesinde SATIR
// ONARIMI.
//
// Operator-reported (prod, 2026-10-01): /traces'te `function_code = …` çipiyle
// gelen satırların Name kolonu kök span'in adı yerine bir MQ "publish" span'inin
// adını gösteriyor; trace detayında kök HTTP span'i duruyor.
//
// Kök neden: arama YOKKEN çipler WHERE'de SPAN düzeyindedir (terfi kolonu /
// kvh indeksi budasın diye — buildGetTracesWhere, v0.10.341) ve liste satırı
// aynı WHERE'in bıraktığı span'lerden kurulur (buildGetTracesListSQL). Çipi
// kök span TAŞIMIYORSA — prod'da fonksiyon kodunu yalnız log-yayın span'leri
// taşıyor — satır yalnız eşleşen span'lerden oluşur:
//
//	Name / Service  → anyIf(kök) boş, any(name) = eşleşen bir span'in adı
//	Süre            → eşleşen span'lerin aralığı (21 ms; trace 25 ms)
//	Span sayısı     → eşleşen span sayısı (4; trace 12)
//
// v0.10.258 aynı sınıfı Errors için kapatmıştı (2. aşama satırı TÜM
// span'lerden kurar). Burada kural çiplere genellendi: ÇİP TRACE'İ SEÇER,
// SATIRI ŞEKİLLENDİRMEZ. Sayfa belli olduktan sonra (≤ sayfa boyutu id) satır
// alanları çipsiz WHERE ile yeniden kurulur — id listesi PREWHERE'de
// (idx_trace bloom), pencere sayfanın kendi zaman aralığı; maliyet pencereden
// değil id sayısından gelir (fillTraceExtras ile aynı sınıf). Servis / ortam /
// küme gibi diğer yüklemler AYNEN kalır: satır "çipler olmasaydı ne olacaksa"
// odur. Sıra korunur (sayfa üyeliği ve sıralama 1. geçişin kararıdır).

import (
	"context"
	"log"
	"time"
)

const (
	// traceRowRepairLead — kök span, eşleşen ilk span'den ÖNCE başlar; alt
	// sınır bu kadar geri açılır (traceExtrasToSlack ile aynı 5 dk).
	traceRowRepairLead = 5 * time.Minute
	// traceRowRepairMaxIDs — onarılan en büyük sayfa. CSV dışa aktarımı
	// (≤50k id) onarılmaz: o yol satırları olduğu gibi yazar.
	traceRowRepairMaxIDs = 500
)

// spanScopedChips — SAF: çipler WHERE'de span düzeyinde mi (satır yalnız
// eşleşen span'lerden kuruluyor)? Arama varken çipler HAVING'dedir
// (filtersTraceLevel) ve satır zaten tüm span'lerden kurulur; span-düzeyi
// problar (forceFiltersInWhere) eski şekli bilerek ister.
func spanScopedChips(f TraceFilter) bool {
	if f.forceFiltersInWhere || filtersTraceLevel(f) {
		return false
	}
	if len(f.Filters) > 0 {
		return true
	}
	return f.FilterRoot != nil && f.FilterRoot.hasPredicate()
}

// withoutChips — SAF: aynı süzgecin çipsiz, hata-WHERE'siz kopyası (satırı
// TÜM span'lerden kurmak için). Trace seçimi çoktan yapıldı.
func withoutChips(f TraceFilter) TraceFilter {
	f.Filters, f.FilterRoot = nil, nil
	f.HasError = false
	return f
}

// traceRowRepairBounds — SAF: onarım okumasının zaman aralığı. Alt sınır geri
// açılır (kök eşleşen span'den önce başlar), üst sınır geç span payı kadar ileri.
func traceRowRepairBounds(rows []TraceRow) (time.Time, time.Time) {
	from, to := traceExtrasBounds(rows)
	return from.Add(-traceRowRepairLead), to.Add(traceExtrasToSlack)
}

// mergeRepairedRows — SAF (tablo testli): onarılan alanları sayfaya yazar.
// SIRA ve satır kümesi DEĞİŞMEZ; onarım satırı olmayan trace olduğu gibi kalır
// (kök CH'de yoksa bile eski etiket — boş satırdan iyidir). Extras korunur.
func mergeRepairedRows(page, repaired []TraceRow) {
	by := make(map[string]*TraceRow, len(repaired))
	for i := range repaired {
		by[repaired[i].TraceID] = &repaired[i]
	}
	for i := range page {
		r, ok := by[page[i].TraceID]
		if !ok {
			continue
		}
		page[i].RootName, page[i].ServiceName, page[i].RootRoute = r.RootName, r.ServiceName, r.RootRoute
		page[i].StartTime, page[i].DurationMs = r.StartTime, r.DurationMs
		page[i].SpanCount, page[i].HasError, page[i].ErrorSpans = r.SpanCount, r.HasError, r.ErrorSpans
	}
}

// repairSpanScopedRows — sayfanın satırlarını çipsiz WHERE ile yeniden kurar
// (dosya başı). Yumuşak düşer: onarım okuması hata verirse liste eski
// satırlarıyla döner — etiket yanlış olabilir ama liste boş kalmaz.
func (s *Store) repairSpanScopedRows(ctx context.Context, rows []TraceRow, f TraceFilter) {
	if len(rows) == 0 || len(rows) > traceRowRepairMaxIDs || !spanScopedChips(f) {
		return
	}
	lf := withoutChips(f)
	lf.From, lf.To = traceRowRepairBounds(rows)
	lwc := buildGetTracesWhere(lf, s.clusterExpr())
	querySQL := buildGetTracesListSQLWith(stage2PrewhereSQL(len(rows))+lwc.sql(), "", "trace_start", "DESC", stage2Settings)
	args := make([]any, 0, len(rows)+len(lwc.args)+2)
	for _, r := range rows {
		args = append(args, r.TraceID)
	}
	args = append(args, lwc.args...)
	args = append(args, len(rows), 0)
	t0 := time.Now()
	qrows, err := s.telemetryReadConn().Query(ctx, querySQL, args...)
	if err != nil {
		f.Explain.step("row-repair", querySQL, args, t0, 0, err)
		log.Printf("[traces] satır onarımı düştü (%d id) — satırlar yalnız çiple eşleşen span'lerden: %v", len(rows), err)
		return
	}
	repaired, err := scanTraceListRows(qrows)
	qrows.Close()
	f.Explain.step("row-repair", querySQL, args, t0, len(repaired), err)
	if err != nil {
		log.Printf("[traces] satır onarımı okunamadı (%d id): %v", len(rows), err)
		return
	}
	mergeRepairedRows(rows, repaired)
}
