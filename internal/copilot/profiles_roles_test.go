package copilot

// profiles_roles_test.go — v0.10.1138: profil rol allowlist'i + kısa açıklama.
// Sözleşme: boş Roles = herkes; ValidateProfile yalnız admin/editor/viewer
// kabul eder (tekrar yok); CheckProfileAccess bilinmeyen kimliği
// ErrProfileNotFound, rolüne kapalıyı ErrProfileForbidden döner; açıklama ve
// roller kalıcı blob'dan geri gelir.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestValidateProfileRolesAndDescription(t *testing.T) {
	base := ModelProfile{ID: "fast", Provider: ProviderOpenAI}
	cases := []struct {
		name  string
		mod   func(p *ModelProfile)
		valid bool
	}{
		{"boş roller", func(p *ModelProfile) {}, true},
		{"geçerli roller", func(p *ModelProfile) { p.Roles = []string{"admin", "editor"} }, true},
		{"bilinmeyen rol", func(p *ModelProfile) { p.Roles = []string{"owner"} }, false},
		{"tekrar eden rol", func(p *ModelProfile) { p.Roles = []string{"viewer", "viewer"} }, false},
		{"kısa açıklama", func(p *ModelProfile) { p.Description = "Hızlı" }, true},
		{"uzun açıklama", func(p *ModelProfile) { p.Description = strings.Repeat("ç", 121) }, false},
	}
	for _, tc := range cases {
		p := base
		tc.mod(&p)
		if err := ValidateProfile(p); (err == nil) != tc.valid {
			t.Errorf("%s: err=%v valid=%v", tc.name, err, tc.valid)
		}
	}
}

func TestProfileRoleAllowed(t *testing.T) {
	open := ModelProfile{ID: "a"}
	adminOnly := ModelProfile{ID: "b", Roles: []string{"admin"}}
	for _, role := range []string{"admin", "editor", "viewer", ""} {
		if !ProfileRoleAllowed(open, role) {
			t.Errorf("boş allowlist %q'yu reddetti", role)
		}
	}
	if !ProfileRoleAllowed(adminOnly, "admin") || ProfileRoleAllowed(adminOnly, "viewer") || ProfileRoleAllowed(adminOnly, "") {
		t.Fatal("admin-only allowlist yanlış")
	}
}

func TestCheckProfileAccess(t *testing.T) {
	s := New("anthropic", "", "")
	s.SetProfiles([]ModelProfile{
		{ID: "fast", Provider: ProviderOpenAI, BaseURL: "http://llm.example.test/v1", Model: "small"},
		{ID: "deep", Provider: ProviderOpenAI, BaseURL: "http://llm.example.test/v1", Model: "big", Roles: []string{"admin", "editor"}},
	}, "fast", nil)
	if err := s.CheckProfileAccess("fast", "viewer"); err != nil {
		t.Fatalf("açık profil: %v", err)
	}
	if err := s.CheckProfileAccess("deep", "editor"); err != nil {
		t.Fatalf("editor deep: %v", err)
	}
	if err := s.CheckProfileAccess("deep", "viewer"); !errors.Is(err, ErrProfileForbidden) {
		t.Fatalf("viewer deep: %v, want ErrProfileForbidden", err)
	}
	if err := s.CheckProfileAccess("nope", "admin"); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("bilinmeyen: %v, want ErrProfileNotFound", err)
	}
	var nilSvc *Service
	if err := nilSvc.CheckProfileAccess("fast", "admin"); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("nil servis: %v", err)
	}
}

func TestProfileRolesPersistRoundTrip(t *testing.T) {
	store := newMemStore()
	s := New("anthropic", "", "")
	ctx := context.Background()
	if err := s.UpsertProfile(ctx, store, ModelProfile{ID: "fast", Provider: ProviderOpenAI, BaseURL: "http://llm.example.test/v1", Description: "Hızlı"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertProfile(ctx, store, ModelProfile{ID: "deep", Provider: ProviderOpenAI, BaseURL: "http://llm.example.test/v1", Description: "Derin", Roles: []string{"admin"}}); err != nil {
		t.Fatal(err)
	}
	s2 := New("anthropic", "", "")
	if err := s2.LoadPersisted(ctx, store); err != nil {
		t.Fatal(err)
	}
	for _, p := range s2.Profiles() {
		if p.ID == "deep" && (p.Description != "Derin" || len(p.Roles) != 1 || p.Roles[0] != "admin") {
			t.Fatalf("deep geri gelmedi: %+v", p)
		}
	}
	if err := s2.CheckProfileAccess("deep", "viewer"); !errors.Is(err, ErrProfileForbidden) {
		t.Fatalf("yeniden yüklemeden sonra allowlist kayıp: %v", err)
	}
}
