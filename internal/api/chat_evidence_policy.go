package api

// chat_evidence_policy.go — v0.10.2360 (docs/cosre/investigation-design.md
// S3, §4.4; sre-evidence-audit §5.4): kanıt bloklarının toplanması ve ekleme
// kararı. Yalnız v2 ve cosre_conversation.evidence ≠ off:
//
//   - shadow: adaylar toplanır, karar yalnız ekranda çizilmeyen
//     `evidence_policy` adımına yazılır (answer_class emsali); akış ve araç
//     metni bayt aynı.
//   - on: blok kuran araçta modele giden metin kısa özettir (model görünümü,
//     ≤600 B; ham satır ve yığın modele dönmez), seçilen bloklar `answer`
//     yayınından hemen önce `block` olarak akar (data-block-<tip>).
//
// Karar SAF (decideEvidence): cevap sınıfı bilgi / sohbet → 0; tavan 2,
// "göster" turunda 4; tür başına 1 (tür başına son başarılı aday — yedek
// kural, doğrulayıcının kaynak bağı henüz yok); öncelik exception > trace >
// logs > anomaly > trace_list. Eklenen: cevapta anahtarı (trace / span
// kimliği, istisna tipi, ML olay kimliği, operasyon) geçen adaylar; "göster"
// turunda kalanlar da; eşleşme yokken canlı veri iddiası ya da eylem
// yönlendirmesi varsa en öncelikli tek aday. Eylem var ama aday yoksa
// missingAction (evidence_missing_action, yalnız sayılır).

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/ai/evalrubric"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// evidencePolicyTool — etkileşim satırındaki ölçüm adımı (ekranda çizilmez, i=-1).
const evidencePolicyTool = "evidence_policy"

// evidenceCapDefault — cevap başına blok tavanı ("göster" turunda evalrubric.EvidenceCapShowMe).
const evidenceCapDefault = 2

// evidencePriority — araştırma cevabındaki öncelik.
var evidencePriority = map[string]int{blocks.TypeException: 0, blocks.TypeTrace: 1, blocks.TypeLogs: 2, blocks.TypeAnomaly: 3, blocks.TypeTraceList: 4}

// evidenceDecisionIn — kararın girdileri.
type evidenceDecisionIn struct {
	Class    answerClass
	Question string
	Answer   string
	Cands    []mcptools.EvidencePart // yayın sırasıyla
}

// evidenceDecision — kararın çıktısı.
type evidenceDecision struct {
	Cap           int                     `json:"cap"`
	Pool          int                     `json:"pool"`
	ShowMe        bool                    `json:"showMe,omitempty"`
	Action        bool                    `json:"action,omitempty"`
	Claim         bool                    `json:"claim,omitempty"`
	Matched       int                     `json:"matched"`
	Fallback      bool                    `json:"fallback,omitempty"`
	MissingAction bool                    `json:"missingAction,omitempty"`
	Types         []string                `json:"attached"`
	Picked        []mcptools.EvidencePart `json:"-"`
}

// evidencePool — SAF: tür başına son aday, öncelik sırasıyla.
func evidencePool(cands []mcptools.EvidencePart) []mcptools.EvidencePart {
	last := map[string]int{}
	for i, c := range cands {
		last[c.Block.Type] = i
	}
	out := make([]mcptools.EvidencePart, 0, len(last))
	for _, i := range last {
		out = append(out, cands[i])
	}
	sort.SliceStable(out, func(a, b int) bool { return evidencePriority[out[a].Block.Type] < evidencePriority[out[b].Block.Type] })
	return out
}

// evidenceMatches — SAF: adayın anahtarlarından biri cevapta geçiyor mu
// (32-hex kimlikler 8 karakterlik kısaltmasıyla da).
func evidenceMatches(p mcptools.EvidencePart, answer string) bool {
	for _, k := range p.Keys {
		if strings.Contains(answer, k) || (len(k) == 32 && strings.Contains(answer, k[:8])) {
			return true
		}
	}
	return false
}

// decideEvidence — SAF karar tablosu (TestDecideEvidenceTable).
func decideEvidence(in evidenceDecisionIn) evidenceDecision {
	pool := evidencePool(in.Cands)
	d := evidenceDecision{Cap: evidenceCapDefault, Pool: len(pool), Types: []string{},
		ShowMe: evalrubric.ShowMeCue(in.Question), Action: evalrubric.ActionCue(in.Answer), Claim: evalrubric.LiveDataClaim(in.Answer)}
	if d.ShowMe {
		d.Cap = evalrubric.EvidenceCapShowMe
	}
	if in.Class == classKnowledge || in.Class == classChat {
		d.Cap = 0
	}
	d.MissingAction = d.Action && len(pool) == 0 && d.Cap > 0
	if d.Cap == 0 || len(pool) == 0 {
		return d
	}
	picked := map[int]bool{}
	for i, p := range pool {
		if evidenceMatches(p, in.Answer) {
			picked[i] = true
			d.Matched++
		}
	}
	switch {
	case d.ShowMe:
		for i := range pool {
			picked[i] = true
		}
	case d.Matched == 0 && (d.Claim || d.Action):
		picked[0], d.Fallback = true, true
	}
	for i, p := range pool {
		if picked[i] && len(d.Picked) < d.Cap {
			d.Picked = append(d.Picked, p)
			d.Types = append(d.Types, p.Block.Type)
		}
	}
	return d
}

// evidenceTurn — alışverişin kanıt adayları; nil = kanıt kapalı (off / v1).
type evidenceTurn struct {
	mode     string
	question string
	mu       sync.Mutex
	cands    []mcptools.EvidencePart
	done     bool
}

// newEvidenceTurn — v2 + evidence ≠ off iken tur nesnesi; aksi hâlde nil.
func newEvidenceTurn(ctx context.Context, question string) *evidenceTurn {
	mode := cosreEvidenceMode(ctx)
	if mode != chstore.CosreEvidenceShadow && mode != chstore.CosreEvidenceOn {
		return nil
	}
	return &evidenceTurn{mode: mode, question: question}
}

// on — blok + model görünümü kipi.
func (e *evidenceTurn) on() bool { return e != nil && e.mode == chstore.CosreEvidenceOn }

// evidenceOf — planın kanıt turu (v1 / plan yok: nil).
func (p *v2LoopPlan) evidenceOf() *evidenceTurn {
	if p == nil {
		return nil
	}
	return p.Evidence
}

// collect — sonuç dikişi: blok kurulabiliyorsa aday olur; on iken modele
// giden metin model görünümüdür. Kapalı / hata / blok yok → metin aynen.
func (e *evidenceTurn) collect(tool, content string, isErr bool) string {
	if e == nil || isErr {
		return content
	}
	p, ok := mcptools.BuildEvidencePart(tool, content)
	if !ok {
		return content
	}
	e.mu.Lock()
	e.cands = append(e.cands, p)
	e.mu.Unlock()
	if e.on() && p.ModelView != "" {
		return p.ModelView
	}
	return content
}

// attach — `answer`dan hemen önce: karar, on iken blok yayını, ölçüm adımı.
func (e *evidenceTurn) attach(ctx context.Context, emit func(string, any), seq *blocks.Sequencer, answer string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	if e.done {
		e.mu.Unlock()
		return
	}
	e.done = true
	cands := append([]mcptools.EvidencePart(nil), e.cands...)
	e.mu.Unlock()
	class, _ := answerStyleFrom(ctx).snapshot()
	d := decideEvidence(evidenceDecisionIn{Class: class, Question: e.question, Answer: answer, Cands: cands})
	var shown []string
	if e.on() {
		for _, p := range d.Picked {
			emit("block", seq.Next(p.Block.Type, p.Block.Payload))
		}
		shown = d.Types
	}
	evalConvFrom(ctx).noteEvidence(e.mode, shown)
	if x := chatExchangeFrom(ctx); x != nil {
		args, _ := json.Marshal(struct {
			Mode  string `json:"mode"`
			Class string `json:"class,omitempty"`
			evidenceDecision
		}{e.mode, string(class), d})
		x.noteStep(map[string]any{"tool": evidencePolicyTool, "args": string(args), "i": -1}, time.Now())
	}
}
