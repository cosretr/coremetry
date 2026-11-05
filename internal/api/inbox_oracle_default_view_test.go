package api

// inbox_oracle_default_view_test.go — v0.10.1109 (operatör: "oracledan gelen
// teknik hatalar problems sayfasında da gözüksün isterim").
//
// Pin: Oracle hata tablosu grubu (`ora:`) oraclePriorityAt'in P1 dediği anda
// Problems'in VARSAYILAN görünümünde (prio=P1, ilk görülme sıralı) exception
// satırı olarak P1 kalır — span merdiveninin sticky hacim P1'ine düşmeden,
// tür kuralına (forceNonExceptionP3) ve oluşum tabanına takılmadan. Sürekli
// akan (P3) Oracle grubu varsayılan görünümde YOK. Gerçek ClickHouse'a karşı
// tam inboxView derlemesi de aynı sonucu verdi (sürüm notu: DECISIONS 1109) —
// liste yolu Oracle satırını düşürmüyordu; eksik bildirim yolundaydı.
//
// v0.10.1803 (operatör 2026-11-05: "Oracle P1 problemse Problems'a dönsün;
// ama 30'dan fazla hata olursa P1 olsun") — v0.10.1796 varsayılan tür kümesini
// problem+anomaly+incident yapınca pin Exceptions çipine taşınmıştı; Oracle P1
// parametresiz görünümden düşmüştü. Pin geri döndü: Oracle P1 satırı "problem"
// kovasında (inboxRowInProblemBucket), Oracle P2/P3 ve span exception'ı yok.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/oracle"
)

func TestInboxDefaultViewKeepsOracleP1(t *testing.T) {
	t.Cleanup(func() { oracleStatsFn.Store(nil) })
	now := time.Now()
	burst := chstore.ExceptionGroup{
		Fingerprint: chstore.OracleGroupFingerprint("src-crm", "ORA-00001", "OP_ORDERS"),
		Type:        "ORA-00001", Message: "OP_ORDERS", Service: chstore.OracleGroupFallbackService("crm-db"),
		State: chstore.ExStateNew, Occurrences: 400_000,
		FirstSeen: now.Add(-48 * time.Hour).UnixNano(), LastSeen: now.Add(-16 * time.Minute).UnixNano(),
	}
	steady := burst
	steady.Fingerprint = chstore.OracleGroupFingerprint("src-crm", "ORA-12899", "OP_ORDERS")
	steady.Type = "ORA-12899"
	SetOracleGroupStats(func(g chstore.ExceptionGroup) (oracle.GroupStats, bool) {
		if g.Fingerprint == burst.Fingerprint {
			return oracle.GroupStats{Lag: 16 * time.Minute, LastHour: 20_000, PrevHour: 1_000, BlobOK: true}, true
		}
		// son 1 sa 25 ≤ eşik 30 → P1 değil.
		return oracle.GroupStats{Lag: 16 * time.Minute, LastHour: 25, PrevHour: 24, BlobOK: true}, true
	})
	span := chstore.ExceptionGroup{Fingerprint: "a1b2c3d4e5f60718", Type: "SyntheticTimeoutError", Message: "read timed out",
		Service: "svc-orders", State: chstore.ExStateNew, Occurrences: 900,
		FirstSeen: now.Add(-time.Hour).UnixNano(), LastSeen: now.Add(-time.Minute).UnixNano()}

	items := []InboxItem{exceptionToInbox(span), exceptionToInbox(burst), exceptionToInbox(steady)}
	items, hidden := applyInboxMinOcc(items, effectiveDefaultFloor(currentExceptionTriage()), newFloorExemption(nil))
	if hidden != 0 {
		t.Fatalf("oluşum tabanı Oracle satırı gizledi: %d", hidden)
	}
	// Parametresiz varsayılan görünüm (rozetle aynı sorgu): Oracle P1 VAR.
	rows, _ := inboxDefaultViewRows(t, append([]InboxItem(nil), items...), nil, inboxBadgeQuery(""))
	byID := map[string]InboxItem{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	got, ok := byID["exception:"+burst.Fingerprint]
	if !ok {
		t.Fatalf("P1 Oracle grubu parametresiz Problems görünümünde yok: %+v", rows)
	}
	if got.Kind != "exception" || got.Priority != "P1" || !strings.Contains(got.PriorityReason, "> eşik 30") {
		t.Errorf("Oracle satırı exception + P1 + eşik gerekçesi olmalı: %+v", got)
	}
	if _, ok := byID["exception:"+steady.Fingerprint]; ok {
		t.Error("eşik altı (P3) Oracle grubu varsayılan görünüme girmemeli")
	}
	if _, ok := byID["exception:"+span.Fingerprint]; ok {
		t.Error("span exception'ı Exceptions çipi kapalıyken varsayılan görünüme girmemeli")
	}
	// Exceptions çipi açıkken span P1 de görünür (kıyas), Oracle P1 tek kez.
	q := inboxBadgeQuery("")
	q.Set("kind", "problem,exception,httperror,anomaly,incident")
	all, _ := inboxDefaultViewRows(t, append([]InboxItem(nil), items...), nil, q)
	oraN, spanN := 0, 0
	for _, r := range all {
		switch r.ID {
		case "exception:" + burst.Fingerprint:
			oraN++
		case "exception:" + span.Fingerprint:
			spanN++
		}
	}
	if oraN != 1 || spanN != 1 {
		t.Errorf("exception çipi açık: Oracle P1 bir kez + span P1 beklenir: %+v", all)
	}
	// Exceptions görünümü (kind=exception, tüm öncelikler) değişmez: eşik
	// altı (P3) Oracle grubu orada HÂLÂ görünür.
	exq := inboxBadgeQuery("")
	exq.Set("kind", "exception")
	exq.Set("prio", "P1,P2,P3")
	exRows, _ := inboxDefaultViewRows(t, append([]InboxItem(nil), items...), nil, exq)
	seen := map[string]string{}
	for _, r := range exRows {
		seen[r.ID] = r.Priority
	}
	if p, ok := seen["exception:"+steady.Fingerprint]; !ok || p != "P3" {
		t.Errorf("Exceptions görünümünde eşik altı Oracle grubu P3 olarak kalmalı: %+v", exRows)
	}
	if _, ok := seen["exception:"+burst.Fingerprint]; !ok {
		t.Errorf("Exceptions görünümünde Oracle P1 de görünmeli: %+v", exRows)
	}
	// "problem" seçili değilse kova kuralı devreye girmez.
	q.Set("kind", "anomaly,incident")
	if r, _ := inboxDefaultViewRows(t, append([]InboxItem(nil), items...), nil, q); len(r) != 0 {
		t.Errorf("problem kapalıyken Oracle P1 görünmemeli: %+v", r)
	}
}

// v0.10.1803 — kova kuralı SAF; FE rowInProblemBucket aynı tablo.
func TestInboxRowInProblemBucket(t *testing.T) {
	ora := chstore.OracleGroupFingerprint("src-crm", "ORA-00001", "OP_ORDERS")
	for _, c := range []struct {
		name string
		it   InboxItem
		want bool
	}{
		{"Oracle P1", InboxItem{Kind: "exception", Priority: "P1", Exception: &InboxExceptionRef{Fingerprint: ora}}, true},
		{"Oracle P2", InboxItem{Kind: "exception", Priority: "P2", Exception: &InboxExceptionRef{Fingerprint: ora}}, false},
		{"span P1", InboxItem{Kind: "exception", Priority: "P1", Exception: &InboxExceptionRef{Fingerprint: "a1b2c3d4e5f60718"}}, false},
		{"ref yok", InboxItem{Kind: "exception", Priority: "P1"}, false},
		{"httperror", InboxItem{Kind: "httperror", Priority: "P1", Exception: &InboxExceptionRef{Fingerprint: ora}}, false},
	} {
		if got := inboxRowInProblemBucket(c.it); got != c.want {
			t.Errorf("%s: %v, beklenen %v", c.name, got, c.want)
		}
	}
}
