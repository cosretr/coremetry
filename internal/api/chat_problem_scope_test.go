package api

// v0.10.2480 (C/E) — Problem kapsamı: pencere ve ilk araç ipucu.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

func TestProblemChatWindow(t *testing.T) {
	now := time.Date(2026, 11, 26, 21, 7, 0, 0, time.UTC)
	start := time.Date(2026, 11, 26, 12, 50, 0, 0, time.UTC)
	// Açık Problem: başlangıç − 15 dk → şimdi; toMs 0 (şimdiye çapalı).
	rs, to, ok := problemChatWindow(start.UnixNano(), 0, now)
	if !ok || to != 0 || rs != int64(now.Sub(start.Add(-problemWindowMargin))/time.Second) {
		t.Fatalf("açık: %d %d %v", rs, to, ok)
	}
	// Kapanmış: → bitiş.
	end := time.Date(2026, 11, 26, 14, 10, 0, 0, time.UTC)
	rs, to, ok = problemChatWindow(start.UnixNano(), end.UnixNano(), now)
	if !ok || to != end.UnixMilli() || rs != int64(end.Sub(start.Add(-problemWindowMargin))/time.Second) {
		t.Fatalf("kapalı: %d %d", rs, to)
	}
	if _, _, ok := problemChatWindow(0, 0, now); ok {
		t.Fatal("başlangıçsız pencere")
	}
	// Çok eski Problem 30 günle sınırlı.
	if rs, _, _ := problemChatWindow(now.Add(-90*24*time.Hour).UnixNano(), 0, now); rs != problemWindowMaxS {
		t.Fatalf("tavan: %d", rs)
	}
}

func TestProblemWindowLabels(t *testing.T) {
	now := time.Date(2026, 11, 26, 21, 7, 0, 0, time.UTC)
	start := time.Date(2026, 11, 26, 12, 50, 0, 0, time.UTC)
	if got := problemWindowLabelTR(start.UnixNano(), 0, now, time.UTC); got != "Problem penceresi: 12:35 → şimdi" {
		t.Fatal(got)
	}
	end := time.Date(2026, 11, 26, 14, 10, 0, 0, time.UTC)
	if got := problemWindowLabelTR(start.UnixNano(), end.UnixNano(), now, time.UTC); got != "Problem penceresi: 12:35 → 14:10" {
		t.Fatal(got)
	}
	if got := problemWindowLabelTR(start.Add(-24*time.Hour).UnixNano(), 0, now, time.UTC); got != "Problem penceresi: 25.11 12:35 → şimdi" {
		t.Fatal(got)
	}
	// Geçmişte biten pencere ASLA "son N dakika" değil (operatör: 19:37'de biten 15 dk, 21:07'de).
	to := time.Date(2026, 11, 26, 19, 37, 0, 0, time.UTC)
	if got := pastWindowLabelTR(900, to.UnixMilli(), now, time.UTC); got != "19:22 → 19:37" {
		t.Fatal(got)
	}
	if got := pastWindowLabelTR(900, now.Add(-time.Minute).UnixMilli(), now, time.UTC); got != "son 15 dakika" {
		t.Fatal(got)
	}
	if got := pastWindowLabelTR(3600, 0, now, time.UTC); got != "son 1 saat" {
		t.Fatal(got)
	}
	// Durum etiketi: geçmişte biten pencerenin "son N" etiketi düzeltilir; özel etiket kalır.
	past := ChatContext{RangeS: 900, WinToMs: time.Now().Add(-90 * time.Minute).UnixMilli(), WinLabel: "son 15 dakika"}
	if got := stateWindowLabel(past); strings.HasPrefix(got, "son ") || !strings.Contains(got, "→") {
		t.Fatalf("durum etiketi: %q", got)
	}
	if got := stateWindowLabel(ChatContext{RangeS: 900, WinToMs: past.WinToMs, WinLabel: "dün aynı saatte"}); got != "dün aynı saatte" {
		t.Fatal(got)
	}
}

func TestApplyProblemWindow(t *testing.T) {
	now := time.Now()
	ps := &chatProblemScope{ID: "p", StartedAt: now.Add(-2 * time.Hour).UnixNano()}
	var req chatRequest
	req.Context.RangeS, req.Context.ToMs = 900, now.Add(-90*time.Minute).UnixMilli() // sayfada kalmış eski aralık
	if applyProblemWindow(&req, nil, ps, now) != "" || req.Context.RangeS != 900 {
		t.Fatal("çip kapalıyken (problemWindow yok) dokunulmamalı")
	}
	req.Context.ProblemWindow = true
	label := applyProblemWindow(&req, nil, ps, now)
	if !strings.HasPrefix(label, "Problem penceresi: ") || req.Context.ToMs != 0 || req.Context.RangeS != int64((2*time.Hour+problemWindowMargin)/time.Second) {
		t.Fatalf("uygulanmadı: %q %+v", label, req.Context)
	}
	// Kullanıcının bu turdaki açık penceresi kazanır.
	stt := &chatStateTurn{st: &chatContextState{}, res: stateResolveOut{WindowSet: true}}
	var req2 chatRequest
	req2.Context.ProblemWindow, req2.Context.RangeS = true, 3600
	if applyProblemWindow(&req2, stt, ps, now) != "" || req2.Context.RangeS != 3600 {
		t.Fatal("kullanıcı penceresi ezildi")
	}
	// Durum açık pencere taşımıyorsa Problem penceresini öğrenir (Konuşma bağlamı satırı).
	stt2 := &chatStateTurn{st: &chatContextState{ctx: ChatContext{RangeS: 900, WinLabel: "son 15 dakika"}}}
	var req3 chatRequest
	req3.Context.ProblemWindow = true
	applyProblemWindow(&req3, stt2, ps, now)
	if !strings.HasPrefix(stt2.st.ctx.WinLabel, "Problem penceresi") || !stt2.st.dirty {
		t.Fatalf("durum: %+v", stt2.st.ctx)
	}
}

func TestProblemKindAndHint(t *testing.T) {
	cases := []struct {
		metric, rule, desc string
		want               problemKind
	}{
		{"ml_group_lag", "anomaly-auto:group:x", "", problemKindLag},
		{"ml_group_lag_pattern", "anomaly-auto:group:x", "", problemKindLag},
		{"error_rate", "anomaly:abc", "", problemKindError},
		{"p99_ms", "anomaly:abc", "", problemKindLatency},
		{"latency", "svc-slowdown:x", "", problemKindLatency},
		{"ml_pod_oom", "anomaly-auto:svc:x", "", problemKindOOM},
		{"ml_group_signals", "anomaly-auto:group:x", "wl · pod bellek sınırında sonlandırıldı (OOMKilled): 2 pod", problemKindOOM},
		{"request_rate", "anomaly:abc", "", problemKindNone},
	}
	for _, c := range cases {
		if got := problemKindOf(c.metric, c.rule, c.desc); got != c.want {
			t.Errorf("%+v: %q", c, got)
		}
	}
	specs := []copilot.ToolSpec{{Name: "messaging_flow"}, {Name: "list_exception_groups"}, {Name: "get_exception_samples"}, {Name: "get_pod_health"}}
	h := problemFirstToolsHintTR(problemKindLag, "svc-a", specs)
	if !strings.Contains(h, "tüketici gecikmesi — önce messaging_flow (service=svc-a)") {
		t.Fatalf("lag ipucu: %q", h)
	}
	if h := problemFirstToolsHintTR(problemKindError, "", specs); !strings.Contains(h, "önce list_exception_groups, sonra get_exception_samples çağır") {
		t.Fatalf("hata ipucu: %q", h)
	}
	if problemFirstToolsHintTR(problemKindLatency, "x", specs) != "" {
		t.Fatal("katalogda olmayan araç önerilmemeli")
	}
	if problemFirstToolsHintTR(problemKindNone, "x", specs) != "" {
		t.Fatal("tür yokken ipucu yok")
	}
	var nilScope *chatProblemScope
	if nilScope.promptLine(specs) != "" {
		t.Fatal("kapsam yokken satır yok")
	}
	rs := int64(1)
	ps := problemScopeOf(chstore.Problem{ID: "p", Status: "resolved", ResolvedAt: &rs, StartedAt: 5}, "P-1")
	if ps.ResolvedAt != 1 || ps.Ref != "P-1" {
		t.Fatalf("%+v", ps)
	}
}

// Kaynak pini (C/G/H): üç ek döngü isteminde sohbet çekirdeğinin ÖNÜNDE
// (DataNotInstruction sonda kalır), alan adı kuralı tek kısa satır.
func TestLoopPromptCarriesProblemHintPlanAndFieldRule(t *testing.T) {
	b, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "probScope.promptLine(specs) +")
	j := strings.Index(src, "planBlockTR(loopCx.Subs) +")
	k := strings.Index(src, "chatFieldNameRuleTR +")
	core := strings.Index(src, "withAddressee(addressee, loopChatPromptFor(ctx)) +")
	if i < 0 || j < i || k < j || core < k {
		t.Fatalf("sıra: %d %d %d %d", i, j, k, core)
	}
	if strings.Count(chatFieldNameRuleTR, "\n") > 2 || len(chatFieldNameRuleTR) > 200 {
		t.Fatalf("kural kısa kalmalı: %q", chatFieldNameRuleTR)
	}
}
