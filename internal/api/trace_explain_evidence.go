package api

// trace_explain_evidence.go — v0.10.2480 (operatör-bildirimli, prod
// v0.10.2478): trace açıklamasının KANIT görünümü.
//
// Bölümlü şablonda model hata zincirini, kritik yolu, log satırını, stack'i ve
// iş verisini yeniden YAZIYORDU (HTTP 400 ×5, sınıf adları, host, request id —
// "göz yoruyor"); evidence satırları UTC basıyordu, Window çipi yerel saatti.
// Anlatım biçiminde model yalnız TÜR + KARAR + Sonraki adım yazar; kanıtı UI
// bu görünümden çizer (FE TraceNarrativeBody, katlanır "Kanıt"). Görünüm
// prompt bloklarıyla AYNI sunucu hesabından kurulur (tracechain.Chain,
// CriticalPath, okunan loglar, Oracle satırları) — ek okuma yok. Zaman
// damgaları unix ns: UI izleyicinin saat diliminde çizer. Boyutlar tavanlı
// (sohbet arşivine blok olarak girer).
//
// v0.10.2483 (operatör onaylı, "logları ayrıca altta göstermene gerek yok,
// sekmede görüyor — onu da açıklamaya dahil et"): görünüm log LİSTESİ çizmez;
// loglar KARAR'ın 2. paragrafında anlatılır. Görünüm yalnız (a) modele giden
// desenlerin sınırlı künyesini (LogIndex: saat, servis, seviye, kısa anahtar —
// UI KARAR'daki saati bunlarla birebir eşleyip Loglar sekmesine bağlar) ve
// (b) "N satırın M'si incelendi" sayımını (LogsReviewed) taşır. Hata zinciri
// her zaman açık; zincir yoksa kritik yol aynı biçimde (CritPath: en büyük
// paylı adım önce, sonra köke kadar çağıranlar). Yaygınlık satırı
// (Prevalence, trace_explain_prevalence.go) okuma başarılıysa.
//
// Fallback — model KARAR yazmazsa UI'ın göstereceği deterministik tek satır
// (AI yazmadı diye etiketlenir); zincir → kritik yol → kök süresi sırasıyla.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/tracechain"
)

// Görünüm tavanları.
const (
	traceEvChainMax    = 8
	traceEvCritMax     = 8  // kritik yol satırı (en büyük pay + çağıranlar)
	traceEvLogIndexMax = 40 // LogIndex tavanı (modele giden desen ≤30 × ölçek)
	traceEvLogKeyRunes = 60
	traceEvOracleMax   = 3
	traceEvOthersMax   = 3
	traceEvFieldsMax   = 4
	traceEvTextRunes   = 240
	traceEvFrameRunes  = 200
)

// traceEvidenceView — `trace_evidence` bloğunun gövdesi (FE TraceEvidenceView).
type traceEvidenceView struct {
	Spans      int              `json:"spans"`               // analiz edilen span
	TotalSpans int              `json:"totalSpans"`          // trace'in tamamı
	LogRows    int              `json:"logRows"`             // okunan log satırı
	LogState   string           `json:"logState"`            // rows | none | failed
	LogReason  string           `json:"logReason,omitempty"` // failed: timeout | error | nostore
	OracleRows int              `json:"oracleRows"`          // Oracle hata satırı
	Chain      []traceEvHop     `json:"chain,omitempty"`     // kaynak → kök
	ChainSkip  int              `json:"chainSkipped,omitempty"`
	Critical   *traceEvCritical `json:"critical,omitempty"`
	Origin     *traceEvOrigin   `json:"origin,omitempty"`
	// CritPath — v0.10.2483: zincir yoksa açık "Kritik yol" (en büyük pay önce).
	CritPath []traceEvStep `json:"critPath,omitempty"`
	// LogIndex / LogsReviewed — v0.10.2483: modele giden log desenlerinin
	// künyesi (UI çizmez; saat eşleşmesi) ve inceleme sayımı.
	LogIndex     []traceEvLogRef      `json:"logIndex,omitempty"`
	LogsReviewed *traceEvLogsReviewed `json:"logsReviewed,omitempty"`
	Prevalence   *traceEvPrevalence   `json:"prevalence,omitempty"`
	Frame        *traceEvFrame        `json:"frame,omitempty"`
	Fields       []traceEvField       `json:"fields,omitempty"`
	Oracle       []traceEvOracle      `json:"oracle,omitempty"`
	Others       []traceEvHop         `json:"others,omitempty"`
	OthersMore   int                  `json:"othersMore,omitempty"`
	RootMs       float64              `json:"rootMs,omitempty"`
	Fallback     string               `json:"fallback"`
	// Kod İÇERİĞİ alanı YOK (v0.9.1254 kuralı, v0.10.2482'de teyit): kod
	// turunun künyesi answer.explain.code'da (dosya/satır/link, içeriksiz).
}

type traceEvHop struct {
	SpanID  string  `json:"spanId"`
	Service string  `json:"service"`
	Kind    string  `json:"kind,omitempty"`
	Call    string  `json:"call"`             // metot + host + yol ya da span adı
	Status  int     `json:"status,omitempty"` // HTTP kodu
	Error   string  `json:"error,omitempty"`  // exception / durum metni (aynen, kırpık)
	Failed  bool    `json:"failed"`
	DurMs   float64 `json:"durMs"`
	P50Ms   float64 `json:"p50Ms,omitempty"` // yalnız kaynak halka: 24 sa p50
}

type traceEvCritical struct {
	SpanID   string  `json:"spanId"`
	Service  string  `json:"service"`
	Kind     string  `json:"kind,omitempty"`
	Target   string  `json:"target"`
	DurMs    float64 `json:"durMs"`
	SharePct int     `json:"sharePct"`
	Mode     string  `json:"mode"` // external | dependent | internal
	Child    string  `json:"child,omitempty"`
	P50Ms    float64 `json:"p50Ms,omitempty"` // v0.10.2483 — adımın 24 sa ortancası (biliniyorsa)
}

// traceEvStep — kritik yolun bir satırı (FE TraceEvidenceStep).
type traceEvStep struct {
	SpanID   string  `json:"spanId"`
	Service  string  `json:"service"`
	Kind     string  `json:"kind,omitempty"`
	Call     string  `json:"call"`
	Status   int     `json:"status,omitempty"`
	DurMs    float64 `json:"durMs"`
	SharePct int     `json:"sharePct,omitempty"` // yalnız en büyük paylı adım
	P50Ms    float64 `json:"p50Ms,omitempty"`    // yalnız en büyük paylı adım
	Root     bool    `json:"root,omitempty"`
}

// traceEvLogRef — modele giden bir log deseninin künyesi (FE TraceEvidenceLogRef).
type traceEvLogRef struct {
	TsNs     int64  `json:"tsNs"`
	LastTsNs int64  `json:"lastTsNs,omitempty"` // desen ×N ise son kaydın saati
	Service  string `json:"service"`
	Level    string `json:"level"`
	Key      string `json:"key"`
}

// traceEvLogsReviewed — "N log satırının hepsi / M'si incelendi".
type traceEvLogsReviewed struct {
	Total int `json:"total"`
	Sent  int `json:"sent"`
}

type traceEvOrigin struct {
	Service string `json:"service"`
	Pod     string `json:"pod,omitempty"`
}

type traceEvFrame struct {
	Service string `json:"service,omitempty"`
	ExType  string `json:"exType,omitempty"`
	Frame   string `json:"frame"`
}

type traceEvField struct {
	K string `json:"k"`
	V string `json:"v"`
}

type traceEvOracle struct {
	TsNs      int64  `json:"tsNs"`
	Operation string `json:"operation,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
	Message   string `json:"message,omitempty"`
	Count     int    `json:"count,omitempty"`
}

// traceEvidenceSrc — görünümün girdisi (buildTraceExplainInputNear'ın elinde).
type traceEvidenceSrc struct {
	Spans      []chstore.SpanRow // TÜM span'ler
	Analyzed   int
	Chain      tracechain.Chain
	Crit       tracechain.CriticalPath
	CritP50Ms  float64       // v0.10.2483 — en büyük paylı adımın 24 sa p50'si (zincir yoksa; 0 = bilinmiyor)
	LogRefs    []traceLogRef // v0.10.2483 — modele giden desenler
	LogsSent   int           // v0.10.2483 — o desenlerin kapsadığı kayıt
	Prevalence *traceEvPrevalence
	LogOutcome traceLogOutcome
	RawStack   string
	StackSvc   string
	StackType  string
	Oracle     []chstore.OracleErrorRow
	MainOp     string
}

// buildTraceEvidenceView — SAF.
func buildTraceEvidenceView(src traceEvidenceSrc) *traceEvidenceView {
	v := &traceEvidenceView{Spans: src.Analyzed, TotalSpans: len(src.Spans),
		LogRows: src.LogOutcome.Rows, LogState: src.LogOutcome.State, LogReason: src.LogOutcome.Reason, OracleRows: len(src.Oracle)}
	if len(src.Spans) > 0 {
		v.RootMs = float64(tracechain.SpanDurNs(traceRootSpan(src.Spans))) / 1e6
	}
	if c := src.Chain; c.OK && len(c.Hops) > 0 {
		hops := c.Hops
		if len(hops) > traceEvChainMax {
			v.ChainSkip = len(hops) - traceEvChainMax
			hops = append(append([]tracechain.Hop(nil), hops[:traceEvChainMax-1]...), hops[len(hops)-1])
		}
		for i, h := range hops {
			e := traceEvHopOf(h)
			if i == 0 && c.BaseOK && c.BaseP50Ms > 0 {
				e.P50Ms = c.BaseP50Ms
			}
			v.Chain = append(v.Chain, e)
		}
		o := c.Origin.Span
		v.Origin = &traceEvOrigin{Service: o.ServiceName, Pod: spanPod(o)}
		byID := make(map[string]chstore.SpanRow, len(src.Spans))
		for _, sp := range src.Spans {
			byID[sp.SpanID] = sp
		}
		for _, id := range c.OtherIDs {
			if sp, ok := byID[id]; ok && len(v.Others) < traceEvOthersMax {
				v.Others = append(v.Others, traceEvHopOf(tracechain.Hop{Span: sp, Fail: tracechain.SpanFailure(sp), Peer: tracechain.PeerOf(sp)}))
			}
		}
		if c.OtherLeaf > len(v.Others) {
			v.OthersMore = c.OtherLeaf - len(v.Others)
		}
	}
	if top, ok := src.Crit.TopStep(); ok && len(src.Crit.Steps) >= traceCritMinSteps {
		mode := "internal"
		switch {
		case src.Crit.TopIsExternalWait():
			mode = "external"
		case tracechain.KindRank(top.Kind) == 1:
			mode = "dependent"
		}
		v.Critical = &traceEvCritical{SpanID: top.SpanID, Service: top.Service, Kind: strings.ToUpper(top.Kind),
			Target: top.TargetLabel(), DurMs: float64(top.DurNs) / 1e6, SharePct: int(top.Share*100 + 0.5),
			Mode: mode, Child: top.ChildService}
		if mode == "internal" {
			v.Critical.DurMs = float64(top.SelfOnPathNs) / 1e6
		}
		if !src.Chain.OK {
			v.Critical.P50Ms = src.CritP50Ms
			v.CritPath = traceEvCritPath(src.Crit, v.Critical)
		}
	}
	v.LogIndex = traceEvLogIndex(src.LogRefs)
	if src.LogOutcome.State == traceLogRows && src.LogOutcome.Rows > 0 {
		v.LogsReviewed = &traceEvLogsReviewed{Total: src.LogOutcome.Rows, Sent: min(src.LogsSent, src.LogOutcome.Rows)}
	}
	v.Prevalence = src.Prevalence
	if f := stackTopFrame(src.RawStack); f != "" {
		v.Frame = &traceEvFrame{Service: src.StackSvc, ExType: src.StackType, Frame: truncRunesN(f, traceEvFrameRunes)}
	}
	if op := strings.TrimSpace(src.MainOp); op != "" {
		v.Fields = append(v.Fields, traceEvField{K: "operation", V: op})
	}
	for _, id := range traceCorrelationIDs(src.Spans) {
		if len(v.Fields) >= traceEvFieldsMax {
			break
		}
		v.Fields = append(v.Fields, traceEvField{K: id.Key, V: id.Value})
	}
	v.Oracle = pickTraceEvOracle(src.Oracle)
	v.Fallback = traceEvidenceFallback(src.Chain, src.Crit, v.RootMs)
	return v
}

func traceEvHopOf(h tracechain.Hop) traceEvHop {
	e := traceEvHop{SpanID: h.Span.SpanID, Service: h.Span.ServiceName, Kind: strings.ToUpper(strings.TrimSpace(h.Span.Kind)),
		Call: tracechain.HopLabel(h), Status: h.Fail.HTTPCode, Failed: h.Span.StatusCode == "error",
		DurMs: float64(tracechain.SpanDurNs(h.Span)) / 1e6}
	switch {
	case h.Fail.ExType != "" && h.Fail.LogMsg != "":
		e.Error = h.Fail.ExType + ": " + h.Fail.LogMsg
	case h.Fail.ExType != "" && h.Fail.ExMsg != "":
		e.Error = h.Fail.ExType + ": " + h.Fail.ExMsg
	case h.Fail.ExType != "":
		e.Error = h.Fail.ExType
	case h.Fail.ExMsg != "":
		e.Error = h.Fail.ExMsg
	}
	e.Error = truncRunesN(logOneLine(e.Error), traceEvTextRunes)
	return e
}

// spanPod — span'in pod'u (resource) ya da host'u.
func spanPod(sp chstore.SpanRow) string {
	for _, k := range []string{"k8s.pod.name", "pod", "host.name"} {
		if v := sp.ResourceAttributes[k]; v != "" {
			return v
		}
	}
	return sp.HostName
}

// traceEvCritPath — SAF: açık "Kritik yol" satırları — en büyük paylı adım
// önce (süre, kök payı, 24 sa p50), sonra köke kadar çağıranlar. Tavanı
// aşan ara çağıranlar atlanır; kök her zaman son satır.
func traceEvCritPath(cp tracechain.CriticalPath, top *traceEvCritical) []traceEvStep {
	if top == nil || cp.Top < 0 || cp.Top >= len(cp.Steps) {
		return nil
	}
	step := func(i int) traceEvStep {
		st := cp.Steps[i]
		return traceEvStep{SpanID: st.SpanID, Service: st.Service, Kind: strings.ToUpper(st.Kind), Call: st.TargetLabel(),
			Status: st.HTTPCode, DurMs: float64(st.DurNs) / 1e6, Root: i == 0}
	}
	var out []traceEvStep
	for i := cp.Top; i >= 0; i-- {
		if len(out) == traceEvCritMax-1 && i > 0 {
			i = 0
		}
		e := step(i)
		if i == cp.Top {
			e.DurMs, e.SharePct, e.P50Ms = top.DurMs, top.SharePct, top.P50Ms
		}
		out = append(out, e)
	}
	return out
}

// traceEvLogIndex — SAF: modele giden desenlerin künyesi (tavanlı, sıra korunur).
func traceEvLogIndex(refs []traceLogRef) []traceEvLogRef {
	var out []traceEvLogRef
	for _, r := range refs {
		if len(out) >= traceEvLogIndexMax {
			break
		}
		if r.FirstTs <= 0 {
			continue
		}
		e := traceEvLogRef{TsNs: r.FirstTs, Service: r.Service, Level: r.Level, Key: truncRunesN(r.Key, traceEvLogKeyRunes)}
		if r.Count > 1 && r.LastTs > r.FirstTs {
			e.LastTsNs = r.LastTs
		}
		out = append(out, e)
	}
	return out
}

// stackTopFrame — SAF: stack'in ilk "at …" satırı (yoksa "").
func stackTopFrame(stack string) string {
	for _, ln := range strings.Split(stack, "\n") {
		if t := strings.TrimSpace(ln); strings.HasPrefix(t, "at ") {
			return strings.TrimSpace(strings.TrimPrefix(t, "at "))
		}
	}
	return ""
}

// pickTraceEvOracle — SAF: zamana göre ilk ≤3 Oracle satırı.
func pickTraceEvOracle(rows []chstore.OracleErrorRow) []traceEvOracle {
	if len(rows) == 0 {
		return nil
	}
	sorted := append([]chstore.OracleErrorRow(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Time.Before(sorted[j].Time) })
	var out []traceEvOracle
	for _, r := range sorted {
		if len(out) >= traceEvOracleMax {
			break
		}
		e := traceEvOracle{TsNs: r.Time.UnixNano(), Operation: r.OperationCode, ErrorCode: r.ErrorCode,
			Message: truncRunesN(logOneLine(r.Body), traceEvTextRunes)}
		if w := r.EffectiveWeight(); w > 1 {
			e.Count = w
		}
		out = append(out, e)
	}
	return out
}

// traceEvidenceFallback — SAF: modelsiz tek satırlık özet (Türkçe; UI "AI
// yazmadı" diye etiketler).
func traceEvidenceFallback(c tracechain.Chain, cp tracechain.CriticalPath, rootMs float64) string {
	if c.OK && len(c.Hops) > 0 {
		o := c.Origin
		s := fmt.Sprintf("Hata kaynağı: %s · %s", o.Span.ServiceName, tracechain.HopLabel(o))
		if f := tracechain.FailText(o.Fail); f != "" {
			s += " — " + f
		}
		s += "."
		if len(c.Hops) > 1 {
			r := c.Hops[len(c.Hops)-1]
			s += fmt.Sprintf(" Hata %s · %s isteğine yansıdı.", r.Span.ServiceName, tracechain.HopLabel(r))
		}
		return truncRunesN(s, 2*traceEvTextRunes)
	}
	if t, ok := cp.TopStep(); ok && len(cp.Steps) >= traceCritMinSteps {
		return fmt.Sprintf("Hata yok. Sürenin en büyük payı: %s · %s — %s (kök süresinin %%%d'i).",
			t.Service, t.TargetLabel(), tracechain.FmtMs(float64(t.DurNs)/1e6), int(t.Share*100+0.5))
	}
	if rootMs > 0 {
		return fmt.Sprintf("Hata yok; kök süresi %s.", tracechain.FmtMs(rootMs))
	}
	return "Hata yok."
}
