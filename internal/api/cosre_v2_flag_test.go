package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// cosre_v2_flag_test.go — v0.10.1592 (CoSRE v2 Faz 1, R4): admin bayrağı.

func TestCosreV2FlagBlobRoundTrip(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{`{"enabled":true}`, true},
		{`{"enabled":false}`, false},
		{``, false},
		{`bozuk`, false},
		{`{"enabled":"evet"}`, false},
	}
	for _, c := range cases {
		if got := parseCosreV2Flag([]byte(c.raw)); got != c.want {
			t.Errorf("parseCosreV2Flag(%q) = %v", c.raw, got)
		}
	}
	raw := marshalCosreV2Flag(true)
	if string(raw) != `{"enabled":true}` || !parseCosreV2Flag(raw) {
		t.Fatalf("gidiş-dönüş: %s", raw)
	}
}

func TestCosreV2MemoTTLAndDefaultClosed(t *testing.T) {
	now := time.Unix(1000, 0)
	loads := 0
	val, err := true, error(nil)
	m := &cosreV2Memo{
		now:  func() time.Time { return now },
		load: func(context.Context) (bool, error) { loads++; return val, err },
	}
	ctx := context.Background()
	if !m.get(ctx) || loads != 1 {
		t.Fatalf("ilk okuma: loads=%d", loads)
	}
	val = false
	now = now.Add(cosreV2MemoTTL - time.Second)
	if !m.get(ctx) || loads != 1 {
		t.Fatal("TTL içinde yeniden okundu")
	}
	now = now.Add(2 * time.Second)
	if m.get(ctx) || loads != 2 {
		t.Fatalf("TTL sonrası tazelenmedi: loads=%d", loads)
	}
	// Hata → kapalı.
	val, err = true, errors.New("ch down")
	now = now.Add(cosreV2MemoTTL + time.Second)
	if m.get(ctx) {
		t.Fatal("okuma hatasında bayrak açık")
	}
	// set memo'yu anında günceller (PUT yerel memo'yu sıfırlar).
	m.set(true)
	if !m.get(ctx) {
		t.Fatal("set sonrası kapalı")
	}
	// Store'suz sunucu → kapalı.
	if (&Server{}).cosreV2Enabled(ctx) {
		t.Fatal("store'suz sunucuda bayrak açık")
	}
}

func TestCosreV2FlagAuditAndRoutes(t *testing.T) {
	src, err := os.ReadFile("cosre_v2_flag.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, must := range []string{
		`s.audit(r, "settings.cosre_v2.update", "settings", cosreV2SettingKey`,
		`registerRoutesExtra("cosre-v2-flag"`,
		`"GET /api/admin/cosre-v2"`, `"PUT /api/admin/cosre-v2"`,
		`auth.RequireRole(auth.RoleAdmin`,
		`http.MaxBytesReader(w, r.Body, cosreV2FlagMaxBody)`,
	} {
		if !strings.Contains(s, must) {
			t.Errorf("cosre_v2_flag.go %q içermiyor", must)
		}
	}
	mux := (&Server{}).buildMux()
	for _, m := range []string{http.MethodGet, http.MethodPut} {
		_, pattern := mux.Handler(httptest.NewRequest(m, "/api/admin/cosre-v2", nil))
		if pattern != m+" /api/admin/cosre-v2" {
			t.Errorf("%s çözümü %q", m, pattern)
		}
	}
}

func TestCopilotConfigCosreV2FieldOnlyWhenOn(t *testing.T) {
	s := &Server{copilot: copilot.New(copilot.ProviderOpenAI, "", "")}
	get := func() map[string]any {
		r := httptest.NewRequest(http.MethodGet, "/api/copilot/config", nil)
		r = r.WithContext(auth.ContextWithClaims(r.Context(), &auth.Claims{UserID: "u", Role: "viewer"}))
		w := httptest.NewRecorder()
		s.copilotConfig(w, r)
		var m map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return m
	}
	if _, has := get()["cosreV2"]; has {
		t.Fatal("bayrak kapalıyken cosreV2 alanı var (eski şekil bozuldu)")
	}
	s.cosreV2Memo().set(true)
	t.Cleanup(func() { cosreV2Memos.Delete(s) })
	if v, _ := get()["cosreV2"].(bool); !v {
		t.Fatal("bayrak açıkken cosreV2 yok")
	}
}
