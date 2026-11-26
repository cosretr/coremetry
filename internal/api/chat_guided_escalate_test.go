package api

import (
	"context"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// chat_guided_escalate_test.go — v0.10.2473: guided → döngü devri (SAF karar,
// tohum çifti, yalnız yuvalı ctx'te).

func TestGuidedEscalatesToLoopTable(t *testing.T) {
	cases := []struct {
		intent guidedIntent
		q      string
		want   bool
	}{
		// birebir cevap veren rotalar anlatımda kalır
		{guidedTopErrorServices, "en çok hata veren 5 servis", false},
		{guidedServiceHealth, "checkout sağlığı nasıl?", false},
		{guidedMetricChart, "checkout p95 trendi son 6 saat grafik çiz", false},
		{guidedServiceHealth, "checkout servisi nasıl", false},
		// çok parçalı soru: demet ikinci parçayı kapsamaz → döngü
		{guidedServiceHealth, "checkout nasıl ve hangi endpoint en yavaş", true},
		{guidedTopErrorServices, "en çok hata veren servisler hangileri ve hangisi en yavaş", true},
		{guidedFanout, "checkout hangi servisleri çağırıyor ve hangisi en yavaş", true},
		{guidedMetricChart, "checkout p95 grafiğini çiz and which endpoint is slowest", true},
		// kıyas: tek servis demeti kıyası veremez; kıyas demeti olan rota veremez değil
		{guidedServiceHealth, "checkout ile payments'ı karşılaştır", true},
		{guidedServiceHealth, "checkout vs payments", true},
		{guidedWindowCompare, "checkout dün ile bugünü karşılaştır", false},
		{guidedServiceSet, "harvester-* servislerini karşılaştır", false},
		{guidedTeamServices, "takımımın servislerini kıyasla", false},
		// netleştirme / yönlendirme rotaları hiç tırmanmaz
		{guidedAskService, "checkout ve hangisi", false},
		{guidedHowTo, "nasıl karşılaştırırım", false},
		{guidedNone, "checkout ile payments'ı karşılaştır", false},
	}
	for _, c := range cases {
		if got := guidedEscalatesToLoop(c.intent, chatLoopCue(c.q)); got != c.want {
			t.Errorf("%s %q (işaret %q) = %v, beklenen %v", c.intent, c.q, chatLoopCue(c.q), got, c.want)
		}
	}
}

func TestGuidedEscalationSeed(t *testing.T) {
	var nilEsc *guidedEscalation
	msgs := []copilot.ChatMessage{{Role: "user", Text: "checkout nasıl ve hangi endpoint en yavaş"}}
	if got := nilEsc.seedScaled(msgs, 1); len(got) != 1 || nilEsc.seededTools() != nil || nilEsc.summary() != "" {
		t.Fatalf("nil devir mesajları değiştirmemeli: %d", len(got))
	}
	g := newGuidedEscalation(guidedRoute{Intent: guidedServiceHealth, Service: "checkout"}, loopCueMulti, 3600, "checkout RED: 120 rpm, hata %0.4")
	out := g.seedScaled(msgs, 1)
	if len(out) != 3 || len(msgs) != 1 {
		t.Fatalf("tohum çifti eklenmeli, girdi değişmemeli: %d / %d", len(out), len(msgs))
	}
	call, res := out[1], out[2]
	if call.Role != "assistant" || len(call.ToolCalls) != 1 || call.ToolCalls[0].Name != guidedEscTool || call.ToolCalls[0].ID != guidedEscToolID {
		t.Fatalf("çağrı: %+v", call)
	}
	if a := string(call.ToolCalls[0].Input); !strings.Contains(a, `"route":"service_health"`) || !strings.Contains(a, `"service":"checkout"`) || !strings.Contains(a, `"rangeS":3600`) {
		t.Fatalf("argümanlar: %s", a)
	}
	if res.Role != "user" || len(res.ToolResults) != 1 || res.ToolResults[0].CallID != guidedEscToolID || !strings.Contains(res.ToolResults[0].Content, "120 rpm") {
		t.Fatalf("sonuç: %+v", res)
	}
	if !chatToolNameSet(map[string]bool{})[guidedEscTool] {
		t.Fatal("sentetik ad yankı kümesinde olmalı")
	}
}

func TestTryGuidedEscalateNeedsSlot(t *testing.T) {
	route := guidedRoute{Intent: guidedServiceHealth, Service: "checkout"}
	q := "checkout ile payments'ı karşılaştır"
	emit := func(string, any) {}
	// yuvasız ctx (niyet / kapsam kademeleri) → bugünkü anlatım
	if tryGuidedEscalate(context.Background(), emit, route, q, 3600, "ev") {
		t.Fatal("yuvasız ctx tırmanmamalı")
	}
	ctx, sl := withGuidedEscSlot(context.Background())
	if tryGuidedEscalate(ctx, emit, route, "checkout nasıl", 3600, "ev") || sl.get() != nil {
		t.Fatal("işaretsiz soru tırmanmamalı")
	}
	if !tryGuidedEscalate(ctx, emit, route, q, 3600, "ev") || sl.get() == nil || sl.get().Cue != loopCueCompare {
		t.Fatalf("kıyas sorusu tırmanmalı: %+v", sl.get())
	}
}
