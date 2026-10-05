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
		return oracle.GroupStats{Lag: 16 * time.Minute, LastHour: 900, PrevHour: 880, BlobOK: true}, true
	})
	span := chstore.ExceptionGroup{Fingerprint: "a1b2c3d4e5f60718", Type: "SyntheticTimeoutError", Message: "read timed out",
		Service: "svc-orders", State: chstore.ExStateNew, Occurrences: 900,
		FirstSeen: now.Add(-time.Hour).UnixNano(), LastSeen: now.Add(-time.Minute).UnixNano()}

	items := []InboxItem{exceptionToInbox(span), exceptionToInbox(burst), exceptionToInbox(steady)}
	items, hidden := applyInboxMinOcc(items, effectiveDefaultFloor(currentExceptionTriage()), newFloorExemption(nil))
	if hidden != 0 {
		t.Fatalf("oluşum tabanı Oracle satırı gizledi: %d", hidden)
	}
	rows, _ := inboxDefaultViewRows(t, items, nil, inboxBadgeQuery(""))

	byID := map[string]InboxItem{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	got, ok := byID["exception:"+burst.Fingerprint]
	if !ok {
		t.Fatalf("P1 Oracle grubu varsayılan Problems görünümünde yok: %+v", rows)
	}
	if got.Kind != "exception" || got.Priority != "P1" || !strings.Contains(got.PriorityReason, "patlaması") {
		t.Errorf("Oracle satırı exception + P1 + patlama gerekçesi olmalı: %+v", got)
	}
	if _, ok := byID["exception:"+steady.Fingerprint]; ok {
		t.Error("sürekli akan (P3) Oracle grubu varsayılan P1 görünümüne girmemeli")
	}
	if _, ok := byID["exception:"+span.Fingerprint]; !ok {
		t.Error("span P1 grubu da görünmeli (kıyas)")
	}
}
