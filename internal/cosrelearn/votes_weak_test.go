package cosrelearn

// v0.10.1281 — operatör 2026-10-17: "oylara çok güvenmeyelim".
//
// İki değişmez:
//  1. 👍/👎 ağırlığı, gözlenen davranış sinyallerinin en zayıfından bile
//     büyük olamaz (oy seyrek ve öznel; davranış ölçülür).
//  2. Oylar ve onlardan türeyen kalite puanları ÇALIŞMA ZAMANI kararlarına
//     (sohbet yönlendirme, araç seçimi, prompt, AI SRE hipotez puanı)
//     okunmaz — yalnız ölçüm/inceleme yüzeylerinde görünür.

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVotesWeighLessThanObservedSignals(t *testing.T) {
	vote := math.Max(math.Abs(Weight[SigThumbsUp]), math.Abs(Weight[SigThumbsDown]))
	observed := []string{SigCopy, SigProblemNote, SigProblemResolved, SigRephrase, SigRegenerate, SigAbandon, SigTimeout, SigError, SigEmpty, SigNoEvidence, SigUngrounded}
	for _, k := range observed {
		if math.Abs(Weight[k]) < vote {
			t.Errorf("oy ağırlığı %.2f, gözlenen %q (%.2f) sinyalinden büyük olamaz", vote, k, Weight[k])
		}
	}
}

// runtimeDirs — karar veren paketler; buralarda kalite puanı / oy okuyucusu
// çağrılmamalı. ai_feedback.go (yazma + /ai listesi) ve rag_curate.go (admin
// KB aday listesi, insan onaylı) ölçüm/inceleme yüzeyidir, hariç.
var runtimeDirs = []string{"../copilot", "../mcptools", "../sre", "../anomaly", "../api"}

var readerNeedles = []string{
	"AIFeedbackVerdictsSince", "ListCosreAnswerQuality", "ListCosreToolQuality",
	"CosreAnswerQuality(", "CosreToolQuality(",
	// v0.10.1301 — AI SRE Faz 5: sre-* 👎 inceleme listesi. YALNIZ /ai inceleme
	// yüzeyi okur; değerlendirme puanı (internal/sre scoreEval) okumaz.
	"ListSREReviewDownvotes",
}

var reviewSurfaces = map[string]bool{
	"ai_feedback.go": true, "rag_curate.go": true, "rag.go": true,
	"ai_quality.go": true, "cosre_quality.go": true, "ai_exchanges.go": true,
	// v0.10.1301 — api/sre_eval.go: değerlendirme panelinin "incelemeye değer"
	// 👎 listesi (admin, salt-okunur liste; etiket / puan girdisi değil).
	"sre_eval.go": true,
	// v0.10.1464 — api/ai_routing_report.go: /ai "Şüpheli yönlendirmeler"
	// inceleme listesi (admin). 👎 yalnız ikincil kolon; puan/liste/sıra
	// oydan bağımsız (TestRoutingReportVotesNeverScore pinler).
	"ai_routing_report.go": true,
}

func TestVotesNeverReachRuntimeDecisions(t *testing.T) {
	for _, dir := range runtimeDirs {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range files {
			base := filepath.Base(f)
			if strings.HasSuffix(base, "_test.go") || reviewSurfaces[base] {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range readerNeedles {
				if strings.Contains(string(b), n) {
					t.Errorf("%s: %q — oy/kalite puanı çalışma zamanı kararına okunmamalı (ölçüm yüzeyiyse reviewSurfaces'a gerekçeyle ekle)", f, n)
				}
			}
		}
	}
}

// v0.10.1291 — takip çipi tıkı (followup_click) gözlenen davranıştır ama çift
// anlamlıdır (cevap yetti → ilerle / yetmedi → başka yere bak). Pin: olumlu,
// ≤0.10, link/kanıt tıkından büyük değil ve 👍'den büyük değil — oy testinin
// "gözlenen" listesine girmez ki oy ağırlıklarını aşağı çekmeye zorlamasın.
func TestFollowupClickIsWeakObserved(t *testing.T) {
	w := Weight[SigFollowupClick]
	switch {
	case w <= 0:
		t.Fatalf("followup_click ağırlığı %.2f — olumlu olmalı", w)
	case w > 0.10:
		t.Errorf("followup_click ağırlığı %.2f > 0.10", w)
	case w > Weight[SigLinkClick] || w > Weight[SigEvidenceClick]:
		t.Errorf("followup_click (%.2f) link/kanıt tıkından ağır olamaz", w)
	case w > Weight[SigThumbsUp]:
		t.Errorf("followup_click (%.2f) 👍'den ağır olamaz", w)
	}
	if !ClientSignalKinds[SigFollowupClick] {
		t.Error("followup_click istemci sinyali olarak kabul edilmeli")
	}
}

// v0.10.1597 (CoSRE v2 Faz 1, R7) — kart "ilgili / ilgisiz" bir OY'dur
// (operatörün öznel yargısı), gözlenen davranış değil: ağırlığı 👍/👎 ile
// aynı mertebede kalır ve "güçlü gözlenen" listesine girmez. Serbest not
// nötrdür (0): içeriği puana çevrilmez; insan incelemesi içindir.
func TestEvidenceRelevanceSignalsAreVoteWeight(t *testing.T) {
	vote := math.Max(math.Abs(Weight[SigThumbsUp]), math.Abs(Weight[SigThumbsDown]))
	if w := Weight[SigEvidenceRelevant]; w <= 0 || w > vote {
		t.Errorf("evidence_relevant %.2f — (0, %.2f] olmalı", w, vote)
	}
	if w := Weight[SigEvidenceIrrelevant]; w >= 0 || -w > vote {
		t.Errorf("evidence_irrelevant %.2f — [-%.2f, 0) olmalı", w, vote)
	}
	if w, ok := Weight[SigNote]; !ok || w != 0 {
		t.Errorf("note ağırlığı %v (tanımlı=%v) — 0 olmalı", w, ok)
	}
	for _, k := range []string{SigEvidenceRelevant, SigEvidenceIrrelevant, SigNote} {
		if !ClientSignalKinds[k] {
			t.Errorf("%s istemci sinyali olarak kabul edilmeli", k)
		}
	}
	if _, pos, neg := AnswerScore([]string{SigNote}, nil, DefaultSignalWeights()); pos != 0 || neg != 0 {
		t.Error("not olumlu/olumsuz sayılmamalı")
	}
}
