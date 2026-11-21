package sre

// hypotheses.go — v0.10.1288 (AI SRE Faz 2: hipotez kartları).
//
// SAF, deterministik Score(ev) — Faz 1b adımlarının ZATEN topladığı gerçekler
// (+ tembel node okuması, + olaylar adımı) üzerinden 8 hipotez türü. LLM
// skora DOKUNMAZ: anlatım yalnız en olası hipotezi anabilir (narrate.go).
//
//	score = clamp(prior + Σ ağırlık × sonuç, 0, 1)   sonuç ∈ {+1 doğrular, −1 çürütür, 0 bilinmiyor}
//	bant  = yüksek ≥ 0.70 · orta ≥ 0.40 · düşük
//
// Her kartın kontrolleri {ad, sonuç, gerekçe, kanıt künyesi}; künye
// "<adım>" ya da "<adım>.evidences[i]" (FE adımın kanıt çipine bağlar).
// Öncül/ağırlık tablosu AŞAĞIDA kodda (hypothesisTable) — oy, operatör
// işareti (HumanState) ya da LLM çıktısı skora GİRMEZ (ratchet:
// TestHypothesisMarkNeverChangesScore). Kategori incident_cases
// (cosrelearn.CaseCategories) sözlüğünden — Faz 5 değerlendirmesi top-1/top-3
// isabetini bu eşlemeyle ölçer.
//
// Ajan root_cause_hypotheses / derin kanıt / problems / öncelik YAZMAZ
// (TestNeverWritesHypothesisTable); kök-neden sentezleyicisi AYRI motordur ve
// yalnız SynthRef olarak okunur (bir kontrolün girdisi: "sentezleyici aynı
// komşuyu gösteriyor").
//
// v0.10.1310 — konfigürasyonun argo_sync kontrolü (0.20) artık Facts.Argo'dan
// okunur (koşu başına tek Deps.ArgoApps okuması, skordan önce); önceden hep 0
// ("okunmuyor") olduğu için config 0.70'te tavanlanıyordu. Tekrarda 0 + not.

import (
	"context"
	"fmt"
	"github.com/cilcenk/coremetry/internal/healthverdict"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/runtimesig"
)

// Hipotez türleri (kayıttaki Hypothesis.Kind; FE aynası hypothesisView.ts).
const (
	HypDeploy         = "deploy"
	HypConfig         = "config"
	HypSaturation     = "saturation"
	HypDependency     = "dependency"
	HypExternal       = "external"
	HypDBSlow         = "db_slow"
	HypTrafficSurge   = "traffic_surge"
	HypCodeRegression = "code_regression"
)

// Bantlar.
const (
	BandHigh   = "yüksek"
	BandMedium = "orta"
	BandLow    = "düşük"

	bandHighMin   = 0.70
	bandMediumMin = 0.40
)

// Operatör işaretleri (HumanState.State).
const (
	MarkDismissed = "dismissed"
	MarkConfirmed = "confirmed"
)

// HypothesesShown — kartta / sohbette gösterilen en çok hipotez.
const HypothesesShown = 3

// HypothesisCheck — tek kontrolün sonucu.
type HypothesisCheck struct {
	Name        string  `json:"name"`
	Outcome     int     `json:"outcome"` // +1 doğrular · −1 çürütür · 0 bilinmiyor
	Weight      float64 `json:"weight"`
	Reason      string  `json:"reason"`
	EvidenceRef string  `json:"evidenceRef,omitempty"`
}

// HumanState — operatörün işareti (Ele / Onayla). Skoru DEĞİŞTİRMEZ.
type HumanState struct {
	State string `json:"state"` // dismissed | confirmed
	Note  string `json:"note,omitempty"`
	By    string `json:"by,omitempty"`
	At    int64  `json:"at"` // unix ns
}

// Hypothesis — tek hipotez kartı.
type Hypothesis struct {
	Kind       string            `json:"kind"`
	Category   string            `json:"category"`
	Label      string            `json:"label"`
	Score      float64           `json:"score"`
	Band       string            `json:"band"`
	Prior      float64           `json:"prior"`
	Checks     []HypothesisCheck `json:"checks"`
	HumanState *HumanState       `json:"humanState,omitempty"`
	// CoSRE Faz B2 — açık hipotez durumu (hypothesis_state.go; skoru
	// DEĞİŞTİRMEZ): supported | ruled_out | tested | untested + durumu
	// belirleyen kanıt künyeleri ve kısa gerekçe. Plain varyantta / eski
	// kayıtta boş.
	State       string   `json:"state,omitempty"`
	StateRefs   []string `json:"stateRefs,omitempty"`
	StateReason string   `json:"stateReason,omitempty"`
}

// Evidence — Score'un girdisi: kayıttaki gerçekler + problemin şekli.
type Evidence struct {
	Service  string
	Symptom  string
	StartNs  int64
	Synth    *SynthRef
	Facts    Facts
	Steps    map[string]string // adım → durum (ok | partial | error | skipped; yok = koşmadı)
	Evidence map[string][]evRef
}

// evRef — adımın kanıt künyesinin halkası (Step.Evidence[i].Hop / Tool).
type evRef struct {
	Hop  string
	Tool string
}

// check — kontrol işlevi (SAF).
type check struct {
	Name   string
	Weight float64
	Run    func(ev Evidence) (int, string, string)
}

// hypothesisSpec — tablo satırı.
type hypothesisSpec struct {
	Kind     string
	Category string // incident_cases kategorisi
	Label    string
	Prior    float64
	Checks   []check
}

// hypothesisTable — öncül ve ağırlıklar (tek kaynak; değişiklik =
// TestHypothesisTable satırı + DECISIONS.md notu). Sıra eşitlikte kart sırası.
var hypothesisTable = []hypothesisSpec{
	{HypDeploy, "deploy", "Deploy", 0.20, []check{
		{"deploy_near", 0.35, chkDeployNear},
		{"deploy_before_error", 0.20, chkDeployBeforeError},
		{"recurring_before_deploy", 0.25, chkRecurring},
		{"rollout_failed", 0.15, chkRolloutFailed},
	}},
	{HypConfig, "config", "Konfigürasyon değişikliği", 0.10, []check{
		{"change_without_image", 0.45, chkConfigChange},
		{"change_before_error", 0.15, chkConfigBeforeError},
		{"argo_sync", 0.20, chkArgo},
	}},
	{HypSaturation, "capacity", "Doygunluk / OOM", 0.15, []check{
		{"pod_limits", 0.35, chkPodLimits},
		{"jvm_post_gc", 0.25, chkJVM},
		{"runtime_signals", 0.25, chkRuntimeSignals}, // 2026-11-21 — .NET (yalnız .NET serviste listelenir)
		{"node_pressure", 0.20, chkNode},
		{"latency_symptom", 0.10, chkLatencySymptom},
	}},
	{HypDependency, "dependency", "Bağımlılık arızası", 0.15, []check{
		{"downstream_worse", 0.40, chkDownstreamWorse},
		{"synth_agrees", 0.20, chkSynthAgrees},
		{"downstream_errors", 0.15, chkDownstreamErrors},
		// Follow-up 2026-11-21 — VM client histogramı (örnekleme öncesi); yalnız
		// okunduğunda listelenir (checkApplies), diğer servislerin skoru aynen.
		{"downstream_exact", 0.25, chkDownstreamExact},
	}},
	{HypExternal, "external", "Dış servis kesintisi", 0.10, []check{
		{"external_problem_open", 0.45, chkExternalProblem},
		{"external_edge_errors", 0.30, chkExternalEdges},
	}},
	{HypDBSlow, "dependency", "Veritabanı yavaşlığı", 0.10, []check{
		{"db_health_open", 0.40, chkDBHealth},
		{"pivot_db_path", 0.30, chkPivotDBPath},
		{"db_edge_slow", 0.15, chkDBEdge},
	}},
	{HypTrafficSurge, "capacity", "Trafik sıçraması", 0.10, []check{
		{"rate_up", 0.45, chkRateUp},
		{"p99_up", 0.20, chkP99Up},
		{"upstream_surge", 0.15, chkUpstreamSurge},
	}},
	{HypCodeRegression, "code_bug", "Kod gerilemesi / yeni exception", 0.15, []check{
		{"new_error_pattern", 0.30, chkNewPattern},
		{"after_deploy", 0.25, chkAfterDeploy},
		{"pivot_error_spans", 0.20, chkPivotErrors},
		{"error_symptom", 0.10, chkErrorSymptom},
	}},
}

// IsHypothesisKind — tabloda olan tür mü (API doğrulaması).
func IsHypothesisKind(k string) bool {
	for _, s := range hypothesisTable {
		if s.Kind == k {
			return true
		}
	}
	return false
}

// HypothesisLabel — türün Türkçe adı ("" = bilinmeyen).
func HypothesisLabel(k string) string {
	for _, s := range hypothesisTable {
		if s.Kind == k {
			return s.Label
		}
	}
	return ""
}

// HypothesisCategory — türün incident_cases kategorisi.
func HypothesisCategory(k string) string {
	for _, s := range hypothesisTable {
		if s.Kind == k {
			return s.Category
		}
	}
	return ""
}

// BandOf — SAF: skor → bant.
func BandOf(score float64) string {
	switch {
	case score >= bandHighMin:
		return BandHigh
	case score >= bandMediumMin:
		return BandMedium
	}
	return BandLow
}

// Score — SAF: gerçekler → 8 hipotez, skora göre azalan (eşitlikte tablo
// sırası). Aynı girdi her zaman aynı çıktı (TestScoreDeterministic).
func Score(ev Evidence) []Hypothesis {
	out := make([]Hypothesis, 0, len(hypothesisTable))
	for _, spec := range hypothesisTable {
		h := Hypothesis{Kind: spec.Kind, Category: spec.Category, Label: spec.Label, Prior: spec.Prior, Checks: make([]HypothesisCheck, 0, len(spec.Checks))}
		sum := spec.Prior
		for _, c := range spec.Checks {
			if ap := checkApplies[c.Name]; ap != nil && !ap(ev) {
				continue // 2026-11-21 — runtime'a uymayan kontrol kartta HİÇ yok (.NET'te JVM satırı yok)
			}
			o, why, ref := c.Run(ev)
			if o > 1 {
				o = 1
			} else if o < -1 {
				o = -1
			}
			sum += c.Weight * float64(o)
			h.Checks = append(h.Checks, HypothesisCheck{Name: c.Name, Outcome: o, Weight: c.Weight, Reason: why, EvidenceRef: ref})
		}
		h.Score = math.Round(math.Max(0, math.Min(1, sum))*100) / 100
		h.Band = BandOf(h.Score)
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// carryHuman — SAF: önceki kartların operatör işaretlerini yeni skora taşır
// (türe göre). Skor ve bant DEĞİŞMEZ.
func carryHuman(next, prev []Hypothesis) []Hypothesis {
	marks := map[string]*HumanState{}
	for _, h := range prev {
		if h.HumanState != nil {
			hs := *h.HumanState
			marks[h.Kind] = &hs
		}
	}
	for i := range next {
		if m, ok := marks[next[i].Kind]; ok {
			next[i].HumanState = m
		}
	}
	return next
}

// ── Evidence kurucusu ───────────────────────────────────────────────────────

// evidenceFrom — SAF: kayıt → Score girdisi.
func evidenceFrom(inv *Investigation) Evidence {
	ev := Evidence{Service: inv.Service, Symptom: inv.Anchor.Symptom, StartNs: inv.ProblemAt, Synth: inv.SynthRef,
		Steps: map[string]string{}, Evidence: map[string][]evRef{}}
	if inv.Facts != nil {
		ev.Facts = *inv.Facts
	}
	for _, s := range inv.Steps {
		ev.Steps[s.Name] = s.State
		refs := make([]evRef, 0, len(s.Evidence))
		for _, e := range s.Evidence {
			refs = append(refs, evRef{Hop: e.Hop, Tool: e.Tool})
		}
		ev.Evidence[s.Name] = refs
	}
	if ev.StartNs <= 0 {
		ev.StartNs = inv.Anchor.From + int64(15*time.Minute)
	}
	return ev
}

// read — adım veri döndürdü mü (ok ya da kısmi).
func (ev Evidence) read(step string) bool {
	s := ev.Steps[step]
	return s == StepOK || s == StepPartial
}

// ref — "<adım>.evidences[i]" (halka eşleşen ilk künye; hop "" = ilk künye),
// künye yoksa "<adım>", adım yoksa "".
func (ev Evidence) ref(step, hop string) string {
	if _, ok := ev.Steps[step]; !ok {
		return ""
	}
	for i, r := range ev.Evidence[step] {
		if hop == "" || r.Hop == hop {
			return fmt.Sprintf("%s.evidences[%d]", step, i)
		}
	}
	return step
}

// refAt — "<adım>.evidences[i]" (künye varsa), yoksa ref(adım, "").
func (ev Evidence) refAt(step string, i int) string {
	if i >= 0 && i < len(ev.Evidence[step]) {
		return fmt.Sprintf("%s.evidences[%d]", step, i)
	}
	return ev.ref(step, "")
}

// Bağımlılık adımının künye sırası (deps.go depsEvidence): 0 topoloji,
// 1 korele değişiklikler, 2 blast radius.
const (
	depsEvTopology = 0
	depsEvChanges  = 1
	// depsEvExact — follow-up 2026-11-21: giden RED künyesi (yalnız okunduğunda).
	depsEvExact = 3
)

const (
	deployNearBefore = 60 * time.Minute
	deployNearAfter  = 5 * time.Minute
	deployHorizon    = 2 * time.Hour
)

// nearDeploys — başlangıcın [−60 dk, +5 dk] aralığındaki satırlar (zaman sırası).
func (ev Evidence) nearDeploys() []DeployFact {
	if ev.Facts.Deploys == nil {
		return nil
	}
	var out []DeployFact
	for _, r := range ev.Facts.Deploys.Rows {
		d := time.Duration(r.At - ev.StartNs)
		if d >= -deployNearBefore && d <= deployNearAfter {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

// deploysWithin — başlangıcın ±2 sa içindeki satır sayısı.
func (ev Evidence) deploysWithin() int {
	if ev.Facts.Deploys == nil {
		return 0
	}
	n := 0
	for _, r := range ev.Facts.Deploys.Rows {
		d := time.Duration(r.At - ev.StartNs)
		if d >= -deployHorizon && d <= deployHorizon {
			n++
		}
	}
	return n
}

// firstError — ilk hata anı (unix ns) + kaynağı. Hata belirtisinde problem
// başlangıcı; log artışı (ERROR seviyesi) ve hatalı temsilci trace daha erken
// bir anı gösterebilir. Bilinmiyorsa ok=false.
func (ev Evidence) firstError() (int64, string, bool) {
	at, src, _, ok := ev.firstErrorK()
	return at, src, ok
}

// firstErrorK — firstError + kaynağın metin anahtarı (v0.10.2301 S4; çizelge
// etiketi). Türkçe kaynak metni aynen.
func (ev Evidence) firstErrorK() (int64, string, string, bool) {
	var at int64
	src, key := "", ""
	take := func(ns int64, s, k string) {
		if ns > 0 && (at == 0 || ns < at) {
			at, src, key = ns, s, k
		}
	}
	if ev.Symptom == symptomError || ev.Symptom == symptomDown {
		take(ev.StartNs, "problem başlangıcı", "tl.src.start")
	}
	if sp := ev.Facts.Spike; sp != nil && sp.Level == "error" && sp.FromMs > 0 && sp.Factor >= spikeFindingFactor {
		take(sp.FromMs*int64(time.Millisecond), "ERROR log artışı", "tl.src.errorLog")
	}
	if pv := ev.Facts.Pivot; pv != nil && pv.ErrorSpans > 0 && pv.StartMs > 0 {
		take(pv.StartMs*int64(time.Millisecond), "hatalı temsilci trace", "tl.src.errorTrace")
	}
	return at, src, key, at > 0
}

func relMin(at, anchor int64) string {
	d := time.Duration(at - anchor)
	m := int(math.Round(math.Abs(d.Minutes())))
	switch {
	case m == 0:
		return "başlangıçla aynı dakikada"
	case d < 0:
		return fmt.Sprintf("başlangıçtan %d dk önce", m)
	}
	return fmt.Sprintf("başlangıçtan %d dk sonra", m)
}

func deployName(r DeployFact) string {
	n := r.Name
	if r.Version != "" {
		n += "@" + r.Version
	}
	return n
}

// imageChanged — satır imaj/sürüm değiştirdi mi.
func imageChanged(r DeployFact) bool {
	return r.Version != "" && r.Version != r.Prev
}

func deployFailed(st string) bool {
	switch strings.ToLower(strings.TrimSpace(st)) {
	case "stalled", "rolled_back", "stuck", "failed":
		return true
	}
	return false
}

// ── kontroller: deploy ──────────────────────────────────────────────────────

func chkDeployNear(ev Evidence) (int, string, string) {
	ref := ev.ref(stepDeploys, "")
	if !ev.read(stepDeploys) {
		return 0, "deploy geçmişi okunamadı", ref
	}
	if near := ev.nearDeploys(); len(near) > 0 {
		r := near[len(near)-1]
		return 1, fmt.Sprintf("%s %s (%s)", relMin(r.At, ev.StartNs), deployName(r), firstNonEmpty(r.Source, "sürüm geçişi")), ref
	}
	if ev.deploysWithin() == 0 {
		return -1, "başlangıcın ±2 sa içinde deploy / rollout yok", ref
	}
	return 0, "deploy var ama başlangıca yakın değil (−60/+5 dk dışında)", ref
}

func chkDeployBeforeError(ev Evidence) (int, string, string) {
	near := ev.nearDeploys()
	if len(near) == 0 {
		return 0, "başlangıca yakın deploy yok", ev.ref(stepDeploys, "")
	}
	e, src, ok := ev.firstError()
	if !ok {
		return 0, "ilk hata anı bilinmiyor (hata belirtisi / ERROR artışı / hatalı trace yok)", ev.ref(stepDeploys, "")
	}
	d := near[0]
	m := int(math.Round(time.Duration(e - d.At).Minutes()))
	if d.At <= e {
		return 1, fmt.Sprintf("%s ilk hatadan %d dk önce (%s)", deployName(d), m, src), ev.ref(stepDeploys, "")
	}
	return -1, fmt.Sprintf("hata %s deploy'dan %d dk önce başladı (%s)", deployName(d), -m, src), ev.ref(stepDeploys, "")
}

func chkRecurring(ev Evidence) (int, string, string) {
	near := ev.nearDeploys()
	pf := ev.Facts.Problem
	if len(near) == 0 {
		return 0, "başlangıca yakın deploy yok", ""
	}
	if pf == nil || pf.EpisodeCount < 2 {
		return 0, "yinelenme bilgisi yok (terfi edilmiş anomali bölümü değil)", ""
	}
	d := near[0]
	if chstore.AnomalyPredatesDeploy(pf.FirstStartedAt, ev.StartNs, pf.EpisodeCount, d.At) {
		return -1, fmt.Sprintf("anomali deploy'dan önce de düzenli görülüyor (%d bölüm)", pf.EpisodeCount), ""
	}
	return 0, fmt.Sprintf("%d bölüm var ama deploy'dan önce düzenli değil", pf.EpisodeCount), ""
}

func chkRolloutFailed(ev Evidence) (int, string, string) {
	if ev.Facts.Deploys == nil {
		return 0, "deploy geçmişi okunmadı", ""
	}
	for _, r := range ev.Facts.Deploys.Rows {
		if r.Source == "rollout" && deployFailed(r.Status) {
			return 1, fmt.Sprintf("rollout %s: %s", r.Status, deployName(r)), ev.ref(stepDeploys, "")
		}
	}
	return 0, "başarısız rollout yok", ev.ref(stepDeploys, "")
}

// ── kontroller: konfigürasyon ───────────────────────────────────────────────

// configChanges — yakın pencerede imaj değiştirmeyen rollout / deploy olayı.
func (ev Evidence) configChanges() []DeployFact {
	var out []DeployFact
	for _, r := range ev.nearDeploys() {
		if (r.Source == "rollout" || r.Source == "event") && !imageChanged(r) {
			out = append(out, r)
		}
	}
	return out
}

func chkConfigChange(ev Evidence) (int, string, string) {
	ref := ev.ref(stepDeploys, "")
	if !ev.read(stepDeploys) {
		return 0, "deploy / rollout geçmişi okunamadı", ref
	}
	if cc := ev.configChanges(); len(cc) > 0 {
		r := cc[len(cc)-1]
		return 1, fmt.Sprintf("%s imaj değişmeden %s: %s (config / secret değişikliği olası)", relMin(r.At, ev.StartNs), firstNonEmpty(r.Source, "değişiklik"), r.Name), ref
	}
	if ev.deploysWithin() == 0 {
		return -1, "başlangıcın ±2 sa içinde rollout / değişiklik yok", ref
	}
	return 0, "yakın değişiklik imaj değiştirdi ya da başlangıca yakın değil", ref
}

func chkConfigBeforeError(ev Evidence) (int, string, string) {
	cc := ev.configChanges()
	if len(cc) == 0 {
		return 0, "imajsız yakın değişiklik yok", ""
	}
	e, src, ok := ev.firstError()
	if !ok {
		return 0, "ilk hata anı bilinmiyor", ""
	}
	if cc[0].At <= e {
		return 1, fmt.Sprintf("değişiklik ilk hatadan önce (%s)", src), ev.ref(stepDeploys, "")
	}
	return -1, fmt.Sprintf("hata değişiklikten önce başladı (%s)", src), ev.ref(stepDeploys, "")
}

// argoSyncHorizon — Syncs24h'in kapsadığı geri bakış (okuma anından).
const argoSyncHorizon = 24 * time.Hour

// argoOwns — SAF: uygulama bu iş yükünü (ya da servisi) deploy ediyor mu;
// servise tek uygulama eşlenmişse o (recommend.go argoFor kuralı).
func argoOwns(a mcptools.ArgoAppStatus, workload, service string, apps int) bool {
	if apps == 1 {
		return true
	}
	for _, w := range a.Workloads {
		if w == workload || w == service {
			return true
		}
	}
	return false
}

// argoCalm — Synced + Healthy + okunmuş sıfır senkron.
func argoCalm(a mcptools.ArgoAppStatus) bool {
	return strings.EqualFold(a.Sync, "Synced") && strings.EqualFold(a.Health, "Healthy") && a.Syncs24h != nil && *a.Syncs24h == 0
}

// chkArgo — v0.10.1310: Facts.Argo (koşu başına tek Deps.ArgoApps okuması)
// üzerinden SAF. Dikiş zaman damgası taşımaz; [başlangıç − 60 dk, +5 dk]
// penceresi okuma anının 24 saatlik senkron sayımıyla (Syncs24h) kapsanır.
//
//	+1  servise eşlenen bir uygulama OutOfSync; ya da son 24 sa'te senkronu
//	    olan uygulamanın iş yükünde pencerede İMAJSIZ rollout / olay var
//	    (senkron imaj değiştirmeden config getirmiş).
//	−1  her uygulama Synced + Healthy, 24 sa'te senkron yok VE okuma anı
//	    pencereyi kapsıyor (ReadAt − 24 sa ≤ başlangıç − 60 dk, ReadAt ≥ başlangıç).
//	 0  okunmadı / okunamadı / eşleme yok / tekrar (Argo durumu "şimdi"dir,
//	    as-of değil — geçmiş vaka canlı durumla puanlanmaz) / senkron var ama
//	    pencereye bağlanamıyor.
func chkArgo(ev Evidence) (int, string, string) {
	a := ev.Facts.Argo
	switch {
	case a == nil:
		return 0, "Argo CD durumu okunmadı (dikiş bağlı değil)", ""
	case a.State == argoStateReplay:
		return 0, firstNonEmpty(a.Note, ReplayArgoNote), ""
	case a.State != argoStateOK:
		return 0, "Argo CD durumu okunamadı", ""
	case len(a.Apps) == 0:
		return 0, "servise eşlenen Argo CD uygulaması yok", ""
	}
	for _, app := range a.Apps {
		if strings.EqualFold(app.Sync, "OutOfSync") {
			return 1, fmt.Sprintf("Argo CD %s OutOfSync (%s) — canlı durum Git'teki tanımdan sapıyor", app.App, argoStatusTR(app)), ""
		}
	}
	for _, r := range ev.configChanges() {
		for _, app := range a.Apps {
			if app.Syncs24h != nil && *app.Syncs24h > 0 && argoOwns(app, r.Name, ev.Service, len(a.Apps)) {
				return 1, fmt.Sprintf("Argo CD %s son 24 sa'te %d senkron; %s imaj değişmeden %s (%s) — senkron config getirmiş olası",
					app.App, *app.Syncs24h, relMin(r.At, ev.StartNs), firstNonEmpty(r.Source, "değişiklik"), r.Name), ev.ref(stepDeploys, "")
			}
		}
	}
	calm := true
	var names []string
	for _, app := range a.Apps {
		calm = calm && argoCalm(app)
		names = append(names, app.App)
	}
	list := strings.Join(names, ", ")
	if calm {
		read := time.Unix(0, a.ReadAt)
		from := time.Unix(0, ev.StartNs).Add(-deployNearBefore)
		if a.ReadAt > 0 && !read.Add(-argoSyncHorizon).After(from) && a.ReadAt >= ev.StartNs {
			return -1, fmt.Sprintf("Argo CD %s Synced/Healthy, pencerede (−60/+5 dk) senkron yok", list), ""
		}
		return 0, fmt.Sprintf("Argo CD %s Synced/Healthy ama okuma anının 24 sa'i başlangıç penceresini kapsamıyor", list), ""
	}
	return 0, fmt.Sprintf("Argo CD %s: senkron var ya da sayım okunmadı, pencereye bağlanamıyor (imajsız yakın değişiklik yok)", list), ""
}

// ── kontroller: doygunluk / OOM ─────────────────────────────────────────────

func pct(p *float64) float64 {
	if p == nil {
		return -1
	}
	return *p
}

func chkPodLimits(ev Evidence) (int, string, string) {
	pf := ev.Facts.Pods
	ref := ev.ref(stepSweep, "pods")
	if pf == nil || (pf.PodsState != "ok" && pf.PodsState != "partial") || pf.Pods == 0 {
		why := "pod metrikleri okunamadı / pod yok"
		if pf != nil && pf.PodsState == "skipped" {
			why = "pod metrikleri yok (Thanos yapılandırılmamış)"
		}
		return 0, why, ref
	}
	if len(pf.OOMPods) > 0 {
		return 1, "pencerede OOMKilled: " + strings.Join(pf.OOMPods, ", "), ref
	}
	cpu, mem := pct(pf.MaxCPUPct), pct(pf.MaxMemPct)
	switch {
	case cpu >= 90:
		return 1, fmt.Sprintf("%s CPU limitinin %%%.0f'i", pf.HotPod, cpu), ref
	case mem >= 90:
		return 1, fmt.Sprintf("%s bellek limitinin %%%.0f'i", pf.HotPod, mem), ref
	case cpu < 0 && mem < 0:
		return 0, "pod limitleri tanımsız — limit yüzdesi yok", ref
	case cpu < 70 && mem < 70:
		return -1, fmt.Sprintf("%d pod limitin altında (CPU ≤ %%%.0f, bellek ≤ %%%.0f)", pf.Pods, math.Max(cpu, 0), math.Max(mem, 0)), ref
	}
	return 0, fmt.Sprintf("pod'lar limite yakın ama eşik altı (CPU %%%.0f, bellek %%%.0f)", math.Max(cpu, 0), math.Max(mem, 0)), ref
}

func chkJVM(ev Evidence) (int, string, string) {
	pf := ev.Facts.Pods
	ref := ev.ref(stepSweep, "jvm")
	if pf == nil || pf.JVMPods == 0 || pf.MaxPostGCPct == nil {
		return 0, "JVM post-GC verisi yok", ref
	}
	v := *pf.MaxPostGCPct
	switch {
	case v >= 85:
		return 1, fmt.Sprintf("%s post-GC heap %%%.0f", pf.HotJVM, v), ref
	case v < 60:
		return -1, fmt.Sprintf("post-GC heap ≤ %%%.0f (%d pod)", v, pf.JVMPods), ref
	}
	return 0, fmt.Sprintf("post-GC heap %%%.0f (eşik %%85)", v), ref
}

func chkNode(ev Evidence) (int, string, string) {
	nf := ev.Facts.Node
	ref := ev.ref(stepNode, "")
	if nf == nil {
		return 0, "node kontrolü koşmadı (pod kanıtı yeterli ya da Thanos yok)", ""
	}
	if nf.State != "ok" && nf.State != "partial" {
		return 0, firstNonEmpty(nf.Summary, "node okunamadı"), ref
	}
	known, calm := 0, true
	for _, n := range nf.Nodes {
		if n.CPUPct != nil {
			known++
			if *n.CPUPct >= 90 {
				return 1, fmt.Sprintf("node %s CPU %%%.0f (allocatable'ın)", n.Node, *n.CPUPct), ref
			}
			if *n.CPUPct >= 70 {
				calm = false
			}
		}
		for _, p := range n.TopPods {
			if !p.Service && p.Pct >= 40 {
				return 1, fmt.Sprintf("gürültülü komşu %s node %s CPU'sunun %%%.0f'i", p.Pod, n.Node, p.Pct), ref
			}
		}
	}
	if known > 0 && known == len(nf.Nodes) && calm {
		return -1, fmt.Sprintf("%d node'da baskı / gürültülü komşu yok", known), ref
	}
	return 0, "node baskısı belirsiz", ref
}

func chkLatencySymptom(ev Evidence) (int, string, string) {
	if ev.Symptom == symptomLatency || ev.Symptom == symptomSaturation {
		return 1, "belirti gecikme / doygunluk", ""
	}
	return 0, "belirti gecikme değil", ""
}

// ── kontroller: bağımlılık ──────────────────────────────────────────────────

func (ev Evidence) serviceEdges() []EdgeFact {
	if ev.Facts.Deps == nil {
		return nil
	}
	var out []EdgeFact
	for _, e := range ev.Facts.Deps.Edges {
		if isServiceNode(e.Target) {
			out = append(out, e)
		}
	}
	return out
}

func chkDownstreamWorse(ev Evidence) (int, string, string) {
	df := ev.Facts.Deps
	ref := ev.refAt(stepDeps, depsEvChanges)
	if df == nil || !df.TopologyOK || !df.ChangesOK {
		return 0, "topoloji / korele değişiklikler okunamadı", ref
	}
	for _, c := range df.Causes {
		if (c.Relation == chstore.RelationDownstream || c.Relation == chstore.RelationBoth) &&
			(c.Direction == chstore.ChangeWorse || c.Direction == chstore.ChangeLost) {
			verb := "kötüleşti"
			if c.Direction == chstore.ChangeLost {
				verb = "trafiği kesildi"
			}
			return 1, fmt.Sprintf("aşağı akış %s %s (kenarı doğrulanmış korelasyon)", c.Service, verb), ref
		}
	}
	if len(ev.serviceEdges()) == 0 {
		return -1, "aşağı akış servis bağımlılığı (kenar) yok", ref
	}
	return -1, fmt.Sprintf("%d aşağı akış komşudan hiçbiri kötüleşmedi", len(ev.serviceEdges())), ref
}

func chkSynthAgrees(ev Evidence) (int, string, string) {
	if ev.Synth == nil || ev.Synth.TopSuspect == "" {
		return 0, "sentezleyicinin şüphelisi yok", ""
	}
	top := ev.Synth.TopSuspect
	if top == ev.Service {
		return -1, fmt.Sprintf("sentezleyici servisin kendisini gösteriyor (%s)", top), ""
	}
	for _, e := range ev.serviceEdges() {
		if e.Target == top {
			return 1, fmt.Sprintf("sentezleyici de aşağı akış %s'i gösteriyor (güven %%%.0f)", top, ev.Synth.Confidence*100), ""
		}
	}
	return 0, fmt.Sprintf("sentezleyici şüphelisi %s aşağı akış komşu değil", top), ""
}

func chkDownstreamErrors(ev Evidence) (int, string, string) {
	df := ev.Facts.Deps
	ref := ev.refAt(stepDeps, depsEvTopology)
	if df == nil || !df.TopologyOK {
		return 0, "topoloji okunamadı", ref
	}
	edges := ev.serviceEdges()
	if len(edges) == 0 {
		return 0, "aşağı akış servis kenarı yok", ref
	}
	clean := true
	for _, e := range edges {
		if e.ErrorRate >= 5 && e.Calls >= 20 {
			return 1, fmt.Sprintf("%s çağrılarında hata %%%.1f", e.Target, e.ErrorRate), ref
		}
		if e.ErrorRate >= 1 {
			clean = false
		}
	}
	if clean {
		return -1, "aşağı akış çağrılarında hata < %1", ref
	}
	return 0, "aşağı akış hataları eşik altı", ref
}

// Tam sayım eşikleri: hata oranı ≥ %5 ve ≥ 0,1 istek/sn (pencerede ~30+
// çağrı); gecikme payı: hedef p95 ≥ servis p99'unun yarısı VE servis p99'u
// ≥ %20 artmış.
const (
	exactErrPct     = 5.0
	exactErrMinRPS  = 0.1
	exactCleanPct   = 1.0
	exactLatShare   = 0.5
	exactP99RisePct = 20.0
)

// chkDownstreamExact — follow-up 2026-11-21: hedef başına giden RED, VM
// HTTP client histogramından (prod span'leri upstream örneklenmiş; bu sayım
// örnekleme ÖNCESİ). Hata ya da gecikme baskınlığı → +1; tüm hedefler temiz
// → -1; aksi 0.
func chkDownstreamExact(ev Evidence) (int, string, string) {
	df := ev.Facts.Deps
	ref := ev.refAt(stepDeps, depsEvExact)
	if df == nil || !df.ExactOK {
		return 0, "giden çağrı tam sayımı okunmadı", ref
	}
	if len(df.Exact) == 0 {
		return 0, "giden HTTP çağrısı yok (client histogramı boş)", ref
	}
	name := func(t runtimesig.TargetRED) string {
		if t.Service != "" {
			return t.Service + " (" + t.Address + ")"
		}
		return t.Address
	}
	clean := true
	for _, t := range df.Exact {
		if t.ErrorPct >= exactErrPct && t.RPS >= exactErrMinRPS {
			why := fmt.Sprintf("giden çağrı %s: hata %%%.1f (%.2f istek/sn, tam sayım)", name(t), t.ErrorPct, t.RPS)
			if t.DominantError != "" {
				why += fmt.Sprintf(", baskın hata %s (%%%.0f)", t.DominantError, t.DominantErrorPct)
			}
			if t.P95Ms >= 0 {
				why += fmt.Sprintf(", p95 %.0f ms", t.P95Ms)
			}
			return 1, why, ref
		}
		if t.ErrorPct >= exactCleanPct {
			clean = false
		}
	}
	if s := df.Subject; s != nil && s.CurP99Ms > 0 && s.P99DeltaPct >= exactP99RisePct {
		for _, t := range df.Exact {
			if t.P95Ms >= exactLatShare*s.CurP99Ms {
				return 1, fmt.Sprintf("giden çağrı %s p95 %.0f ms — servis p99'unun (%.0f ms, +%%%.0f) %%%.0f'i (tam sayım)",
					name(t), t.P95Ms, s.CurP99Ms, s.P99DeltaPct, t.P95Ms/s.CurP99Ms*100), ref
			}
		}
	}
	if clean {
		return -1, fmt.Sprintf("%d giden hedefin hepsinde hata < %%1 (tam sayım)", len(df.Exact)), ref
	}
	return 0, "giden çağrı hataları eşik altı (tam sayım)", ref
}

// ── kontroller: dış servis ──────────────────────────────────────────────────

func (ev Evidence) edgesWithPrefix(prefix string) []EdgeFact {
	if ev.Facts.Deps == nil {
		return nil
	}
	var out []EdgeFact
	for _, e := range ev.Facts.Deps.Edges {
		if strings.HasPrefix(e.Target, prefix) {
			out = append(out, e)
		}
	}
	return out
}

// relatedMatch — olaylar adımının aşağı akış düğümü problemleri arasında
// önekli kural + hedefle eşleşen ilk satır.
func (ev Evidence) relatedMatch(rulePrefixes []string, targets []EdgeFact) *AlertFact {
	ef := ev.Facts.Events
	if ef == nil {
		return nil
	}
	want := map[string]bool{}
	for _, t := range targets {
		want[t.Target] = true
		want[strings.TrimPrefix(strings.TrimPrefix(t.Target, "ext:"), "db:")] = true
	}
	for i := range ef.Related {
		a := &ef.Related[i]
		if !want[a.Service] {
			continue
		}
		for _, p := range rulePrefixes {
			if strings.HasPrefix(a.RuleID, p) {
				return a
			}
		}
	}
	return nil
}

func chkExternalProblem(ev Evidence) (int, string, string) {
	ext := ev.edgesWithPrefix("ext:")
	ref := ev.ref(stepEvents, "")
	if len(ext) == 0 {
		return 0, "dış bağımlılık kenarı yok", ref
	}
	if a := ev.relatedMatch([]string{"anomaly:ext-down:", "anomaly:ext:", "anomaly-cluster:ext:"}, ext); a != nil {
		return 1, fmt.Sprintf("dış kaynak problemi açık: %s (%s)", firstNonEmpty(a.RuleName, a.RuleID), a.Service), ref
	}
	if ev.Facts.Events != nil && ev.Facts.Events.RelatedOK {
		return -1, fmt.Sprintf("%d dış bağımlılık için pencerede problem yok", len(ext)), ref
	}
	return 0, "dış bağımlılık problemleri okunamadı", ref
}

func chkExternalEdges(ev Evidence) (int, string, string) {
	ext := ev.edgesWithPrefix("ext:")
	ref := ev.refAt(stepDeps, depsEvTopology)
	if len(ext) == 0 {
		return 0, "dış bağımlılık kenarı yok", ref
	}
	clean := true
	for _, e := range ext {
		if e.ErrorRate >= 20 && e.Calls >= 10 {
			return 1, fmt.Sprintf("%s çağrılarında hata %%%.0f", e.Target, e.ErrorRate), ref
		}
		if e.ErrorRate >= 1 {
			clean = false
		}
	}
	if clean {
		return -1, "dış çağrılarda hata < %1", ref
	}
	return 0, "dış çağrı hataları eşik altı", ref
}

// ── kontroller: veritabanı ──────────────────────────────────────────────────

// dbLike — SAF: kritik yol adımı veritabanı çağrısına benziyor mu.
func dbLike(p PathFact) bool {
	n := strings.ToLower(strings.TrimSpace(p.Name))
	if strings.HasPrefix(p.Service, "db:") {
		return true
	}
	for _, kw := range []string{"select ", "insert ", "update ", "delete ", "merge ", "call ", "exec "} {
		if strings.HasPrefix(n+" ", kw) || strings.HasPrefix(n, kw) {
			return true
		}
	}
	for _, kw := range []string{"jdbc", "sql", "postgres", "mysql", "oracle", "mongo", "redis", "db.", "database", "couchbase", "cassandra"} {
		if strings.Contains(n, kw) {
			return true
		}
	}
	return false
}

func chkDBHealth(ev Evidence) (int, string, string) {
	dbs := ev.edgesWithPrefix("db:")
	ref := ev.ref(stepEvents, "")
	if len(dbs) == 0 {
		return 0, "veritabanı kenarı yok", ref
	}
	if a := ev.relatedMatch([]string{chstore.RuleDBHealthPrefix, "db-capacity:", "db-slow-stmt"}, dbs); a != nil {
		return 1, fmt.Sprintf("çağrılan veritabanında problem açık: %s (%s)", firstNonEmpty(a.RuleName, a.RuleID), a.Service), ref
	}
	if ev.Facts.Events != nil && ev.Facts.Events.RelatedOK {
		return -1, fmt.Sprintf("%d veritabanı için pencerede db-health problemi yok", len(dbs)), ref
	}
	return 0, "veritabanı problemleri okunamadı", ref
}

func chkPivotDBPath(ev Evidence) (int, string, string) {
	pv := ev.Facts.Pivot
	ref := ev.ref(stepPivot, "trace")
	if pv == nil || len(pv.Path) == 0 || pv.WallMs <= 0 {
		return 0, "temsilci trace'in kritik yolu yok", ref
	}
	top := pv.Path[0]
	share := top.SelfMs / pv.WallMs * 100
	if dbLike(top) && share >= 40 {
		return 1, fmt.Sprintf("kritik yolun %%%.0f'i DB adımı (%s)", share, truncRunes(top.Name, 60)), ref
	}
	for _, p := range pv.Path {
		if dbLike(p) {
			return 0, fmt.Sprintf("DB adımı kritik yolda ama baskın değil (%s)", truncRunes(p.Name, 60)), ref
		}
	}
	return -1, fmt.Sprintf("kritik yolun en büyük adımı DB değil (%s)", truncRunes(top.Name, 60)), ref
}

func chkDBEdge(ev Evidence) (int, string, string) {
	dbs := ev.edgesWithPrefix("db:")
	ref := ev.refAt(stepDeps, depsEvTopology)
	if len(dbs) == 0 {
		return 0, "veritabanı kenarı yok", ref
	}
	calm := true
	for _, e := range dbs {
		if e.AvgMs >= 500 || (e.ErrorRate >= 5 && e.Calls >= 20) {
			return 1, fmt.Sprintf("%s ort. %.0f ms, hata %%%.1f", e.Target, e.AvgMs, e.ErrorRate), ref
		}
		if e.AvgMs >= 50 || e.ErrorRate >= 1 {
			calm = false
		}
	}
	if calm {
		return -1, "veritabanı çağrıları hızlı (ort. < 50 ms, hata < %1)", ref
	}
	return 0, "veritabanı çağrıları eşik altı", ref
}

// ── kontroller: trafik sıçraması ────────────────────────────────────────────

func chkRateUp(ev Evidence) (int, string, string) {
	ref := ev.refAt(stepDeps, depsEvChanges)
	if ev.Facts.Deps == nil || ev.Facts.Deps.Subject == nil {
		return 0, "servisin hız değişimi okunamadı", ref
	}
	r := ev.Facts.Deps.Subject.RateDeltaPct
	switch {
	case r >= 50:
		return 1, fmt.Sprintf("istek hızı %+.0f%% (önceki pencereye göre)", r), ref
	case r <= 10:
		return -1, fmt.Sprintf("istek hızı düz / düşük (%+.0f%%)", r), ref
	}
	return 0, fmt.Sprintf("istek hızı %+.0f%% (eşik +%%50)", r), ref
}

func chkP99Up(ev Evidence) (int, string, string) {
	ref := ev.refAt(stepDeps, depsEvChanges)
	if ev.Facts.Deps == nil || ev.Facts.Deps.Subject == nil {
		return 0, "servisin p99 değişimi okunamadı", ref
	}
	sc := ev.Facts.Deps.Subject
	p := sc.P99DeltaPct
	switch {
	case p <= 0:
		return -1, fmt.Sprintf("p99 artmadı (%+.0f%%)", p), ref
	case p < 100*healthverdict.LatWarnRel:
		return 0, fmt.Sprintf("p99 %+.0f%% (eşik +%%25)", p), ref
	case !healthverdict.LatencyFloorsMet(sc.CurP99Ms, sc.BaseP99Ms):
		// v0.10.1299 — göreli artış mutlak tabanı geçmedi (≥ 100 ms ve ≥ +50 ms).
		return 0, fmt.Sprintf("p99 %+.0f%% ama %.0f ms → %.0f ms — pratik eşik altı (≥ %.0f ms ve ≥ +%.0f ms)",
			p, sc.BaseP99Ms, sc.CurP99Ms, healthverdict.LatMinP99Ms, healthverdict.LatWarnDeltaMs), ref
	case ev.Facts.Pods == nil || ev.Facts.Pods.LatencyCorroborated == 0:
		// v0.10.1299 — MV p99 farkı tek başına OLASI sinyal; ham span doğrulaması
		// (servis taramasının gecikme adayları) yoksa bilinmez.
		return 0, fmt.Sprintf("p99 %+.0f%% (%.0f → %.0f ms) ama ham span doğrulaması yok — olası", p, sc.BaseP99Ms, sc.CurP99Ms), ref
	}
	return 1, fmt.Sprintf("p99 %+.0f%% (%.0f → %.0f ms; %d operasyonda ham span doğruladı)", p, sc.BaseP99Ms, sc.CurP99Ms, ev.Facts.Pods.LatencyCorroborated), ref
}

func chkUpstreamSurge(ev Evidence) (int, string, string) {
	df := ev.Facts.Deps
	if df == nil || !df.ChangesOK {
		return 0, "korele değişiklikler okunamadı", ev.refAt(stepDeps, depsEvChanges)
	}
	for _, c := range df.Causes {
		if c.Relation == chstore.RelationUpstream {
			return 1, fmt.Sprintf("çağıran %s trafiğini artırdı", c.Service), ev.refAt(stepDeps, depsEvChanges)
		}
	}
	return 0, "çağıranlarda trafik sıçraması yok", ev.refAt(stepDeps, depsEvChanges)
}

// ── kontroller: kod gerilemesi / yeni exception ─────────────────────────────

func chkNewPattern(ev Evidence) (int, string, string) {
	if sp := ev.Facts.Spike; sp != nil && sp.Factor >= spikeFindingFactor {
		existed := 0
		for _, c := range sp.Contributors {
			if c.Dim != "pattern" && c.Dim != "error_type" {
				continue
			}
			if c.New {
				what := "desen"
				if c.Dim == "error_type" {
					what = "hata türü"
				}
				return 1, fmt.Sprintf("yeni %s «%s» — artışın %%%.0f'i", what, truncRunes(c.Value, 80), c.SharePct), ev.ref(stepLogSpike, "patterns")
			}
			if !c.BaseUnknown && c.BaseCount > 0 {
				existed++
			}
		}
		if existed > 0 {
			return -1, "artışın desenleri önceki pencerede de vardı", ev.ref(stepLogSpike, "patterns")
		}
	}
	if pf := ev.Facts.Pods; pf != nil && len(pf.NewPatterns) > 0 {
		return 1, "yeni log deseni: «" + truncRunes(pf.NewPatterns[0], 80) + "»", ev.ref(stepSweep, "patterns")
	}
	return 0, "yeni hata deseni görülmedi (desen okunmadı ya da artış yok)", ""
}

func chkAfterDeploy(ev Evidence) (int, string, string) {
	ref := ev.ref(stepDeploys, "")
	if !ev.read(stepDeploys) {
		return 0, "deploy geçmişi okunamadı", ref
	}
	near := ev.nearDeploys()
	if len(near) == 0 {
		if ev.deploysWithin() == 0 {
			return -1, "başlangıcın ±2 sa içinde deploy yok", ref
		}
		return 0, "başlangıca yakın deploy yok", ref
	}
	for _, d := range near {
		if imageChanged(d) || d.Source == "" {
			e, _, ok := ev.firstError()
			if !ok || d.At <= e {
				return 1, fmt.Sprintf("hatalar %s sonrasında", deployName(d)), ref
			}
		}
	}
	return 0, "yakın değişiklik imaj değiştirmedi ya da hatadan sonra", ref
}

func chkPivotErrors(ev Evidence) (int, string, string) {
	pv := ev.Facts.Pivot
	ref := ev.ref(stepPivot, "trace")
	if pv == nil || pv.TraceID == "" {
		return 0, "temsilci trace yok", ref
	}
	if pv.ErrorSpans > 0 {
		return 1, fmt.Sprintf("temsilci trace'te %d hatalı span", pv.ErrorSpans), ref
	}
	return 0, "temsilci trace'te hatalı span yok", ref
}

func chkErrorSymptom(ev Evidence) (int, string, string) {
	if ev.Symptom == symptomError {
		return 1, "belirti hata", ""
	}
	return 0, "belirti hata değil", ""
}

// ── işaret (Ele / Onayla) ───────────────────────────────────────────────────

// MarkRequest — operatörün kart işareti.
type MarkRequest struct {
	Kind  string
	State string // dismissed | confirmed (| markClear — kararı kaldır)
	Note  string
	By    string
	Now   time.Time
}

// Mark hataları.
var (
	ErrMarkInvalid  = fmt.Errorf("geçersiz işaret")
	ErrMarkNotFound = fmt.Errorf("araştırma ya da hipotez yok")
	ErrMarkBusy     = fmt.Errorf("araştırma sürüyor — bitince işaretleyin")
)

// markNoteMax — notun rune tavanı.
const markNoteMax = 500

// ApplyHypothesisMark — kayıttaki hipotezin HumanState'ini yazar. Skor, bant
// ve kontroller DEĞİŞMEZ. Kuyrukta / koşan kayıt reddedilir (drain'in
// checkpoint'i işareti ezerdi); paylaşılan kayıt da (kartlar kaynağın).
// Tek yazım kendi kaydı (QueueStore).
func ApplyHypothesisMark(ctx context.Context, st QueueStore, problemID string, m MarkRequest) (Hypothesis, error) {
	if !IsHypothesisKind(m.Kind) || (m.State != MarkDismissed && m.State != MarkConfirmed && m.State != markClear) {
		return Hypothesis{}, ErrMarkInvalid
	}
	inv, ok, err := Load(ctx, st, problemID)
	if err != nil {
		return Hypothesis{}, err
	}
	if !ok {
		return Hypothesis{}, ErrMarkNotFound
	}
	switch inv.State {
	case StateQueued, StateRunning:
		return Hypothesis{}, ErrMarkBusy
	}
	idx := -1
	for i, h := range inv.Hypotheses {
		if h.Kind == m.Kind {
			idx = i
		}
	}
	if idx < 0 {
		return Hypothesis{}, ErrMarkNotFound
	}
	now := m.Now
	if now.IsZero() {
		now = time.Now()
	}
	if m.State == markClear {
		inv.Hypotheses[idx].HumanState = nil
	} else {
		inv.Hypotheses[idx].HumanState = &HumanState{State: m.State, Note: truncRunes(strings.TrimSpace(m.Note), markNoteMax), By: m.By, At: now.UnixNano()}
	}
	// Kanıt kapısı kararı kayıtta: ekip kararı değişti → aynı kip ile yeniden
	// (SAF; okuma yok). Kip off / plain → Coverage nil kalır.
	if inv.Coverage != nil && !inv.Plain {
		g := evaluateCoverage(inv.Hypotheses, inv.Playbook, inv.Reach, inv.Coverage.Mode)
		inv.Coverage = &g
	}
	if err := save(ctx, st, inv); err != nil {
		return Hypothesis{}, err
	}
	return inv.Hypotheses[idx], nil
}
