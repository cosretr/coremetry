package sre

// contextpack.go — v0.10.1462 (CoSRE A1): Context Pack. Modele giden
// deterministik bağlamın TEK kurucusu: kayıt (+ problem) → sıralı bölümler →
// metin. İki tüketici bugün aynı yoldan geçer:
//
//   - anlatım girdisi (renderInvestigationTR → BuildContextPack + RenderTR,
//     narrationBudget = eski 4 KB tek kesim, capBytes),
//   - sohbetin "KAYITLI ARAŞTIRMA (HH:MM)" bloğu (RenderSavedTR →
//     BuildSavedPack + RenderTR, chatBudget = tavansız, bugünkü davranış).
//
// A1 saf refactor'dur: metin bayt-bayt aynı (testdata/narration altınları,
// TestNarrationGoldenByteIdentical). Yeni bölümler (baseline / anomaliler /
// komşular / servis / hata özeti) ve öncelikli kırpma A2'de, bayrak arkasında.
//
// SAF: ctx yok, okuma yok. Girdi yalnız kayıt alanları; oy / geri bildirim /
// operatör kuralı okunmaz (contextpack_test.go korkuluğu +
// TestRulesNeverReachScoring). Hipotez satırları renderHypothesesTR'den aynen
// gelir (operatör işareti orada "veri" olarak basılır, skora girmez).

import (
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// PackModeLegacy — tüm bölümler birleşir, sonra tek bayt kesimi (capBytes).
const PackModeLegacy = "legacy"

// PackModePriority — v0.10.1494 (CoSRE 2d): tavan aşılırsa en düşük öncelikli
// (Priority en büyük) bölümün satırları sondan kırpılır, satırı kalmayan
// bölüm düşer; öncelik 0–1 asla kırpılmaz (son emniyet capBytes).
const PackModePriority = "priority"

// PackSection — paketin tek bölümü. Text bölüm başlığını ve satır sonlarını
// içerir; boş Text = bölüm yok.
type PackSection struct {
	Key      string // problem|rule|start|steps|synth|hypotheses|recommendations|findings · saved_head|saved_findings|saved_hypotheses|saved_summary
	Priority int    // 0 = asla düşmez … 9 = ilk düşen (A2 öncelikli kırpma; A1'de yalnız künye)
	Text     string
	Rows     int // satır sayısı ("\n")
}

// ContextPack — sıralı bölümler.
type ContextPack struct {
	Sections []PackSection
}

// PackBudget — RenderTR tavanı. MaxBytes ≤ 0 = tavansız.
type PackBudget struct {
	Mode     string
	MaxBytes int
}

// PackSectionMeta — bölümün künyesi (metin değil).
type PackSectionMeta struct {
	Key   string `json:"key"`
	Bytes int    `json:"bytes"`
	Rows  int    `json:"rows"`
	// v0.10.1494 — öncelikli kipte kırpılan satır / düşen bölüm.
	TrimmedRows int  `json:"trimmedRows,omitempty"`
	Dropped     bool `json:"dropped,omitempty"`
}

// PackMeta — paketin künyesi (A1'de kayda yazılmaz; A2 bayrak açıkken yazar).
type PackMeta struct {
	Mode      string            `json:"mode"`
	Bytes     int               `json:"bytes"`
	EstTokens int               `json:"estTokens"`
	Capped    bool              `json:"capped,omitempty"`
	Sections  []PackSectionMeta `json:"sections"`
	// v0.10.1494 — öncelikli kip: tavan (bayt) ve kırpma oldu mu.
	MaxBytes int  `json:"maxBytes,omitempty"`
	Trimmed  bool `json:"trimmed,omitempty"`
}

// narrationBudget — anlatım girdisi: eski 4 KB tek kesim (renderMaxBytes).
var narrationBudget = PackBudget{Mode: PackModeLegacy, MaxBytes: renderMaxBytes}

// chatBudget — sohbet bloğu: bugün tavan yok (satırlar truncRunes ile sınırlı).
var chatBudget = PackBudget{Mode: PackModeLegacy}

func section(key string, prio int, text string) PackSection {
	return PackSection{Key: key, Priority: prio, Text: text, Rows: strings.Count(text, "\n")}
}

// BuildContextPack — SAF: kayıt + problem → anlatım bölümleri. Sıra: başlık →
// kural → pencere → adımlar → sentezleyici → hipotezler → öneriler → bulgular
// (serbest metin EN SONDA — veri talimat değildir). Damga loc diliminde
// (nil → UTC).
func BuildContextPack(inv Investigation, p chstore.Problem, loc *time.Location) ContextPack {
	return BuildContextPackWith(inv, p, loc, false)
}

// BuildContextPackWith — v0.10.1494 (CoSRE 2d): withReaders=true → dört
// okuyucunun bölümleri (baseline / değişiklikler / anomaliler / komşular /
// hata özeti; contextpack_sections.go) adımlardan sonra, sentezleyiciden önce.
// false → BuildContextPack ile bayt bayt aynı (altınlar).
func BuildContextPackWith(inv Investigation, p chstore.Problem, loc *time.Location, withReaders bool) ContextPack {
	if loc == nil {
		loc = time.UTC
	}
	hm := func(ns int64) string { return time.Unix(0, ns).In(loc).Format("15:04") }
	zone := time.Unix(0, inv.Anchor.To).In(loc).Format("MST")
	var pack ContextPack
	add := func(key string, prio int, text string) {
		if text != "" {
			pack.Sections = append(pack.Sections, section(key, prio, text))
		}
	}

	head := []string{firstNonEmpty(inv.DisplayID, inv.ProblemID), "servis=" + inv.Service}
	if inv.Env != "" {
		head = append(head, "env="+inv.Env)
	}
	if inv.Priority != "" {
		head = append(head, "öncelik="+inv.Priority)
	}
	if inv.Anchor.Symptom != "" {
		head = append(head, "belirti="+inv.Anchor.Symptom)
	}
	add("problem", 0, "PROBLEM: "+strings.Join(head, " · ")+"\n")

	if rule := firstNonEmpty(p.RuleName, p.RuleID); rule != "" {
		line := "KURAL: " + truncRunes(rule, 120)
		if p.Metric != "" {
			line += fmt.Sprintf(" · %s=%.4g (eşik %.4g)", p.Metric, p.Value, p.Threshold)
		}
		add("rule", 0, line+"\n")
	}
	if inv.ProblemAt > 0 {
		add("start", 0, fmt.Sprintf("BAŞLANGIÇ: %s %s · pencere %s–%s %s\n", hm(inv.ProblemAt), zone, hm(inv.Anchor.From), hm(inv.Anchor.To), zone))
	}
	add("known", 1, knownSectionTR(inv.Facts)) // v0.10.2477 — gerçek yoksa bölüm yok (altınlar aynen)

	var b strings.Builder
	b.WriteString("ADIMLAR:\n")
	for _, s := range inv.Steps {
		if s.Name == stepSummary {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s", stepLabelTR(s.Name), stepStateTR(s.State))
		if s.Summary != "" {
			b.WriteString(" — " + truncRunes(oneLine(s.Summary), 240))
		}
		b.WriteString("\n")
	}
	add("steps", 1, b.String())
	if withReaders {
		for _, s := range readerSections(inv, hm) {
			add(s.Key, s.Priority, s.Text)
		}
	}

	if sr := inv.SynthRef; sr != nil {
		if sr.TopSuspect != "" {
			add("synth", 1, fmt.Sprintf("SENTEZLEYİCİ: şüpheli %s (güven %%%.0f)\n", sr.TopSuspect, sr.Confidence*100))
		} else {
			add("synth", 1, "SENTEZLEYİCİ: belirgin şüpheli yok\n")
		}
	}
	add("hypotheses", 1, renderHypothesesTR(inv.Hypotheses))
	add("recommendations", 2, renderRecommendationsTR(inv.Recommendations)) // v0.10.1293 — ≤2 öneri, bulgulardan önce
	add("findings", 1, renderFindingsTR(inv.Findings))
	return pack
}

// renderFindingsTR — SAF: anlatım girdisinin bulgu bölümü (≤3 gerekçe/satır).
func renderFindingsTR(fs []Finding) string {
	if len(fs) == 0 {
		return "BULGULAR: yok — okunan bölümlerde eşik aşan bulgu çıkmadı\n"
	}
	var b strings.Builder
	b.WriteString("BULGULAR (sunucu sırası):\n")
	for i, f := range fs {
		fmt.Fprintf(&b, "%d. [%s] %s %s", i+1, f.Level, kindTR(f.Kind), truncRunes(oneLine(f.Name), 120))
		if len(f.Reasons) > 0 {
			rs := f.Reasons
			if len(rs) > 3 {
				rs = rs[:3]
			}
			b.WriteString(" — " + truncRunes(oneLine(strings.Join(rs, "; ")), 260))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// BuildSavedPack — SAF: kayıtlı araştırmanın sohbet özeti → "KAYITLI
// ARAŞTIRMA (HH:MM)" bölümleri. Bitmemiş ya da bulgusuz-özetsiz-hipotezsiz
// kayıt boş paket. Damga loc diliminde (nil → UTC).
func BuildSavedPack(problemRef string, s mcptools.InvestigationSummary, loc *time.Location) ContextPack {
	switch s.State {
	case StateDone, StatePartial, StateShared:
	default:
		return ContextPack{}
	}
	if len(s.Findings) == 0 && s.Summary == "" && len(s.Hypotheses) == 0 {
		return ContextPack{}
	}
	if loc == nil {
		loc = time.UTC
	}
	var pack ContextPack
	add := func(key string, prio int, text string) {
		if text != "" {
			pack.Sections = append(pack.Sections, section(key, prio, text))
		}
	}
	head := fmt.Sprintf("KAYITLI ARAŞTIRMA (%s) — AI SRE ajanı, problem %s", time.Unix(0, s.At).In(loc).Format("15:04"), problemRef)
	switch s.State {
	case StatePartial:
		head += " · kısmi (bazı adımlar okunamadı)"
	case StateShared:
		head += " · aynı servisin yakın araştırması (" + s.SharedFrom + ")"
	}
	add("saved_head", 0, head+". Bu bir ÖNERİDİR, hiçbir şey değiştirilmedi; yaşı yukarıdaki saatte.\n")
	var f strings.Builder
	for _, line := range s.Findings {
		f.WriteString("- " + line + "\n")
	}
	add("saved_findings", 1, f.String())
	add("saved_hypotheses", 1, renderSavedHypothesesTR(s.Hypotheses))
	if s.Summary != "" {
		add("saved_summary", 2, "Ajanın özeti:\n"+truncRunes(s.Summary, 1200)+"\n")
	}
	return pack
}

// RenderTR — SAF: paket → metin + künye. Legacy kip: bölümler sırayla
// birleşir, sonra (MaxBytes > 0 ise) tek capBytes kesimi — UTF-8 rune
// sınırında, "…\n" ile biter. Bölüm künyesi kesim ÖNCESİ boyutları taşır.
func RenderTR(pack ContextPack, b PackBudget) (string, PackMeta) {
	if b.Mode == PackModePriority {
		return renderPriority(pack, b.MaxBytes)
	}
	meta := PackMeta{Mode: PackModeLegacy, Sections: []PackSectionMeta{}}
	var sb strings.Builder
	for _, s := range pack.Sections {
		if s.Text == "" {
			continue
		}
		sb.WriteString(s.Text)
		meta.Sections = append(meta.Sections, PackSectionMeta{Key: s.Key, Bytes: len(s.Text), Rows: s.Rows})
	}
	out := sb.String()
	if b.MaxBytes > 0 && len(out) > b.MaxBytes {
		out, meta.Capped = capBytes(out, b.MaxBytes), true
	}
	meta.Bytes, meta.EstTokens = len(out), estimateTokens(out)
	return out, meta
}

// estimateTokens — kaba token tahmini: 4 bayt/token, yukarı yuvarlanır
// (model-bağımsız; gerçek sayım sağlayıcının usage alanında).
func estimateTokens(s string) int {
	return (len(s) + 3) / 4
}
