package sre

// replay_grade.go — CoSRE doğruluk ölçümü Faz A, dilim A2:
// replay notlamasının SAF çekirdeği (incident.io emsali).
//
// Not ölçeği: bullseye 100 · on_target 65 · miss 35 · nowhere 0. Not = yerel
// hakem modelin (arka plan profili, yalnız self-hosted) "etiket ↔ sonuç"
// karşılaştırması. Etiket = Problem'in "Kök neden"i (onaylı incident_cases +
// suçlu eki, api/problem_rootcause_label.go). 👍/👎 ve triage düğmeleri
// buraya GİRMEZ (vote_runtime_guard_test.go, karar D4).
//
// "Erişim" (reach) = araştırmanın topladığı kanıt etiketteki suçlu
// servisi / bileşeni içeriyor mu. Etikette suçlu varsa DETERMİNİSTİK
// (evidenceEntities + normEntity); yoksa hakemin "reach" cevabı.
//
// Sonuç metni (replayConclusion) tekrarın hipotez sırası + bulgularıdır —
// tekrar anlatım çağırmaz (orchestrator.go replay dalı), notlanan şey
// ajanın yapısal sonucudur.

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// Notlar.
const (
	GradeBullseye = "bullseye"
	GradeOnTarget = "on_target"
	GradeMiss     = "miss"
	GradeNowhere  = "nowhere"
)

// ReplayGrades — sıralı not kümesi (FE dağılım sırası).
var ReplayGrades = []string{GradeBullseye, GradeOnTarget, GradeMiss, GradeNowhere}

// GradeScore — SAF: not → puan (bilinmeyen not ok=false).
func GradeScore(g string) (int, bool) {
	switch g {
	case GradeBullseye:
		return 100, true
	case GradeOnTarget:
		return 65, true
	case GradeMiss:
		return 35, true
	case GradeNowhere:
		return 0, true
	}
	return 0, false
}

const (
	replayConclusionRunes = 900 // hakeme giden sonuç
	replayStoredRunes     = 400 // rapora yazılan sonuç
	replayEntitiesMax     = 30
)

// evidenceEntities — SAF: tekrarın kanıtında geçen varlık adları (servis,
// bağımlılık düğümü, deploy, pod, kritik yol, log katkısı, sentezleyici
// şüphelisi, hipotez özneleri). Sıra kararlı, tekrarsız, ≤30.
func evidenceEntities(inv Investigation) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	add(inv.Service)
	for _, h := range caseTop(inv) {
		add(h.Subject)
	}
	if inv.SynthRef != nil {
		add(inv.SynthRef.TopSuspect)
	}
	for _, f := range inv.Findings {
		add(f.Name)
	}
	if f := inv.Facts; f != nil {
		if f.Deps != nil {
			for _, c := range f.Deps.Causes {
				add(c.Service)
			}
			for _, e := range f.Deps.Edges {
				add(e.Target)
			}
		}
		if f.Deploys != nil {
			for _, d := range f.Deploys.Rows {
				add(d.Name)
			}
		}
		if f.Pivot != nil {
			for _, p := range f.Pivot.Path {
				add(p.Service)
			}
		}
		if f.Pods != nil {
			add(f.Pods.HotPod)
			add(f.Pods.HotJVM)
			for _, p := range f.Pods.OOMPods {
				add(p)
			}
		}
		if f.Spike != nil {
			for _, c := range f.Spike.Contributors {
				add(c.Value)
			}
		}
	}
	if len(out) > replayEntitiesMax {
		out = out[:replayEntitiesMax]
	}
	return out
}

// culpritReached — SAF: normalize suçlu, bir kanıt varlığına eşit ya da onun
// sözcük sınırlı parçası ("orders" ↔ "orders-api", "db:orders" ↔ "orders").
func culpritReached(culprit string, entities []string) bool {
	c := normEntity(culprit)
	if c == "" {
		return false
	}
	for _, e := range entities {
		n := normEntity(e)
		if n == "" {
			continue
		}
		if n == c || strings.Contains(" "+n+" ", " "+c+" ") || strings.Contains(" "+c+" ", " "+n+" ") {
			return true
		}
	}
	return false
}

// replayInconclusive — SAF: kanıt kapısı "yetersiz" dedi mi (kipten
// bağımsız: tekrar gösterilecek olanı ölçer; plain → kapı yok → false).
func replayInconclusive(inv Investigation) bool {
	return !inv.Plain && inv.Coverage != nil && inv.Coverage.Verdict == GateInsufficient
}

// replayConclusion — SAF: tekrarın sonucu düz metin (hipotez sırası ≤3 +
// bulgular ≤4). Hipotez yoksa dürüst "sonuç yok". CoSRE Faz B: kanıt kapısı
// yetersiz dediyse hipotez sırası YERİNE "KANIT YETERSİZ — eksik …" (yanlış
// yerine sonuçsuz); erişim beyanı sona eklenir (plain'de yok).
func replayConclusion(inv Investigation) string {
	var b strings.Builder
	top := caseTop(inv)
	if replayInconclusive(inv) {
		top = nil
		b.WriteString(coverageTR(*inv.Coverage) + "\n")
	}
	for i, h := range top {
		label := h.Kind
		if i < len(inv.Hypotheses) && inv.Hypotheses[i].Label != "" {
			label = inv.Hypotheses[i].Label
		}
		fmt.Fprintf(&b, "%d. %s [%s] skor %.2f (%s)", i+1, label, h.Category, h.Score, h.Band)
		if h.Subject != "" {
			fmt.Fprintf(&b, " — özne: %s", h.Subject)
		}
		b.WriteString("\n")
	}
	for i, f := range inv.Findings {
		if i >= 4 {
			break
		}
		fmt.Fprintf(&b, "Bulgu: %s (%s)", f.Name, f.Level)
		if len(f.Reasons) > 0 {
			fmt.Fprintf(&b, ": %s", f.Reasons[0])
		}
		b.WriteString("\n")
	}
	if len(inv.Reach) > 0 && b.Len() > 0 {
		b.WriteString(reachTR(inv.Reach) + "\n")
	}
	if b.Len() == 0 {
		return "sonuç yok (hipotez ya da bulgu üretilmedi)"
	}
	return truncRunes(strings.TrimSpace(b.String()), replayConclusionRunes)
}

// BuildReplayJudgeUser — SAF: hakemin kullanıcı bloğu (etiket + sonuç + kanıt varlıkları).
func BuildReplayJudgeUser(item ReplayItem, conclusion string, entities []string) string {
	var b strings.Builder
	b.WriteString("ETİKET (ekibin olaydan sonra girdiği gerçek kök neden):\n")
	fmt.Fprintf(&b, "neden: %s\nkategori: %s\n", truncRunes(item.Cause, 600), item.Category)
	if item.Culprit != "" {
		fmt.Fprintf(&b, "suçlu: %s\n", item.Culprit)
	} else {
		b.WriteString("suçlu: belirtilmedi\n")
	}
	fmt.Fprintf(&b, "\nCoSRE ARAŞTIRMASININ SONUCU (servis %s; hipotez sırası + bulgular):\n%s\n", item.Service, conclusion)
	b.WriteString("\nKANITTA GEÇEN VARLIKLAR:\n")
	if len(entities) == 0 {
		b.WriteString("(yok)\n")
	} else {
		b.WriteString(strings.Join(entities, ", "))
		b.WriteString("\n")
	}
	return b.String()
}

// ParseReplayGrade — SAF: hakem çıktısı → not, gerekçe, erişim (unknown → nil).
// Kod bloğu / önsöz tolere edilir (ilk '{' … son '}'); bilinmeyen not hata.
func ParseReplayGrade(out string) (grade, why string, reach *bool, err error) {
	i, j := strings.Index(out, "{"), strings.LastIndex(out, "}")
	if i < 0 || j <= i {
		return "", "", nil, fmt.Errorf("hakem JSON döndürmedi")
	}
	var v struct {
		Grade string `json:"grade"`
		Why   string `json:"why"`
		Reach string `json:"reach"`
	}
	if e := json.Unmarshal([]byte(out[i:j+1]), &v); e != nil {
		return "", "", nil, fmt.Errorf("hakem JSON'u çözülemedi: %w", e)
	}
	grade = strings.ToLower(strings.TrimSpace(v.Grade))
	grade = strings.ReplaceAll(strings.ReplaceAll(grade, " ", "_"), "-", "_")
	if _, ok := GradeScore(grade); !ok {
		return "", "", nil, fmt.Errorf("bilinmeyen not %q", v.Grade)
	}
	switch strings.ToLower(strings.TrimSpace(v.Reach)) {
	case "yes", "evet", "true":
		t := true
		reach = &t
	case "no", "hayır", "hayir", "false":
		f := false
		reach = &f
	}
	return grade, truncRunes(strings.TrimSpace(v.Why), 240), reach, nil
}

// ── özet (SAF) ──────────────────────────────────────────────────────────────

// ReplaySummary — raporun ölçüleri (okuma anında hesaplanır; oran alanları payda 0 iken null).
type ReplaySummary struct {
	Problems int `json:"problems"` // notlanabilir (atlanmamış) Problem
	Skipped  int `json:"skipped"`  // atlanan Problem
	Runs     int `json:"runs"`
	Pending  int `json:"pending"` // tekrar ya da not sürüyor
	Graded   int `json:"graded"`
	Errored  int `json:"errored"` // tekrar tamamlanamadı / not alınamadı
	// Score — notlanan koşuların ortalama puanı (0–100).
	Score *float64       `json:"score"`
	Dist  map[string]int `json:"dist"`
	// Reach — erişimi bilinen koşularda suçluya varanların payı.
	ReachKnown int      `json:"reachKnown"`
	Reached    int      `json:"reached"`
	ReachRate  *float64 `json:"reachRate"`
	// Varyans — ≥2 notlu koşusu olan Problem'lerde koşudan koşuya fark.
	MultiRun   int      `json:"multiRun"`
	Unstable   int      `json:"unstable"`   // notu koşudan koşuya değişen Problem
	MeanSpread *float64 `json:"meanSpread"` // ortalama (en yüksek − en düşük) puan
	MaxSpread  int      `json:"maxSpread"`
	// Inconclusive — CoSRE Faz B3: kapının "kanıt yetersiz" dediği biten koşu.
	Inconclusive int `json:"inconclusive"`
}

// ItemStats — SAF: bir Problem'in notlu koşuları → ortalama puan, yayılım
// (en yüksek − en düşük), notlu koşu sayısı, notlar farklı mı.
func ItemStats(it ReplayItem) (mean *float64, spread, graded int, unstable bool) {
	lo, hi, sum := math.MaxInt, math.MinInt, 0
	first := ""
	for _, r := range it.Runs {
		sc, ok := GradeScore(r.Grade)
		if !ok {
			continue
		}
		graded++
		sum += sc
		lo, hi = min(lo, sc), max(hi, sc)
		if first == "" {
			first = r.Grade
		} else if r.Grade != first {
			unstable = true
		}
	}
	if graded == 0 {
		return nil, 0, 0, false
	}
	m := math.Round(float64(sum)/float64(graded)*10) / 10
	return &m, hi - lo, graded, unstable
}

func round1(v float64) *float64 {
	r := math.Round(v*10) / 10
	return &r
}

// SummarizeReplay — SAF: rapor → ölçüler.
func SummarizeReplay(rep ReplayReport) ReplaySummary {
	sm := ReplaySummary{Dist: map[string]int{}}
	for _, g := range ReplayGrades {
		sm.Dist[g] = 0
	}
	sum, spreadSum := 0, 0
	for _, it := range rep.Items {
		if it.State == CaseSkipped {
			sm.Skipped++
			continue
		}
		sm.Problems++
		for _, r := range it.Runs {
			sm.Runs++
			sc, graded := GradeScore(r.Grade)
			switch {
			case graded:
				sm.Graded++
				sum += sc
				sm.Dist[r.Grade]++
			case r.State == StateError || r.State == CaseSkipped || r.GradeErr != "":
				sm.Errored++
			default:
				sm.Pending++
			}
			if r.Inconclusive {
				sm.Inconclusive++
			}
			if r.Reach != nil {
				sm.ReachKnown++
				if *r.Reach {
					sm.Reached++
				}
			}
		}
		if _, spread, n, unstable := ItemStats(it); n >= 2 {
			sm.MultiRun++
			spreadSum += spread
			sm.MaxSpread = max(sm.MaxSpread, spread)
			if unstable {
				sm.Unstable++
			}
		}
	}
	if sm.Graded > 0 {
		sm.Score = round1(float64(sum) / float64(sm.Graded))
	}
	sm.ReachRate = ratio(sm.Reached, sm.ReachKnown)
	if sm.MultiRun > 0 {
		sm.MeanSpread = round1(float64(spreadSum) / float64(sm.MultiRun))
	}
	return sm
}
