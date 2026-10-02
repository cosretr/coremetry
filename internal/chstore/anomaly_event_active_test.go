package chstore

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// v0.10.1046 — batch gecikme kapısının aktif-olay okuması: tür, (isteğe bağlı)
// pattern, aktiflik ve servis koşulu SQL'de (LIMIT yalnız ilgili satırlarda
// ısırır), sınırlı.
func TestActiveAnomalyKeysQueryShape(t *testing.T) {
	sens := AnomalySensitivityConfig{} // varsayılan ["-batch"]
	cond, cargs := sens.BatchServiceSQL("service")
	q, args := activeAnomalyKeysQuery("trace_op_latency", "", 15*time.Minute, cond, cargs, 201)

	for _, frag := range []string{
		"FROM anomaly_events FINAL",
		"WHERE kind = ?",
		"AND last_seen >= now64() - INTERVAL ? SECOND",
		"AND " + cond,
		"ORDER BY last_seen DESC, id",
		"LIMIT ?",
		"SETTINGS max_execution_time = 5",
	} {
		if !strings.Contains(q, frag) {
			t.Fatalf("sorgu %q taşımıyor:\n%s", frag, q)
		}
	}
	if strings.Contains(q, "pattern = ?") {
		t.Fatal("boş pattern'de pattern koşulu eklendi")
	}
	// Servis koşulu LIMIT'ten ÖNCE (WHERE'de): tavan batch dışı satırlara harcanmaz.
	if strings.Index(q, cond) > strings.Index(q, "LIMIT ?") {
		t.Fatal("servis koşulu LIMIT'ten sonra")
	}
	want := []any{"trace_op_latency", int64(900), "-batch", 201}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("argümanlar %v, beklenen %v", args, want)
	}
	if strings.Count(q, "?") != len(args) {
		t.Fatalf("yer tutucu %d, argüman %d", strings.Count(q, "?"), len(args))
	}

	// Pattern dolu: WHERE'de (tavan diğer pattern'lere harcanmaz); age 0 → 10 dk.
	q2, args2 := activeAnomalyKeysQuery("behavior_change", "P99 · x", 0, cond, cargs, 10)
	if !strings.Contains(q2, "AND pattern = ?") || strings.Index(q2, "pattern = ?") > strings.Index(q2, "LIMIT ?") {
		t.Fatalf("pattern koşulu WHERE'de değil:\n%s", q2)
	}
	if !reflect.DeepEqual(args2, []any{"behavior_change", int64(600), "P99 · x", "-batch", 10}) {
		t.Fatalf("argümanlar %v", args2)
	}

	// Servis koşulu yoksa daraltma yok.
	q3, args3 := activeAnomalyKeysQuery("behavior_change", "", 5*time.Minute, "", nil, 10)
	if strings.Contains(q3, "positionCaseInsensitive") || !reflect.DeepEqual(args3, []any{"behavior_change", int64(300), 10}) {
		t.Fatalf("koşulsuz sorgu beklenmedik:\n%s\n%v", q3, args3)
	}
}
