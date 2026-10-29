// Package cosrelearn — CoSRE'nin açık geri bildirim OLMADAN öğrenmesi.
//
// Faz 2 (bu dosya + quality.go): cosre_interactions turlarından ve
// istemcinin örtük sinyallerinden (cosre_signals) cevap ve tool başına
// gece kalite puanı. Kullanıcı 👍/👎'si seyrek (operatör, 2026-10-15):
// puanın çoğu türetilen sinyallerden gelir, 👍/👎 yalnız ek ağırlık.
//
// Her şey SAF: girdi dilimleri → sinyal/puan. G/Ç job.go'da.
package cosrelearn

import (
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Sinyal türleri. Olumlu/olumsuz ayrımı Weight'in işaretidir.
const (
	SigCopy            = "copy"
	SigLinkClick       = "link_click"
	SigEvidenceClick   = "evidence_click"
	SigProblemNote     = "problem_note"
	SigProblemResolved = "problem_resolved"
	SigThumbsUp        = "thumbs_up"
	// SigFollowupClick — v0.10.1291: operatör cevabın KURAL-türevli takip
	// çipine tıkladı (chat_followup_chips.go). Gözlenen davranış, oy değil.
	SigFollowupClick = "followup_click"
	// v0.10.1597 (CoSRE v2 Faz 1, R7) — operatör kararı §0-2: 👍/👎 yerine
	// kart başına "ilgili / ilgisiz" (ayrıntı = kartın adım kimliği s<i>) ve
	// mesaj düzeyinde serbest not.
	SigEvidenceRelevant   = "evidence_relevant"
	SigEvidenceIrrelevant = "evidence_irrelevant"
	SigNote               = "note"

	SigRephrase   = "rephrase"
	SigRegenerate = "regenerate"
	SigRetry      = "retry"
	SigEdit       = "edit"
	SigAbandon    = "abandon"
	SigTimeout    = "timeout"
	SigError      = "error"
	SigToolError  = "tool_error"
	SigEmpty      = "empty"
	SigNoEvidence = "no_evidence"
	SigThumbsDown = "thumbs_down"
	SigUngrounded = "ungrounded" // Faz 4 dayanak denetimi
)

// ClientSignalKinds — istemcinin POST edebileceği kapalı küme.
var ClientSignalKinds = map[string]bool{
	SigCopy: true, SigLinkClick: true, SigEvidenceClick: true, SigProblemNote: true,
	SigFollowupClick:    true,
	SigEvidenceRelevant: true, SigEvidenceIrrelevant: true, SigNote: true, // v0.10.1597
}

// Weight — sinyalin puana katkısı (taban 0.5, sonuç [0,1]).
var Weight = map[string]float64{
	SigCopy:            0.15,
	SigLinkClick:       0.10,
	SigEvidenceClick:   0.10,
	SigProblemNote:     0.30,
	SigProblemResolved: 0.25,
	// v0.10.1281 (operatör 2026-10-17: "oylara çok güvenmeyelim") — 👍/👎
	// seyrek ve öznel; gözlenen davranış sinyallerinin ALTINDA tartılır.
	SigThumbsUp: 0.10,
	// v0.10.1291 — takip çipi tıkı: gözlenen davranış ama ÇİFT anlamlı
	// (cevap ilerlemeye değer bulundu YA DA cevap yetmedi, bir sonraki adım
	// arandı). Bu yüzden en zayıf olumlu sinyal: link/kanıt tıkının (0.10)
	// yarısı ve 👍'nin (0.10) altında. Oy testi (TestVotesWeighLessThan-
	// ObservedSignals) onu "güçlü gözlenen" listesine KATMAZ — eşiği oyları
	// aşağı çekmeye zorlamasın; kendi pini TestFollowupClickIsWeakObserved.
	// Çalışma zamanı kararına okunmaz (yalnız gece kalite puanı).
	SigFollowupClick: 0.05,
	// v0.10.1597 — kart ilgili/ilgisiz bir OY'dur (öznel yargı), gözlenen
	// davranış değil: 👍/👎 mertebesinde (±0.10), "güçlü gözlenen" listesine
	// girmez (TestEvidenceRelevanceSignalsAreVoteWeight). Serbest not nötr
	// (0): içeriği puana çevrilmez, insan incelemesi içindir. Ağırlıklar
	// operatör onayı bekleyen öneri (Faz 1 planı açık soru 4).
	// Operatör 2026-10-29: puanlama ilk ikisini system_settings("cosre")
	// signals.weights'ten okur (signal_weights.go, ±0.25); not 0'a
	// KİLİTLİ (v0.10.1605). Buradaki değerler VARSAYILAN ve oy-ağırlığı
	// pininin dayanağıdır.
	SigEvidenceRelevant:   defaultEvidenceRelevantWeight,
	SigEvidenceIrrelevant: defaultEvidenceIrrelevantWeight,
	SigNote:               defaultNoteWeight,

	SigRephrase:   -0.30,
	SigRegenerate: -0.30,
	SigRetry:      -0.20,
	SigEdit:       -0.25,
	SigAbandon:    -0.30,
	SigTimeout:    -0.30,
	SigError:      -0.40,
	SigToolError:  -0.10, // tool başına, toplam en çok iki kez
	SigEmpty:      -0.50,
	SigNoEvidence: -0.25,
	SigThumbsDown: -0.15, // v0.10.1281 — bkz. SigThumbsUp notu
	SigUngrounded: -0.30,
}

const (
	// RephraseWindow — bir sonraki soru bu sürede gelirse "yeniden sorma" adayı.
	RephraseWindow = 2 * time.Minute
	// RephraseSimilarity — iki sorunun kelime kümesi Jaccard eşiği.
	RephraseSimilarity = 0.5
	// ResolvedWindow — konuşmanın son cevabından sonra problem bu sürede çözülürse olumlu.
	ResolvedWindow   = 30 * time.Minute
	maxToolErrorHits = 2
)

// Inputs — bir puanlama koşusunun girdileri.
type Inputs struct {
	Interactions []chstore.CosreInteraction // created_at'e göre sıralı olmak zorunda değil
	Signals      []chstore.CosreSignal
	Feedback     map[string]int8      // exchange_id → 1 / -1
	Resolved     map[string]time.Time // problem_id → resolved_at
	Ungrounded   map[string]bool      // exchange_id → Faz 4 bulgusu
}

// DeriveSignals — SAF: exchange_id → o cevaba düşen sinyal türleri (tekil).
func DeriveSignals(in Inputs) map[string][]string {
	out := map[string]map[string]int{}
	add := func(id, kind string) {
		if id == "" {
			return
		}
		m := out[id]
		if m == nil {
			m = map[string]int{}
			out[id] = m
		}
		m[kind]++
	}
	known := map[string]bool{}
	for _, r := range in.Interactions {
		known[r.ExchangeID] = true
		for _, k := range turnSignals(r) {
			add(r.ExchangeID, k)
		}
	}
	for _, g := range in.Signals {
		if known[g.ExchangeID] && ClientSignalKinds[g.Kind] {
			add(g.ExchangeID, g.Kind)
		}
	}
	for id, v := range in.Feedback {
		if !known[id] {
			continue
		}
		switch {
		case v > 0:
			add(id, SigThumbsUp)
		case v < 0:
			add(id, SigThumbsDown)
		}
	}
	for id := range in.Ungrounded {
		if known[id] {
			add(id, SigUngrounded)
		}
	}
	// v0.10.1291 — takip çipinden gelen sonraki soru "yeniden sorma" DEĞİL:
	// metni bizim kuralımız üretti, benzerliği cevabın kusuru sayılmaz.
	chipped := map[string]bool{}
	for _, g := range in.Signals {
		if g.Kind == SigFollowupClick {
			chipped[g.ExchangeID] = true
		}
	}
	for _, conv := range conversations(in.Interactions) {
		for i, r := range conv {
			if i+1 < len(conv) {
				if k := followUpSignal(r, conv[i+1]); k != "" && !(k == SigRephrase && chipped[r.ExchangeID]) {
					add(r.ExchangeID, k)
				}
				continue
			}
			// konuşmanın son cevabı: ardından problem çözüldü mü
			if r.ProblemID == "" {
				continue
			}
			end := r.CreatedAt.Add(time.Duration(r.TotalMs) * time.Millisecond)
			if at, ok := in.Resolved[r.ProblemID]; ok && !at.Before(end) && at.Sub(end) <= ResolvedWindow {
				add(r.ExchangeID, SigProblemResolved)
			}
		}
	}
	res := make(map[string][]string, len(out))
	for id, m := range out {
		kinds := make([]string, 0, len(m))
		for k, n := range m {
			if k == SigToolError && n > maxToolErrorHits {
				n = maxToolErrorHits
			}
			for j := 0; j < n; j++ {
				kinds = append(kinds, k)
			}
		}
		sort.Strings(kinds)
		res[id] = kinds
	}
	return res
}

// turnSignals — SAF: tek turun kendinden okunan sinyaller.
func turnSignals(r chstore.CosreInteraction) []string {
	var out []string
	switch r.Status {
	case "aborted":
		// v0.10.1280 — sonuç artık "aborted" olarak yazılıyor (istemci cevap
		// gelmeden gitti); eski satırlar aşağıdaki "context canceled" dalında.
		out = append(out, SigAbandon)
	case "error":
		msg := strings.ToLower(r.ErrorMsg)
		switch {
		case strings.Contains(msg, "context canceled"):
			out = append(out, SigAbandon)
		case strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "timeout"):
			out = append(out, SigTimeout)
		default:
			out = append(out, SigError)
		}
	default:
		if r.EmptyAnswer {
			out = append(out, SigEmpty)
		}
		if r.NoEvidence {
			out = append(out, SigNoEvidence)
		}
	}
	for _, t := range r.Tools {
		if !t.OK && t.Error != "skipped" {
			out = append(out, SigToolError)
		}
	}
	return out
}

// followUpSignal — SAF: önceki cevaba, ardından gelen turdan düşen olumsuz sinyal.
// İstemci beyanı (attempt) süreden bağımsız; beyansız yeniden sorma süre +
// benzerlik ister.
func followUpSignal(prev, next chstore.CosreInteraction) string {
	switch next.Attempt {
	case "regenerate":
		return SigRegenerate
	case "retry":
		return SigRetry
	case "edit":
		return SigEdit
	}
	end := prev.CreatedAt.Add(time.Duration(prev.TotalMs) * time.Millisecond)
	gap := next.CreatedAt.Sub(end)
	if gap < 0 {
		gap = next.CreatedAt.Sub(prev.CreatedAt)
	}
	if gap <= RephraseWindow && QuestionSimilarity(prev.Question, next.Question) >= RephraseSimilarity {
		return SigRephrase
	}
	return ""
}

// conversations — SAF: turlar konuşmaya göre gruplu, zamana göre sıralı.
// Konuşma kimliği yoksa kullanıcı + sayfa bağlamı (problem) anahtar olur.
func conversations(rows []chstore.CosreInteraction) [][]chstore.CosreInteraction {
	groups := map[string][]chstore.CosreInteraction{}
	var keys []string
	for _, r := range rows {
		k := r.ConversationID
		if k == "" {
			k = "u:" + r.UserEmail + "|" + r.ProblemID
		}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], r)
	}
	sort.Strings(keys)
	out := make([][]chstore.CosreInteraction, 0, len(keys))
	for _, k := range keys {
		g := groups[k]
		sort.SliceStable(g, func(i, j int) bool { return g[i].CreatedAt.Before(g[j].CreatedAt) })
		out = append(out, g)
	}
	return out
}

// QuestionSimilarity — SAF: küçük harf kelime kümelerinin Jaccard benzerliği
// (≥2 karakterli harf/rakam dizileri).
func QuestionSimilarity(a, b string) float64 {
	sa, sb := wordSet(a), wordSet(b)
	if len(sa) == 0 || len(sb) == 0 {
		return 0
	}
	inter := 0
	for w := range sa {
		if sb[w] {
			inter++
		}
	}
	union := len(sa) + len(sb) - inter
	return float64(inter) / float64(union)
}

func wordSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-'
	}) {
		if len([]rune(w)) >= 2 {
			out[w] = true
		}
	}
	return out
}
