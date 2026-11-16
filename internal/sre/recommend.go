package sre

// recommend.go — v0.10.1293 — AI SRE Faz 4: yalnız öneri (runbook eşleme +
// geri alma metni).
//
// OPERATÖR KURALI: YALNIZ ÖNERİ. Ajan hiçbir şeyi uygulamaz — runbook
// çalıştırmaz, rollout geri almaz, Argo senkronu tetiklemez, problem /
// öncelik yazmaz. Öneri bir METİNDİR (+ insanın açacağı bir UI bağlantısı):
//
//   - hipotez önerisi: operatörün elemediği, bandı ≥ orta ilk ≤3 hipotez için
//     gerçeklerden (Investigation.Facts) türeyen tek cümle + gerekçe;
//   - geri alma METNİ: deploy adımının ZATEN okuduğu ListDeploymentsWindow
//     satırlarından (Facts.Deploys; yeni okuma yok) — "<iş yükü> v42 → v41
//     geri alma değerlendirilebilir (Argo: OutOfSync)". Argo eki yalnız
//     Deps.ArgoApps dikişi bağlıysa (api/sre_argo.go; servis → Argo eşlemesi
//     api katmanında, internal/sre api'yi import etmez); v0.10.1310: eki
//     kayıttaki Facts.Argo besler (koşu başına TEK okuma, argo_sync ile ortak);
//   - runbook: SAF matchRunbooks — Problem.RunbookURL → etkin runbook'ların
//     service:<svc> / cause:<tür> / rule:<önek> etiketleri → başlık jetonu
//     örtüşmesi ≥0.5; ≤3. "Runbook'u aç" mevcut runbook sayfasına problemId
//     ile gider; çalıştırmayı İNSAN başlatır (mevcut runbook.execute audit'i).
//
// Durum "öneri — uygulanmadı" (RecPending); operatör "Uygulandı" (ack) ya da
// "Ele" (dismissed) der — POST …/investigation/recommendations/{rid}
// (editor+, audit sre.recommendation.ack). İşaret yalnız etikettir: yeniden
// koşuda kimliğe göre taşınır, hipotez skoruna / plana hiçbir yoldan girmez.
// Korkuluklar: TestSRENoMutatingCalls, TestSREImports,
// TestSRENeverUpsertsProblemsOrPriority, TestRecommendationIsTextOnly,
// TestMatchRunbooks.

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/safelink"
)

// Öneri durumları.
const (
	RecPending   = "öneri — uygulanmadı"
	RecAck       = "ack"
	RecDismissed = "dismissed"
)

const (
	// RecommendationsMax — kayıttaki en çok öneri (≤3 hipotez + ≤3 runbook).
	RecommendationsMax = 6
	// RecommendationsInChat — get_problem / anlatım girdisine giden öneri.
	RecommendationsInChat = 2
	// runbookMatchMax — eşleşen en çok runbook.
	runbookMatchMax = 3
	// runbookTitleOverlapMin — başlık jetonlarının bağlamda bulunma oranı.
	runbookTitleOverlapMin = 0.5
	recTextMax             = 300
	recRationaleMax        = 400
)

// RecRunbook — önerinin runbook bağlantısı (insan açar). URL bir UI yoludur
// (/runbook?id=…&problemId=…) ya da problemin kendi runbook bağlantısı —
// asla /api/ ucu (TestRecommendationIsTextOnly).
type RecRunbook struct {
	ID    string `json:"id,omitempty"` // "" = dış bağlantı (Problem.RunbookURL)
	Title string `json:"title"`
	URL   string `json:"url"`
}

// RecArgo — servisin Argo CD uygulamasının okunan durumu. PrevRevision:
// Argo metrikleri revizyon geçmişi taşımaz — rollout satırının önceki sürümü
// (yoksa boş).
type RecArgo struct {
	App          string `json:"app"`
	Status       string `json:"status"`
	PrevRevision string `json:"prevRevision,omitempty"`
}

// RecRollout — geri alma METNİNİN dayandığı rollout / deploy satırı.
type RecRollout struct {
	Workload string `json:"workload"`
	From     string `json:"from"` // şimdiki sürüm
	To       string `json:"to"`   // geri dönülebilecek önceki sürüm
}

// Recommendation — tek öneri kartı. YALNIZ METİN + bağlantı.
type Recommendation struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	// TextMsg — v0.10.2301 (S4): Text'in metin anahtarı (ek alan; Text aynen;
	// MCP özeti Text'i kopyalar). Rationale kontrol gerekçesi taşır → özgün.
	TextMsg        *RecTextMsg `json:"textMsg,omitempty"`
	Rationale      string      `json:"rationale"`
	HypothesisKind string      `json:"hypothesisKind,omitempty"`
	Runbook        *RecRunbook `json:"runbook,omitempty"`
	Argo           *RecArgo    `json:"argo,omitempty"`
	Rollout        *RecRollout `json:"rollout,omitempty"`
	State          string      `json:"state"`
	AckBy          string      `json:"ackBy,omitempty"`
	AckAt          int64       `json:"ackAt,omitempty"` // unix ns
}

// RecTextMsg — v0.10.2301 (S4): öneri metninin anahtarı. JSON şekli
// chstore.StdMsg ile AYNI ({k, a}; FE stdMsgText çizer) ama tip ağacı yalnız
// metin: öneri harita / arayüz taşımaz (TestRecommendationIsTextOnly).
type RecTextMsg struct {
	K string       `json:"k"`
	A *RecTextArgs `json:"a,omitempty"`
}

// RecTextArgs — öneri anahtarlarının ham argümanları (hepsi metin).
type RecTextArgs struct {
	Name   string `json:"name,omitempty"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Argo   string `json:"argo,omitempty"`
	ArgoK  string `json:"argoK,omitempty"`
	App    string `json:"app,omitempty"`
	Pods   string `json:"pods,omitempty"`
	Pod    string `json:"pod,omitempty"`
	CPU    string `json:"cpu,omitempty"`
	Mem    string `json:"mem,omitempty"`
	GC     string `json:"gc,omitempty"`
	Svc    string `json:"svc,omitempty"`
	Target string `json:"target,omitempty"`
	DB     string `json:"db,omitempty"`
	D      string `json:"d,omitempty"`
	Title  string `json:"title,omitempty"`
}

// recMsg — SAF: anahtar + ad/değer çiftleri (bilinmeyen ad yok sayılır —
// testte fikstür her argümanın çizildiğini doğrular).
func recMsg(k string, kv ...string) *RecTextMsg {
	m := &RecTextMsg{K: k}
	if len(kv) == 0 {
		return m
	}
	a := &RecTextArgs{}
	for i := 0; i+1 < len(kv); i += 2 {
		v := kv[i+1]
		switch kv[i] {
		case "name":
			a.Name = v
		case "from":
			a.From = v
		case "to":
			a.To = v
		case "argo":
			a.Argo = v
		case "argoK":
			a.ArgoK = v
		case "app":
			a.App = v
		case "pods":
			a.Pods = v
		case "pod":
			a.Pod = v
		case "cpu":
			a.CPU = v
		case "mem":
			a.Mem = v
		case "gc":
			a.GC = v
		case "svc":
			a.Svc = v
		case "target":
			a.Target = v
		case "db":
			a.DB = v
		case "d":
			a.D = v
		case "title":
			a.Title = v
		}
	}
	m.A = a
	return m
}

// recInput — SAF buildRecommendations'ın girdisi.
type recInput struct {
	ProblemID string
	Problem   chstore.Problem // RunbookURL okuma zenginleştirmesinden
	Service   string
	StartNs   int64
	Hyps      []Hypothesis
	Facts     *Facts
	Books     []chstore.Runbook
	Argo      []mcptools.ArgoAppStatus
	ArgoRead  bool // Argo okuması denendi ve başarılı
}

// activeHyps — SAF: önerilebilir hipotezler (bant ≥ orta, operatör elememiş;
// skor sırası korunur), ≤HypothesesShown.
func activeHyps(hs []Hypothesis) []Hypothesis {
	var out []Hypothesis
	for i, h := range hs {
		if i >= HypothesesShown {
			break
		}
		if h.Band == BandLow || (h.HumanState != nil && h.HumanState.State == MarkDismissed) {
			continue
		}
		out = append(out, h)
	}
	return out
}

// rollbackCandidate — SAF: başlangıcın [−60 dk, +5 dk] aralığındaki EN YENİ
// sürüm değiştiren satır (önceki sürümü bilinen). Zaten geri alınmış rollout
// aday değildir.
func rollbackCandidate(f *Facts, startNs int64) *DeployFact {
	if f == nil || f.Deploys == nil {
		return nil
	}
	ev := Evidence{StartNs: startNs, Facts: Facts{Deploys: f.Deploys}}
	near := ev.nearDeploys()
	for i := len(near) - 1; i >= 0; i-- {
		r := near[i]
		if !imageChanged(r) || r.Prev == "" || strings.EqualFold(strings.TrimSpace(r.Status), "rolled_back") {
			continue
		}
		return &r
	}
	return nil
}

// argoFor — SAF: geri alınacak iş yüküne (ya da servise) eşlenen ilk Argo
// uygulaması; yoksa tek uygulama varsa o.
func argoFor(apps []mcptools.ArgoAppStatus, workload, service string) *mcptools.ArgoAppStatus {
	for i := range apps {
		for _, w := range apps[i].Workloads {
			if w == workload || w == service {
				return &apps[i]
			}
		}
	}
	if len(apps) == 1 {
		return &apps[0]
	}
	return nil
}

// argoStatusTR — SAF: "OutOfSync" / "OutOfSync, Degraded" (Healthy düşer).
func argoStatusTR(a mcptools.ArgoAppStatus) string {
	parts := []string{}
	if a.Sync != "" {
		parts = append(parts, a.Sync)
	}
	if a.Health != "" && !strings.EqualFold(a.Health, "Healthy") {
		parts = append(parts, a.Health)
	}
	if len(parts) == 0 {
		return "durum bilinmiyor"
	}
	return strings.Join(parts, ", ")
}

// supportReason — SAF: hipotezin ilk doğrulayan gerekçesi.
func supportReason(h Hypothesis) string {
	for _, c := range h.Checks {
		if c.Outcome > 0 {
			return c.Reason
		}
	}
	return ""
}

func hypRationale(h Hypothesis, extra string) string {
	r := fmt.Sprintf("%s hipotezi %s %.2f (kod skoru)", h.Label, h.Band, h.Score)
	if s := supportReason(h); s != "" {
		r += " — " + s
	}
	if extra != "" {
		r += "; " + extra
	}
	return truncRunes(r, recRationaleMax)
}

// argoStatusArg — SAF: argoStatusTR'nin anahtar ikizi (ad, değer).
func argoStatusArg(a mcptools.ArgoAppStatus) (string, string) {
	if st := argoStatusTR(a); st != "durum bilinmiyor" {
		return "argo", st
	}
	return "argoK", "rec.argoUnknown"
}

// pctRaw — SAF: pctTR'nin dilden bağımsız ikizi ("91%", "?").
func pctRaw(p *float64) string {
	if p == nil {
		return "?"
	}
	return fmt.Sprintf("%.0f%%", *p)
}

func pctTR(p *float64) string {
	if p == nil {
		return "?"
	}
	return fmt.Sprintf("%%%.0f", *p)
}

// rollbackRec — SAF: geri alma METNİ (+ okunduysa Argo eki).
func rollbackRec(h Hypothesis, r DeployFact, in recInput) Recommendation {
	rec := Recommendation{ID: "rollback", HypothesisKind: h.Kind, State: RecPending,
		Rollout: &RecRollout{Workload: r.Name, From: r.Version, To: r.Prev}}
	text := fmt.Sprintf("%s %s → %s geri alma değerlendirilebilir", r.Name, r.Version, r.Prev)
	rec.TextMsg = recMsg("rec.rollback", "name", r.Name, "from", r.Version, "to", r.Prev)
	argoNote := ""
	if in.ArgoRead {
		if a := argoFor(in.Argo, r.Name, in.Service); a != nil {
			st := argoStatusTR(*a)
			rec.Argo = &RecArgo{App: a.App, Status: st, PrevRevision: r.Prev}
			text += " (Argo: " + st + ")"
			argoNote = "Argo CD uygulaması " + a.App
			an, av := argoStatusArg(*a)
			rec.TextMsg = recMsg("rec.rollbackArgo", "name", r.Name, "from", r.Version, "to", r.Prev, an, av)
		}
	}
	text += " — kararı ve uygulamayı insan verir"
	extra := argoNote
	if supportReason(h) == "" { // gerekçede zaman yoksa satırın kendisi
		extra = strings.Trim(fmt.Sprintf("%s %s; %s", relMin(r.At, in.StartNs), firstNonEmpty(deploySourceTR(r.Source), "değişiklik"), argoNote), "; ")
	}
	rec.Text = truncRunes(text, recTextMax)
	rec.Rationale = hypRationale(h, extra)
	return rec
}

func deploySourceTR(src string) string {
	switch src {
	case "rollout":
		return "rollout"
	case "event":
		return "deploy olayı"
	case "inferred":
		return "sürüm geçişi"
	}
	return src
}

// hypothesisRec — SAF: hipotez türü → tek öneri cümlesi (gerçeklerden).
// ok=false → bu tür için söylenecek somut bir şey yok.
func hypothesisRec(h Hypothesis, in recInput, rb *DeployFact) (Recommendation, bool) {
	rec := Recommendation{ID: "hyp-" + h.Kind, HypothesisKind: h.Kind, State: RecPending}
	f := in.Facts
	if f == nil {
		f = &Facts{}
	}
	text := ""
	var msg *RecTextMsg
	switch h.Kind {
	case HypDeploy, HypCodeRegression:
		if rb != nil {
			return rollbackRec(h, *rb, in), true
		}
		if h.Kind == HypDeploy {
			text, msg = "Başlangıca yakın değişikliği (deploy adımı) sürüm notlarıyla birlikte gözden geçirin", recMsg("rec.deploy")
		} else {
			text, msg = "Yeni hata desenini ve son kod değişikliğini inceleyin (kanıt: log artışı / temsilci trace)", recMsg("rec.codeRegression")
		}
	case HypConfig:
		text, msg = "İmaj değişmeden yapılan son yapılandırma / secret değişikliğini gözden geçirin", recMsg("rec.config")
		if in.ArgoRead {
			if a := argoFor(in.Argo, "", in.Service); a != nil {
				rec.Argo = &RecArgo{App: a.App, Status: argoStatusTR(*a)}
				text += fmt.Sprintf(" (Argo %s: %s)", a.App, rec.Argo.Status)
				an, av := argoStatusArg(*a)
				msg = recMsg("rec.configArgo", "app", a.App, an, av)
			}
		}
	case HypSaturation:
		p := f.Pods
		switch {
		case p != nil && len(p.OOMPods) > 0:
			text = fmt.Sprintf("OOMKilled pod'lar (%s): bellek limitini / sızıntıyı gözden geçirin", strings.Join(p.OOMPods, ", "))
			msg = recMsg("rec.sat.oom", "pods", strings.Join(p.OOMPods, ", "))
		case p != nil && p.HotPod != "":
			text = fmt.Sprintf("%s kaynak kullanımını (CPU %s / bellek %s limit) ve ölçeklemeyi gözden geçirin", p.HotPod, pctTR(p.MaxCPUPct), pctTR(p.MaxMemPct))
			msg = recMsg("rec.sat.hot", "pod", p.HotPod, "cpu", pctRaw(p.MaxCPUPct), "mem", pctRaw(p.MaxMemPct))
		case p != nil && p.HotJVM != "":
			text = fmt.Sprintf("%s JVM heap baskısını (GC sonrası %s) gözden geçirin", p.HotJVM, pctTR(p.MaxPostGCPct))
			msg = recMsg("rec.sat.jvm", "pod", p.HotJVM, "gc", pctRaw(p.MaxPostGCPct))
		default:
			text, msg = "Pod / node kaynak baskısını ve ölçeklemeyi gözden geçirin", recMsg("rec.sat.generic")
		}
	case HypDependency:
		if f.Deps != nil && len(f.Deps.Causes) > 0 {
			text = fmt.Sprintf("Aşağı akış %s servisinin sağlığını ve açık problemlerini kontrol edin", f.Deps.Causes[0].Service)
			msg = recMsg("rec.dep", "svc", f.Deps.Causes[0].Service)
		} else {
			text, msg = "Aşağı akış bağımlılıklarının sağlığını kontrol edin", recMsg("rec.depGeneric")
		}
	case HypExternal:
		if ext := (Evidence{Facts: *f}).edgesWithPrefix("ext:"); len(ext) > 0 {
			text = fmt.Sprintf("Dış servis %s durumunu sağlayıcının durum sayfasından doğrulayın", strings.TrimPrefix(ext[0].Target, "ext:"))
			msg = recMsg("rec.ext", "target", strings.TrimPrefix(ext[0].Target, "ext:"))
		} else {
			text, msg = "Dış bağımlılığın durumunu sağlayıcıdan doğrulayın", recMsg("rec.extGeneric")
		}
	case HypDBSlow:
		if db := (Evidence{Facts: *f}).edgesWithPrefix("db:"); len(db) > 0 {
			text = fmt.Sprintf("%s veritabanının yavaş sorgularını ve bağlantı havuzunu inceleyin", strings.TrimPrefix(db[0].Target, "db:"))
			msg = recMsg("rec.db", "db", strings.TrimPrefix(db[0].Target, "db:"))
		} else {
			text, msg = "Veritabanı yavaş sorgularını ve bağlantı havuzunu inceleyin", recMsg("rec.dbGeneric")
		}
	case HypTrafficSurge:
		if f.Deps != nil && f.Deps.Subject != nil {
			text = fmt.Sprintf("Trafik %+.0f%% arttı: ölçeklemeyi / hız sınırlamayı değerlendirin", f.Deps.Subject.RateDeltaPct)
			msg = recMsg("rec.traffic", "d", fmt.Sprintf("%+.0f%%", f.Deps.Subject.RateDeltaPct))
		} else {
			text, msg = "Trafik artışı: ölçeklemeyi / hız sınırlamayı değerlendirin", recMsg("rec.trafficGeneric")
		}
	default:
		return Recommendation{}, false
	}
	rec.Text = truncRunes(text, recTextMax)
	rec.TextMsg = msg
	rec.Rationale = hypRationale(h, "")
	return rec, true
}

// ── runbook eşleme (SAF) ────────────────────────────────────────────────────

// RunbookMatch — matchRunbooks çıktısı.
type RunbookMatch struct {
	Runbook RecRunbook
	Kind    string // eşleşen cause:<tür> etiketi ("" = başka yol)
	Reason  string
}

// runbookUIPath — SAF: mevcut runbook sayfası, problem önceden dolu.
func runbookUIPath(id, problemID string) string {
	out := "/runbook?id=" + url.QueryEscape(id)
	if problemID != "" {
		out += "&problemId=" + url.QueryEscape(problemID)
	}
	return out
}

// internalRunbookID — SAF: "/runbook?id=X" biçimli bağlantının runbook kimliği.
func internalRunbookID(u string) string {
	if !strings.HasPrefix(u, "/runbook?") {
		return ""
	}
	q, err := url.ParseQuery(strings.TrimPrefix(u, "/runbook?"))
	if err != nil {
		return ""
	}
	return q.Get("id")
}

// recStop — başlık örtüşmesinde sayılmayan genel sözcükler.
var recStop = map[string]bool{
	"the": true, "and": true, "for": true, "service": true, "servis": true, "servisi": true,
	"runbook": true, "ile": true, "için": true, "bir": true, "high": true, "yüksek": true,
}

// recTokens — SAF: küçük harf, harf/rakam dışı ayraç, ≥3 rune, durak sözcük yok.
func recTokens(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(t)) < 3 || recStop[t] || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// matchRunbooks — SAF: önerilecek runbook'lar (≤3), öncelik sırası:
//  1. Problem.RunbookURL (kural / servis kataloğu bağlantısı; iç "/runbook?id="
//     ise o runbook, dış ise bağlantının kendisi);
//  2. etkin runbook'ların etiketleri — service:<svc>, cause:<tür|kategori>
//     (önerilebilir hipotezler), rule:<önek> (problemin kural kimliği öneki);
//     çok etiket eşleşen önce, eşitlikte başlık;
//  3. başlık jetonu örtüşmesi ≥0.5 (başlık jetonlarının servis / kural /
//     metrik / hipotez bağlamında bulunma oranı).
//
// Devre dışı runbook ÖNERİLMEZ (çalıştırılamaz).
func matchRunbooks(p chstore.Problem, hyps []Hypothesis, books []chstore.Runbook) []RunbookMatch {
	var out []RunbookMatch
	seen := map[string]bool{}
	add := func(m RunbookMatch) bool {
		key := m.Runbook.ID
		if key == "" {
			key = m.Runbook.URL
		}
		if seen[key] || len(out) >= runbookMatchMax {
			return false
		}
		seen[key] = true
		out = append(out, m)
		return true
	}
	enabled := map[string]chstore.Runbook{}
	var ids []string
	for _, b := range books {
		if b.Enabled && b.ID != "" {
			enabled[b.ID] = b
			ids = append(ids, b.ID)
		}
	}
	sort.Strings(ids)

	// 1. Problemin kendi bağlantısı. v0.10.1341 — safelink.Sanitize: okuma
	// zenginleştirmesi zaten süzüyor; çağıran ham bir Problem verirse de
	// çizilemeyen bağlantı öneriye girmez.
	if u := safelink.Sanitize(p.RunbookURL); u != "" {
		if id := internalRunbookID(u); id != "" {
			if b, ok := enabled[id]; ok {
				add(RunbookMatch{Runbook: RecRunbook{ID: b.ID, Title: b.Title, URL: runbookUIPath(b.ID, p.ID)}, Reason: "problemin runbook bağlantısı (kural / servis kataloğu)"})
			}
		} else if !strings.HasPrefix(u, "/api/") {
			add(RunbookMatch{Runbook: RecRunbook{Title: "Problemin runbook bağlantısı", URL: u}, Reason: "problemin runbook bağlantısı (kural / servis kataloğu)"})
		}
	}

	// 2. Etiketler.
	active := activeHyps(hyps)
	causes := map[string]string{} // etiket değeri → hipotez türü
	for _, h := range active {
		causes[strings.ToLower(h.Kind)] = h.Kind
		if h.Category != "" {
			if _, ok := causes[strings.ToLower(h.Category)]; !ok {
				causes[strings.ToLower(h.Category)] = h.Kind
			}
		}
	}
	type labelHit struct {
		b       chstore.Runbook
		n       int
		kind    string
		reasons []string
	}
	var hits []labelHit
	for _, id := range ids {
		b := enabled[id]
		hit := labelHit{b: b}
		for _, l := range b.Labels {
			k, v, ok := strings.Cut(strings.TrimSpace(l), ":")
			if !ok || strings.TrimSpace(v) == "" {
				continue
			}
			v = strings.TrimSpace(v)
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "service":
				if p.Service != "" && strings.EqualFold(v, p.Service) {
					hit.n++
					hit.reasons = append(hit.reasons, "service:"+v)
				}
			case "cause":
				if kind, ok := causes[strings.ToLower(v)]; ok {
					hit.n++
					if hit.kind == "" {
						hit.kind = kind
					}
					hit.reasons = append(hit.reasons, "cause:"+v)
				}
			case "rule":
				if p.RuleID != "" && strings.HasPrefix(p.RuleID, v) {
					hit.n++
					hit.reasons = append(hit.reasons, "rule:"+v)
				}
			}
		}
		if hit.n > 0 {
			hits = append(hits, hit)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].n != hits[j].n {
			return hits[i].n > hits[j].n
		}
		return hits[i].b.Title < hits[j].b.Title
	})
	for _, h := range hits {
		add(RunbookMatch{Runbook: RecRunbook{ID: h.b.ID, Title: h.b.Title, URL: runbookUIPath(h.b.ID, p.ID)}, Kind: h.kind,
			Reason: "etiket eşleşmesi: " + strings.Join(h.reasons, ", ")})
	}

	// 3. Başlık jetonu örtüşmesi.
	ctxParts := []string{p.Service, p.RuleName, p.RuleID, p.Metric}
	for _, h := range active {
		ctxParts = append(ctxParts, h.Kind, h.Label, h.Category)
	}
	ctxTok := map[string]bool{}
	for _, t := range recTokens(strings.Join(ctxParts, " ")) {
		ctxTok[t] = true
	}
	type titleHit struct {
		b     chstore.Runbook
		ratio float64
	}
	var th []titleHit
	for _, id := range ids {
		b := enabled[id]
		tt := recTokens(b.Title)
		if len(tt) == 0 {
			continue
		}
		n := 0
		for _, t := range tt {
			if ctxTok[t] {
				n++
			}
		}
		if r := float64(n) / float64(len(tt)); n > 0 && r >= runbookTitleOverlapMin {
			th = append(th, titleHit{b, r})
		}
	}
	sort.SliceStable(th, func(i, j int) bool {
		if th[i].ratio != th[j].ratio {
			return th[i].ratio > th[j].ratio
		}
		return th[i].b.Title < th[j].b.Title
	})
	for _, h := range th {
		add(RunbookMatch{Runbook: RecRunbook{ID: h.b.ID, Title: h.b.Title, URL: runbookUIPath(h.b.ID, p.ID)},
			Reason: fmt.Sprintf("başlık örtüşmesi %%%.0f (servis / kural / hipotez)", h.ratio*100)})
	}
	return out
}

// recIDSafe — SAF: kimlik URL yol parçasına güvenli ([a-z0-9-_]).
func recIDSafe(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return truncRunes(b.String(), 80)
}

// buildRecommendations — SAF: girdi → öneriler (≤RecommendationsMax): önce
// hipotez önerileri (skor sırası), sonra runbook'lar. Hepsi RecPending.
func buildRecommendations(in recInput) []Recommendation {
	var out []Recommendation
	rb := rollbackCandidate(in.Facts, in.StartNs)
	seen := map[string]bool{}
	for _, h := range activeHyps(in.Hyps) {
		rec, ok := hypothesisRec(h, in, rb)
		if !ok || seen[rec.ID] {
			continue
		}
		seen[rec.ID] = true
		out = append(out, rec)
	}
	p := in.Problem
	if p.ID == "" {
		p.ID = in.ProblemID
	}
	if p.Service == "" {
		p.Service = in.Service
	}
	for _, m := range matchRunbooks(p, in.Hyps, in.Books) {
		id := "runbook-url"
		if m.Runbook.ID != "" {
			id = recIDSafe("rb-" + m.Runbook.ID)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		r := m.Runbook
		out = append(out, Recommendation{ID: id, HypothesisKind: m.Kind, State: RecPending, Runbook: &r,
			Text:      truncRunes(fmt.Sprintf("Runbook'u gözden geçirin: %s — çalıştırmayı insan başlatır", r.Title), recTextMax),
			TextMsg:   recMsg("rec.runbook", "title", r.Title),
			Rationale: truncRunes(m.Reason, recRationaleMax)})
	}
	if len(out) > RecommendationsMax {
		out = out[:RecommendationsMax]
	}
	return out
}

// carryRecState — SAF: önceki kaydın operatör işaretlerini (ack / dismissed)
// kimliğe göre yeni önerilere taşır.
func carryRecState(next, prev []Recommendation) []Recommendation {
	marks := map[string]Recommendation{}
	for _, r := range prev {
		if r.State == RecAck || r.State == RecDismissed {
			marks[r.ID] = r
		}
	}
	for i := range next {
		if m, ok := marks[next[i].ID]; ok {
			next[i].State, next[i].AckBy, next[i].AckAt = m.State, m.AckBy, m.AckAt
		}
	}
	return next
}

// ReplayArgoNote — tekrar koşusunun Argo parçası notu (argo_sync gerekçesi).
const ReplayArgoNote = "değerlendirme tekrarı — Argo CD durumu bugünü gösterir (as-of değil); geçmiş olay canlı Argo durumuyla puanlanmaz"

// argoFactsFor — v0.10.1310: koşunun Argo CD gerçeği, koşu başına EN ÇOK BİR
// Deps.ArgoApps okuması (≤readCap). Skordan ÖNCE çağrılır; argo_sync kontrolü
// (SAF Score) ve öneri eki (recommend) aynı parçayı okur — öneri ikinci kez
// okumaz.
//
//   - tekrar (Kind=replay): okuma YOK → State=replay + not (Argo "şimdi"dir;
//     geçmiş vakayı bugünün durumuyla puanlamak tekrarı kirletir).
//   - dikiş bağlı değil: nil (kontrol dürüst "okunmadı").
//   - önceki koşunun başarılı parçası yeniden kullanılır: normal koşuda
//     stepFreshForReuse'tan tazeyse; doğrulamada (Kind=verify) hipotez config
//     DEĞİLSE (config doğrulaması Argo'yu ZORLA yeniden okur).
//   - okuma hatası: State=error (hata metni yankılanmaz — URL/token taşıyabilir).
func argoFactsFor(ctx context.Context, rd readers, prev *ArgoFacts, service string, replay bool, verify string, now func() time.Time) *ArgoFacts {
	if replay {
		return &ArgoFacts{State: argoStateReplay, Note: ReplayArgoNote}
	}
	if rd.argo == nil {
		return nil
	}
	if prev != nil && prev.State == argoStateOK && verify != HypConfig &&
		(verify != "" || now().Sub(time.Unix(0, prev.ReadAt)) < stepFreshForReuse) {
		return prev
	}
	if ctx.Err() != nil {
		return prev
	}
	c, cancel := context.WithTimeout(ctx, readCap)
	defer cancel()
	apps, err := rd.argo(c, service)
	if err != nil {
		return &ArgoFacts{State: argoStateError, ReadAt: now().UnixNano(), Note: "Argo CD durumu okunamadı"}
	}
	return argoFactsOf(apps, now())
}

// recommend — okumalar (runbook listesi, problemin runbook bağlantısı) + SAF
// buildRecommendations. Argo eki v0.10.1310'den beri kayıttaki Facts.Argo'dan
// (argoFactsFor; koşu başına tek okuma) — burada Argo okunmaz. Okuma hatası
// öneriyi düşürmez: o parça yok sayılır. Hiçbir yazım yok.
func recommend(ctx context.Context, rd readers, inv *Investigation, p chstore.Problem) []Recommendation {
	in := recInput{ProblemID: inv.ProblemID, Problem: p, Service: inv.Service, StartNs: inv.ProblemAt, Hyps: inv.Hypotheses, Facts: inv.Facts}
	if ctx.Err() == nil && rd.runbookURL != nil {
		c, cancel := context.WithTimeout(ctx, readCap)
		if u := rd.runbookURL(c, p); u != "" {
			in.Problem.RunbookURL = u
		}
		cancel()
	}
	if ctx.Err() == nil && rd.runbooks != nil {
		c, cancel := context.WithTimeout(ctx, readCap)
		if books, err := rd.runbooks(c); err == nil {
			in.Books = books
		}
		cancel()
	}
	if inv.Facts != nil && inv.Facts.Argo != nil && inv.Facts.Argo.State == argoStateOK {
		in.Argo, in.ArgoRead = inv.Facts.Argo.Apps, true
	}
	return buildRecommendations(in)
}

// ── operatör işareti (Uygulandı / Ele) ──────────────────────────────────────

// RecAckRequest — operatörün öneri işareti.
type RecAckRequest struct {
	ID    string
	State string // ack | dismissed
	By    string
	Now   time.Time
}

// ApplyRecommendationAck — kayıttaki önerinin durumunu yazar (yalnız etiket;
// ajan hiçbir şeyi uygulamaz, "Uygulandı" insanın beyanıdır). Kuyrukta /
// koşan kayıt reddedilir (drain checkpoint'i ezerdi). Tek yazım kendi kaydı
// (QueueStore) — hipotez hatalarıyla aynı sınıflar (ErrMark*).
func ApplyRecommendationAck(ctx context.Context, st QueueStore, problemID string, m RecAckRequest) (Recommendation, error) {
	if m.ID == "" || (m.State != RecAck && m.State != RecDismissed) {
		return Recommendation{}, ErrMarkInvalid
	}
	inv, ok, err := Load(ctx, st, problemID)
	if err != nil {
		return Recommendation{}, err
	}
	if !ok {
		return Recommendation{}, ErrMarkNotFound
	}
	switch inv.State {
	case StateQueued, StateRunning:
		return Recommendation{}, ErrMarkBusy
	}
	idx := -1
	for i, r := range inv.Recommendations {
		if r.ID == m.ID {
			idx = i
		}
	}
	if idx < 0 {
		return Recommendation{}, ErrMarkNotFound
	}
	now := m.Now
	if now.IsZero() {
		now = time.Now()
	}
	inv.Recommendations[idx].State, inv.Recommendations[idx].AckBy, inv.Recommendations[idx].AckAt = m.State, m.By, now.UnixNano()
	if err := save(ctx, st, inv); err != nil {
		return Recommendation{}, err
	}
	return inv.Recommendations[idx], nil
}

// ── sohbet / anlatım ────────────────────────────────────────────────────────

// recStateTR — durumun kısa Türkçe etiketi.
func recStateTR(s string) string {
	switch s {
	case RecAck:
		return "operatör uyguladığını işaretledi"
	case RecDismissed:
		return "operatör eledi"
	}
	return RecPending
}

// summaryRecommendations — SAF: elenmemiş ilk ≤2 öneri → sohbet özeti.
func summaryRecommendations(rs []Recommendation) []mcptools.InvestigationRecommendation {
	var out []mcptools.InvestigationRecommendation
	for _, r := range rs {
		if len(out) >= RecommendationsInChat {
			break
		}
		if r.State == RecDismissed {
			continue
		}
		x := mcptools.InvestigationRecommendation{Text: r.Text, Rationale: truncRunes(r.Rationale, 200), State: r.State}
		if r.Runbook != nil {
			x.Runbook = r.Runbook.Title
		}
		out = append(out, x)
	}
	return out
}

// renderRecommendationsTR — SAF: anlatım girdisinin öneri satırları (≤2).
func renderRecommendationsTR(rs []Recommendation) string {
	sum := summaryRecommendations(rs)
	if len(sum) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("AJAN ÖNERİLERİ (yalnız öneri — hiçbiri uygulanmadı; uygulamayı insan yapar):\n")
	for _, r := range sum {
		line := "- " + r.Text
		if r.State != RecPending {
			line += " (" + recStateTR(r.State) + ")"
		}
		b.WriteString(truncRunes(oneLine(line), 320) + "\n")
	}
	return b.String()
}
