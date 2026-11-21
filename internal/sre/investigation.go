package sre

// investigation.go — v0.10.1282 — araştırma kaydı (= kuyruk satırının
// gövdesi) ve 64 KB tavanlı kodlayıcı.
//
// 0a yalnız bugün doldurulan alanları taşır; sonraki fazların alanları
// (hipotezler, zaman çizelgesi, öneriler, Zoom zinciri, anlatım) JSON'a
// geriye uyumlu eklenir — eski satır yeni alanı boş okur.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// Durumlar (name kolonu).
const (
	StateQueued  = "queued"
	StateRunning = "running"
	StateDone    = "done"
	StatePartial = "partial"
	StateShared  = "shared"
	StateError   = "error"
	StateSkipped = "skipped"
)

// Türler.
const (
	KindAuto    = "auto"
	KindManual  = "manual"
	KindVerify  = "verify"
	KindReplay  = "replay"
	KindResolve = "resolve"
)

// Adım durumları.
const (
	StepOK      = "ok"
	StepPartial = "partial"
	StepError   = "error"
	StepSkipped = "skipped"
)

const (
	// maxStepPayload — adım yükü tavanı.
	maxStepPayload = 12 << 10
	// maxFindings — kayıtta tutulan en çok bulgu.
	maxFindings = 8
	maxWarnings = 10
	// maxStepEvidence — v0.10.1287: adım başına saklanan kanıt künyesi.
	maxStepEvidence = 6
	// maxEvidenceQuery — künyedeki sorgu metninin rune tavanı.
	maxEvidenceQuery = 600
)

// Anchor — araştırmanın penceresi.
type Anchor struct {
	From    int64  `json:"from"` // unix ns
	To      int64  `json:"to"`   // unix ns
	Symptom string `json:"symptom,omitempty"`
	// Families — v0.10.1287: anomaly.PlanFamilies (planın belirti aileleri).
	Families []string `json:"families,omitempty"`
}

// Step — tek okuma adımı (önbellek anahtarlı, idempotent; resume ok adımı
// tazeyse atlar).
type Step struct {
	Name         string          `json:"name"`
	State        string          `json:"state"`
	Summary      string          `json:"summary,omitempty"`
	DurationMs   int64           `json:"durationMs"`
	At           int64           `json:"at"` // unix ns, adımın bittiği an
	EvidenceRefs []string        `json:"evidenceRefs,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	// Evidence — v0.10.1287: adımın kanıt künyeleri (sohbet çipiyle aynı şekil,
	// "Explore'da aç" href'i dahil; ≤6). Bulgunun EvidenceRefs'i
	// "evidences[i]" → bu dizinin i'nci kalemi (Finding.Step adımında).
	Evidence []mcptools.ChatEvidence `json:"evidence,omitempty"`
	// NoData — v0.10.1301 (Faz 5): okuyucu baktı, pencerede veri YOK ("veri
	// yok: …" — saklama dışı / örneklenmemiş / hiç gelmemiş). Okunamadı
	// (StepError) ≠ yok; değerlendirmenin kapsam ölçüsü buradan sayar.
	NoData bool `json:"noData,omitempty"`
	// SummaryMsg — v0.10.2301 (sunucu metinleri S4): atlama / veri-yok
	// özetinin metin anahtarı (ek alan; Summary aynen). Araç mesajı geçen
	// özetlerde YOK (özgün dil + işaret). Yalnız bu sürümden sonra yazılan
	// kayıtlarda; eski kayıt anahtarsız → EN arayüz özgün metni işaretle çizer.
	SummaryMsg *chstore.StdMsg `json:"summaryMsg,omitempty"`
}

// Finding — sıralı bulgu (0a: servis taramasının bulguları).
type Finding struct {
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	Level        string   `json:"level"`
	Reasons      []string `json:"reasons,omitempty"`
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
	// Step — v0.10.1287: bulguyu üreten adım (EvidenceRefs o adımın Evidence'ına
	// bakar). 0a kayıtlarında boş = sweep.
	Step string `json:"step,omitempty"`
}

// Narration — v0.10.1287: araştırmanın AI anlatımı (tek LLM çağrısı). Gated =
// arka plan AI kapısı kapalıydı; Text o zaman dürüst sabit cümle.
//
// v0.10.1312 — CallID / ExchangeID: anlatımı üreten ai_calls satırının kimliği
// ve çağrının exchange kimliği (ikisi de çağrıdan ÖNCE basılır). FE özetin
// altında 👍/👎 rayını bunlarla çizer: exchangeId → oy satırının anahtarı +
// yüzey çözümü (sre-investigation), callId → oyun call_id'si → /ai inceleme
// listesinin ai_calls birleşimi. Kapı kapalı / tekrar (Kind=replay) / eski
// kayıt → boş, buton çizilmez. Oy ZAYIF sinyal: bu paket oyları OKUMAZ;
// kimlikler yalnız yazılır, skor/plan onlara bakmaz.
type Narration struct {
	Text       string `json:"text"`
	Model      string `json:"model,omitempty"`
	CallID     string `json:"callId,omitempty"`
	ExchangeID string `json:"exchangeId,omitempty"`
	At         int64  `json:"at"` // unix ns
	Gated      bool   `json:"gated,omitempty"`
	Reason     string `json:"reason,omitempty"` // kapı nedeni (kapalı / kota / saatlik tavan / yapılandırılmamış)
	// GateMsg — v0.10.2301 (S4): kapalı kapı satırının ("Text — Reason")
	// metin anahtarı; yalnız Gated'de. Sohbet özeti (SummaryOf) okumaz.
	GateMsg *chstore.StdMsg `json:"gateMsg,omitempty"`
}

// SynthRef — v0.10.1287: kök-neden sentezleyicisinin hipotezinin SALT-OKUNUR
// kopyası (GetHypothesis; root_cause_hypotheses'a yazılmaz — tek yazıcı
// sentezleyici, /aiops §6).
type SynthRef struct {
	TopSuspect string  `json:"topSuspect,omitempty"`
	Confidence float64 `json:"confidence"`
	ComputedAt int64   `json:"computedAt"` // unix ns
}

// Investigation — saved_views query_string gövdesi.
type Investigation struct {
	ProblemID  string    `json:"problemId"`
	DisplayID  string    `json:"displayId,omitempty"`
	Service    string    `json:"service"`
	Env        string    `json:"env,omitempty"`
	Kind       string    `json:"kind"`
	State      string    `json:"state"`
	Priority   string    `json:"priority,omitempty"` // salt-okunur kopya (enqueue anı)
	Attempts   int       `json:"attempts"`
	Run        int       `json:"run"` // kaçıncı koşu (elle yeniden araştırma ++)
	ClaimedBy  string    `json:"claimedBy,omitempty"`
	EnqueuedAt int64     `json:"enqueuedAt"`
	ClaimedAt  int64     `json:"claimedAt,omitempty"`
	StartedAt  int64     `json:"startedAt,omitempty"`
	FinishedAt int64     `json:"finishedAt,omitempty"`
	SharedFrom string    `json:"sharedFrom,omitempty"`
	RequestBy  string    `json:"requestBy,omitempty"`
	ProblemAt  int64     `json:"problemStartedAt"` // problemin başlangıcı (unix ns)
	Anchor     Anchor    `json:"anchor"`
	Steps      []Step    `json:"steps"`
	Findings   []Finding `json:"findings"`
	Error      string    `json:"error,omitempty"`
	Warnings   []string  `json:"warnings,omitempty"`
	// v0.10.1287 (Faz 1b) — anlatım + sentezleyici kopyası.
	Summary  *Narration `json:"summary,omitempty"`
	SynthRef *SynthRef  `json:"synthRef,omitempty"`
	// v0.10.1288 (Faz 2) — hipotez kartları (SAF Score, skora göre azalan; 8
	// tür, kartta ilk 3), zaman çizelgesi (≤30), adımların yapısal gerçekleri
	// (skorun girdisi — resume / doğrulama aynı girdiden yeniden hesaplar) ve
	// doğrulama koşusunun hipotezi (Kind=verify).
	Hypothesis string          `json:"hypothesis,omitempty"`
	Hypotheses []Hypothesis    `json:"hypotheses,omitempty"`
	Timeline   []TimelineEvent `json:"timeline,omitempty"`
	Facts      *Facts          `json:"facts,omitempty"`
	// v0.10.1293 (Faz 4) — öneriler: YALNIZ METİN (+ insanın açacağı runbook
	// bağlantısı); ajan hiçbir şeyi uygulamaz. Operatör işareti (ack /
	// dismissed) yeniden koşuda kimliğe göre taşınır (recommend.go).
	Recommendations []Recommendation `json:"recommendations,omitempty"`
	// v0.10.1301 (Faz 5) — değerlendirme. Kind=replay işinin koşusu + vakası;
	// AsOf = tekrarın "şimdi"si (problem başlangıcı + replayAsOfDelay; plan
	// pencereleri bu ana göre kurulur, bugünün verisi sızmaz). Tekrar işi kendi
	// kuyruk anahtarında yaşar (QueueKey) — canlı kaydı ASLA ezmez.
	EvalRun string `json:"evalRun,omitempty"`
	CaseID  string `json:"caseId,omitempty"`
	AsOf    int64  `json:"asOf,omitempty"` // unix ns
	// FirstHypothesisAt — canlı koşuda hipotez kartlarının İLK hesaplandığı an
	// (unix ns; yeniden koşuda taşınır). İlk hipoteze varış süresi =
	// FirstHypothesisAt − ProblemAt (değerlendirme paneli p50/p90).
	FirstHypothesisAt int64 `json:"firstHypothesisAt,omitempty"`
	// TraceID — v0.10.1463 (CoSRE D1): son koşunun sre.investigation trace'i
	// (32 hex; yalnız örneklenmişse — self-obs kapalı = boş). PR 3'te
	// ai_investigations.trace_id kolonuna taşınır; PR 6 "Trace'te aç".
	TraceID string `json:"traceId,omitempty"`
	// Pack / ContextPack — v0.10.1494 (CoSRE 2d): bu koşu Context Pack
	// okuyucularıyla mı koştu (canlı: sre_agent.contextpack.enabled; tekrar:
	// koşu isteği) ve anlatım paketinin KÜNYESİ (bölüm / bayt / kırpılan; metin
	// değil). Kapalı → ikisi de yok, kayıt bayt bayt eski.
	Pack        bool      `json:"pack,omitempty"`
	ContextPack *PackMeta `json:"contextPack,omitempty"`
	// CoSRE Faz B (araştırma kalitesi). Plain — değerlendirme varyantı:
	// oyun kitabı / hipotez durumu / kanıt kapısı KAPALI (B öncesi davranış;
	// yalnız tekrarda, canlı koşu hep false). Playbook — problem türünün
	// sıralı kontrolleri (playbook.go); Reach — hangi kaynağa bakıldı / bakılamadı
	// (reach.go); Coverage — kanıt kapsama kapısının kararı (coverage.go).
	Plain    bool          `json:"plain,omitempty"`
	Playbook *Playbook     `json:"playbook,omitempty"`
	Reach    []ReachEntry  `json:"reach,omitempty"`
	Coverage *CoverageGate `json:"coverage,omitempty"`
}

// QueueKey — v0.10.1301: kuyruk anahtarı (satır kimliği
// chstore.SREInvestigationID(QueueKey()), iş kilidi, uçuş haritası). Canlı
// kayıt = problemId; tekrar işi = "replay-<runId>-<problemId>" (ayrı satır).
func (inv Investigation) QueueKey() string {
	if inv.Kind == KindReplay && inv.EvalRun != "" {
		return chstore.SREReplayKey(inv.EvalRun, inv.ProblemID)
	}
	return inv.ProblemID
}

// step — ada göre adım (yoksa nil).
func (inv *Investigation) step(name string) *Step {
	for i := range inv.Steps {
		if inv.Steps[i].Name == name {
			return &inv.Steps[i]
		}
	}
	return nil
}

// setStep — adımı ekler ya da yerinde değiştirir.
func (inv *Investigation) setStep(s Step) {
	if cur := inv.step(s.Name); cur != nil {
		*cur = s
		return
	}
	inv.Steps = append(inv.Steps, s)
}

// Encode — SAF: kaydı ≤ SREInvestigationMaxBytes JSON'a kodlar. Sığmazsa
// sırayla küçültür: metin anahtarları (v0.10.2301) → adım yükleri → uyarılar → bulgular ≤3 → adım özetleri
// kırpılır; yine sığmazsa hata (yazım reddedilir, kayıt yarım yazılmaz).
func Encode(inv Investigation) (string, error) {
	if inv.Steps == nil {
		inv.Steps = []Step{}
	}
	if inv.Findings == nil {
		inv.Findings = []Finding{}
	}
	if len(inv.Findings) > maxFindings {
		inv.Findings = inv.Findings[:maxFindings]
	}
	if len(inv.Warnings) > maxWarnings {
		inv.Warnings = inv.Warnings[:maxWarnings]
	}
	// v0.10.1287 — çağıranın kaydına dokunma: küçültme adımları dilim
	// elemanlarını yerinde değiştirir (checkpoint canlı kaydı kodluyor).
	inv.Steps = append(make([]Step, 0, len(inv.Steps)), inv.Steps...)
	for i := range inv.Steps {
		inv.Steps[i].Evidence = append([]mcptools.ChatEvidence(nil), inv.Steps[i].Evidence...)
	}
	inv.Findings = append(make([]Finding, 0, len(inv.Findings)), inv.Findings...)
	if inv.Summary != nil {
		n := *inv.Summary
		inv.Summary = &n
	}
	inv.Hypotheses = append([]Hypothesis(nil), inv.Hypotheses...)
	inv.Timeline = append([]TimelineEvent(nil), inv.Timeline...)
	if len(inv.Timeline) > TimelineMax {
		inv.Timeline = inv.Timeline[:TimelineMax]
	}
	if inv.Facts != nil {
		f := *inv.Facts
		inv.Facts = &f
	}
	inv.Recommendations = append([]Recommendation(nil), inv.Recommendations...) // v0.10.1293
	if len(inv.Recommendations) > RecommendationsMax {
		inv.Recommendations = inv.Recommendations[:RecommendationsMax]
	}
	for i := range inv.Steps {
		if len(inv.Steps[i].Payload) > maxStepPayload {
			inv.Steps[i].Payload = nil
		}
		if len(inv.Steps[i].Evidence) > maxStepEvidence {
			inv.Steps[i].Evidence = inv.Steps[i].Evidence[:maxStepEvidence]
		}
		for j := range inv.Steps[i].Evidence {
			inv.Steps[i].Evidence[j].Query = truncRunes(inv.Steps[i].Evidence[j].Query, maxEvidenceQuery)
		}
	}
	shrink := []func(){
		// v0.10.2301 (S4) — metin anahtarları İLK düşer: bu sürümden önce
		// sığan bir kayıt, anahtarlar yüzünden başka hiçbir şey kaybetmez.
		func() { dropTextMsgs(&inv) },
		func() {
			for i := range inv.Steps {
				inv.Steps[i].Payload = nil
			}
		},
		func() { inv.Warnings = nil },
		func() { // v0.10.1287 — kanıt künyeleri (sorgu metinleri) düşer
			for i := range inv.Steps {
				inv.Steps[i].Evidence = nil
			}
		},
		func() {
			if len(inv.Findings) > 3 {
				inv.Findings = inv.Findings[:3]
			}
			for i := range inv.Findings {
				if len(inv.Findings[i].Reasons) > 3 {
					inv.Findings[i].Reasons = inv.Findings[i].Reasons[:3]
				}
				inv.Findings[i].EvidenceRefs = nil
			}
		},
		func() { // v0.10.1288 — gerçeklerin uzun listeleri (skor yeniden hesaplanmaz; kartlar kayıtta)
			if inv.Facts != nil {
				inv.Facts.Events, inv.Facts.Node = nil, nil
			}
			if len(inv.Timeline) > 15 {
				inv.Timeline = inv.Timeline[:15]
			}
		},
		func() {
			if len(inv.Hypotheses) > HypothesesShown {
				inv.Hypotheses = inv.Hypotheses[:HypothesesShown]
			}
			inv.Facts = nil
		},
		func() {
			for i := range inv.Steps {
				inv.Steps[i].Summary = truncRunes(inv.Steps[i].Summary, 400)
				inv.Steps[i].EvidenceRefs = nil
			}
			inv.Error = truncRunes(inv.Error, 400)
			for i := range inv.Recommendations { // v0.10.1293
				inv.Recommendations[i].Rationale = truncRunes(inv.Recommendations[i].Rationale, 160)
			}
			if inv.Summary != nil {
				inv.Summary.Text = truncRunes(inv.Summary.Text, 2000)
			}
		},
	}
	for i := 0; ; i++ {
		raw, err := json.Marshal(inv)
		if err != nil {
			return "", err
		}
		if len(raw) <= chstore.SREInvestigationMaxBytes {
			return string(raw), nil
		}
		if i >= len(shrink) {
			return "", fmt.Errorf("sre: kayıt %d B, tavan %d B — küçültülemedi", len(raw), chstore.SREInvestigationMaxBytes)
		}
		shrink[i]()
	}
}

// Decode — SAF: satır → kayıt. Gövdedeki problemId satır kimliğiyle
// eşleşmiyorsa ok=false (başka yoldan yazılmış sahte satır kayıt sayılmaz).
// v0.10.1301: kimlik QueueKey'den — tekrar satırı canlı kayıt olarak, canlı
// satır tekrar olarak çözülemez.
func Decode(v chstore.SavedView) (Investigation, bool) {
	var inv Investigation
	if v.Page != chstore.SREInvestigationPage || v.OwnerID != "" {
		return Investigation{}, false
	}
	if err := json.Unmarshal([]byte(v.QueryString), &inv); err != nil || inv.ProblemID == "" {
		return Investigation{}, false
	}
	if v.ID != chstore.SREInvestigationID(inv.QueueKey()) {
		return Investigation{}, false
	}
	// name kolonu otoritedir (yazım tek satırda ikisini birlikte taşır).
	inv.State = v.Name
	if inv.EnqueuedAt == 0 {
		inv.EnqueuedAt = v.CreatedAt
	}
	return inv, true
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

// dropTextMsgs — v0.10.2301 (S4): kayıttaki metin anahtarlarını siler (EN
// arayüz özgün metni işaretle çizer). Elemanlar kopya dilimde (yukarıda).
func dropTextMsgs(inv *Investigation) {
	for i := range inv.Steps {
		inv.Steps[i].SummaryMsg = nil
	}
	for i := range inv.Recommendations {
		inv.Recommendations[i].TextMsg = nil
	}
	for i := range inv.Timeline {
		inv.Timeline[i].LabelMsg = nil
	}
	if inv.Summary != nil {
		inv.Summary.GateMsg = nil
	}
}
