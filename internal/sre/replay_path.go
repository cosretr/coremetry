package sre

// replay_path.go — CoSRE ölçüm: replay koşusunun YOL notu. Hakemin notu
// "vardı mı" der; yol notu "nasıl vardı" — kaç okuma, kaç dalga, kaçı boşa,
// tavana dayandı mı. DETERMİNİSTİK: yalnız biten tekrar kaydını (Investigation)
// okur, LLM çağırmaz, rapora koşu başına küçük bir künye yazar.
//
// Tanımlar (tekrar = deterministik plan; serbest model döngüsü yok):
//   - Steps     kayıttaki adım sayısı (atlanan + anlatım dahil — planın boyu)
//   - ToolCalls gerçekten koşan okuma: atlanmamış, anlatım (summary) olmayan adım
//   - Rounds    koşan okumaların dalga sayısı (plan.go Wave 1/2/3 — ajanın
//               "tur"u; dalgası bilinmeyen adım tura girmez)
//   - Wasted    BOŞA ÇAĞRI VEKİLİ: sonucu hiçbir hipotez kontrolünün
//               EvidenceRef'ine / StateRefs'ine ("<adım>.evidences[i]") girmeyen
//               VE hiçbir bulgu üretmeyen (Finding.Step; boş = 0a taraması)
//               okuma. Hata veren okuma sonuç üretmedi → boşa sayılır.
//   - CapHit    iş bütçesi doldu (runWave'in stepBudgetExhausted özeti ya da
//               kayıt hatasında deadline) → "budget"; yoksa bir adım kendi
//               tavanında zaman aşımına uğradı → "step".

import (
	"math"
	"sort"
	"strings"
)

// stepBudgetExhausted — runWave'in bütçe dolunca dönmeyen adıma yazdığı özet.
const stepBudgetExhausted = "süre bütçesi doldu"

// Tavan türleri.
const (
	PathCapBudget = "budget"
	PathCapStep   = "step"
)

// pathWastedMax — rapora yazılan boşa adım adı tavanı.
const pathWastedMax = 12

// stepWave — plan.go'nun dalgaları (adım adı → dalga). Plan bir adımı başka
// dalgaya taşırsa yalnız tur sayısı etkilenir; çağrı / boşa sayımı adla çalışır.
var stepWave = map[string]int{
	stepSweep: 1, stepDeploys: 1, stepDeps: 1, stepErrors: 1,
	stepPivot: 2, stepLogSpike: 2, stepEvents: 2, stepBaseline: 2, stepAnomalies: 2,
	stepNode: 3,
}

// ReplayPath — koşunun yol künyesi.
type ReplayPath struct {
	Steps       int      `json:"steps"`
	ToolCalls   int      `json:"toolCalls"`
	Rounds      int      `json:"rounds"`
	Wasted      int      `json:"wasted"`
	WastedSteps []string `json:"wastedSteps,omitempty"`
	CapHit      bool     `json:"capHit,omitempty"`
	Cap         string   `json:"cap,omitempty"` // budget | step
}

// refStep — "<adım>.evidences[i]" → adım ("deps" yalın da olabilir).
func refStep(ref string) string {
	if i := strings.IndexByte(ref, '.'); i >= 0 {
		return ref[:i]
	}
	return ref
}

func isStepTimeout(summary string) bool {
	m := strings.ToLower(summary)
	return strings.Contains(m, "deadline exceeded") || strings.Contains(m, "timeout") ||
		strings.Contains(m, "timed out") || strings.Contains(m, "zaman aşımı")
}

// replayPathOf — SAF: tekrar kaydı → yol künyesi.
func replayPathOf(inv Investigation) ReplayPath {
	used := map[string]bool{}
	for _, h := range inv.Hypotheses {
		for _, c := range h.Checks {
			if c.EvidenceRef != "" {
				used[refStep(c.EvidenceRef)] = true
			}
		}
		for _, r := range h.StateRefs {
			used[refStep(r)] = true
		}
	}
	for _, f := range inv.Findings {
		used[firstNonEmpty(f.Step, stepSweep)] = true
	}
	p := ReplayPath{Steps: len(inv.Steps)}
	waves := map[int]bool{}
	budget, stepCap := false, false
	for _, s := range inv.Steps {
		if s.Name == stepSummary || s.State == StepSkipped {
			continue
		}
		p.ToolCalls++
		if w := stepWave[s.Name]; w > 0 {
			waves[w] = true
		}
		if s.State == StepError {
			switch {
			case strings.HasPrefix(s.Summary, stepBudgetExhausted):
				budget = true
			case isStepTimeout(s.Summary):
				stepCap = true
			}
		}
		if s.State == StepError || !used[s.Name] {
			p.Wasted++
			if len(p.WastedSteps) < pathWastedMax {
				p.WastedSteps = append(p.WastedSteps, s.Name)
			}
		}
	}
	p.Rounds = len(waves)
	if inv.State == StateError && isStepTimeout(inv.Error) {
		budget = true
	}
	switch {
	case budget:
		p.CapHit, p.Cap = true, PathCapBudget
	case stepCap:
		p.CapHit, p.Cap = true, PathCapStep
	}
	return p
}

// medianOf — SAF: medyan (çift sayıda orta ikinin ortalaması; boş → nil), 1 basamak.
func medianOf(xs []int) *float64 {
	if len(xs) == 0 {
		return nil
	}
	s := append([]int(nil), xs...)
	sort.Ints(s)
	n := len(s)
	v := float64(s[n/2])
	if n%2 == 0 {
		v = float64(s[n/2-1]+s[n/2]) / 2
	}
	v = math.Round(v*10) / 10
	return &v
}

// pathAcc — özetin yol toplayıcısı.
type pathAcc struct {
	steps, calls, rounds, wasted []int
	capHits                      int
}

func (a *pathAcc) add(p *ReplayPath) {
	if p == nil {
		return
	}
	a.steps = append(a.steps, p.Steps)
	a.calls = append(a.calls, p.ToolCalls)
	a.rounds = append(a.rounds, p.Rounds)
	a.wasted = append(a.wasted, p.Wasted)
	if p.CapHit {
		a.capHits++
	}
}

func (a *pathAcc) fill(sm *ReplaySummary) {
	sm.PathRuns, sm.CapHits = len(a.calls), a.capHits
	sm.MedSteps, sm.MedToolCalls = medianOf(a.steps), medianOf(a.calls)
	sm.MedRounds, sm.MedWasted = medianOf(a.rounds), medianOf(a.wasted)
}
