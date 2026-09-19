package copilot

import (
	"context"
	"testing"
)

// v0.10.819 — yüzey haritada yoksa grubunun kayıtlı kardeşi: eski kayıtlar yeni
// yüzeyi (chat-offtopic) bilmez; varsayılan (büyük) profile düşmesin.
func TestResolveProfileGroupSiblingFallback(t *testing.T) {
	s := &Service{
		profiles:        map[string]*profileRuntime{"small": {cfg: ModelProfile{ID: "small"}}, "big": {cfg: ModelProfile{ID: "big"}}},
		surfaceProfiles: map[string]string{"chat-intent": "small"},
		defaultID:       "big",
	}
	ctx := WithMeta(context.Background(), CallMeta{Surface: "chat-offtopic"})
	if rt := s.resolveProfileLocked(ctx); rt.cfg.ID != "small" {
		t.Fatalf("grup kardeşi (chat-intent) profili beklenirdi, %q", rt.cfg.ID)
	}
	// Kayıtlı yüzey önce gelir.
	s.surfaceProfiles["chat-offtopic"] = "big"
	if rt := s.resolveProfileLocked(ctx); rt.cfg.ID != "big" {
		t.Fatalf("kayıtlı yüzey kazanmalı, %q", rt.cfg.ID)
	}
	// Grupsuz yüzey → varsayılan.
	if rt := s.resolveProfileLocked(WithMeta(context.Background(), CallMeta{Surface: "explain-slow-query"})); rt.cfg.ID != "big" {
		t.Fatalf("grupsuz yüzey varsayılana düşmeli, %q", rt.cfg.ID)
	}
}
