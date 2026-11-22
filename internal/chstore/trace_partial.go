package chstore

// trace_partial.go — v0.10.2466 (Operator-reported 2026-11-22): Traces
// listesi bütçeyi (max_execution_time) aşınca HTTP 500 yerine kısmi sonuç.
//
// timeout_overflow_mode='break' BİLİNÇLİ kullanılmıyor: liste sorgusu
// GROUP BY trace_id + ORDER BY trace_start; break, kesildiği anda hash
// tablosunda ne varsa onu döndürür — trace'ler YARIM toplanır (span sayısı,
// süre eksik) ve "en yeni" sırası taranan granüllere bağlı olur. Yani anlam
// izin vermiyor. Kısmi sonuç yenilik basamaklarından gelir
// (trace_raw_probe.go): basamağın trace_start ≥ floor satırları tam
// taramayla bire bir aynı, eksik olan yalnız floor'dan eski trace'ler.

import (
	"context"
	"time"
)

// TracePartial — GetTraces'in kısmi sonuç OUT parametresi.
type TracePartial struct {
	// Hit — okuma bütçesi aşıldı; liste tam pencereyi KAPSAMIYOR.
	Hit bool
	// CoveredFrom — kesin kapsanan alt sınır (bu andan SONRA başlayan
	// trace'ler eksiksiz). Sıfır = hiçbir basamak tamamlanmadı (liste boş).
	CoveredFrom time.Time
	// Err — bütçe hatasının metni (teşhis).
	Err string
}

func (p *TracePartial) mark(coveredFrom time.Time, err error) {
	if p == nil {
		return
	}
	p.Hit = true
	if coveredFrom.After(p.CoveredFrom) {
		p.CoveredFrom = coveredFrom
	}
	if err != nil && p.Err == "" {
		p.Err = err.Error()
	}
}

// traceTimeoutAsPartial — GetTraces'in son defer'i: Partial istenmişse
// bütçe aşımı hatası boş kısmi sonuca döner (hiçbir basamak tamamlanmadı);
// kısmi işaretlenen her sonuç CoSRE tur kayıtçısına da yazılır.
func traceTimeoutAsPartial(ctx context.Context, p *TracePartial, rows []TraceRow, hasMore bool, err error) ([]TraceRow, bool, error) {
	if p == nil {
		return rows, hasMore, err
	}
	if err != nil {
		if !isResourceExhaustion(err) {
			return rows, hasMore, err
		}
		p.mark(time.Time{}, err)
		rows, hasMore, err = []TraceRow{}, false, nil
	}
	if p.Hit {
		NoteReadTimeout(ctx)
	}
	return rows, hasMore, err
}
