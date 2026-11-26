package api

import "testing"

// TestChatLoopCue — v0.10.2472: ölçülen yanlış rotaların soruları + değişmemesi
// gereken sorular (işaret yok).
func TestChatLoopCue(t *testing.T) {
	cases := []struct {
		q    string
		want loopCueKind
	}{
		{"api-gateway en yavaş 5 endpoint hangisi", loopCueRank},
		{"en yavaş 5 endpoint hangisi", loopCueRank},
		{"hangi pod en çok memory kullanıyor", loopCueRank},
		{"checkout servisinde en yavaş operasyonlar hangileri", loopCueRank},
		{"top 5 slowest endpoints of payments", loopCueRank},
		{"en çok lag olan kafka consumer hangisi", loopCueRank},
		{"son 1 saatte kaç trace hata aldı", loopCueCount},
		{"checkout son 1 saatte kaç istek aldı", loopCueCount},
		{"how many requests did checkout get in the last hour", loopCueCount},
		{"checkout ile payments'ı karşılaştır", loopCueCompare},
		{"checkout vs payments latency", loopCueCompare},
		{"compare checkout and payments error rate", loopCueCompare},
		{"api-gateway hangi servisleri çağırıyor ve hangisi en yavaş", loopCueMulti},
		{"en yavaş servisler hangileri", loopCueRank},
		{"neden bu kadar çok 500 alıyoruz", loopCueWhyVol},
		{"why are we getting so many 500s", loopCueWhyVol},
		// işaret YOK
		{"son 3 saatte kaç hata oldu?", loopCueNone},
		{"en yavaş trace'leri göster", loopCueNone},
		{"checkout sağlığı nasıl?", loopCueNone},
		{"p95 trendi son 24 saat grafik çiz", loopCueNone},
		{"circuit breaker nedir", loopCueNone},
		{"checkout neden yavaş", loopCueNone},
		{"checkout servisinde en yavaş trace'ler", loopCueNone},
		{"Şu an açık problemler neler? Hangisi checkout servisini etkiliyor?", loopCueNone},
		{"", loopCueNone},
	}
	for _, c := range cases {
		if got := chatLoopCue(c.q); got != c.want {
			t.Errorf("chatLoopCue(%q) = %q, want %q", c.q, got, c.want)
		}
	}
}

// TestLoopCueGuidedRouting — router: ölçülen yanlış rotalar artık guided'a
// oturmaz (boş rota → kademe zinciri döngüye); birebir cevaplayan rotalar aynen.
func TestLoopCueGuidedRouting(t *testing.T) {
	services := []string{"api-gateway", "checkout", "payments"}
	cases := []struct {
		q    string
		want guidedIntent
	}{
		{"en yavaş 5 endpoint hangisi", guidedNone},
		{"son 1 saatte kaç trace hata aldı", guidedNone},
		{"neden bu kadar çok 500 alıyoruz", guidedNone},
		// değişmemesi gerekenler
		{"en çok hata veren 5 servis", guidedTopErrorServices},
		{"checkout p95 trendi son 24 saat grafik çiz", guidedMetricChart},
		{"checkout sağlığı nasıl?", guidedServiceHealth},
		{"son 3 saatte kaç hata oldu?", guidedProblems},
		{"en yavaş trace'leri göster", guidedSlowTraces},
	}
	for _, c := range cases {
		if got := loopCueGuidedRoute(c.q, routeGuidedIntent(c.q, services, nil, nil, "")).Intent; got != c.want {
			t.Errorf("route(%q) = %q, want %q", c.q, got, c.want)
		}
	}
}

// TestLoopCueEndpointPick — kart yalnız açık uçlu endpoint sorusunda; sıralama
// sorusu ve bütün-servis sağlık sorusu kart almaz.
func TestLoopCueEndpointPick(t *testing.T) {
	pc := endpointPickCtx{Service: "api-gateway"}
	cases := []struct {
		q    string
		want bool
	}{
		{"api-gateway en yavaş 5 endpoint hangisi", false},
		{"api-gateway en çok hata veren endpointler", false},
		{"api-gateway sağlığı nasıl?", false},
		{"api-gateway endpointlerinin sağlığı nasıl", true},
	}
	for _, c := range cases {
		if got := needsEndpointPick(pc, c.q); got != c.want {
			t.Errorf("needsEndpointPick(%q) = %v, want %v", c.q, got, c.want)
		}
	}
}

// TestIntentNoneStaysGeneral — on_loop kipi: yalnız saf genel bilgi araçsız.
func TestIntentNoneStaysGeneral(t *testing.T) {
	services := []string{"api-gateway", "checkout", "payments"}
	cases := []struct {
		q    string
		want bool
	}{
		{"circuit breaker nedir", true},
		{"türkiyenin başkenti neresi", true},
		{"merhaba", true},
		{"hangi pod en çok memory kullanıyor", false},
		{"en yavaş 5 endpoint hangisi", false},
		{"checkout bugün nasıl gidiyor", false},
		{"dün gece 02:00 civarı ne oldu", false},
		{"kafka lag en yüksek consumer group", false},
		{"son 30 dakikada ne değişti", false},
	}
	for _, c := range cases {
		if got := intentNoneStaysGeneral(c.q, services, nil); got != c.want {
			t.Errorf("intentNoneStaysGeneral(%q) = %v, want %v", c.q, got, c.want)
		}
	}
}
