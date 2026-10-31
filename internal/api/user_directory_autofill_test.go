package api

// user_directory_autofill_test.go — v0.10.1741 (operator-reported): v0.10.1732
// öncesi girişli LDAP hesabının detayında dizin alanları "—" geliyordu (yalnız
// "AD ile denetle" sonrası görünüyordu); yönetici ham DN'di. Otomatik doldurma
// yalnız boş / bayat dizinde ve yalnız etkin LDAP hesabında tetiklenir;
// yönetici etiketi ASLA ham DN değildir. Adlar SENTETİK.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ldap"
)

func dirAt(t *testing.T, at time.Time) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(ldap.DirectoryInfo{Title: "Uzman", SyncedAt: at.UnixNano()})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDirectoryAutofillNeeded(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	ldapU := func(dir json.RawMessage) *chstore.User {
		return &chstore.User{ID: "u1", AuthProvider: "ldap", LdapUsername: "user1", Directory: dir}
	}
	unstamped := json.RawMessage(`{"title":"Uzman"}`)
	cases := []struct {
		name   string
		u      *chstore.User
		ldapOn bool
		want   bool
	}{
		{"boş dizin", ldapU(nil), true, true},
		{"damgasız (1741 öncesi) dizin", ldapU(unstamped), true, true},
		{"25 saatlik dizin", ldapU(dirAt(t, now.Add(-25*time.Hour))), true, true},
		{"1 saatlik dizin", ldapU(dirAt(t, now.Add(-time.Hour))), true, false},
		{"tam 24 saat", ldapU(dirAt(t, now.Add(-24*time.Hour))), true, true},
		{"LDAP kapalı", ldapU(nil), false, false},
		{"yerel hesap", &chstore.User{ID: "u2", AuthProvider: "local"}, true, false},
		{"OIDC hesabı", &chstore.User{ID: "u3", AuthProvider: "oidc"}, true, false},
		{"OIDC'ye bağlanmış eski LDAP hesabı (kart yok)", &chstore.User{ID: "u4", AuthProvider: "oidc", LdapUsername: "user4"}, true, false},
		{"engelli hesap", &chstore.User{ID: "u5", AuthProvider: "ldap", Disabled: true, Blocked: true}, true, false},
		{"nil", nil, true, false},
	}
	for _, c := range cases {
		if got := directoryAutofillNeeded(c.u, c.ldapOn, now); got != c.want {
			t.Errorf("%s: directoryAutofillNeeded = %v, want %v", c.name, got, c.want)
		}
	}
}

// Başarısızlık 10 dk hatırlanır: aynı hesap için ikinci deneme yok.
func TestTryLogClaim(t *testing.T) {
	var l tryLog
	t0 := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	if !l.claim("u1", t0, 10*time.Minute) {
		t.Fatal("ilk deneme alınmalı")
	}
	if l.claim("u1", t0.Add(9*time.Minute), 10*time.Minute) {
		t.Fatal("10 dk dolmadan ikinci deneme olmamalı")
	}
	if !l.recent("u1", t0.Add(9*time.Minute), 10*time.Minute) {
		t.Fatal("recent true olmalı")
	}
	if !l.claim("u2", t0.Add(time.Minute), 10*time.Minute) {
		t.Fatal("başka hesap bağımsız")
	}
	if !l.claim("u1", t0.Add(10*time.Minute), 10*time.Minute) {
		t.Fatal("10 dk sonra yeniden denenmeli")
	}
}

// Sayfa asılmaz: yavaş LDAP okuması süre sınırında kesilir.
func TestReadWithinTimesOut(t *testing.T) {
	start := time.Now()
	_, err := readWithin(context.Background(), 20*time.Millisecond, func(ctx context.Context) (int, error) {
		time.Sleep(500 * time.Millisecond)
		return 1, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 300*time.Millisecond {
		t.Fatalf("zaman aşımı beklenirdi: err=%v süre=%s", err, time.Since(start))
	}
	v, err := readWithin(context.Background(), time.Second, func(ctx context.Context) (int, error) { return 7, nil })
	if err != nil || v != 7 {
		t.Fatalf("hızlı okuma: v=%d err=%v", v, err)
	}
}

func TestManagerDisplay(t *testing.T) {
	dn := "CN=Manager One,OU=Users,DC=example,DC=com"
	cases := []struct {
		name      string
		dn        string
		p         *ldap.DirectoryPerson
		stored    string
		wantName  string
		wantEmpID string
	}{
		{"okunan düğüm: görünen ad + sicil", dn, &ldap.DirectoryPerson{Name: "Manager One Display", EmployeeID: "E200"}, "Manager One", "Manager One Display", "E200"},
		{"okuma yok: saklı CN", dn, nil, "Manager One", "Manager One", ""},
		{"saklı ad yok: DN'nin CN'i", dn, nil, "", "Manager One", ""},
		{"kaçışlı virgül", `CN=Doe\, Jane,OU=Users,DC=example,DC=com`, nil, "", "Doe, Jane", ""},
		{"düğüm adı boş: saklıya düşer", dn, &ldap.DirectoryPerson{EmployeeID: "E200"}, "Manager One", "Manager One", "E200"},
		{"CN'siz DN: ham DN ASLA", "UID=mgr1,OU=Users,DC=example,DC=com", nil, "", "", ""},
		{"bozuk DN: ham DN ASLA", "not a dn", nil, "", "", ""},
	}
	for _, c := range cases {
		got := managerDisplay(c.dn, c.p, c.stored)
		if got.Name != c.wantName || got.EmployeeID != c.wantEmpID {
			t.Errorf("%s: managerDisplay = %+v, want {%q %q}", c.name, got, c.wantName, c.wantEmpID)
		}
		if strings.Contains(got.Name, "=") {
			t.Errorf("%s: etiket ham DN taşıyor: %q", c.name, got.Name)
		}
	}
}

// Önbellek anahtarı tüm girdileri özetler (v0.5.187): DN (harf duyarsız) ve
// yapılandırma özeti.
func TestUserManagerKeyHashesAllInputs(t *testing.T) {
	a := userManagerKey("CN=Manager One,OU=Users,DC=example,DC=com", 1)
	if a != userManagerKey("cn=manager one,ou=users,dc=example,dc=com", 1) {
		t.Error("DN harf duyarsız olmalı")
	}
	if a == userManagerKey("CN=Manager Two,OU=Users,DC=example,DC=com", 1) || a == userManagerKey("CN=Manager One,OU=Users,DC=example,DC=com", 2) {
		t.Error("anahtar çakıştı")
	}
}

// Otomatik doldurma detay okumasında koşar ve takımı taşır; otomatik okuma
// audit yazmaz (operatör kararı).
func TestAutofillWiredIntoDetailWithoutAudit(t *testing.T) {
	b, err := os.ReadFile("user_detail.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "func (s *Server) getUserDetail(")
	body := src[i:]
	body = body[:strings.Index(body, "\n}\n")]
	if !strings.Contains(body, "s.autofillUserDirectory(r.Context(), u)") || !strings.Contains(body, "s.userDirectoryManager(r.Context(), u)") {
		t.Fatal("getUserDetail otomatik doldurma + yönetici çözümünü çağırmıyor")
	}
	a, err := os.ReadFile("user_directory_autofill.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(a), "s.audit(") {
		t.Fatal("otomatik dizin okuması audit yazmamalı")
	}
	if !strings.Contains(string(a), "after.Team = u.Team") {
		t.Fatal("otomatik okuma takımı taşımalı (giriş kuralı)")
	}
}
