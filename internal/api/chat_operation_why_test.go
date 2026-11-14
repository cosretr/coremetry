package api

// chat_operation_why_test.go — v0.10.2137 (CoSRE router boşlukları D4,
// docs/cosre/router-gaps-audit.md E). Prod: operasyon/endpoint ön-getirmesi
// tutan "neden / uzuyor / en uzun süren kısımlar" soruları serbest döngüye
// tohumlanıp boşluğa düşüyordu; v2'de tek anlatım. v1 aynen.

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestOpWhyNarrationAsk(t *testing.T) {
	cases := []struct {
		q    string
		want bool
	}{
		{"POST /app/x/y son 1 haftadaki süre artışı neden olabilir", true},
		{"/app/x/y içindeki en uzun süren kısımları listele", true},
		{"OP_NAME_REST servisi nerede uzuyor?", true},
		{"OP_NAME_REST neden yavaş", true},
		{"OP_NAME_REST gecikiyor mu", true},
		{"OP_NAME_REST p99 nedir", true},
		// tutmaması gerekenler: düz bakış / liste / sayı
		{"OP_NAME_REST operasyonuna bak", false},
		{"POST /app/x/y trace'lerini getir", false},
		{"OP_NAME_REST çağrı sayısı", false},
		{"OP_NAME_REST hata oranı", false},
	}
	for _, c := range cases {
		if got := opWhyNarrationAsk(c.q); got != c.want {
			t.Errorf("opWhyNarrationAsk(%q) = %v, want %v", c.q, got, c.want)
		}
	}
}

func TestOpWhyTierWanted(t *testing.T) {
	q := "OP_NAME_REST neden uzuyor"
	p := &opPrefetch{Operation: "OP_NAME_REST"}
	cases := []struct {
		name    string
		v2      bool
		p       *opPrefetch
		planned bool
		q       string
		want    bool
	}{
		{"v2 neden → kademe", true, p, false, q, true},
		{"v1 → döngü (bayt bayt eski)", false, p, false, q, false},
		{"ön-getirme yok", true, nil, false, q, false},
		{"rota incelemesi (rtPlan) kendi deneyiminde", true, p, true, q, false},
		{"hata-neden Exceptions kademesinde", true, &opPrefetch{errorAsk: true}, false, q, false},
		{"neden/gecikme yok → döngü", true, p, false, "OP_NAME_REST operasyonuna bak", false},
	}
	for _, c := range cases {
		if got := opWhyTierWanted(c.v2, c.p, c.planned, c.q); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// Birim karışması dersi (v0.6.36): her birim ayrı satır.
func TestV2OpPrefetchRangeS(t *testing.T) {
	const screen = int64(3600)
	cases := []struct {
		q    string
		v2   bool
		want int64
	}{
		{"POST /app/x/y son 1 haftadaki süre artışı neden olabilir", true, 7 * 86400},
		{"POST /app/x/y son 1 haftadaki süre artışı neden olabilir", false, screen},
		{"OP_NAME_REST son 6 saatte neden uzadı", true, 6 * 3600},
		{"OP_NAME_REST son 45 dakikada neden yavaş", true, 45 * 60},
		{"OP_NAME_REST son 1 günde neden yavaş", true, 86400},
		{"OP_NAME_REST son 3 günde neden yavaş", true, 86400}, // guided tavanı 24 sa
		{"OP_NAME_REST geçen hafta neden yavaştı", true, screen},
		{"OP_NAME_REST neden yavaş", true, screen},
	}
	for _, c := range cases {
		if got := v2OpPrefetchRangeS(c.q, screen, c.v2); got != c.want {
			t.Errorf("v2OpPrefetchRangeS(%q, v2=%v) = %d, want %d", c.q, c.v2, got, c.want)
		}
	}
}

func TestOpTraceFrom(t *testing.T) {
	to := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	week := to.Add(-7 * 24 * time.Hour)
	hour := to.Add(-time.Hour)
	if got := opTraceFrom(week, to, 0); !got.Equal(week) {
		t.Errorf("traceMaxS=0 → from aynen, got %v", got)
	}
	if got := opTraceFrom(week, to, guidedDrillMaxRangeS); !got.Equal(to.Add(-24 * time.Hour)) {
		t.Errorf("7 gün → son 24 sa, got %v", got)
	}
	if got := opTraceFrom(hour, to, guidedDrillMaxRangeS); !got.Equal(hour) {
		t.Errorf("1 sa pencere daraltılmaz, got %v", got)
	}
}

func TestOpPrefetchSummaryTraceWindowNote(t *testing.T) {
	to := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	p := &opPrefetch{Operation: "OP_NAME_REST", MatchKind: "exact", RangeS: 7 * 86400, From: to.Add(-7 * 24 * time.Hour), To: to}
	if strings.Contains(p.summary(), "trace örnekleri ve kırılım yalnız") {
		t.Fatal("traceFrom boşken not basılmamalı (v1 özeti bayt bayt)")
	}
	p.traceFrom = to.Add(-24 * time.Hour)
	if s := p.summary(); !strings.Contains(s, "trace örnekleri ve kırılım yalnız son") {
		t.Fatalf("daraltılmış trace penceresi notu yok:\n%s", s)
	}
}

func TestOpWhyEvidenceAndLabel(t *testing.T) {
	to := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	p := &opPrefetch{Operation: "OP_NAME_REST", MatchKind: "exact", Services: []string{"svc-a"}, RangeS: 3600, From: to.Add(-time.Hour), To: to,
		Drill: "KIRILIM-METNI\n"}
	ev := opWhyEvidence(p, "DEPLOY-METNI\n")
	for _, want := range []string{opWhyEvidenceHeader, "Operasyon OP_NAME_REST otomatik getirildi", "KIRILIM-METNI", "DEPLOY-METNI"} {
		if !strings.Contains(ev, want) {
			t.Errorf("kanıtta %q yok", want)
		}
	}
	if got := guidedRouteLabel(guidedRoute{Intent: guidedRootCause, opWhy: p}); got != guidedOperationRootCauseRoute {
		t.Errorf("etiket %q", got)
	}
	if got := guidedRouteLabel(guidedRoute{Intent: guidedRootCause, Service: "svc-a"}); got != string(guidedRootCause) {
		t.Errorf("servis kök nedeni etiketi değişmemeli: %q", got)
	}
	if src := opWhySources(p, "deploy geçmişi"); !strings.Contains(src, "en yavaş trace kırılımı") || !strings.Contains(src, "deploy geçmişi") {
		t.Errorf("künye: %q", src)
	}
}

// Kademe yeri: ön-getirmeden SONRA, rota planından SONRA (rtPlan okunur),
// guided'dan ÖNCE; v2 kapısı çağrının kendisinde.
func TestOpWhyTierPlacement(t *testing.T) {
	b, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	src := stripGoCommentsAPI(string(b))
	pre := strings.Index(src, "s.chatOperationPrefetch(ctx, emit,")
	plan := strings.Index(src, "s.planRouteTurn(ctx,")
	why := strings.Index(src, "s.chatOperationWhyTier(ctx, emit,")
	guided := strings.Index(src, "s.copilotChatGuided(ctx, emit,")
	if pre < 0 || plan < 0 || why < 0 || guided < 0 {
		t.Fatalf("işaretler: pre=%d plan=%d why=%d guided=%d", pre, plan, why, guided)
	}
	if !(pre < plan && plan < why && why < guided) {
		t.Fatalf("sıra bozuk: pre=%d plan=%d why=%d guided=%d", pre, plan, why, guided)
	}
	if !strings.Contains(src, "opWhyTierWanted(cosreV2Marked(ctx), opPre, rtPlan != nil,") {
		t.Fatal("kademe kapısı v2 + rtPlan okumalı")
	}
	if !strings.Contains(src, "v2OpPrefetchRangeS(lastUserText(req.Messages), int64(req.Context.RangeS), cosreV2Marked(ctx))") {
		t.Fatal("ön-getirme penceresi v2 kapılı olmalı")
	}
}
