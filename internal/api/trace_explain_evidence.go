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
// Fallback — model KARAR yazmazsa UI'ın göstereceği deterministik tek satır
// (AI yazmadı diye etiketlenir); zincir → kritik yol → kök süresi sırasıyla.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/tracechain"
)

// Görünüm tavanları.
const (
	traceEvChainMax   = 8
	traceEvLogsMax    = 3
	traceEvOracleMax  = 3
	traceEvOthersMax  = 3
	traceEvFieldsMax  = 4
	traceEvTextRunes  = 240
	traceEvFrameRunes = 200
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
	Logs       []traceEvLog     `json:"logs,omitempty"`
	// LogsVisible — v0.10.2480 (operatör: "Logla ilgili kısım da ekleyelim"):
	// her zaman görünen LOG satırında çizilen ilk N log (1, ya da ikinci satır
	// başka servisin WARN/ERROR'uysa 2). Katlanır Kanıt yalnız Logs[N:]'i listeler.
	LogsVisible int             `json:"logsVisible"`
	Frame       *traceEvFrame   `json:"frame,omitempty"`
	Fields      []traceEvField  `json:"fields,omitempty"`
	Oracle      []traceEvOracle `json:"oracle,omitempty"`
	Others      []traceEvHop    `json:"others,omitempty"`
	OthersMore  int             `json:"othersMore,omitempty"`
	RootMs      float64         `json:"rootMs,omitempty"`
	Fallback    string          `json:"fallback"`
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
}

type traceEvOrigin struct {
	Service string `json:"service"`
	Pod     string `json:"pod,omitempty"`
}

type traceEvLog struct {
	TsNs    int64  `json:"tsNs"`
	Level   string `json:"level"`
	Service string `json:"service"`
	Pod     string `json:"pod,omitempty"`
	Message string `json:"message"`
	Count   int    `json:"count,omitempty"`
	CorrID  string `json:"corrId,omitempty"` // requestId / correlationId (anahtar=değer)
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
	Logs       []*logstore.LogRecord
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
	}
	// Odak span: hata kaynağı; hata yoksa kritik yolun en büyük paylı adımı.
	focusSpan := ""
	if src.Chain.OK {
		focusSpan = src.Chain.Origin.Span.SpanID
	} else if v.Critical != nil {
		focusSpan = v.Critical.SpanID
	}
	v.Logs = pickTraceEvLogs(src.Logs, focusSpan)
	v.LogsVisible = traceEvLogsVisible(v.Logs)
	if v.Origin == nil && len(v.Logs) > 0 && v.Logs[0].Pod != "" {
		v.Origin = &traceEvOrigin{Service: v.Logs[0].Service, Pod: v.Logs[0].Pod}
	}
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

// pickTraceEvLogs — SAF: en ilgili ≤3 log deseni. Sıra: kaynak span'in
// logu, sonra seviye (ERROR > WARN > diğer), sonra zaman; aynı servis +
// gövde tek satır (Count).
func pickTraceEvLogs(logs []*logstore.LogRecord, originSpan string) []traceEvLog {
	type grp struct {
		rec   *logstore.LogRecord
		n     int
		focus bool
	}
	idx := map[string]*grp{}
	var gs []*grp
	for _, l := range logs {
		if l == nil || strings.TrimSpace(l.Body) == "" {
			continue
		}
		k := l.ServiceName + "\x00" + firstLineOf(l.Body)
		if g, ok := idx[k]; ok {
			g.n++
			g.focus = g.focus || (originSpan != "" && l.SpanID == originSpan)
			continue
		}
		g := &grp{rec: l, n: 1, focus: originSpan != "" && l.SpanID == originSpan}
		idx[k] = g
		gs = append(gs, g)
	}
	sort.SliceStable(gs, func(i, j int) bool {
		a, b := gs[i], gs[j]
		if a.focus != b.focus {
			return a.focus
		}
		if sa, sb := traceLogSevClass(a.rec), traceLogSevClass(b.rec); sa != sb {
			return sa > sb
		}
		return a.rec.Timestamp < b.rec.Timestamp
	})
	var out []traceEvLog
	for _, g := range gs {
		if len(out) >= traceEvLogsMax {
			break
		}
		if traceLogSevClass(g.rec) == 0 && len(out) > 0 {
			break // INFO yalnız başka satır yoksa
		}
		e := traceEvLog{TsNs: g.rec.Timestamp, Level: traceLogLevel(g.rec), Service: g.rec.ServiceName,
			Pod: traceLogPod(g.rec), Message: truncRunesN(logOneLine(firstLineOf(g.rec.Body)), traceEvTextRunes)}
		if g.n > 1 {
			e.Count = g.n
		}
		e.CorrID = logCorrID(g.rec)
		out = append(out, e)
	}
	return out
}

// traceEvLogsVisible — SAF: LOG satırında görünen log sayısı. İlk satır
// her zaman; ikincisi yalnız BAŞKA servisten ve WARN/ERROR ise (tavan 2).
func traceEvLogsVisible(logs []traceEvLog) int {
	switch {
	case len(logs) == 0:
		return 0
	case len(logs) >= 2 && logs[1].Service != logs[0].Service && traceEvLevelRelevant(logs[1].Level):
		return 2
	}
	return 1
}

func traceEvLevelRelevant(level string) bool {
	u := strings.ToUpper(level)
	return strings.HasPrefix(u, "ERR") || strings.HasPrefix(u, "WARN") || strings.HasPrefix(u, "FATAL") || strings.HasPrefix(u, "CRIT")
}

// logCorrID — SAF: logun ilk korelasyon kimliği ("anahtar=değer", sıralı anahtar).
func logCorrID(l *logstore.LogRecord) string {
	keys := make([]string, 0, 2)
	for k, v := range l.Attributes {
		if isCorrIDKey(k) && v != "" && len([]rune(v)) <= traceExplainCorrIDRunes {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	return keys[0] + "=" + l.Attributes[keys[0]]
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
