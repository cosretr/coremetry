package anomaly

// sample_trace_logs.go — v0.10.1370 (operatör-bildirimli: "Teknik hatalar
// için Exceptions sayfasında trace incelemesi gibi loga bakmıyor — Ask CoSRE
// veya explain için").
//
// ── KUSUR ───────────────────────────────────────────────────────────────
//
// Exception açıklamasının log okuması trace'in ClickHouse'ta YÜKLENMESİNE
// bağlıydı: traceLogsEvidence maxT<=0'da hiç gitmez (v0.9.414 ES maliyet
// disiplini) ve zarf yalnız store.GetTrace'ten (CH) gelir. Oracle (`ora:`)
// gruplarında satırların trace id'leri çoğu zaman Coremetry'de YOK →
// oracleCoremetryTrace "" → log sorgusu HİÇ atılmıyor, prompt "Coremetry
// trace'i: yok" deyip modele çağıranı analiz etme diyordu. Oysa CoSRE trace
// kırılımı (readDrillLogs) span varlığına bakmadan TEK çoklu-trace sorgusuyla
// o trace id'lerin loglarını okuyor (ES trace id'yi alan şekillerinden bağımsız
// eşler — örneklemeden bağımsız).
//
// ── KARAR ───────────────────────────────────────────────────────────────
//
// Örnek trace'lerin (≤3, en yeni önce) logları span'e bağlı OLMADAN TEK
// sorguyla okunur: pencere örnek damgalarının [min−2 dk, max+2 dk], ≤60
// kayıt, 6 sn; trace başına ≤8 satır (logstore.GroupByTrace — kırılımla AYNI
// gruplama). Tek-trace bloğu (traceLogsEvidence) bayt bayt aynı kalır; yeni
// blok onun YANINA eklenir ve tek-trace'te zaten okunan id dışarıda bırakılır.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/promptfmt"
	"github.com/cilcenk/coremetry/internal/stackparse"
)

const (
	// v0.10.1370 — örnek trace logları bütçesi: TEK sorgu, ≤3 trace, ≤60
	// kayıt, pencere ±2 dk, 6 sn; trace başına ≤8 satır, gövde ≤300 rune.
	sampleLogTraces    = 3
	sampleLogFetch     = 60
	sampleLogPerTrace  = 8
	sampleLogPad       = 2 * time.Minute
	sampleLogTimeout   = 6 * time.Second
	sampleLogBodyRunes = 300

	// Kaynak etiketleri (v0.10.1370) — trace'in span'lerinin bulunduğu yer.
	sampleLogSrcCH    = "clickhouse"
	sampleLogSrcTempo = "tempo"
	sampleLogSrcNone  = "Coremetry'de yok"
)

// sampleLogRef — v0.10.1370: logu okunacak örnek trace. AtNs örneğin (span
// örneği ya da Oracle satırı) damgası — pencere bundan kurulur, span zarfından
// DEĞİL. Source prompt etiketi (clickhouse | tempo | Coremetry'de yok).
type sampleLogRef struct {
	TraceID string
	AtNs    int64
	Source  string
}

// sampleTraceLogsResult — v0.10.1370: okumanın özeti + prompt bloğu. Block
// boş = sorgu atılmadı ya da düştü (çağıranın prompt'u bayt bayt eski).
type sampleTraceLogsResult struct {
	Asked    int // sorulan trace sayısı (exclude ve tekrar sonrası, ≤3)
	WithLogs int // logu bulunan trace sayısı
	Lines    int // prompt'a giren toplam log satırı
	Block    string
}

// pickSampleLogRefs — SAF (v0.10.1370): boş / tekrar (büyük-küçük harf
// duyarsız) / exclude id'leri atar, sırayı korur, ≤n.
func pickSampleLogRefs(refs []sampleLogRef, exclude string, n int) []sampleLogRef {
	ex := strings.ToLower(strings.TrimSpace(exclude))
	seen := map[string]bool{}
	var out []sampleLogRef
	for _, r := range refs {
		id := strings.ToLower(strings.TrimSpace(r.TraceID))
		if id == "" || id == ex || seen[id] {
			continue
		}
		seen[id] = true
		r.TraceID = strings.TrimSpace(r.TraceID)
		out = append(out, r)
		if len(out) >= n {
			break
		}
	}
	return out
}

// sampleLogFilter — SAF (v0.10.1370): referanslar → TEK çoklu-trace süzgeci.
// Pencere pozitif damgaların [min−2 dk, max+2 dk]; hiç damga yoksa ok=false
// (sınırsız pencereyle log deposuna gidilmez — ES maliyet disiplini).
func sampleLogFilter(refs []sampleLogRef) (logstore.Filter, bool) {
	var ids []string
	var minNs, maxNs int64
	for _, r := range refs {
		ids = append(ids, r.TraceID)
		if r.AtNs <= 0 {
			continue
		}
		if minNs == 0 || r.AtNs < minNs {
			minNs = r.AtNs
		}
		if r.AtNs > maxNs {
			maxNs = r.AtNs
		}
	}
	if len(ids) == 0 || maxNs <= 0 {
		return logstore.Filter{}, false
	}
	return logstore.Filter{
		TraceIDs: ids,
		From:     time.Unix(0, minNs).Add(-sampleLogPad),
		To:       time.Unix(0, maxNs).Add(sampleLogPad),
		Limit:    sampleLogFetch,
	}, true
}

// sampleTraceLogsEvidence — v0.10.1370: örnek trace'lerin logları, TEK sorgu,
// span varlığına bağlı DEĞİL. logs nil (CH-only kurulum / işçi bağlamı),
// referans yok ya da hepsi exclude → sorgu yok, sıfır değer. Okuma düşerse
// (yavaş/erişilemez) de sıfır değer: best-effort, prompt eskisi gibi.
func sampleTraceLogsEvidence(ctx context.Context, logs logstore.Store, refs []sampleLogRef, exclude string) sampleTraceLogsResult {
	return sampleTraceLogsEvidenceLim(ctx, logs, refs, exclude, exLimitsFor(1))
}

// sampleTraceLogsEvidenceLim — v0.10.2384: okunan kayıt, trace başına satır ve
// gövde tavanı parametreli (kanıt ölçeği). Yine TEK sorgu, ≤3 trace, damga
// ±2 dk, sampleLogTimeout; yalnız LIMIT büyür (60 → 60 × ölçek).
// exLimitsFor(1) = sampleTraceLogsEvidence, bayt bayt.
func sampleTraceLogsEvidenceLim(ctx context.Context, logs logstore.Store, refs []sampleLogRef, exclude string, lim exLimits) sampleTraceLogsResult {
	if logs == nil {
		return sampleTraceLogsResult{}
	}
	picked := pickSampleLogRefs(refs, exclude, sampleLogTraces)
	f, ok := sampleLogFilter(picked)
	if !ok {
		return sampleTraceLogsResult{}
	}
	f.Limit = lim.sampleFetch
	page, err := logstore.SearchWithTimeout(ctx, logs, f, sampleLogTimeout)
	if err != nil || page == nil {
		return sampleTraceLogsResult{}
	}
	return renderSampleTraceLogsLim(picked, page.Logs, lim)
}

// renderSampleTraceLogs — SAF (v0.10.1370): gruplanmış loglar → deterministik
// blok. Trace sırası referans sırası (en yeni önce); satırlar şiddet azalan,
// sonra zaman artan (GroupByTrace). Logu olmayan trace açıkça "log
// bulunamadı" der — model "bakılmadı" ile "yok"u karıştırmasın.
func renderSampleTraceLogs(refs []sampleLogRef, lgs []*logstore.LogRecord) sampleTraceLogsResult {
	return renderSampleTraceLogsLim(refs, lgs, exLimitsFor(1))
}

// renderSampleTraceLogsLim — SAF (v0.10.2384): trace başına satır ve gövde
// tavanı parametreli; ölçekli yolda blok toplam gövde baytıyla da sınırlı
// (lim.sampleBlockBytes) ve yazılmayan satır notla söylenir.
// exLimitsFor(1) = renderSampleTraceLogs, bayt bayt.
func renderSampleTraceLogsLim(refs []sampleLogRef, lgs []*logstore.LogRecord, lim exLimits) sampleTraceLogsResult {
	r := sampleTraceLogsResult{Asked: len(refs)}
	if len(refs) == 0 {
		return r
	}
	ids := make([]string, len(refs))
	for i, ref := range refs {
		ids[i] = ref.TraceID
	}
	groups := logstore.GroupByTrace(lgs, ids, lim.samplePerTrace)
	var body strings.Builder
	grouped, blockBytes := 0, 0 // ölçekli yol: bütçe sayacı
	for i, ref := range refs {
		src := ref.Source
		if src == "" {
			src = sampleLogSrcNone
		}
		g := groups[i]
		if len(g) == 0 {
			fmt.Fprintf(&body, "\ntrace %s (%s): log bulunamadı", sampleLogID12(ref.TraceID), src)
			continue
		}
		grouped += len(g)
		lines := make([]liteLog, 0, len(g))
		for _, lg := range g {
			if lim.sampleBlockBytes > 0 && blockBytes >= lim.sampleBlockBytes {
				break
			}
			_, stackFromBody := stackparse.FromLog(lg.Attributes, lg.Body)
			b := lg.Body
			if stackFromBody {
				b = stackparse.MessageHead(lg.Body)
			}
			lines = append(lines, liteLog{Sev: lg.SeverityText, Svc: lg.ServiceName,
				ExType: lg.Attributes["exception.type"], Body: truncRunes(b, lim.sampleBodyRunes)})
			blockBytes += len(lines[len(lines)-1].Body)
		}
		if len(lines) == 0 { // bütçe bu trace'e yer bırakmadı (yalnız ölçekli yol)
			r.WithLogs++ // log VAR; yazılmadı
			fmt.Fprintf(&body, "\ntrace %s (%s): bütçe nedeniyle yazılmadı", sampleLogID12(ref.TraceID), src)
			continue
		}
		lp, err := json.Marshal(lines)
		if err != nil {
			fmt.Fprintf(&body, "\ntrace %s (%s): log bulunamadı", sampleLogID12(ref.TraceID), src)
			continue
		}
		r.WithLogs++
		r.Lines += len(lines)
		fmt.Fprintf(&body, "\ntrace %s (%s, %d satır):\n```json\n%s\n```", sampleLogID12(ref.TraceID), src, len(lines), promptfmt.FenceSafe(string(lp)))
	}
	r.Block = fmt.Sprintf("\n\nÖrnek trace'lerin LOGLARI (trace id ile, %d trace soruldu / %d trace'te log bulundu; yüksek severity önce):", r.Asked, r.WithLogs) +
		body.String() + lim.logsOmittedNote(grouped, r.Lines) // ölçek 1'de "" (bayt bayt eski)
	return r
}

// sampleLogID12 — SAF (v0.10.1370): trace id'nin ilk 12 karakteri + "…"
// (kırılımın drillTraceID12 biçimi; model önekle anar).
func sampleLogID12(id string) string {
	if len(id) > 12 {
		return id[:12] + "…"
	}
	return id
}
