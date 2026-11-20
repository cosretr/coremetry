package anomaly

// trace_evidence.go — v0.10.1103 (operatör: "Oracle hata grubu için varsa
// coremetry üzerindeki trace ve o trace loglarını da kullanabilsin"): tek
// trace'in kanıtı (kompakt span bloğu + kanıt span'leri + SQL ifadeleri +
// zaman zarfı) ve o trace'in logları ORTAK yardımcılarda. Gövde
// BuildExceptionExplainInput'un v0.9.414'ten beri satır içi döngüsüdür —
// davranış değişmeden çıkarıldı (span yolunun prompt'u bayt bayt aynı;
// pin trace_evidence_test.go eski satır içi kodun kopyasına karşı). İkinci
// tüketici BuildOracleExceptionExplainInput: Oracle satırının taşıdığı trace
// Coremetry'de varsa onu AYNI sınırlarla okur — iki kopya sürüklenmez.

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/stackparse"
)

const (
	// traceEvidenceTimeout — GetTrace (kademeli pencere araması dahil) tavanı.
	traceEvidenceTimeout = 8 * time.Second
	// traceEvidenceErrSpans / traceEvidenceMaxSpans — kompakt blokta önce
	// ≤20 hata span'i GARANTİLİ (v0.9.414 verify: düz baş-kesim derindeki
	// hatayı düşürüyordu), sonra baştan ≤60'a tamamlanır.
	traceEvidenceErrSpans = 20
	traceEvidenceMaxSpans = 60
	traceEvidenceEvSpans  = 5 // kanıt span id'leri (hata span'leri)
	traceEvidenceDBStmts  = 3 // ayrık SQL ifadeleri (şema kanıtı girdisi)
	// traceLogsTimeout / traceLogsFetch / traceLogsLines — trace pivotu: tek
	// sorgu, ≤30 log çekilir, severity'ye göre ≤12 satır prompt'a girer.
	traceLogsTimeout = 6 * time.Second
	traceLogsFetch   = 30
	traceLogsLines   = 12
)

// TraceSpanLoader — tek trace'in span'leri (*chstore.Store.GetTrace; testte sahte).
type TraceSpanLoader interface {
	GetTrace(ctx context.Context, traceID string) ([]chstore.SpanRow, error)
}

// liteSpan — prompt'a giren span (v0.9.414 şekli; JSON anahtarları pinli).
type liteSpan struct {
	Name       string  `json:"name"`
	Service    string  `json:"service"`
	Kind       string  `json:"kind"`
	ParentSpan string  `json:"parent,omitempty"`
	SpanID     string  `json:"id"`
	DurationMs float64 `json:"durMs"`
	Status     string  `json:"status,omitempty"`
	StatusMsg  string  `json:"statusMsg,omitempty"`
	// v0.10.115 — yalnız hata span'larında: SQL hatasında çalışan ifade.
	DBSystem    string `json:"dbSystem,omitempty"`
	DBStatement string `json:"dbStatement,omitempty"`
}

// traceEvidenceResult — tek trace'in kanıtı. Spans boş = yüklenmedi (hata,
// zaman aşımı ya da trace yok); diğer alanlar o zaman sıfır değer.
type traceEvidenceResult struct {
	Spans        []chstore.SpanRow // ham (çalışan sürüm seçimi, v0.10.1044)
	Compact      []liteSpan        // prompt'a giren ≤60 span
	EvSpans      []string          // ≤5 hata span id'si
	DBStatements []string          // ≤3 ayrık, 600 rune
	MinT, MaxT   int64             // span zarfı (log penceresi)
}

// Loaded — trace okundu ve en az bir span taşıyor.
func (r traceEvidenceResult) Loaded() bool { return len(r.Spans) > 0 }

// compactJSON — kompakt span dizisinin JSON'u (ok=false: yüklenmedi / marshal düştü).
func (r traceEvidenceResult) compactJSON() (string, bool) {
	if !r.Loaded() {
		return "", false
	}
	tp, err := json.Marshal(r.Compact)
	if err != nil {
		return "", false
	}
	return string(tp), true
}

// traceEvidence — trace'i sınırlı bekleyişle okur ve kompakt kanıta çevirir.
// Okuma düşerse sıfır değer (best-effort; çağıran bloğu basmaz).
func traceEvidence(ctx context.Context, ld TraceSpanLoader, traceID string) traceEvidenceResult {
	return traceEvidenceLim(ctx, ld, traceID, exLimitsFor(1))
}

// traceEvidenceLim — v0.10.2384: kompakt blok tavanları parametreli (okuma aynı).
func traceEvidenceLim(ctx context.Context, ld TraceSpanLoader, traceID string, lim exLimits) traceEvidenceResult {
	if ld == nil || traceID == "" {
		return traceEvidenceResult{}
	}
	tctx, cancel := context.WithTimeout(ctx, traceEvidenceTimeout)
	spans, err := ld.GetTrace(tctx, traceID)
	cancel()
	if err != nil || len(spans) == 0 {
		return traceEvidenceResult{}
	}
	return compactTraceEvidenceLim(spans, lim)
}

// compactTraceEvidence — SAF çekirdek: span'ler → zarf + kompakt blok + kanıt.
func compactTraceEvidence(spans []chstore.SpanRow) traceEvidenceResult {
	return compactTraceEvidenceLim(spans, exLimitsFor(1))
}

// compactTraceEvidenceLim — v0.10.2384: span / hata span'i sayısı ve SQL
// ifadesi tavanı parametreli (kanıt ölçeği). Yapısal DBStatements (şema
// kanıtı girdisi) HER ölçekte 600 rune'luk kopyadan: ölçek yalnız prompt'a
// giren ifadeyi büyütür. exLimitsFor(1) = compactTraceEvidence, bayt bayt.
func compactTraceEvidenceLim(spans []chstore.SpanRow, lim exLimits) traceEvidenceResult {
	if len(spans) == 0 {
		return traceEvidenceResult{}
	}
	r := traceEvidenceResult{Spans: spans, MinT: spans[0].StartTime, MaxT: spans[0].EndTime}
	for _, sp := range spans {
		if sp.StartTime < r.MinT {
			r.MinT = sp.StartTime
		}
		if sp.EndTime > r.MaxT {
			r.MaxT = sp.EndTime
		}
	}
	include := make([]bool, len(spans))
	kept, errKept := 0, 0
	for i, sp := range spans {
		if sp.StatusCode == "error" && errKept < lim.traceErrSpans {
			include[i] = true
			kept++
			errKept++
		}
	}
	for i := range spans {
		if kept >= lim.traceSpans {
			break
		}
		if !include[i] {
			include[i] = true
			kept++
		}
	}
	seenStmt := map[string]bool{}
	r.Compact = make([]liteSpan, 0, kept)
	for i, sp := range spans {
		if !include[i] {
			continue
		}
		l := liteSpan{Name: sp.Name, Service: sp.ServiceName, Kind: sp.Kind,
			ParentSpan: sp.ParentSpanID, SpanID: sp.SpanID,
			DurationMs: float64(sp.EndTime-sp.StartTime) / 1e6}
		if sp.StatusCode == "error" {
			l.Status = "error"
			l.StatusMsg = sp.StatusMessage
			if len(r.EvSpans) < traceEvidenceEvSpans {
				r.EvSpans = append(r.EvSpans, sp.SpanID)
			}
			if sp.DBStatement != "" {
				l.DBSystem = sp.DBSystem
				l.DBStatement = truncRunes(sp.DBStatement, lim.dbStmtRunes)
				stmt := truncRunes(sp.DBStatement, exDBStatementRunes) // yapısal kopya: ölçekten bağımsız
				if len(r.DBStatements) < traceEvidenceDBStmts && !seenStmt[stmt] {
					seenStmt[stmt] = true
					r.DBStatements = append(r.DBStatements, stmt)
				}
			}
		}
		r.Compact = append(r.Compact, l)
	}
	return r
}

// traceLogsResult — trace'in logları: satırlar + satır başına HAM stack
// (kırpma ve tekrar katlaması temsilî stack seçildikten SONRA, foldedJSON)
// + kod çekicinin log-fallback istihkakı (ilk stack'li log, v0.9.1225 /
// v0.10.1044).
type traceLogsResult struct {
	Lines        []liteLog
	Read         int // v0.10.2384 — log deposundan okunan satır (yazılmayan satır notu)
	Stacks       []string
	Stack        string            // ilk stack'li logun HAM stack'i
	StackService string            // o logu atan servis
	StackSpan    string            // o logun span'i (çalışan sürüm)
	StackRes     map[string]string // o logun resource'u
}

// traceLogsEvidence — trace'in logları, tek pivot sorgusu. Trace yüklenmediyse
// (maxT 0 → 1970 penceresi) HİÇ gitme (v0.9.414 verify bulgusu, ES maliyet
// disiplini). logs nil olabilir (CH-only kurulum / işçi bağlamı) → boş.
func traceLogsEvidence(ctx context.Context, logs logstore.Store, traceID string, minT, maxT int64) traceLogsResult {
	return traceLogsEvidenceLim(ctx, logs, traceID, minT, maxT, exLimitsFor(1))
}

// traceLogsEvidenceLim — v0.10.2384: okunan / yazılan satır sayısı ve gövde
// tavanı parametreli (kanıt ölçeği). Ölçekli yolda blok toplam bayt bütçesiyle
// de sınırlı (lim.logsBlockBytes; satır × boy karesel büyümesin). Okuma yine
// TEK sorgu, tek trace, span zarfı ±1 dk, traceLogsTimeout; yalnız LIMIT
// büyür (30 → 30 × ölçek). exLimitsFor(1) = traceLogsEvidence, bayt bayt.
func traceLogsEvidenceLim(ctx context.Context, logs logstore.Store, traceID string, minT, maxT int64, lim exLimits) traceLogsResult {
	var r traceLogsResult
	if logs == nil || traceID == "" || maxT <= 0 {
		return r
	}
	from := time.Unix(0, minT).Add(-time.Minute)
	to := time.Unix(0, maxT).Add(time.Minute)
	lctx, cancel := context.WithTimeout(ctx, traceLogsTimeout)
	defer cancel()
	page, err := logstore.LogsForTrace(lctx, logs, traceID, from, to, lim.logFetch)
	if err != nil || page == nil || len(page.Logs) == 0 {
		return r
	}
	lgs := page.Logs
	sort.SliceStable(lgs, func(i, j int) bool { return lgs[i].Severity > lgs[j].Severity })
	r.Read = len(lgs)
	r.Lines = make([]liteLog, 0, min(lim.logLines, len(lgs)))
	blockBytes := 0
	for _, lg := range lgs {
		if len(r.Lines) >= lim.logLines || (lim.logsBlockBytes > 0 && blockBytes >= lim.logsBlockBytes) {
			break
		}
		// v0.9.1182 — kardeş yolla AYNI çözücü: `exception.stacktrace`,
		// ECS `error.stack_trace` ya da gövde içindeki Java stack'i.
		stackText, stackFromBody := stackparse.FromLog(lg.Attributes, lg.Body)
		// v0.9.1225 — span örnekleri stack taşımıyorsa kod çekicinin istihkakı
		// LOGLARDAN; logu atan servis de taşınır (depo çözümü doğru depoya).
		if r.Stack == "" && stackText != "" {
			r.Stack, r.StackService = stackText, lg.ServiceName
			r.StackSpan, r.StackRes = lg.SpanID, lg.ResourceAttributes
		}
		bodyForPrompt := lg.Body
		if stackFromBody {
			bodyForPrompt = stackparse.MessageHead(lg.Body)
		}
		e := liteLog{Sev: lg.SeverityText, Svc: lg.ServiceName, Body: truncRunes(bodyForPrompt, lim.logBodyRunes)}
		e.ExType = lg.Attributes["exception.type"]                           // nil map okuması güvenli
		blockBytes += len(e.Body) + min(len(stackText), lim.logStackRunes*3) // stack sonra katlanır / kesilir: üst kestirim
		r.Lines = append(r.Lines, e)
		r.Stacks = append(r.Stacks, stackText)
	}
	return r
}

// foldedJSON — satırların JSON'u; her logun stack'i prompt'ta GÖRÜNEN
// temsilî stack'e (primary; yoksa "") karşı katlanır (v0.9.1239). Girdi
// satırları değişmez (kopya). ok=false: satır yok / marshal düştü.
func (r traceLogsResult) foldedJSON(primary string) (string, bool) {
	return r.foldedJSONN(primary, exLogStackRunes)
}

// foldedJSONN — v0.10.2384: log stack kopyasının tavanı parametreli.
func (r traceLogsResult) foldedJSONN(primary string, stackRunes int) (string, bool) {
	if len(r.Lines) == 0 {
		return "", false
	}
	lines := append([]liteLog(nil), r.Lines...)
	folded := foldDuplicateLogStacksN(r.Stacks, primary, stackRunes)
	for i := range lines {
		if i < len(folded) {
			lines[i].Stack = folded[i]
		}
	}
	lp, err := json.Marshal(lines)
	if err != nil {
		return "", false
	}
	return string(lp), true
}
