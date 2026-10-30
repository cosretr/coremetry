package auth

// approval_token_test.go — agent-core S4 (docs/cosre/agent-core-ideas.md §4.2):
// onay jetonu imzalı, süreli, türü ayrık; eksik bağlı jeton basılmaz.

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestApprovalTokenRoundTrip(t *testing.T) {
	s := NewService("test-secret-that-is-long-enough-0123456789", time.Hour)
	now := time.Unix(1_800_000_000, 0)
	in := ApprovalClaims{Exchange: "x-1", Step: "s3", Tool: "add_problem_note", ArgsDigest: "abc", User: "u-1", Budget: ApprovalBudget{CallsLeft: 2, RoundsUsed: 3}}
	tok, out, err := s.SignApproval(in, now, 300*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != ApprovalKind || out.Exp != now.Unix()+300 {
		t.Fatalf("claims: %+v", out)
	}
	got, err := s.VerifyApproval(tok, now.Add(299*time.Second))
	if err != nil || got != out {
		t.Fatalf("doğrulama: %+v %v", got, err)
	}
	// zaman aşımı → ErrActionExpired (çağıran ret sayar)
	if _, err := s.VerifyApproval(tok, now.Add(301*time.Second)); !errors.Is(err, ErrActionExpired) {
		t.Errorf("süre dolmuş: %v", err)
	}
	// başka anahtar
	other := NewService("another-secret-that-is-long-enough-9876543210", time.Hour)
	if _, err := other.VerifyApproval(tok, now); !errors.Is(err, ErrActionToken) {
		t.Errorf("yabancı anahtar: %v", err)
	}
	// kurcalanmış yük
	i := strings.LastIndexByte(tok, '.')
	if _, err := s.VerifyApproval("A"+tok[1:i]+tok[i:], now); err == nil {
		t.Error("kurcalanmış yük kabul edildi")
	}
}

func TestApprovalTokenKindSeparation(t *testing.T) {
	s := NewService("test-secret-that-is-long-enough-0123456789", time.Hour)
	now := time.Now()
	act, err := s.SignAction("cosre-note", "p-1", "u-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyApproval(act, now); !errors.Is(err, ErrActionToken) {
		t.Errorf("eylem jetonu onay diye geçti: %v", err)
	}
	apr, _, err := s.SignApproval(ApprovalClaims{Exchange: "x", Step: "s", Tool: "t", ArgsDigest: "h", User: "u"}, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyAction(apr, now); !errors.Is(err, ErrActionToken) {
		t.Errorf("onay jetonu notify eylemi diye geçti: %v", err)
	}
}

func TestSignApprovalRequiresBinding(t *testing.T) {
	s := NewService("test-secret-that-is-long-enough-0123456789", time.Hour)
	full := ApprovalClaims{Exchange: "x", Step: "s", Tool: "t", ArgsDigest: "h", User: "u"}
	for name, mut := range map[string]func(*ApprovalClaims){
		"exchange": func(c *ApprovalClaims) { c.Exchange = "" },
		"step":     func(c *ApprovalClaims) { c.Step = "" },
		"tool":     func(c *ApprovalClaims) { c.Tool = "" },
		"digest":   func(c *ApprovalClaims) { c.ArgsDigest = "" },
		"user":     func(c *ApprovalClaims) { c.User = "" },
	} {
		c := full
		mut(&c)
		if _, _, err := s.SignApproval(c, time.Now(), time.Hour); err == nil {
			t.Errorf("%s boşken jeton basıldı", name)
		}
	}
	if _, _, err := s.SignApproval(full, time.Now(), 0); err == nil {
		t.Error("ttl 0 jeton basıldı")
	}
}
