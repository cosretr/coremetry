package api

// v0.10.2136 (CoSRE router boşlukları D3, docs/cosre/router-gaps-audit.md E)
// — prod (7 gün) yavaşlama "neden" soruları 10 kez boşluğa düştü: "uzun
// süren", "uzuyor", "süre artışı", "hafta" hiçbir sinyal listesinde yoktu.
// Pinler: (1) tek liste — tutması ve TUTMAMASI gereken cümleler; (2) v1'de
// router girdisi bayt bayt aynı; (3) v2 rotaları; (4) karar 5 neden > rapor;
// (5) karar 3 hafta = 7 gün yalnız servis sağlığı / kök neden, kırılım 24 sa.

import (
	"os"
	"strings"
	"testing"
)

var lwServices = []string{"svc-a", "svc-b"}

func lwRoute(q, ctxSvc string, v2 bool) guidedRoute {
	r := routeGuidedIntentExt(v2RouterQuestion(q, v2), lwServices, nil, nil, ctxSvc, false, nil)
	return v2WhyOverReport(r, normalizeGuidedMsg(q), v2)
}

func TestHasLatencyGrowthSignal(t *testing.T) {
	for _, c := range []struct {
		q    string
		want bool
	}{
		{"svc-a altında uzun süren işlemleri listele", true},
		{"svc-a nerede uzuyor?", true},
		{"svc-a cevap süresinde uzama var mı", true},
		{"istek süresi uzadı", true},
		{"yanıt süreleri arttı mı", true},
		{"sürelerinde artış var", true},
		{"svc-a gecikiyor mu", true},
		{"neden uzun sürüyor", true},
		{"dün uzun sürdü", true},
		// tutmaması gerekenler
		{"uzak sunucuya bağlanamıyor", false},
		{"oturum süresi nedir", false},
		{"uzun bir liste ver", false},
		{"trafik artışı var mı", false},
		{"uzun sürüm notlarını göster", false},
		{"svc-a sağlığı nasıl", false},
		{"süren işlemler", false},
	} {
		if got := hasLatencyGrowthSignal(normalizeGuidedMsg(c.q)); got != c.want {
			t.Errorf("%q → %v, want %v", c.q, got, c.want)
		}
	}
}

func TestV2RouterQuestion(t *testing.T) {
	for _, q := range []string{"svc-a altında uzun süren işlemleri listele", "svc-a cevap süreleri arttı mı", "svc-a sağlığı nasıl"} {
		if got := v2RouterQuestion(q, false); got != q {
			t.Errorf("v1 metni değiştirmemeli: %q → %q", q, got)
		}
	}
	for _, c := range []struct{ q, want string }{
		{"svc-a altında uzun süren işlemleri listele", "svc-a altında uzun süren işlemleri listele en uzun yavaşlama"},
		{"svc-a cevap süreleri arttı mı", "svc-a cevap süreleri arttı mı yavaşlama"},
		{"svc-a sağlığı nasıl", "svc-a sağlığı nasıl"},
	} {
		if got := v2RouterQuestion(c.q, true); got != c.want {
			t.Errorf("v2 %q → %q, want %q", c.q, got, c.want)
		}
	}
}

func TestV2LatencyRoutes(t *testing.T) {
	for _, c := range []struct {
		q, ctxSvc  string
		v2         bool
		want, wSvc string
	}{
		{"svc-a altında uzun süren işlemleri listele", "", true, string(guidedSlowTraces), "svc-a"},
		{"uzun süren işlemler hangileri", "", true, string(guidedSlowTraces), ""},
		{"svc-a cevap süreleri arttı mı", "", true, string(guidedServiceHealth), "svc-a"},
		{"svc-a gecikiyor mu", "", true, string(guidedServiceHealth), "svc-a"},
		{"svc-a neden uzuyor?", "", true, string(guidedRootCause), "svc-a"},
		// karar 5: neden > rapor (yalnız v2)
		{"svc-a nedenini inceler misin", "", true, string(guidedRootCause), "svc-a"},
		{"nedenini inceler misin", "svc-a", true, string(guidedRootCause), "svc-a"},
		{"svc-a servisini analiz et, rapor çıkar", "", true, string(guidedServiceReport), "svc-a"}, // neden yok → rapor
		// v1 aynen
		{"svc-a nedenini inceler misin", "", false, string(guidedServiceReport), "svc-a"},
		{"svc-a altında uzun süren işlemleri listele", "", false, "", ""},
		{"svc-a cevap süreleri arttı mı", "", false, "", ""},
	} {
		r := lwRoute(c.q, c.ctxSvc, c.v2)
		if string(r.Intent) != c.want || r.Service != c.wSvc {
			t.Errorf("%q ctx=%q v2=%v → %q/%q, want %q/%q", c.q, c.ctxSvc, c.v2, r.Intent, r.Service, c.want, c.wSvc)
		}
	}
	// Servissiz netleştirmede sorulacak niyet de kök neden olur.
	ask := v2WhyOverReport(guidedRoute{Intent: guidedAskService, AskIntent: guidedServiceReport}, normalizeGuidedMsg("nedenini incele"), true)
	if ask.AskIntent != guidedRootCause {
		t.Errorf("ask_service niyeti %q", ask.AskIntent)
	}
}

func TestV2WeekRange(t *testing.T) {
	rc, sh, st := guidedRoute{Intent: guidedRootCause}, guidedRoute{Intent: guidedServiceHealth}, guidedRoute{Intent: guidedSlowTraces}
	for _, c := range []struct {
		r        guidedRoute
		q        string
		explicit bool
		v2       bool
		want     int64
	}{
		{rc, "svc-a son 1 haftada neden yavaşladı", false, true, 604800},
		{sh, "svc-a son bir hafta nasıl", false, true, 604800},
		{sh, "svc-a 2 haftadır nasıl", false, true, 604800}, // tavan 7 gün
		{sh, "svc-a this week health", false, true, 604800},
		// tutmaması gerekenler
		{sh, "svc-a son bir hafta nasıl", false, false, 0},        // v1
		{sh, "svc-a son 3 gün nasıl hafta", true, true, 0},        // açık pencere kazanır
		{st, "svc-a bu hafta en yavaş trace'ler", false, true, 0}, // kırılım/liste 24 sa
		{rc, "svc-a geçen hafta neden yavaştı", false, true, 0},   // belirli hafta, geriye bakış değil
		{sh, "svc-a nasıl", false, true, 0},
	} {
		got, ok := v2WeekRange(c.r, normalizeGuidedMsg(c.q), c.explicit, c.v2)
		if got != c.want || ok != (c.want > 0) {
			t.Errorf("%s %q → %d/%v, want %d", c.r.Intent, c.q, got, ok, c.want)
		}
	}
	if guidedWeekRangeS != 604800 || fmtAgoTR(guidedWeekRangeS) != "7gün" {
		t.Errorf("hafta = %d (%s)", guidedWeekRangeS, fmtAgoTR(guidedWeekRangeS))
	}
}

func TestDrillWindowS(t *testing.T) {
	for _, c := range []struct {
		rangeS int64
		v2     bool
		want   int64
	}{
		{604800, true, 86400}, {604800, false, 604800}, {86400, true, 86400}, {3600, true, 3600}, {172800, true, 86400},
	} {
		if got := drillWindowS(c.rangeS, c.v2); got != c.want {
			t.Errorf("drillWindowS(%d,%v) = %d, want %d", c.rangeS, c.v2, got, c.want)
		}
	}
}

// Bağlar: sinyal kapısı, router girdisi, hafta penceresi, kök-neden gecikme
// sorusu ve kırılım penceresi copilot_guided.go'da kablolu.
func TestV2LatencyWiring(t *testing.T) {
	b, err := os.ReadFile("copilot_guided.go")
	if err != nil {
		t.Fatal(err)
	}
	src := stripGoCommentsAPI(string(b))
	for _, want := range []string{
		"!(v2 && hasLatencyGrowthSignal(norm))",
		"routerQ := v2RouterQuestion(pastedQ, v2)", // v0.10.2139 — pastedQ v1'de question (chat_bare_entity.go)
		"route = v2WhyOverReport(route, norm, v2)",
		"if wr, ok := v2WeekRange(route, norm, explicitRange, v2); ok {",
		"hasLatencyAsk(qn) || (cosreV2Marked(ctx) && hasLatencyGrowthSignal(qn))",
		"drillS := drillWindowS(rangeS, cosreV2Marked(ctx))",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("copilot_guided.go bağı eksik: %q", want)
		}
	}
	// Hafta penceresi ekran aralığı devralmasından ÖNCE (soru ekranı ezer).
	if w, s := strings.Index(src, "v2WeekRange(route, norm"), strings.Index(src, "if !explicitRange && ctxRangeS > 0 {"); w < 0 || s < 0 || w > s {
		t.Error("hafta penceresi ekran devralmasından önce olmalı")
	}
}
