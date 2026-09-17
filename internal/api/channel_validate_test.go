package api

// channel_validate_test.go — v0.10.747 kanal türü doğrulaması. Saf yarı +
// ULAŞILABİLİRLİK pini: doğrulayıcı create VE update'te çağrılıyor
// (saf çekirdek yeşil, çağrıldığı pinli değil — v0.9.1334 dersi).

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestValidateChannelKinds(t *testing.T) {
	c := chstore.NotificationChannel{Name: "x", Type: "email"}
	c.MatchRules.Kinds = []string{" Anomaly", "problem", "anomaly"}
	if err := validateChannelKinds(&c); err != nil {
		t.Fatalf("geçerli liste reddedildi: %v", err)
	}
	if got := strings.Join(c.MatchRules.Kinds, ","); got != "anomaly,problem" {
		t.Fatalf("normalize edilmedi: %q", got)
	}
	c.MatchRules.Kinds = []string{"exception"}
	if err := validateChannelKinds(&c); err == nil || !strings.Contains(err.Error(), "exception") {
		t.Fatalf("bilinmeyen tür kabul edildi / mesaj değeri anmıyor: %v", err)
	}
	c.MatchRules.Kinds = []string{}
	if err := validateChannelKinds(&c); err != nil || c.MatchRules.Kinds != nil {
		t.Fatalf("boş liste nil'e inmeli: err=%v kinds=%v", err, c.MatchRules.Kinds)
	}
}

func TestValidateChannelKindsReachable(t *testing.T) {
	src, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(src), "validateChannelKinds(&c)"); n != 2 {
		t.Fatalf("validateChannelKinds create+update'te çağrılmalı (2), %d bulundu", n)
	}
}
