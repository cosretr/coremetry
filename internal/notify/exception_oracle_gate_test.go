package notify

// exception_oracle_gate_test.go — v0.10.1092 (Oracle hata tablosu grupları
// Exceptions'ta). Pinler: `ora:` parmak izi bildirim kimliğinden geri
// okunur (Sustur / incident linki / "problem değil" susturması); kapı
// shadow kaynağın grubunu ELER; live grubun tazeliği kapanmış dakika
// gecikmesi kadar geriden ölçülür (yoksa akan grup hiç aday olmazdı).

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestExceptionGroupFingerprintOracle(t *testing.T) {
	fp := chstore.OracleGroupFingerprint("o-1", "APP_ERR_001", "OP_A")
	for _, id := range []string{
		exceptionGroupID(fp, chstore.ExStateNew),
		exceptionGroupID(fp, chstore.ExStateRegressed),
		exceptionRegressionP1ID(fp, nil),
	} {
		if got := exceptionGroupFingerprint(id); got != fp {
			t.Fatalf("%q → %q, beklenen %q", id, got, fp)
		}
	}
	if got := exceptionGroupFingerprint("exception-group:abc:regressed"); got != "abc" {
		t.Fatalf("span kimliği değişmemeli: %q", got)
	}
}

func TestExceptionNotifierGate(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	ora := chstore.ExceptionGroup{Fingerprint: chstore.OracleGroupFingerprint("o-1", "APP_ERR_001", "OP_A"),
		Occurrences: 50, FirstSeen: now.Add(-25 * time.Minute).UnixNano(), LastSeen: now.Add(-17 * time.Minute).UnixNano()}
	e := &ExceptionNotifier{}
	if ok, gnow := e.admit(ora, now); !ok || !gnow.Equal(now) {
		t.Fatal("kapı yokken her grup, kayma yok")
	}
	// Kayma olmadan Oracle grubu (last_seen 17 dk önce) kanal adayı DEĞİL.
	if isChannelCandidate(ora, chstore.ExStateNew, now) {
		t.Fatal("ön koşul: kaymasız aday olmamalı")
	}
	e.SetGroupGate(func(g chstore.ExceptionGroup) (bool, time.Duration) {
		if chstore.IsOracleGroup(g.Fingerprint) {
			return true, 16 * time.Minute // live kaynak, WindowMin 15 + pay
		}
		return true, 0
	})
	ok, gnow := e.admit(ora, now)
	if !ok || !isChannelCandidate(ora, chstore.ExStateNew, gnow) {
		t.Fatalf("live Oracle grubu kaymayla aday olmalı (gnow=%v)", gnow)
	}
	e.SetGroupGate(func(chstore.ExceptionGroup) (bool, time.Duration) { return false, 0 }) // shadow
	if ok, _ := e.admit(ora, now); ok {
		t.Fatal("shadow kaynağın grubu bildirilmez")
	}
}

// M6 — Oracle grubunun takım anonsu: 500'lük span hacim eşiği değil, merdiven
// P1 demeli ve grup (gecikme kaydırılmış) taze olmalı.
func TestOracleAnnounceCandidate(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	g := chstore.ExceptionGroup{Fingerprint: chstore.OracleGroupFingerprint("o-1", "APP_ERR_001", "OP_A"),
		Occurrences: 900000, LastSeen: now.Add(-2 * time.Minute).UnixNano()}
	if !oracleAnnounceCandidate(g, "P1", now) {
		t.Fatal("P1 + taze → anons")
	}
	if oracleAnnounceCandidate(g, "P2", now) || oracleAnnounceCandidate(g, "P3", now) {
		t.Fatal("P1 değilse hacim ne olursa olsun anons yok")
	}
	if oracleAnnounceCandidate(g, "P1", now.Add(10*time.Minute)) {
		t.Fatal("bayat grup anons almaz")
	}
}
