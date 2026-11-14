package api

// ai_routing_report.go — v0.10.1464: haftalık YANLIŞ-YÖNLENDİRME RAPORU.
//
//   GET /api/ai/routing-report?days=1|7|30
//
// Operatör (2026-10-23): yanlış yönlendirmeler ekran görüntüsüyle bulunuyor.
// Bu uç, zaten kayıtlı turlardan (cosre_interactions: kademe, cosre-turn
// etiketindeki rota, sonuç, boş / kanıtsız cevap, istemcinin yeniden deneme
// beyanı) "şüpheli tur" adaylarını DETERMİNİSTİK puanlar; operatör listeden
// turu açar (arşivi aç → /cosre?chat=) ve gerçekten yanlış yönlendirmeyse
// chat_routing_regression_test.go'ya satır ekler (dosya başlığındaki akış).
//
// Neden yeni şema / MV YOK: tur başına tek satır, haftada binler mertebesi;
// tek sınırlı okuma (LIMIT + max_execution_time, metinler kırpık) ve 5 dk
// serveCached yetiyor (router-gaps / quality emsali). LLM YOK: hakem
// istemiyoruz, kural tablosu (routingReasonWeight) okunur ve test edilir.
//
// 👍/👎 (operatör kuralı, 2026-10-17: oy zayıf sinyal) YALNIZ ikincil kolon:
// puana girmez, satırı listeye sokmaz, sırayı değiştirmez
// (TestRoutingReportVotesNeverScore pinler).
//
// Kayıt: ai_routes.go registerAIRoutes → registerAIRoutingReportRoutes
// (registerCosreQualityRoutes emsali). Rol: admin + editor (v0.10.1484,
// operatör kararı; 1464'te yalnız admin — kardeş /api/ai/* okumaları gibi);
// soru metni ve kullanıcı e-postası taşır, viewer'a kapalı. requireCopilot ile SARILMAZ (kardeş okumalar gibi): rapor LLM
// istemez, AI kapalıyken de geçmiş turlar okunur.

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

// aiRoutingReportRoles — v0.10.1484: editor + admin (operatör: rapor editörlere
// de açık). Soru metni ve kullanıcı e-postası taşır → viewer'a kapalı kalır.
var aiRoutingReportRoles = []string{auth.RoleAdmin, auth.RoleEditor}

func (s *Server) registerAIRoutingReportRoutes(mux *http.ServeMux) {
	// v0.10.1484 (operatör kararı) — editörler de görür; viewer 403.
	mux.HandleFunc("GET /api/ai/routing-report", auth.RequireAnyRole(aiRoutingReportRoles, s.aiRoutingReport))
}

const (
	routingReportTurnLimit = 20000 // okuma tavanı; aşılırsa truncated (en yeni turlar kalır)
	routingReportItemLimit = 50    // listelenen şüpheli tur
	routingReportTierLimit = 20    // kademe·rota özet satırı
	// routingSuspectMin — "şüpheli" eşiği: tek başına zayıf bir neden (yalnız
	// kanıtsız / iptal, puan 1) listeye girmez; "hata yok" gibi meşru kanıtsız
	// cevaplar raporu boğmasın.
	routingSuspectMin = 2
	// routingReaskWindow — aynı konuşmada bu süre içinde benzer soru = yeniden sorma.
	routingReaskWindow = 10 * time.Minute
)

// Nedenler (FE etiketleri routingReportView.ts'te). Ağırlık = puana katkı.
const (
	reasonError          = "error"            // tur hata ile bitti
	reasonEmpty          = "empty"            // boş cevap
	reasonRAGNoEvidence  = "rag_no_evidence"  // doküman kademesi "bilgi yok" dedi ve yine de cevapladı (v0.10.1446 sınıfı)
	reasonLoopNoEvidence = "loop_no_evidence" // serbest döngü "veri bulunamadı" (v0.10.1447 sınıfı)
	reasonRouterMiss     = "router_miss"      // telemetri şekilli soru guided'a oturmadı (döngü / doküman / genel / konu dışı)
	reasonReask          = "reask"            // kullanıcı hemen yeniden sordu / tekrar denedi
	reasonNoEvidence     = "no_evidence"      // diğer kademede kanıtsız cevap
	reasonAborted        = "aborted"          // kullanıcı cevabı beklemedi
)

var routingReasonWeight = map[string]int{
	reasonError: 3, reasonEmpty: 3, reasonRAGNoEvidence: 3, reasonLoopNoEvidence: 3,
	reasonRouterMiss: 2, reasonReask: 2, reasonNoEvidence: 1, reasonAborted: 1,
}

// RoutingReportItem — listedeki şüpheli tur.
type RoutingReportItem struct {
	ExchangeID     string    `json:"exchangeId"`
	ConversationID string    `json:"conversationId,omitempty"`
	Turn           uint16    `json:"turn,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UserEmail      string    `json:"userEmail,omitempty"`
	Question       string    `json:"question"`
	Answer         string    `json:"answer,omitempty"`
	Tier           string    `json:"tier"`
	Route          string    `json:"route,omitempty"`
	Service        string    `json:"service,omitempty"`
	Page           string    `json:"page,omitempty"`
	Score          int       `json:"score"`
	Reasons        []string  `json:"reasons"`
	// Downvote — zayıf ikincil kolon (puana/sıraya girmez).
	Downvote bool `json:"downvote,omitempty"`
}

// RoutingReportTier — kademe·rota başına özet.
type RoutingReportTier struct {
	Tier       string `json:"tier"`
	Route      string `json:"route,omitempty"`
	Turns      int    `json:"turns"`
	Suspicious int    `json:"suspicious"`
}

// RoutingReport — ucun cevabı.
type RoutingReport struct {
	Days        int                 `json:"days"`
	Turns       int                 `json:"turns"`
	Suspicious  int                 `json:"suspicious"`
	Truncated   bool                `json:"truncated"`
	Tiers       []RoutingReportTier `json:"tiers"`
	Items       []RoutingReportItem `json:"items"`
	GeneratedAt int64               `json:"generatedAt"`
}

// routingReportDays — SAF: sabit basamak (anahtara girer; serbest değer
// kardinaliteyi patlatır, router-gaps emsali).
func routingReportDays(v string) int {
	switch v {
	case "1":
		return 1
	case "30":
		return 30
	}
	return 7
}

func (s *Server) aiRoutingReport(w http.ResponseWriter, r *http.Request) {
	days := routingReportDays(r.URL.Query().Get("days"))
	key := fmt.Sprintf("ai:routing-report:v1:d=%d", days)
	s.serveCached(w, r, key, 5*time.Minute, func(ctx context.Context) (any, error) {
		to := time.Now()
		from := to.Add(-time.Duration(days) * 24 * time.Hour)
		turns, err := s.store.CosreRoutingTurns(ctx, from, to, routingReportTurnLimit)
		if err != nil {
			return nil, err
		}
		// Oylar yalnız ikincil kolon; okunamazsa rapor oysuz döner.
		votes, verr := s.store.AIFeedbackVerdictsSince(ctx, from)
		if verr != nil {
			votes = nil
		}
		rep := buildRoutingReport(turns, votes)
		rep.Days, rep.Truncated, rep.GeneratedAt = days, len(turns) >= routingReportTurnLimit, time.Now().UnixNano()
		return rep, nil
	})
}

// routingTurnReasons — SAF: tek turun kendinden okunan nedenler.
func routingTurnReasons(t chstore.RoutingTurn, route string) []string {
	var out []string
	switch t.Status {
	case "aborted":
		out = append(out, reasonAborted)
	case "error":
		out = append(out, reasonError)
	}
	if t.Status != "error" && t.Status != "aborted" {
		switch {
		case t.EmptyAnswer:
			out = append(out, reasonEmpty)
		case t.NoEvidence && t.Tier == "rag":
			out = append(out, reasonRAGNoEvidence)
		case t.NoEvidence && t.Tier == aisurface.TierLoop:
			out = append(out, reasonLoopNoEvidence)
		case t.NoEvidence:
			out = append(out, reasonNoEvidence)
		}
	}
	if routingRouterMiss(t, route) {
		out = append(out, reasonRouterMiss)
	}
	return out
}

// routingRouterMiss — SAF: telemetri şekilli soru (hasGuidedSignal) guided'ın
// DIŞINDA cevaplandı: serbest döngü, doküman (RAG), niyetin genel cevabı
// (rota yok) ya da konu dışı. Kapsam / wiki / çekmece / açıklama / hafıza /
// operasyon kademeleri bilinçli seçimlerdir, sayılmaz.
func routingRouterMiss(t chstore.RoutingTurn, route string) bool {
	switch t.Tier {
	case aisurface.TierLoop, "rag":
	case aisurface.TierIntent:
		if route != "" && route != string(guidedOffTopic) {
			return false
		}
	default:
		return false
	}
	return hasGuidedSignal(normalizeGuidedMsg(t.Question))
}

// routingReaskFlags — SAF: exchange_id → bir sonraki tur bu turun yeniden
// sorulması / tekrar denemesi mi. Konuşma kimliği yoksa kullanıcı anahtar
// olur. İstemci beyanı (retry/regenerate/edit) süreden bağımsız; beyansız
// yeniden sorma pencere + kelime benzerliği ister (cosrelearn ile aynı ölçü).
func routingReaskFlags(turns []chstore.RoutingTurn) map[string]bool {
	groups := map[string][]chstore.RoutingTurn{}
	for _, t := range turns {
		k := t.ConversationID
		if k == "" {
			k = "u:" + t.UserEmail
		}
		groups[k] = append(groups[k], t)
	}
	out := map[string]bool{}
	for _, g := range groups {
		sort.SliceStable(g, func(i, j int) bool { return g[i].CreatedAt.Before(g[j].CreatedAt) })
		for i := 0; i+1 < len(g); i++ {
			prev, next := g[i], g[i+1]
			switch next.Attempt {
			case "retry", "regenerate", "edit":
				out[prev.ExchangeID] = true
				continue
			}
			end := prev.CreatedAt.Add(time.Duration(prev.TotalMs) * time.Millisecond)
			gap := next.CreatedAt.Sub(end)
			if gap < 0 {
				gap = next.CreatedAt.Sub(prev.CreatedAt)
			}
			if explainTemplatePair(prev, next) {
				continue // v0.10.2134 — şablon açıklama sorusu, başka özne: yeniden sorma değil
			}
			if gap <= routingReaskWindow && cosrelearn.QuestionSimilarity(prev.Question, next.Question) >= cosrelearn.RephraseSimilarity {
				out[prev.ExchangeID] = true
			}
		}
	}
	return out
}

// explainTemplatePair — SAF, v0.10.2134 (router boşlukları D1): iki ardışık
// AÇIKLAMA turu (kademe explain) farklı soru metniyle. Açıklama sorusu
// istemcinin şablonudur ("Bu trace'i açıkla · <id>", chat_explain_turn.go);
// iki farklı trace'in kelime Jaccard'ı ~0.6 ≥ eşik → beyansız yeniden sorma
// sanılıyordu (/ai "Açıklama turu 189/454"). Aynı metin (aynı özne) ve
// istemci beyanı (retry/regenerate/edit) yeniden sorma sayılmaya devam eder.
func explainTemplatePair(prev, next chstore.RoutingTurn) bool {
	return prev.Tier == aisurface.TierExplain && next.Tier == aisurface.TierExplain &&
		strings.TrimSpace(prev.Question) != strings.TrimSpace(next.Question)
}

// routingScore — SAF: nedenlerin ağırlık toplamı.
func routingScore(reasons []string) int {
	n := 0
	for _, r := range reasons {
		n += routingReasonWeight[r]
	}
	return n
}

// buildRoutingReport — SAF: turlar + (zayıf) oylar → rapor. Sıra: puan ↓,
// zaman ↓, exchange_id (kararlı). Oylar yalnız Downvote kolonunu doldurur.
func buildRoutingReport(turns []chstore.RoutingTurn, votes map[string]int8) RoutingReport {
	reask := routingReaskFlags(turns)
	type tierKey struct{ tier, route string }
	tierAgg := map[tierKey]*RoutingReportTier{}
	rep := RoutingReport{Turns: len(turns), Tiers: []RoutingReportTier{}, Items: []RoutingReportItem{}}
	for _, t := range turns {
		_, route, _, _ := aisurface.ParseTurnLabel(t.Surface)
		reasons := routingTurnReasons(t, route)
		if reask[t.ExchangeID] {
			reasons = append(reasons, reasonReask)
		}
		score := routingScore(reasons)
		k := tierKey{t.Tier, route}
		agg := tierAgg[k]
		if agg == nil {
			agg = &RoutingReportTier{Tier: t.Tier, Route: route}
			tierAgg[k] = agg
		}
		agg.Turns++
		if score < routingSuspectMin {
			continue
		}
		agg.Suspicious++
		rep.Suspicious++
		rep.Items = append(rep.Items, RoutingReportItem{
			ExchangeID: t.ExchangeID, ConversationID: t.ConversationID, Turn: t.Turn,
			CreatedAt: t.CreatedAt, UserEmail: t.UserEmail,
			Question: strings.TrimSpace(t.Question), Answer: strings.TrimSpace(t.Answer),
			Tier: t.Tier, Route: route, Service: t.Service, Page: t.Page,
			Score: score, Reasons: reasons, Downvote: votes[t.ExchangeID] < 0,
		})
	}
	sort.SliceStable(rep.Items, func(i, j int) bool {
		a, b := rep.Items[i], rep.Items[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.After(b.CreatedAt)
		}
		return a.ExchangeID < b.ExchangeID
	})
	if len(rep.Items) > routingReportItemLimit {
		rep.Items = rep.Items[:routingReportItemLimit]
	}
	for _, a := range tierAgg {
		rep.Tiers = append(rep.Tiers, *a)
	}
	sort.Slice(rep.Tiers, func(i, j int) bool {
		a, b := rep.Tiers[i], rep.Tiers[j]
		if a.Suspicious != b.Suspicious {
			return a.Suspicious > b.Suspicious
		}
		if a.Turns != b.Turns {
			return a.Turns > b.Turns
		}
		return a.Tier+"/"+a.Route < b.Tier+"/"+b.Route
	})
	if len(rep.Tiers) > routingReportTierLimit {
		rep.Tiers = rep.Tiers[:routingReportTierLimit]
	}
	return rep
}
