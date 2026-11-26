package sre

// known.go — v0.10.2477 (CoSRE: araştırma platformun BİLDİĞİYLE başlar).
//
// Küçük yerel model (Qwen sınıfı varsayılan, gemma ikincil) zaten
// deterministik olarak bilinen şeyi yeniden keşfetmesin diye, araştırma üç
// sabit gerçekle başlar — her biri kendi adımı (dalga 1, önbellekli resume),
// kendi ayar anahtarı (sre_agent.graphOrigin / impactEvents / similarCases,
// varsayılan AÇIK) ve tekrar varyantı (noKnown → üçü de kapalı):
//
//  1. graph_origin — Problem grafının köken alanı (chstore.StdOriginOf; v0.10.2473
//     evaluator computeOrigin). "origin" bu pakette rollout'un git kaynağıdır
//     (DeployFact.Origin); karışmasın diye burada graphOrigin. Belirsiz köken =
//     gerçek YOK; zayıf köken işaretli ve skoru etkilemez.
//  2. impact — Problem'in üye olaylarının etki kuralı cümleleri ("normalde X,
//     şimdi Y"; ml.ImpactSentence — TEK TANIM chstore) sabit bulgu olarak.
//  3. similar — ONAYLI geçmiş vakalar (incident_cases status=confirmed:
//     kök neden etiketi / onaylı vaka; oy ve triage düğmeleri ASLA), en çok 3,
//     "belirti → kök neden → çözüm" + bağlantı. Tekrarda sızıntı süzgeci:
//     Problem'in kendisi ve Problem başlangıcından sonra kapanan / onaylanan
//     vakalar dışarıda.
//
// Üç adım da erişim beyanına (reach) GİRMEZ: telemetri kaynağı değil, platformun
// türetilmiş bilgisi — atlanmaları bildirimde "kör nokta" satırı açmaz.

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

const (
	stepGraphOrigin = "graph_origin"
	stepImpact      = "impact"
	stepSimilar     = "similar"

	capGraphOrigin = 3 * time.Second
	capImpact      = 4 * time.Second
	capSimilar     = 6 * time.Second

	// graphOriginMaxAge — köken blobunun tazelik tavanı (api inboxGraphBlobMaxAge aynası).
	graphOriginMaxAge = 15 * time.Minute
	// impactFactsMax — saklanan / bulgu olan etki cümlesi.
	impactFactsMax   = 4
	impactFindingMax = 3
	// similarShown — gösterilen onaylı vaka; similarRead — okunan aday (onaylı önce sıralı).
	similarShown    = 3
	similarRead     = 10
	similarLookback = 180 * 24 * time.Hour
	similarCodesMax = 5
	similarCacheTTL = 10 * time.Minute
)

// knownSteps — erişim beyanına girmeyen adımlar (reach.go).
var knownSteps = map[string]bool{stepGraphOrigin: true, stepImpact: true, stepSimilar: true}

// GraphOriginFact — Problem grafının kökeni (yalnız gösterilebilir köken; belirsiz = nil).
type GraphOriginFact struct {
	Entity   string `json:"entity"`
	Type     string `json:"type,omitempty"` // service | database | queue | external
	Name     string `json:"name"`
	Rule     string `json:"rule"` // K-ROLLOUT | K-SHARED | K-DEEP | K-FIRST
	Weak     bool   `json:"weak,omitempty"`
	Entities int    `json:"entities"`
	Self     bool   `json:"self,omitempty"` // bu Problem'in öznesi kökenin kendisi
}

// ImpactFact — üye olayın etki cümlesi.
type ImpactFact struct {
	Detector string `json:"detector"`
	Route    string `json:"route,omitempty"` // uç nokta / topic (olayın Code'u)
	Sentence string `json:"sentence"`
	At       int64  `json:"at"` // olay başlangıcı (unix ns)
}

// ImpactFacts — impact adımı (Rows boş = olay yok; okundu).
type ImpactFacts struct {
	Rows []ImpactFact `json:"rows"`
}

// SimilarCaseFact — onaylı geçmiş vaka.
type SimilarCaseFact struct {
	CaseID     string  `json:"caseId"`
	Service    string  `json:"service,omitempty"`
	Symptom    string  `json:"symptom"`
	RootCause  string  `json:"rootCause"`
	Category   string  `json:"category,omitempty"`
	Resolution string  `json:"resolution,omitempty"`
	Link       string  `json:"link,omitempty"`
	ClosedAt   int64   `json:"closedAt"` // unix ns
	Score      float64 `json:"score"`
}

// SimilarFacts — similar adımı (Cases boş = onaylı benzer vaka yok; okundu).
type SimilarFacts struct {
	Codes []string          `json:"codes,omitempty"`
	Cases []SimilarCaseFact `json:"cases"`
}

// knownFlags — koşunun üç anahtarı.
type knownFlags struct{ GraphOrigin, Impact, Similar bool }

func (k knownFlags) any() bool { return k.GraphOrigin || k.Impact || k.Similar }

// knownFlagsFor — canlı koşuda ayardan, tekrarda varyanttan (noKnown → üçü
// kapalı; canlı ayar tekrarı DEĞİŞTİRMEZ — Context Pack deseni).
func knownFlagsFor(rd readers, inv *Investigation) knownFlags {
	if inv.Kind == KindReplay {
		on := !inv.NoKnown
		return knownFlags{on, on, on}
	}
	get := CurrentSettings
	if rd.settings != nil {
		get = rd.settings
	}
	c := get()
	return knownFlags{c.GraphOriginOn(), c.ImpactEventsOn(), c.SimilarCasesOn()}
}

// wired — SAF: okuyucusu bağlı olmayan adım planlanmaz (kayıt adımsız; test /
// mağazasız kurulum bayt bayt eski).
func (k knownFlags) wired(rd readers) knownFlags {
	k.GraphOrigin = k.GraphOrigin && rd.problemGraph != nil
	k.Impact = k.Impact && rd.problemEvents != nil
	k.Similar = k.Similar && rd.similarCases != nil
	return k
}

// knownRequest — üç adımın ortak girdisi.
type knownRequest struct {
	Problem   chstore.Problem
	ProblemID string
	CaseID    string // tekrarın vakası (sızıntı süzgeci)
	ProblemAt int64  // unix ns
	From, To  time.Time
	Now       time.Time // planın şimdisi (tekrarda AsOf)
	Replay    bool
}

// knownSpecs — SAF: açık anahtarların adımları (dalga 1).
func knownSpecs(f knownFlags, req knownRequest) []StepSpec {
	var out []StepSpec
	add := func(on bool, name string, c time.Duration) {
		if on {
			r := req
			out = append(out, StepSpec{Name: name, Wave: 1, Cap: c, Known: &r})
		}
	}
	add(f.GraphOrigin, stepGraphOrigin, capGraphOrigin)
	add(f.Impact, stepImpact, capImpact)
	add(f.Similar && req.Problem.Service != "", stepSimilar, capSimilar)
	return out
}

// ── 1. Problem grafı kökeni ─────────────────────────────────────────────

// graphOriginRuleTR — SAF: kuralın düz cümlesi (Problem detayı ile aynı).
func graphOriginRuleTR(rule string) string {
	switch rule {
	case chstore.OriginRuleRollout:
		return "deploy sonrası başladı"
	case chstore.OriginRuleShared:
		return "diğerlerinin ortak bağımlılığı"
	case chstore.OriginRuleDeep:
		return "çağrı zincirinde en derinde"
	case chstore.OriginRuleFirst:
		return "ilk bozulan"
	}
	return rule
}

// graphOriginBlobUsable — SAF: blob bu koşuda kullanılabilir mi. Canlı: ≤15 dk
// taze. Tekrar: blob as-of anından SONRA hesaplanmadıysa ve as-of'a ≤15 dk
// yakınsa (köken geçmişi saklanmaz; bugünün kümesi geçmiş Problem'e sızmaz).
func graphOriginBlobUsable(rep chstore.ProblemGraphShadow, ok bool, now time.Time, replay bool) bool {
	if !ok || rep.At <= 0 {
		return false
	}
	at := time.Unix(rep.At, 0)
	if replay && at.After(now) {
		return false
	}
	return now.Sub(at) <= graphOriginMaxAge
}

// graphOriginOf — SAF: blob → Problem'in kökeni (gösterilmeyecekse nil).
func graphOriginOf(rep chstore.ProblemGraphShadow, problemID string) *GraphOriginFact {
	g, ok := chstore.FindProblemGraphGroup(rep, problemID)
	if !ok {
		return nil
	}
	o := chstore.StdOriginOf(g, problemID)
	if o == nil {
		return nil
	}
	return &GraphOriginFact{Entity: o.Entity, Type: o.Type, Name: o.Name, Rule: o.Rule, Weak: o.Confidence == chstore.OriginWeak,
		Entities: o.Entities, Self: o.Self}
}

// graphOriginSummaryTR — SAF: adım özeti / anlatım satırı.
func graphOriginSummaryTR(o *GraphOriginFact) string {
	if o == nil {
		return "köken yok: Problem bağlı bir kümede değil ya da köken belirsiz"
	}
	conf := "güçlü"
	if o.Weak {
		conf = "zayıf (tek kural — skoru etkilemez)"
	}
	s := fmt.Sprintf("köken %s", o.Name)
	if o.Type != "" {
		s += " (" + o.Type + ")"
	}
	s += fmt.Sprintf(" — %s [%s] · %s · küme %d varlık", graphOriginRuleTR(o.Rule), o.Rule, conf, o.Entities)
	if o.Self {
		s += " · bu Problem'in öznesi kökenin kendisi"
	}
	return s
}

func runGraphOrigin(ctx context.Context, rd readers, req knownRequest) stepOutcome {
	if rd.problemGraph == nil {
		return skippedOutcome(stepGraphOrigin, "köken okuyucusu bağlı değil", "step.skip.graphOriginReader")
	}
	rep, ok, err := rd.problemGraph(ctx)
	st := Step{Name: stepGraphOrigin}
	if err != nil {
		st.State, st.Summary = StepError, truncRunes("köken blobu okunamadı: "+err.Error(), 400)
		return stepOutcome{step: st}
	}
	if !graphOriginBlobUsable(rep, ok, req.Now, req.Replay) {
		why := "köken blobu yok ya da bayat (Problem grafı hesaplanmadı)"
		if req.Replay {
			why = "tekrar: köken geçmişi saklanmıyor (as-of anına ait blob yok)"
		}
		return skippedOutcome(stepGraphOrigin, why, "step.skip.graphOriginStale")
	}
	o := graphOriginOf(rep, req.ProblemID)
	st.State, st.Summary = StepOK, graphOriginSummaryTR(o)
	out := stepOutcome{step: st}
	if o != nil {
		out.facts = &Facts{GraphOrigin: o}
		out.findings = []Finding{{Kind: "origin", Name: o.Name, Level: "info", Reasons: []string{graphOriginSummaryTR(o)}, Step: stepGraphOrigin}}
	}
	return out
}

// graphOriginCheck — SAF: hipotez kontrolü ortak gövdesi. match → kökenin bu
// hipotezi işaret edip etmediği; zayıf köken 0 (işaretli, skor etkisi yok).
func graphOriginCheck(ev Evidence, match func(o *GraphOriginFact) int, what string) (int, string, string) {
	o := ev.Facts.GraphOrigin
	if o == nil {
		return 0, "Problem grafı kökeni yok", ""
	}
	ref := ev.ref(stepGraphOrigin, "")
	m := match(o)
	if m == 0 {
		return 0, "Problem grafı kökeni bu hipotezi işaret etmiyor (" + graphOriginSummaryTR(o) + ")", ref
	}
	if o.Weak {
		return 0, "zayıf köken: " + what + " — tek kural, skoru etkilemez (" + o.Name + ")", ref
	}
	if m < 0 {
		return -1, "Problem grafı: " + what + " (" + graphOriginSummaryTR(o) + ")", ref
	}
	return 1, "Problem grafı: " + what + " (" + graphOriginSummaryTR(o) + ")", ref
}

func chkGraphOriginRollout(ev Evidence) (int, string, string) {
	return graphOriginCheck(ev, func(o *GraphOriginFact) int {
		if o.Self && o.Rule == chstore.OriginRuleRollout {
			return 1
		}
		return 0
	}, "köken bu servis, deploy sonrası başladı")
}

func chkGraphOriginElsewhere(ev Evidence) (int, string, string) {
	return graphOriginCheck(ev, func(o *GraphOriginFact) int {
		switch {
		case o.Self:
			return -1
		case o.Type == "" || o.Type == "service" || o.Type == "queue":
			return 1
		}
		return 0
	}, "köken başka bir varlık")
}

func chkGraphOriginExternal(ev Evidence) (int, string, string) {
	return graphOriginCheck(ev, func(o *GraphOriginFact) int {
		if !o.Self && o.Type == "external" {
			return 1
		}
		return 0
	}, "köken bir dış servis")
}

func chkGraphOriginDB(ev Evidence) (int, string, string) {
	return graphOriginCheck(ev, func(o *GraphOriginFact) int {
		if !o.Self && o.Type == "database" {
			return 1
		}
		return 0
	}, "köken bir veritabanı")
}

// ── 2. Etki olayları ────────────────────────────────────────────────────

// impactFactsOf — SAF: Problem + üye olaylar → etki cümleleri (≤4). ML
// Problem'inin kendi manşeti (aynı cümle) varsa ilk satır; olaylar başlangıç
// sırasıyla, tekrarda as-of'tan sonra başlayan olay dışarıda; aynı cümle bir kez.
func impactFactsOf(p chstore.Problem, evs []chstore.MLEvent, asOf int64) *ImpactFacts {
	out := &ImpactFacts{Rows: []ImpactFact{}}
	seen := map[string]bool{}
	add := func(f ImpactFact) {
		f.Sentence = truncRunes(oneLine(f.Sentence), 240)
		if f.Sentence == "" || seen[f.Sentence] || len(out.Rows) >= impactFactsMax {
			return
		}
		seen[f.Sentence] = true
		out.Rows = append(out.Rows, f)
	}
	if h, ok := chstore.MLPromotedHeadline(p); ok && strings.Contains(h, "normalde") {
		add(ImpactFact{Detector: "problem", Sentence: h, At: p.StartedAt})
	}
	sorted := append([]chstore.MLEvent(nil), evs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].StartedAt < sorted[j].StartedAt })
	for _, e := range sorted {
		if asOf > 0 && e.StartedAt > asOf {
			continue
		}
		if s, ok := ml.ImpactSentence(e); ok {
			add(ImpactFact{Detector: e.Detector, Route: e.Code, Sentence: s, At: e.StartedAt})
		}
	}
	return out
}

// impactStep — SAF: gerçekler → adım + sabit bulgular (≤3).
func impactStep(f *ImpactFacts) (Step, []Finding) {
	st := Step{Name: stepImpact, State: StepOK}
	if len(f.Rows) == 0 {
		st.Summary = "etki kuralı olayı yok (Problem'in sayısal etki cümlesi bulunmadı)"
		return st, nil
	}
	var fs []Finding
	var parts []string
	for i, r := range f.Rows {
		parts = append(parts, r.Sentence)
		if i < impactFindingMax {
			fs = append(fs, Finding{Kind: "impact", Name: truncRunes(r.Sentence, 160), Level: "warn", Reasons: []string{"etki kuralı (" + r.Detector + ")"}, Step: stepImpact})
		}
	}
	st.Summary = truncRunes(strings.Join(parts, " "), 600)
	return st, fs
}

func runImpact(ctx context.Context, rd readers, req knownRequest) stepOutcome {
	if rd.problemEvents == nil {
		return skippedOutcome(stepImpact, "olay okuyucusu bağlı değil", "step.skip.impactReader")
	}
	evs, err := rd.problemEvents(ctx, req.Problem)
	if err != nil {
		return stepOutcome{step: Step{Name: stepImpact, State: StepError, Summary: truncRunes("üye olaylar okunamadı: "+err.Error(), 400)}}
	}
	asOf := int64(0)
	if req.Replay {
		asOf = req.Now.UnixNano()
	}
	f := impactFactsOf(req.Problem, evs, asOf)
	st, fs := impactStep(f)
	return stepOutcome{step: st, findings: fs, facts: &Facts{Impact: f}}
}

// ── 3. Benzer onaylı vakalar ────────────────────────────────────────────

// similarCaseLink — SAF: vakanın kaynağına bağlantı.
func similarCaseLink(c chstore.IncidentCase) string {
	if c.SourceID == "" {
		return ""
	}
	if c.Source == "exception" {
		return "/problems?exc=" + url.QueryEscape(c.SourceID)
	}
	return "/problems?problem=" + url.QueryEscape(c.SourceID)
}

// similarFactsOf — SAF: adaylar → onaylı ilk 3. Oy / triage hiç okunmaz:
// yalnız status=confirmed (kök neden etiketi ya da onaylı vaka) ve dolu
// FinalCause. Problem'in kendisi her zaman dışarıda; tekrarda (cutoff > 0)
// cutoff'tan sonra kapanan ya da onaylanan vaka da (cevap sızıntısı).
func similarFactsOf(rows []chstore.SimilarCase, problemID, caseID string, cutoff int64, codes []string) *SimilarFacts {
	out := &SimilarFacts{Codes: codes, Cases: []SimilarCaseFact{}}
	self := chstore.CaseIDFor("problem", problemID)
	for _, c := range rows {
		if len(out.Cases) >= similarShown {
			break
		}
		if c.Status != chstore.CaseConfirmed || strings.TrimSpace(c.FinalCause) == "" {
			continue
		}
		if c.CaseID == self || (caseID != "" && c.CaseID == caseID) || (c.Source == "problem" && c.SourceID == problemID) {
			continue
		}
		if cutoff > 0 && (c.ClosedAt.UnixNano() > cutoff || c.ConfirmedAt.UnixNano() > cutoff) {
			continue
		}
		out.Cases = append(out.Cases, SimilarCaseFact{CaseID: c.CaseID, Service: c.Service, Symptom: truncRunes(oneLine(c.Title), 160),
			RootCause: truncRunes(oneLine(c.FinalCause), 240), Category: c.Category, Resolution: truncRunes(oneLine(c.Resolution), 200),
			Link: similarCaseLink(c.IncidentCase), ClosedAt: c.ClosedAt.UnixNano(), Score: round2(c.Score)})
	}
	return out
}

// similarLineTR — SAF: "belirti → kök neden → çözüm (bağlantı)".
func similarLineTR(c SimilarCaseFact) string {
	s := fmt.Sprintf("%s · %s: %s → kök neden: %s", time.Unix(0, c.ClosedAt).UTC().Format("2006-01-02"), c.Service, c.Symptom, c.RootCause)
	if c.Resolution != "" {
		s += " → çözüm: " + c.Resolution
	}
	if c.Link != "" {
		s += " (" + c.Link + ")"
	}
	return s
}

// similarCacheKey — SAF: TÜM girdilerin özeti (servis, sıralı kodlar, gün çıpası, tavan).
func similarCacheKey(service string, codes []string, from time.Time, limit int) string {
	cs := append([]string(nil), codes...)
	sort.Strings(cs)
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00%d\x00%d", service, strings.Join(cs, "\x01"), from.UTC().Truncate(time.Hour).Unix(), limit)
	return fmt.Sprintf("sre:similar:v1:%x", h.Sum64())
}

func runSimilar(ctx context.Context, rd readers, req knownRequest) stepOutcome {
	if rd.similarCases == nil {
		return skippedOutcome(stepSimilar, "vaka hafızası okuyucusu bağlı değil", "step.skip.similarReader")
	}
	svc := req.Problem.Service
	var codes []string
	if rd.errorCodes != nil {
		if cs, err := rd.errorCodes(ctx, svc, req.From, req.To, similarCodesMax); err == nil {
			codes = cs
		}
	}
	from := req.Now.Add(-similarLookback)
	rows, err := rd.similarCases(ctx, svc, codes, from, similarRead)
	if err != nil {
		return stepOutcome{step: Step{Name: stepSimilar, State: StepError, Summary: truncRunes("vaka hafızası okunamadı: "+err.Error(), 400)}}
	}
	cutoff := int64(0)
	if req.Replay {
		cutoff = req.ProblemAt
	}
	f := similarFactsOf(rows, req.ProblemID, req.CaseID, cutoff, codes)
	st := Step{Name: stepSimilar, State: StepOK}
	if len(f.Cases) == 0 {
		st.Summary = "onaylı benzer geçmiş vaka yok"
	} else {
		parts := make([]string, 0, len(f.Cases))
		for _, c := range f.Cases {
			parts = append(parts, similarLineTR(c))
		}
		st.Summary = truncRunes(fmt.Sprintf("%d onaylı benzer vaka: ", len(f.Cases))+strings.Join(parts, " | "), 900)
	}
	return stepOutcome{step: st, facts: &Facts{Similar: f}}
}

// ── Anlatım bölümü ──────────────────────────────────────────────────────

// knownSectionTR — SAF: anlatım / sohbet girdisinin "PLATFORMUN BİLDİĞİ"
// bölümü (gerçek yoksa ""). Veri satırları; talimat yok.
func knownSectionTR(f *Facts) string {
	if f == nil || (f.GraphOrigin == nil && (f.Impact == nil || len(f.Impact.Rows) == 0) && (f.Similar == nil || len(f.Similar.Cases) == 0)) {
		return ""
	}
	var b strings.Builder
	b.WriteString("PLATFORMUN BİLDİĞİ (deterministik, yeniden keşfetme):\n")
	if o := f.GraphOrigin; o != nil {
		b.WriteString("- Problem grafı " + graphOriginSummaryTR(o) + "\n")
	}
	if f.Impact != nil {
		for _, r := range f.Impact.Rows {
			b.WriteString("- etki: " + r.Sentence + "\n")
		}
	}
	if f.Similar != nil {
		for _, c := range f.Similar.Cases {
			b.WriteString("- onaylı benzer vaka: " + truncRunes(similarLineTR(c), 400) + "\n")
		}
	}
	return b.String()
}

// hasGraphOrigin — checkApplies: köken kontrolleri yalnız köken varken.
func hasGraphOrigin(ev Evidence) bool { return ev.Facts.GraphOrigin != nil }

// cachedSimilarCases — vaka hafızası okumasını önbelleğe sarar (anahtar TÜM
// girdileri özetler; 10 dk). Önbellek yoksa / bozuksa doğrudan okuma.
func cachedSimilarCases(c cache.Cache, read func(ctx context.Context, service string, codes []string, from time.Time, limit int) ([]chstore.SimilarCase, error)) func(ctx context.Context, service string, codes []string, from time.Time, limit int) ([]chstore.SimilarCase, error) {
	return func(ctx context.Context, service string, codes []string, from time.Time, limit int) ([]chstore.SimilarCase, error) {
		key := similarCacheKey(service, codes, from, limit)
		if c != nil {
			if raw, ok, err := c.Get(ctx, key); err == nil && ok {
				var rows []chstore.SimilarCase
				if json.Unmarshal(raw, &rows) == nil {
					return rows, nil
				}
			}
		}
		rows, err := read(ctx, service, codes, from, limit)
		if err == nil && c != nil {
			if raw, merr := json.Marshal(rows); merr == nil {
				_ = c.Set(ctx, key, raw, similarCacheTTL)
			}
		}
		return rows, err
	}
}
