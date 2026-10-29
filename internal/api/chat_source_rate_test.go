package api

// v0.10.1563 — sohbet kod araçlarının kullanıcı başına git çağrı sınırı
// (chat_source_rate.go): kota türe ve kullanıcıya ayrı, pencere dönünce
// sıfırlanır, ret yeniden deneme süresi taşır.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestSourceRateLimiter(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 10, 0, time.UTC)
	l := newSourceRateLimiter(func() time.Time { return now })

	for i := 0; i < sourceReadsPerMin; i++ {
		if ok, _ := l.allow("u1", sourceRateRead); !ok {
			t.Fatalf("okuma %d reddedildi (sınır %d)", i+1, sourceReadsPerMin)
		}
	}
	ok, retry := l.allow("u1", sourceRateRead)
	if ok || retry != 50*time.Second {
		t.Fatalf("sınır+1: ok=%v retry=%v, beklenen ret + 50s", ok, retry)
	}
	// Tür ayrı: okuma kotası dolu, arama hakkı var.
	if ok, _ := l.allow("u1", sourceRateSearch); !ok {
		t.Error("arama kotası okumadan ayrı olmalı")
	}
	// Kullanıcı ayrı.
	if ok, _ := l.allow("u2", sourceRateRead); !ok {
		t.Error("başka kullanıcının kotası ayrı olmalı")
	}
	// Pencere dönünce sıfır.
	now = now.Add(time.Minute)
	if ok, _ := l.allow("u1", sourceRateRead); !ok {
		t.Error("yeni pencerede hak dönmeli")
	}
	// Arama sınırı.
	for i := 0; i < sourceSearchesPerMin; i++ {
		l.allow("u3", sourceRateSearch)
	}
	if ok, _ := l.allow("u3", sourceRateSearch); ok {
		t.Errorf("arama sınırı %d olmalı", sourceSearchesPerMin)
	}
	// Pencere sonuna <1 sn kala ret 1 sn der, 0 değil.
	now = time.Date(2026, 10, 29, 12, 1, 59, 900_000_000, time.UTC)
	for i := 0; i < sourceReadsPerMin; i++ {
		l.allow("u4", sourceRateRead)
	}
	if _, r := l.allow("u4", sourceRateRead); r < time.Second {
		t.Errorf("retry en az 1 sn, got %v", r)
	}
}

func TestSourceRateUserAndReason(t *testing.T) {
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-42"})
	if got := sourceRateUser(ctx); got != "u-42" {
		t.Errorf("kullanıcı = %q", got)
	}
	if got := sourceRateUser(context.Background()); got != "" {
		t.Errorf("kimliksiz bağlam boş kullanıcı, got %q", got)
	}
	r := sourceRateReason(sourceRateSearch, 12*time.Second)
	if !strings.Contains(r, "kod arama") || !strings.Contains(r, "15") || !strings.Contains(r, "12 sn") || !strings.Contains(r, "gönderilmedi") {
		t.Errorf("gerekçe: %s", r)
	}
}

// Ulaşılabilirlik: gerçek okuma yolu (readSourceCode → readScopedSource)
// kota dolunca git'e TEK istek atmadan rate_limited döner; başka kullanıcı
// ve kimliksiz bağlam etkilenmez.
func TestReadSourceCodeRateLimited(t *testing.T) {
	prev := sourceRate
	now := time.Date(2026, 10, 29, 12, 0, 10, 0, time.UTC)
	sourceRate = newSourceRateLimiter(func() time.Time { return now })
	t.Cleanup(func() { sourceRate = prev })

	git, dv := newSCGit(t)
	s := &Server{devops: dv}
	scope := withSourceExceptionScope(context.Background(), exceptionCodeScopeFrom(&chstore.ExceptionGroup{Service: exGroupSvc}, *exInput(exStackSvc, "")))
	asUser := func(uid string) context.Context {
		return auth.ContextWithClaims(scope, &auth.Claims{UserID: uid, Role: auth.RoleViewer})
	}
	req := mcptools.SourceCodeRequest{Service: exStackSvc, File: "ChargeHandler.java", Line: 120, ContextLines: 3}
	for i := 0; i < sourceReadsPerMin; i++ {
		sourceRate.allow("rl-user", sourceRateRead)
	}
	before := len(git.requests())
	sr, err := s.readSourceCode(asUser("rl-user"), req)
	if err != nil || sr.Outcome != devops.SourceRateLimited || !strings.Contains(sr.Reason, "kod okuma sınırı") {
		t.Fatalf("kota dolu kullanıcı rate_limited almalı: %v %+v", err, sr)
	}
	if n := len(git.requests()) - before; n != 0 {
		t.Fatalf("sınırda git'e %d istek gitti", n)
	}
	if sr, err := s.readSourceCode(asUser("other"), req); err != nil || sr.Outcome != devops.SourceOK {
		t.Fatalf("başka kullanıcı okuyabilmeli: %v %+v", err, sr)
	}
	if sr, err := s.readSourceCode(scope, req); err != nil || sr.Outcome != devops.SourceOK {
		t.Fatalf("kimliksiz bağlam sayılmaz: %v %+v", err, sr)
	}
}
