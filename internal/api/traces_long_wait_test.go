package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
)

// v0.10.2466 — "Daha uzun bekle": bütçe doğrulaması, bayrak taşıma, cache
// anahtarı, kısmi zarf ve CoSRE takip çipi.

func TestParseTracesWait(t *testing.T) {
	if s, err := parseTracesWait("", 180); err != nil || s != 0 {
		t.Fatalf("boş = varsayılan bütçe: %d %v", s, err)
	}
	if s, err := parseTracesWait("long", 180); err != nil || s != 60 {
		t.Fatalf("long = 60 (tavan yalnız üst sınır): %d %v", s, err)
	}
	if s, _ := parseTracesWait("long", 30); s != 30 {
		t.Fatalf("tavan 30 iken 30: %d", s)
	}
	if _, err := parseTracesWait("forever", 180); err == nil {
		t.Fatal("bilinmeyen wait değeri reddedilmeli")
	}
}

func TestValidateLongWaitCap(t *testing.T) {
	for _, ok := range []int{25, 60, 180} {
		if validateLongWaitCap(ok) != nil {
			t.Errorf("%d geçerli olmalı", ok)
		}
	}
	for _, bad := range []int{0, 24, 181, -1} {
		if validateLongWaitCap(bad) == nil {
			t.Errorf("%d reddedilmeli", bad)
		}
	}
}

func TestTracesLongWaitKeySuffix(t *testing.T) {
	if tracesLongWaitKeySuffix(0) != "" {
		t.Fatal("varsayılan bütçede anahtar eski kalmalı (soğuk cache yok)")
	}
	if a, b := tracesLongWaitKeySuffix(60), tracesLongWaitKeySuffix(40); a == "" || a == b {
		t.Fatalf("uzun bütçe anahtarı bütçeyi taşımalı: %q %q", a, b)
	}
}

func TestTracesPartialPayload(t *testing.T) {
	if tracesPartialPayload(chstore.TracePartial{}, time.Second, 0) != nil {
		t.Fatal("kısmi değilse zarf yok")
	}
	cov := time.Unix(1700000000, 0)
	p := tracesPartialPayload(chstore.TracePartial{Hit: true, CoveredFrom: cov}, 25100*time.Millisecond, 0)
	if p["limitSec"] != chstore.DefaultReadBudgetSec || p["longWaitSec"].(int) <= chstore.DefaultReadBudgetSec ||
		p["elapsedMs"] != int64(25100) || p["coveredFromNs"] != cov.UnixNano() {
		t.Fatalf("varsayılan bütçe zarfı: %+v", p)
	}
	p = tracesPartialPayload(chstore.TracePartial{Hit: true}, 61*time.Second, 60)
	if p["limitSec"] != 60 || p["longWaitSec"] != 0 {
		t.Fatalf("uzun beklemede düğme yeniden sunulmamalı: %+v", p)
	}
	if _, has := p["coveredFromNs"]; has {
		t.Fatal("hiç basamak bitmediyse coveredFromNs yok")
	}
}

func TestWithLongWaitAskChipOnlyOnPartial(t *testing.T) {
	ans := map[string]any{"text": "Elimdeki 12 trace şunlar.", "followups": []chatFollowup{{Text: "x", Kind: fuKindTrace}}}

	// Kısmi/zaman aşımı yok → olay AYNEN.
	var got any
	emit := func(kind string, p any) { got = p }
	wrapped, ctx := beginChatLongWaitTurn(context.Background(), emit, false)
	wrapped("answer", ans)
	if m := got.(map[string]any); m["text"] != ans["text"] || len(m["followups"].([]chatFollowup)) != 1 {
		t.Fatalf("zaman aşımı yokken cevap değişmemeli: %+v", m)
	}

	// Kısmi sonuç → soru + çip (60 sn, context.longWait).
	chstore.NoteReadTimeout(ctx)
	wrapped("answer", ans)
	m := got.(map[string]any)
	if !strings.Contains(m["text"].(string), "daha uzun bekleyeyim mi?") || !strings.Contains(m["text"].(string), "60 sn") {
		t.Fatalf("soru eklenmemiş: %q", m["text"])
	}
	fu := m["followups"].([]chatFollowup)
	if len(fu) != 2 || fu[1].Kind != longWaitChipKind || !fu[1].LongWait || !strings.Contains(fu[1].Label, "60 sn") {
		t.Fatalf("takip çipi: %+v", fu)
	}
	// Eski istemci metin önerisi olarak göndermez (bayraksız tık uzun beklemez).
	for _, s := range withAnswerFollowups(map[string]any{}, fu)["suggestions"].([]string) {
		if strings.Contains(s, "bekle") {
			t.Fatal("long_wait çipi düz metin önerisine düşmemeli")
		}
	}

	// Evet'li tur: uzun bütçe ctx'te; yine bitmezse soru TEKRARLANMAZ.
	var steps int
	wrapped2, ctx2 := beginChatLongWaitTurn(context.Background(), func(kind string, p any) {
		if kind == "step" {
			steps++
		}
		got = p
	}, true)
	if chstore.LongWaitFrom(ctx2) != 60 || longWaitToolBudget(ctx2) <= 60*time.Second || steps != 1 {
		t.Fatalf("evet turu: bütçe=%d araç=%v adım=%d", chstore.LongWaitFrom(ctx2), longWaitToolBudget(ctx2), steps)
	}
	chstore.NoteReadTimeout(ctx2)
	wrapped2("answer", map[string]any{"text": "kısmi"})
	m = got.(map[string]any)
	if _, has := m["followups"]; has || strings.Contains(m["text"].(string), "bekleyeyim mi") {
		t.Fatalf("uzun beklemede soru/çip tekrar sorulmamalı: %+v", m)
	}
	if longWaitToolBudget(context.Background()) != 0 {
		t.Fatal("evet'siz turda araç bütçesi değişmemeli")
	}
}

func TestWrapLongWaitHandlersNotesTimeout(t *testing.T) {
	byName := map[string]mcp.ToolHandler{
		"slow": func(context.Context, json.RawMessage) (any, error) {
			return nil, errors.New("code: 159, Timeout exceeded: elapsed 25068 ms")
		},
		"bad": func(context.Context, json.RawMessage) (any, error) { return nil, errors.New("bad args") },
	}
	wrapLongWaitHandlers(byName)
	ctx, rec := chstore.WithTimeoutRecorder(context.Background())
	_, _ = byName["bad"](ctx, nil)
	if rec.Hit() {
		t.Fatal("bütçe dışı hata işaretlememeli")
	}
	_, _ = byName["slow"](ctx, nil)
	if !rec.Hit() {
		t.Fatal("159 turu işaretlemeli")
	}
}
