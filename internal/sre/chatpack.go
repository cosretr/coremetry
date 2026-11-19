package sre

// chatpack.go — v0.10.2361 (docs/cosre/investigation-design.md S4, karar
// 13–17): sohbetin hata incelemesi için sınırlı, deterministik kanıt paketi.
//
// Arka plan araştırmasının PLANINI ve SKORUNU değil, OKUYUCULARINI kullanır
// (karar 15): hata özeti runErrors, anomaliler runAnomalies; RED, deploy ve
// topoloji aynı okuyucu alanlarından (readersFrom ile aynı kaynaklar). İki
// dalga, paralel:
//
//   - dalga 1 (≤4 sn): belirti (pencere + önceki eş pencere RED,
//     service_summary_5m), hata özeti (≤3 operasyon + ≤3 istisna grubu),
//     değişiklik (deploy), bağımlılık (topology_edges_5m), anomali (≤3).
//   - dalga 2 (≤4 sn): hata özetinin örnek trace'i (kimlikle tek trace) ve
//     o trace'in logları — Elasticsearch'e alışveriş başına EN ÇOK bir sorgu
//     (karar 16; Logs okuyucusu histogram sormaz).
//
// Adım başına 3 sn; süresi dolan, hata veren ya da okuyucusu olmayan adım
// `unchecked {step, reason}` olur, paket yine teslim edilir. Modele giden
// özet ≤2.048 B (ChatPackDigestMax); ham satır, yığın ve seri gitmez.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// Paket bütçesi ve özet tavanı (tasarım §4.3; test pinler).
const (
	ChatPackWave      = 4 * time.Second
	ChatPackStepLimit = 3 * time.Second
	ChatPackDigestMax = 2048
	chatPackTopN      = 3
)

// Adım adları (çip ve unchecked metni).
const (
	PackStepSymptom   = "belirti"
	PackStepErrors    = "hata özeti"
	PackStepChanges   = "değişiklik"
	PackStepDeps      = "bağımlılık"
	PackStepAnomalies = "anomali"
	PackStepTrace     = "örnek trace"
	PackStepLogs      = "trace logları"
)

// unchecked nedenleri.
const (
	packReasonTimeout  = "zaman aşımı"
	packReasonError    = "okuma hatası"
	packReasonNoReader = "okuyucu yok"
	packReasonNoTrace  = "örnek trace yok"
)

// ChatPackRequest — paketin girdisi: tek servis, tek pencere.
type ChatPackRequest struct {
	Service  string
	From, To time.Time
}

// ChatPackReaders — paketin okumaları. nil alan = okuyucu yok → adım
// unchecked. Trace ve Logs aracın HAM JSON gövdesini döner (explain_trace /
// search_logs biçimi) — aynı gövde S3 kanıt adayı olur.
type ChatPackReaders struct {
	RED        func(ctx context.Context, service string, from, to time.Time) (chstore.ServiceWindowRED, error)
	ErrorOps   func(ctx context.Context, service string, from, to time.Time, limit int) ([]chstore.ErrorRank, error)
	Exceptions func(ctx context.Context, f chstore.ExceptionFilter) ([]chstore.ExceptionRow, error)
	Deploys    func(ctx context.Context, service string, anchor, to time.Time) (mcptools.DeployCheck, error)
	Graph      func(ctx context.Context, service string, from, to time.Time) ([]chstore.ServiceEdge, error)
	Anomalies  func(ctx context.Context, f chstore.ListAnomalyEventsFilter) ([]chstore.AnomalyEvent, error)
	Trace      func(ctx context.Context, traceID string) (string, error)
	Logs       func(ctx context.Context, traceID string, rangeS int) (string, error)
}

// ChatPackReadersFrom — üretim okuyucuları; readersFrom ile aynı kaynaklar
// (MV-first, kendi LIMIT / max_execution_time sınırlarıyla).
func ChatPackReadersFrom(d mcptools.Deps) ChatPackReaders {
	r := ChatPackReaders{
		Deploys: func(ctx context.Context, service string, anchor, to time.Time) (mcptools.DeployCheck, error) {
			return mcptools.ReadDeployCheck(ctx, d, service, anchor, to)
		},
		Trace: func(ctx context.Context, id string) (string, error) { return mcptools.ExplainTraceRead(ctx, d, id) },
		Logs: func(ctx context.Context, id string, rangeS int) (string, error) {
			return mcptools.SearchTraceLogsOnce(ctx, d, id, rangeS)
		},
	}
	if st := d.Store; st != nil {
		r.RED = st.ServiceWindowRED
		r.ErrorOps = st.TopErrorOperations
		r.Exceptions = st.GetExceptions
		r.Anomalies = st.ListAnomalyFeed
		r.Graph = func(ctx context.Context, service string, from, to time.Time) ([]chstore.ServiceEdge, error) {
			return st.GetServiceGraphTopN(ctx, service, 0, from, to, depsGraphTopN)
		}
	}
	return r
}

// ChatPackStep — adım sonucu (çip + ölçüm).
type ChatPackStep struct {
	Step   string `json:"step"`
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
	Label  string `json:"-"`
	Ms     int64  `json:"ms"`
}

// ChatPackDigest — modele giden özet (≤ ChatPackDigestMax bayt).
type ChatPackDigest struct {
	Service    string             `json:"service"`
	Window     PackWindow         `json:"window"`
	RED        *PackRED           `json:"red,omitempty"`
	ErrorOps   []PackErrorOp      `json:"error_ops,omitempty"`
	Exceptions []PackException    `json:"exceptions,omitempty"`
	Deploys    []PackDeploy       `json:"deploys,omitempty"`
	Deps       []PackDep          `json:"deps,omitempty"`
	Anomalies  []PackAnomaly      `json:"anomalies,omitempty"`
	Trace      *PackTrace         `json:"trace,omitempty"`
	Logs       *PackLogs          `json:"logs,omitempty"`
	Unchecked  []PackUncheckedRow `json:"unchecked,omitempty"`
}

type PackWindow struct {
	FromMs int64 `json:"from_ms"`
	ToMs   int64 `json:"to_ms"`
}

type PackRED struct {
	ErrPct     float64  `json:"err_pct"`
	PrevErrPct *float64 `json:"prev_err_pct,omitempty"`
	P95Ms      float64  `json:"p95_ms"`
	PrevP95Ms  *float64 `json:"prev_p95_ms,omitempty"`
	RPS        float64  `json:"rps"`
	Spans      uint64   `json:"spans"`
	Errors     uint64   `json:"errors"`
}

type PackErrorOp struct {
	Name   string  `json:"name"`
	Errors uint64  `json:"errors"`
	ErrPct float64 `json:"err_pct"`
}

type PackException struct {
	Type        string `json:"type"`
	Message     string `json:"message,omitempty"`
	Count       int64  `json:"count"`
	SampleTrace string `json:"sample_trace,omitempty"`
}

type PackDeploy struct {
	Version string `json:"version,omitempty"`
	AtMs    int64  `json:"at_ms"`
	Source  string `json:"source,omitempty"`
}

type PackDep struct {
	Target string  `json:"target"`
	ErrPct float64 `json:"err_pct"`
	AvgMs  float64 `json:"avg_ms"`
	Calls  uint64  `json:"calls"`
}

type PackAnomaly struct {
	Kind      string  `json:"kind"`
	Pattern   string  `json:"pattern"`
	Peak      float64 `json:"peak"`
	StartedMs int64   `json:"started_ms"`
}

type PackTrace struct {
	ID        string   `json:"id"`
	ErrorSpan string   `json:"error_span,omitempty"`
	TopPath   []string `json:"top_path,omitempty"`
}

type PackLogs struct {
	Total   int64    `json:"total"`
	Samples []string `json:"samples,omitempty"`
}

type PackUncheckedRow struct {
	Step   string `json:"step"`
	Reason string `json:"reason"`
}

// ChatPack — paketin sonucu.
type ChatPack struct {
	Digest    ChatPackDigest
	Steps     []ChatPackStep // adım sırasıyla (deterministik)
	TraceJSON string         // explain_trace gövdesi ("" = okunmadı)
	LogsJSON  string         // search_logs gövdesi ("" = okunmadı)
	WallMs    int64
}

// DigestJSON — SAF: ≤ ChatPackDigestMax bayt özet (sığmazsa en az değerli
// alanlar sırayla kırpılır: log örnekleri, anomaliler, yol, bağımlılıklar…).
func (p ChatPack) DigestJSON() string {
	return p.DigestJSONWithin(ChatPackDigestMax)
}

// DigestJSONWithin — v0.10.2378: bayt tavanı parametreli (sohbet, modelin
// kanıt ölçeğiyle çağırır; ChatPackDigestMax = DigestJSON, bayt bayt eski).
// Paketin kendi liste tavanları değişmez; yalnız kırpma eşiği büyür.
func (p ChatPack) DigestJSONWithin(maxBytes int) string {
	d := p.Digest
	trims := []func() bool{
		func() bool { return d.Logs != nil && shrink(&d.Logs.Samples) },
		func() bool { return shrink(&d.Anomalies) },
		func() bool { return d.Trace != nil && shrink(&d.Trace.TopPath) },
		func() bool { return shrink(&d.Deps) },
		func() bool { return shrink(&d.Deploys) },
		func() bool { return shrink(&d.ErrorOps) },
		func() bool { return shrink(&d.Exceptions) },
		func() bool { return shrink(&d.Unchecked) },
	}
	for {
		b, _ := json.Marshal(d)
		if len(b) <= maxBytes {
			return string(b)
		}
		cut := false
		for _, t := range trims {
			if t() {
				cut = true
				break
			}
		}
		if !cut {
			return `{"service":` + jsonStr(d.Service) + `,"truncated":true}`
		}
	}
}

// shrink — SAF: dilimin son elemanını düşürür; boşsa false.
func shrink[T any](s *[]T) bool {
	if len(*s) == 0 {
		return false
	}
	*s = (*s)[:len(*s)-1]
	return true
}

func jsonStr(s string) string { b, _ := json.Marshal(s); return string(b) }

// packTask — dalganın bir adımı; run yalnız kendi sonucunu hesaplar, apply
// toplayıcıda (süre içinde gelirse) pakete yazar — geç gelen sonuç yarışmaz.
type packTask struct {
	name string
	run  func(ctx context.Context) (apply func(*ChatPack), label string, err error)
}

var errPackNoReader = errors.New(packReasonNoReader)

// chatPackBudget — dalga ve adım süreleri (test kısaltır).
type chatPackBudget struct{ wave, step time.Duration }

// RunChatPack — paketi koşar; onStep her dalga bitince adım sırasıyla çağrılır.
func RunChatPack(ctx context.Context, rd ChatPackReaders, req ChatPackRequest, onStep func(ChatPackStep)) ChatPack {
	return runChatPack(ctx, rd, req, chatPackBudget{ChatPackWave, ChatPackStepLimit}, onStep)
}

func runChatPack(ctx context.Context, rd ChatPackReaders, req ChatPackRequest, b chatPackBudget, onStep func(ChatPackStep)) ChatPack {
	t0 := time.Now()
	p := ChatPack{Digest: ChatPackDigest{Service: req.Service, Window: PackWindow{FromMs: req.From.UnixMilli(), ToMs: req.To.UnixMilli()}}}
	if onStep == nil {
		onStep = func(ChatPackStep) {}
	}
	runPackWave(ctx, b, &p, wave1Tasks(rd, req), onStep)
	id := ""
	for _, e := range p.Digest.Exceptions {
		if e.SampleTrace != "" {
			id = e.SampleTrace
			break
		}
	}
	if id == "" {
		for _, n := range []string{PackStepTrace, PackStepLogs} {
			st := ChatPackStep{Step: n, Reason: packReasonNoTrace, Label: "atlandı: " + n + " (" + packReasonNoTrace + ")"}
			p.Steps = append(p.Steps, st)
			p.Digest.Unchecked = append(p.Digest.Unchecked, PackUncheckedRow{n, packReasonNoTrace})
			onStep(st)
		}
	} else {
		rangeS := int(req.To.Sub(req.From).Seconds())
		runPackWave(ctx, b, &p, wave2Tasks(rd, id, rangeS), onStep)
	}
	p.WallMs = time.Since(t0).Milliseconds()
	return p
}

// runPackWave — görevler paralel; dalga süresi dolunca bitmeyenler unchecked.
func runPackWave(ctx context.Context, b chatPackBudget, p *ChatPack, tasks []packTask, onStep func(ChatPackStep)) {
	type res struct {
		i     int
		apply func(*ChatPack)
		label string
		err   error
		ms    int64
	}
	wctx, cancel := context.WithTimeout(ctx, b.wave)
	defer cancel()
	ch := make(chan res, len(tasks)) // tamponlu: geç gelen gönderen bloklanmaz
	for i, t := range tasks {
		go func(i int, t packTask) {
			sctx, scancel := context.WithTimeout(wctx, b.step)
			defer scancel()
			s := time.Now()
			apply, label, err := t.run(sctx)
			if err != nil && sctx.Err() != nil {
				err = context.DeadlineExceeded
			}
			ch <- res{i, apply, label, err, time.Since(s).Milliseconds()}
		}(i, t)
	}
	got := make([]*res, len(tasks))
wait:
	for n := 0; n < len(tasks); n++ {
		select {
		case r := <-ch:
			got[r.i] = &r
		case <-wctx.Done():
			break wait
		}
	}
	for i, t := range tasks {
		st := ChatPackStep{Step: t.name}
		r := got[i]
		switch {
		case r == nil:
			st.Reason = packReasonTimeout
		case r.err == nil:
			st.OK, st.Label, st.Ms = true, r.label, r.ms
			if r.apply != nil {
				r.apply(p)
			}
		case errors.Is(r.err, errPackNoReader):
			st.Reason, st.Ms = packReasonNoReader, r.ms
		case errors.Is(r.err, context.DeadlineExceeded):
			st.Reason, st.Ms = packReasonTimeout, r.ms
		default:
			st.Reason, st.Ms = packReasonError, r.ms
		}
		if !st.OK {
			st.Label = "atlandı: " + t.name + " (" + st.Reason + ")"
			p.Digest.Unchecked = append(p.Digest.Unchecked, PackUncheckedRow{t.name, st.Reason})
		}
		p.Steps = append(p.Steps, st)
		onStep(st)
	}
}

func wave1Tasks(rd ChatPackReaders, req ChatPackRequest) []packTask {
	rs := readers{errorOps: rd.ErrorOps, exceptions: rd.Exceptions, anomalies: rd.Anomalies}
	return []packTask{
		{PackStepSymptom, func(ctx context.Context) (func(*ChatPack), string, error) {
			if rd.RED == nil {
				return nil, "", errPackNoReader
			}
			cur, err := rd.RED(ctx, req.Service, req.From, req.To)
			if err != nil {
				return nil, "", err
			}
			span := req.To.Sub(req.From)
			prev, perr := rd.RED(ctx, req.Service, req.From.Add(-span), req.From)
			red := packRED(cur, prev, perr == nil, span)
			return func(p *ChatPack) { p.Digest.RED = red }, symptomLabel(red), nil
		}},
		{PackStepErrors, func(ctx context.Context) (func(*ChatPack), string, error) {
			o := runErrors(ctx, rs, errorsRequest(req))
			if o.step.State == StepSkipped {
				return nil, "", errPackNoReader
			}
			if o.step.State == StepError || o.facts == nil || o.facts.Errors == nil {
				return nil, "", errors.New(o.step.Summary)
			}
			ops, exs := packErrors(o.facts.Errors)
			return func(p *ChatPack) { p.Digest.ErrorOps, p.Digest.Exceptions = ops, exs },
				fmt.Sprintf("hata özeti: %d operasyon, %d istisna grubu", len(ops), len(exs)), nil
		}},
		{PackStepChanges, func(ctx context.Context) (func(*ChatPack), string, error) {
			if rd.Deploys == nil {
				return nil, "", errPackNoReader
			}
			dc, err := rd.Deploys(ctx, req.Service, req.From, req.To)
			if err != nil {
				return nil, "", err
			}
			deps := packDeploys(dc.Rows)
			label := "değişiklik: deploy yok"
			if len(deps) > 0 {
				label = fmt.Sprintf("değişiklik: %d deploy", len(deps))
			}
			return func(p *ChatPack) { p.Digest.Deploys = deps }, label, nil
		}},
		{PackStepDeps, func(ctx context.Context) (func(*ChatPack), string, error) {
			if rd.Graph == nil {
				return nil, "", errPackNoReader
			}
			edges, err := rd.Graph(ctx, req.Service, req.From, req.To)
			if err != nil {
				return nil, "", err
			}
			deps := packDeps(req.Service, edges)
			return func(p *ChatPack) { p.Digest.Deps = deps }, fmt.Sprintf("bağımlılık: %d aşağı akış kenarı", len(deps)), nil
		}},
		{PackStepAnomalies, func(ctx context.Context) (func(*ChatPack), string, error) {
			o := runAnomalies(ctx, rs, anomaliesRequest{Service: req.Service, From: req.From, To: req.To, TopN: chatPackTopN})
			if o.step.State == StepSkipped {
				return nil, "", errPackNoReader
			}
			if o.step.State == StepError || o.facts == nil || o.facts.Anomalies == nil {
				return nil, "", errors.New(o.step.Summary)
			}
			an := packAnomalies(o.facts.Anomalies.Subject)
			return func(p *ChatPack) { p.Digest.Anomalies = an }, fmt.Sprintf("anomali: %d olay", len(an)), nil
		}},
	}
}

func wave2Tasks(rd ChatPackReaders, id string, rangeS int) []packTask {
	return []packTask{
		{PackStepTrace, func(ctx context.Context) (func(*ChatPack), string, error) {
			if rd.Trace == nil {
				return nil, "", errPackNoReader
			}
			raw, err := rd.Trace(ctx, id)
			if err != nil {
				return nil, "", err
			}
			tr := packTraceOf(id, raw)
			label := "örnek trace: " + shortID(id)
			if tr.ErrorSpan != "" {
				label = "örnek trace: hatalı span " + tr.ErrorSpan
			}
			return func(p *ChatPack) { p.Digest.Trace, p.TraceJSON = tr, raw }, truncRunes(label, 120), nil
		}},
		{PackStepLogs, func(ctx context.Context) (func(*ChatPack), string, error) {
			if rd.Logs == nil {
				return nil, "", errPackNoReader
			}
			raw, err := rd.Logs(ctx, id, rangeS)
			if err != nil {
				return nil, "", err
			}
			lg := packLogsOf(raw)
			return func(p *ChatPack) { p.Digest.Logs, p.LogsJSON = lg, raw }, fmt.Sprintf("trace logları: %d satır", lg.Total), nil
		}},
	}
}

// packRED — SAF.
func packRED(cur, prev chstore.ServiceWindowRED, prevOK bool, span time.Duration) *PackRED {
	sec := span.Seconds()
	if sec <= 0 {
		sec = 1
	}
	r := &PackRED{Spans: cur.Spans, Errors: cur.Errors, P95Ms: round2(cur.P95Ms), RPS: round2(float64(cur.Spans) / sec)}
	if cur.Spans > 0 {
		r.ErrPct = round2(float64(cur.Errors) / float64(cur.Spans) * 100)
	}
	if prevOK && prev.Spans > 0 {
		pe, pp := round2(float64(prev.Errors)/float64(prev.Spans)*100), round2(prev.P95Ms)
		r.PrevErrPct, r.PrevP95Ms = &pe, &pp
	}
	return r
}

// symptomLabel — SAF: belirti çipi.
func symptomLabel(r *PackRED) string {
	if r.Spans == 0 {
		return "belirti: pencerede span yok"
	}
	s := fmt.Sprintf("belirti: hata %%%s", fmtNum(r.ErrPct))
	if r.PrevErrPct != nil {
		s += fmt.Sprintf(" (önceki %%%s)", fmtNum(*r.PrevErrPct))
	}
	return s + fmt.Sprintf(", p95 %s ms", fmtNum(r.P95Ms))
}

func fmtNum(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%.2f", v)
}

// packErrors — SAF.
func packErrors(ef *ErrorFacts) ([]PackErrorOp, []PackException) {
	var ops []PackErrorOp
	for _, o := range ef.Ops {
		ops = append(ops, PackErrorOp{Name: truncRunes(o.Name, 100), Errors: o.Errors, ErrPct: o.ErrPct})
	}
	var exs []PackException
	for _, g := range ef.Groups {
		exs = append(exs, PackException{Type: truncRunes(g.Type, 120), Message: truncRunes(g.Message, 120), Count: g.Count, SampleTrace: g.SampleTraceID})
	}
	return ops, exs
}

// packDeploys — SAF: yeni → eski, ≤3.
func packDeploys(rows []mcptools.ChangeRow) []PackDeploy {
	rs := append([]mcptools.ChangeRow(nil), rows...)
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].TimeUnixNs > rs[j].TimeUnixNs })
	var out []PackDeploy
	for _, r := range rs {
		if len(out) >= chatPackTopN {
			break
		}
		out = append(out, PackDeploy{Version: truncRunes(r.Version, 60), AtMs: r.TimeUnixNs / int64(time.Millisecond), Source: r.Source})
	}
	return out
}

// packDeps — SAF: servisin aşağı akış kenarları, hata oranı azalan (eşitlikte
// çağrı azalan, sonra ad), ≤3.
func packDeps(service string, edges []chstore.ServiceEdge) []PackDep {
	var out []PackDep
	for _, e := range edges {
		if e.Source == service && e.Target != service {
			out = append(out, PackDep{Target: truncRunes(e.Target, 80), ErrPct: round2(e.ErrorRate), AvgMs: round2(e.AvgMs), Calls: e.CallCount})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.ErrPct != b.ErrPct {
			return a.ErrPct > b.ErrPct
		}
		if a.Calls != b.Calls {
			return a.Calls > b.Calls
		}
		return a.Target < b.Target
	})
	if len(out) > chatPackTopN {
		out = out[:chatPackTopN]
	}
	return out
}

// packAnomalies — SAF (sıralı satırlar, ≤3).
func packAnomalies(rows []AnomalyRow) []PackAnomaly {
	var out []PackAnomaly
	for _, r := range rows {
		if len(out) >= chatPackTopN {
			break
		}
		out = append(out, PackAnomaly{Kind: r.Kind, Pattern: truncRunes(r.Pattern, 80), Peak: r.PeakRatio, StartedMs: r.StartedAt / int64(time.Millisecond)})
	}
	return out
}

// packTraceOf — SAF: explain_trace gövdesinden hatalı span ve yolun ilk üç adımı.
func packTraceOf(id, raw string) *PackTrace {
	var d struct {
		Errs []struct {
			Service string `json:"service"`
			Name    string `json:"name"`
			ExType  string `json:"exception_type"`
		} `json:"error_spans"`
		Path []struct {
			Service string `json:"service"`
			Name    string `json:"name"`
		} `json:"critical_path"`
	}
	_ = json.Unmarshal([]byte(raw), &d)
	t := &PackTrace{ID: id}
	if len(d.Errs) > 0 {
		e := d.Errs[0]
		t.ErrorSpan = truncRunes(e.Service+" "+e.Name, 100)
		if e.ExType != "" {
			t.ErrorSpan += " (" + truncRunes(e.ExType, 80) + ")"
		}
	}
	for _, s := range d.Path {
		if len(t.TopPath) >= chatPackTopN {
			break
		}
		t.TopPath = append(t.TopPath, truncRunes(s.Service+" "+s.Name, 80))
	}
	return t
}

// packLogsOf — SAF: search_logs gövdesinden toplam ve ≤3 örnek.
func packLogsOf(raw string) *PackLogs {
	var d struct {
		Total int64 `json:"total"`
		Rows  []struct {
			Sev  string `json:"sev"`
			Svc  string `json:"svc"`
			Body string `json:"body"`
		} `json:"rows"`
	}
	_ = json.Unmarshal([]byte(raw), &d)
	l := &PackLogs{Total: d.Total}
	for _, r := range d.Rows {
		if len(l.Samples) >= chatPackTopN {
			break
		}
		l.Samples = append(l.Samples, truncRunes(oneLine(r.Sev+" "+r.Svc+" "+r.Body), 100))
	}
	return l
}
