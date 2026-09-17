package auth

// action_token_test.go — v0.10.749 bildirim "Sustur" jetonu: gidiş-dönüş,
// süre, kurcalama (gövde ve imza), yanlış anahtar, ayrı anahtar,
// bozuk şekil; ve public yolun SkipPath'te olduğu (aksi hâlde e-posta
// alıcısı 401 görür).

import (
	"strings"
	"testing"
	"time"
)

func TestActionTokenRoundTrip(t *testing.T) {
	s := NewService("test-secret-that-is-long-enough-0123456789", time.Hour)
	tok, err := s.SignAction("problem", "p-1", "oncall@example.com", 7*24*time.Hour)
	if err != nil || tok == "" || !strings.Contains(tok, ".") {
		t.Fatalf("sign: %v %q", err, tok)
	}
	c, err := s.VerifyAction(tok, time.Now())
	if err != nil || c.Kind != "problem" || c.ID != "p-1" || c.Who != "oncall@example.com" {
		t.Fatalf("verify: %v %+v", err, c)
	}
	// Süre: 8 gün sonra geçersiz, ama içerik yine okunur (sayfa "süresi dolmuş" der).
	if _, err := s.VerifyAction(tok, time.Now().Add(8*24*time.Hour)); err != ErrActionExpired {
		t.Fatalf("süre: %v", err)
	}
	// Kurcalama — gövde ya da imza değişince reddedilir.
	i := strings.LastIndexByte(tok, '.')
	if _, err := s.VerifyAction("x"+tok[1:], time.Now()); err != ErrActionToken {
		t.Fatalf("gövde kurcalama: %v", err)
	}
	if _, err := s.VerifyAction(tok[:i+1]+"AAAA", time.Now()); err != ErrActionToken {
		t.Fatalf("imza kurcalama: %v", err)
	}
	// Başka anahtarla imzalanan reddedilir; ayrı eylem anahtarı JWT'nin önüne geçer.
	other := NewService("another-secret-that-is-long-enough-9876543210", time.Hour)
	if _, err := other.VerifyAction(tok, time.Now()); err != ErrActionToken {
		t.Fatalf("yanlış anahtar: %v", err)
	}
	other.SetActionSecret("test-secret-that-is-long-enough-0123456789")
	if _, err := other.VerifyAction(tok, time.Now()); err != nil {
		t.Fatalf("ayrı eylem anahtarı: %v", err)
	}
	other.SetActionSecret("   ") // boş → önceki kalır
	if _, err := other.VerifyAction(tok, time.Now()); err != nil {
		t.Fatalf("boş SetActionSecret anahtarı sıfırladı: %v", err)
	}
}

func TestActionTokenShape(t *testing.T) {
	s := NewService("test-secret-that-is-long-enough-0123456789", time.Hour)
	for _, bad := range []string{"", ".", "abc", "abc.", ".abc", strings.Repeat("a", 3000) + ".b"} {
		if _, err := s.VerifyAction(bad, time.Now()); err == nil {
			t.Errorf("%q kabul edildi", bad)
		}
	}
	if _, err := s.SignAction("", "id", "w", time.Hour); err == nil {
		t.Error("boş kind imzalandı")
	}
	if _, err := s.SignAction("problem", "id", "w", 0); err == nil {
		t.Error("ttl 0 imzalandı")
	}
}

func TestIgnoreLinkPathIsPublic(t *testing.T) {
	if !SkipPath("GET", "/api/public/notify/ignore/abc.def") {
		t.Fatal("/api/public/notify/ yolu public değil — e-posta alıcısı 401 görür")
	}
	if SkipPath("GET", "/api/notify/channels/health") {
		t.Fatal("public önek fazla geniş")
	}
}
