package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache/cachetest"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// Saatlik sınır iki replikada paylaşılır; aşınca açık cevap + sıfırlanma.
func TestChatTurnLimitHourAndDay(t *testing.T) {
	mem := cachetest.NewMem()
	a, b := genTestServer(mem), genTestServer(mem)
	ctx := context.Background()
	cfg := chstore.NormalizeAITurnLimits(chstore.AITurnLimits{PerHour: 3, PerDay: 4})
	u := &auth.Claims{UserID: "u1", Role: auth.RoleEditor}
	now := time.Unix(1_800_000_000, 0) // saat başı + 1200 sn
	for i := 0; i < 3; i++ {
		s := a
		if i%2 == 1 {
			s = b
		}
		if v := decideTurnLimit(ctx, s.sharedRateAllow, cfg, u, now); v.Limited {
			t.Fatalf("tur %d sınırda olmamalı", i)
		}
	}
	v := decideTurnLimit(ctx, b.sharedRateAllow, cfg, u, now)
	if !v.Limited || v.Window != "hour" || v.Limit != 3 || v.Wait <= 0 || v.Wait > time.Hour {
		t.Fatalf("saatlik sınır: %+v", v)
	}
	txt := turnLimitText(v)
	if !strings.Contains(txt, "saatlik soru sınırına") || !strings.Contains(txt, "sonra sıfırlanır") {
		t.Fatalf("metin: %s", txt)
	}
	// Başka kullanıcı etkilenmez; muaf admin sayılmaz.
	if decideTurnLimit(ctx, a.sharedRateAllow, cfg, &auth.Claims{UserID: "u2"}, now).Limited {
		t.Fatal("başka kullanıcı")
	}
	adm := &auth.Claims{UserID: "u1", Role: auth.RoleAdmin}
	ex := cfg
	ex.ExemptAdmins = true
	if decideTurnLimit(ctx, a.sharedRateAllow, ex, adm, now).Limited {
		t.Fatal("muaf admin sınırlanmamalı")
	}
	// Sonraki saat: saat sayacı sıfır, gün sayacı 3 (aşan saat turu güne yazılmadı) → 1 tur, sonra gün sınırı.
	next := now.Add(time.Hour)
	if decideTurnLimit(ctx, a.sharedRateAllow, cfg, u, next).Limited {
		t.Fatal("yeni saatte ilk tur geçmeli")
	}
	if v := decideTurnLimit(ctx, a.sharedRateAllow, cfg, u, next); !v.Limited || v.Window != "day" {
		t.Fatalf("gün sınırı: %+v", v)
	}
	// Kapalı ayar: hiç sayılmaz.
	off := cfg
	off.Disabled = true
	if decideTurnLimit(ctx, a.sharedRateAllow, off, u, next).Limited {
		t.Fatal("kapalı ayar")
	}
}

// R0: Redis yok → engelleme yok.
func TestChatTurnLimitDegradedNeverBlocks(t *testing.T) {
	noRedis := func(context.Context, string, string, int, time.Duration, time.Time) (bool, time.Duration, bool) {
		return false, 0, false
	}
	cfg := chstore.NormalizeAITurnLimits(chstore.AITurnLimits{PerHour: 1, PerDay: 1})
	for i := 0; i < 5; i++ {
		if decideTurnLimit(context.Background(), noRedis, cfg, &auth.Claims{UserID: "u1"}, time.Now()).Limited {
			t.Fatal("Redis yokken tur engellendi")
		}
	}
}

func TestTurnLimitsDefaultsAndWait(t *testing.T) {
	d := chstore.NormalizeAITurnLimits(chstore.AITurnLimits{})
	if d.PerHour != 60 || d.PerDay != 300 || d.Disabled || d.ExemptAdmins {
		t.Fatalf("varsayılan: %+v", d)
	}
	for in, want := range map[time.Duration]string{30 * time.Second: "1 dk", 45 * time.Minute: "45 dk", 2 * time.Hour: "2 sa", 190 * time.Minute: "3 sa 10 dk"} {
		if got := waitTR(in); got != want {
			t.Errorf("waitTR(%v) = %q, beklenen %q", in, got, want)
		}
	}
}

type fakeTurnLimitsStore struct{ saved chstore.AITurnLimits }

func (f *fakeTurnLimitsStore) SaveAITurnLimits(_ context.Context, b chstore.AITurnLimits) (chstore.AITurnLimits, error) {
	f.saved = chstore.NormalizeAITurnLimits(b)
	return f.saved, nil
}

func TestAITurnLimitsRoutesAdminAndAudit(t *testing.T) {
	s, mux, _ := newSRETestEnv(t)
	s.registerAITurnLimitRoutes(mux)
	fs := &fakeTurnLimitsStore{}
	prev := aiTurnLimitsStoreOf
	aiTurnLimitsStoreOf = func(*Server) aiTurnLimitsStore { return fs }
	t.Cleanup(func() { aiTurnLimitsStoreOf = prev; chstore.SetAITurnLimits(chstore.AITurnLimits{}) })
	if w := sreDo(mux, "PUT", "/api/ai/turn-limits", `{"perHour":10}`, auth.RoleEditor); w.Code != http.StatusForbidden {
		t.Fatalf("editor PUT = %d", w.Code)
	}
	w := sreDo(mux, "PUT", "/api/ai/turn-limits", `{"perHour":10,"exemptAdmins":true}`, auth.RoleAdmin)
	if w.Code != 200 || fs.saved.PerHour != 10 || fs.saved.PerDay != 300 || !fs.saved.ExemptAdmins {
		t.Fatalf("admin PUT = %d %+v", w.Code, fs.saved)
	}
	if a := <-s.auditQ; a.Action != "settings.ai_turn_limits.update" {
		t.Fatalf("audit = %+v", a)
	}
	if chstore.CurrentAITurnLimits().PerHour != 10 {
		t.Fatal("pod ayarı güncellenmeli")
	}
}
