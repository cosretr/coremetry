package api

// ai_routing_report_test.go — v0.10.1464: haftalık yanlış-yönlendirme raporu.
// Pinler: (1) tur nedenleri (RAG "bilgi yok", döngü "veri bulunamadı", router
// kaçağı, hata/boş/iptal); (2) yeniden sorma eşleştirmesi (pencere + benzerlik
// + istemci beyanı); (3) puan sırası, eşik, kademe özeti, tavan; (4) 👍/👎
// puana / listeye / sıraya GİRMEZ (operatör kuralı); (5) uç admin kapılı.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

var rrT0 = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

func rrTurn(id, tier, route, q string) chstore.RoutingTurn {
	return chstore.RoutingTurn{ExchangeID: id, ConversationID: "c-" + id, CreatedAt: rrT0, UserEmail: "u@example.test",
		Tier: tier, Surface: aisurface.TurnLabel(tier, route, false), Question: q, Status: "ok", TotalMs: 1000}
}

func TestRoutingTurnReasons(t *testing.T) {
	noEv := func(r chstore.RoutingTurn) chstore.RoutingTurn { r.NoEvidence = true; return r }
	cases := []struct {
		name string
		turn chstore.RoutingTurn
		want []string
	}{
		{"v0.10.1446 sınıfı: doküman kademesi 'bilgi yok' + telemetri sorusu",
			noEv(rrTurn("a", "rag", "", "orders-api mainframe cevap sürelerimde artış var mı?")), []string{reasonRAGNoEvidence, reasonRouterMiss}},
		{"v0.10.1447 sınıfı: serbest döngü 'veri bulunamadı'",
			noEv(rrTurn("b", aisurface.TierLoop, "", "kısmı için grafikleri ortalamayı gösterebilirsin")), []string{reasonLoopNoEvidence}},
		{"döngüye düşen hata sorusu → router kaçağı", rrTurn("c", aisurface.TierLoop, "", "orders-api hata veriyor mu"), []string{reasonRouterMiss}},
		{"konu dışı dendi ama telemetri sorusu", rrTurn("d", aisurface.TierIntent, string(guidedOffTopic), "orders-api pod restart oluyor"), []string{reasonRouterMiss}},
		{"niyet guided rotasına eşledi → kaçak değil", rrTurn("e", aisurface.TierIntent, string(guidedServiceHealth), "orders-api hata var mı"), nil},
		{"gerçek konu dışı → temiz", rrTurn("f", aisurface.TierIntent, string(guidedOffTopic), "Türkiye'nin başkenti neresidir"), nil},
		{"kapsam kademesi bilinçli → kaçak sayılmaz", rrTurn("g", aisurface.TierScope, "", "@wiki hata kodları"), nil},
		{"guided kanıtsız → zayıf neden", noEv(rrTurn("h", aisurface.TierGuided, string(guidedServiceHealth), "orders-api nasıl")), []string{reasonNoEvidence}},
		{"hata", func() chstore.RoutingTurn {
			r := rrTurn("i", aisurface.TierGuided, "x", "q")
			r.Status = "error"
			return r
		}(), []string{reasonError}},
		{"iptal (kanıtsız sayılmaz)", func() chstore.RoutingTurn {
			r := noEv(rrTurn("j", aisurface.TierGuided, "x", "q"))
			r.Status = "aborted"
			return r
		}(), []string{reasonAborted}},
		{"boş cevap", func() chstore.RoutingTurn {
			r := rrTurn("k", aisurface.TierGuided, "x", "q")
			r.EmptyAnswer, r.NoEvidence = true, true
			return r
		}(), []string{reasonEmpty}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, route, _, _ := aisurface.ParseTurnLabel(c.turn.Surface)
			if got := routingTurnReasons(c.turn, route); !reflect.DeepEqual(got, c.want) {
				t.Errorf("nedenler %v, want %v", got, c.want)
			}
		})
	}
}

func TestRoutingReaskFlags(t *testing.T) {
	at := func(id, conv, q string, d time.Duration, attempt string) chstore.RoutingTurn {
		r := rrTurn(id, aisurface.TierGuided, "x", q)
		r.ConversationID, r.CreatedAt, r.Attempt = conv, rrT0.Add(d), attempt
		return r
	}
	turns := []chstore.RoutingTurn{
		at("a1", "A", "orders-api mainframe cevap süreleri", 0, ""),
		at("a2", "A", "orders-api mainframe cevap süreleri arttı mı", 2*time.Minute, ""), // benzer, pencere içi → a1 yeniden soruldu
		at("a3", "A", "billing-api deploy ne zaman", 4*time.Minute, ""),                  // benzemiyor
		at("a4", "A", "billing-api deploy ne zaman oldu", 40*time.Minute, ""),            // benzer ama pencere dışı
		at("b1", "B", "x sorusu", 0, ""),
		at("b2", "B", "tamamen başka", time.Minute, "retry"),                     // beyan: süreden/benzerlikten bağımsız
		at("c1", "C", "orders-api mainframe cevap süreleri", 30*time.Second, ""), // başka konuşma — A ile eşleşmez
	}
	got := routingReaskFlags(turns)
	want := map[string]bool{"a1": true, "b1": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("yeniden sorma %v, want %v", got, want)
	}
}

func TestBuildRoutingReport(t *testing.T) {
	noEv := func(r chstore.RoutingTurn) chstore.RoutingTurn { r.NoEvidence = true; return r }
	older := rrTurn("old", "rag", "", "orders-api mainframe cevap sürelerimde artış var mı?")
	older.CreatedAt = rrT0.Add(-time.Hour)
	turns := []chstore.RoutingTurn{
		noEv(rrTurn("rag1", "rag", "", "orders-api mainframe cevap sürelerimde artış var mı?")), // 3+2
		noEv(older), // 3+2, daha eski
		noEv(rrTurn("loop1", aisurface.TierLoop, "", "grafikleri ortalamayı göster")),         // 3
		noEv(rrTurn("weak", aisurface.TierGuided, string(guidedServiceHealth), "orders-api")), // 1 → eşik altı
		rrTurn("ok1", aisurface.TierGuided, string(guidedServiceHealth), "orders-api sağlığı nasıl?"),
		rrTurn("ok2", aisurface.TierGuided, string(guidedServiceHealth), "billing-api sağlığı nasıl?"),
	}
	rep := buildRoutingReport(turns, nil)
	var ids []string
	for _, it := range rep.Items {
		ids = append(ids, fmt.Sprintf("%s:%d", it.ExchangeID, it.Score))
	}
	if got := strings.Join(ids, " "); got != "rag1:5 old:5 loop1:3" {
		t.Errorf("sıra/puan %q", got)
	}
	if rep.Turns != 6 || rep.Suspicious != 3 {
		t.Errorf("sayımlar turns=%d suspicious=%d", rep.Turns, rep.Suspicious)
	}
	if len(rep.Tiers) == 0 || rep.Tiers[0].Tier != "rag" || rep.Tiers[0].Suspicious != 2 || rep.Tiers[0].Turns != 2 {
		t.Errorf("kademe özeti: %+v", rep.Tiers)
	}
	for _, tr := range rep.Tiers {
		if tr.Tier == aisurface.TierGuided && (tr.Route != string(guidedServiceHealth) || tr.Turns != 3 || tr.Suspicious != 0) {
			t.Errorf("guided/service_health özeti: %+v", tr)
		}
	}
	it := rep.Items[2]
	if it.Tier != aisurface.TierLoop || it.ConversationID != "c-loop1" || !reflect.DeepEqual(it.Reasons, []string{reasonLoopNoEvidence}) {
		t.Errorf("satır alanları: %+v", it)
	}
	// Tavan.
	var many []chstore.RoutingTurn
	for i := 0; i < routingReportItemLimit+7; i++ {
		r := rrTurn(fmt.Sprintf("m%03d", i), aisurface.TierLoop, "", "orders-api hata veriyor mu")
		r.Status = "error"
		many = append(many, r)
	}
	if rep := buildRoutingReport(many, nil); len(rep.Items) != routingReportItemLimit || rep.Suspicious != len(many) {
		t.Errorf("tavan: items=%d suspicious=%d", len(rep.Items), rep.Suspicious)
	}
	if rep := buildRoutingReport(nil, nil); rep.Items == nil || rep.Tiers == nil {
		t.Error("boş rapor null değil [] dönmeli (FE .map())")
	}
}

// TestRoutingReportVotesNeverScore — operatör kuralı (2026-10-17): 👍/👎
// hiçbir kararı sürmez. Oylar yalnız Downvote kolonunu doldurur: aynı turlar
// oylu/oysuz aynı listeyi, aynı puanı, aynı sırayı verir; yalnız 👎 alan
// temiz tur listeye GİRMEZ.
func TestRoutingReportVotesNeverScore(t *testing.T) {
	noEv := func(r chstore.RoutingTurn) chstore.RoutingTurn { r.NoEvidence = true; return r }
	turns := []chstore.RoutingTurn{
		noEv(rrTurn("s1", "rag", "", "orders-api mainframe cevap sürelerimde artış var mı?")),
		noEv(rrTurn("s2", aisurface.TierLoop, "", "grafikleri göster")),
		rrTurn("clean", aisurface.TierGuided, string(guidedServiceHealth), "orders-api sağlığı nasıl?"),
	}
	plain := buildRoutingReport(turns, nil)
	voted := buildRoutingReport(turns, map[string]int8{"s2": -1, "clean": -1, "s1": 1})
	if len(plain.Items) != len(voted.Items) || plain.Suspicious != voted.Suspicious || !reflect.DeepEqual(plain.Tiers, voted.Tiers) {
		t.Fatalf("oy listeyi değiştirdi: %+v vs %+v", plain, voted)
	}
	for i := range plain.Items {
		a, b := plain.Items[i], voted.Items[i]
		if a.ExchangeID != b.ExchangeID || a.Score != b.Score || !reflect.DeepEqual(a.Reasons, b.Reasons) {
			t.Errorf("oy puanı/sırayı değiştirdi: %+v vs %+v", a, b)
		}
		if b.Downvote != (b.ExchangeID == "s2") {
			t.Errorf("Downvote kolonu: %+v", b)
		}
	}
	for r := range routingReasonWeight {
		if strings.Contains(r, "vote") || strings.Contains(r, "thumb") {
			t.Errorf("oy bir puan nedeni olamaz: %q", r)
		}
	}
}

func TestRoutingReportDaysAndGate(t *testing.T) {
	for in, want := range map[string]int{"": 7, "1": 1, "7": 7, "30": 30, "365": 7, "x": 7} {
		if got := routingReportDays(in); got != want {
			t.Errorf("days(%q)=%d want %d", in, got, want)
		}
	}
	// v0.10.1484 — rol kapısı: viewer 403, editor 200, admin 200. Gövde
	// önbellekten (store'suz sunucu; anahtar handler'ınkiyle aynı).
	s := &Server{cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
	s.l1.set("ai:routing-report:v1:d=7", []byte(`{"days":7,"rows":[]}`), time.Minute)
	mux := http.NewServeMux()
	s.registerAIRoutingReportRoutes(mux)
	for role, want := range map[string]int{auth.RoleViewer: http.StatusForbidden, auth.RoleEditor: http.StatusOK, auth.RoleAdmin: http.StatusOK} {
		r := httptest.NewRequest(http.MethodGet, "/api/ai/routing-report?days=7", nil)
		r = r.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-" + role, Role: role}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("%s: kod %d, beklenen %d (%s)", role, w.Code, want, w.Body.String())
		}
	}
}

// v0.10.2134 (router boşlukları D1) — açıklama turları istemci şablonudur
// ("Bu trace'i açıkla · <id>"): iki farklı trace art arda açıklanınca kelime
// benzerliği eşiği geçer ve tur "hemen yeniden soruldu" sayılıyordu (/ai
// "Açıklama turu 189/454"). Farklı özne muaf; aynı metin ve istemci beyanı
// (retry) sayılmaya devam eder; açıklama olmayan kademe etkilenmez.
func TestRoutingReaskExplainTemplateExempt(t *testing.T) {
	q1 := "Bu trace'i açıkla · 5b8efff7a1c2d3e4"
	q2 := "Bu trace'i açıkla · 9c0d11aa22bb33cc"
	if !routingSimilarForTest(q1, q2) {
		t.Fatal("ön koşul: şablon soruları benzerlik eşiğini geçmeli (muafiyet olmadan reask sayılırdı)")
	}
	at := func(id, conv, tier, q string, d time.Duration, attempt string) chstore.RoutingTurn {
		r := rrTurn(id, tier, "", q)
		r.ConversationID, r.CreatedAt, r.Attempt = conv, rrT0.Add(d), attempt
		return r
	}
	turns := []chstore.RoutingTurn{
		at("e1", "E", aisurface.TierExplain, q1, 0, ""),
		at("e2", "E", aisurface.TierExplain, q2, time.Minute, ""),   // farklı trace → muaf
		at("e3", "E", aisurface.TierExplain, q2, 2*time.Minute, ""), // aynı metin → e2 yeniden soruldu
		at("f1", "F", aisurface.TierExplain, q1, 0, ""),
		at("f2", "F", aisurface.TierExplain, q2, time.Minute, "retry"), // beyan → f1 sayılır
		at("g1", "G", aisurface.TierGuided, q1, 0, ""),
		at("g2", "G", aisurface.TierGuided, q2, time.Minute, ""), // açıklama değil → eski kural
	}
	got := routingReaskFlags(turns)
	want := map[string]bool{"e2": true, "f1": true, "g1": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("yeniden sorma %v, want %v", got, want)
	}
}

func routingSimilarForTest(a, b string) bool {
	return cosrelearn.QuestionSimilarity(a, b) >= cosrelearn.RephraseSimilarity
}
