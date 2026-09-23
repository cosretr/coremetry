package oracle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.908 (operatör önerisi: "instanceid'de pod adları var, oradan servis
// çıkar") — trace'siz op pod adından çözülür (yalnız canlı servis adı), trace
// varsa trace kazanır, harita pod oylarıyla da öğrenir, canlılık listesi
// okunamazsa pod oyu verilmez.
func TestSubjectResolverPodFallback(t *testing.T) {
	now := time.Date(2026, 9, 23, 21, 0, 0, 0, time.UTC)
	aliveNames := []string{"bsa-mobile-login", "bsa-digital-mobile-pushconfirm-prod"}
	aliveErr := error(nil)
	aliveFn := func(context.Context, time.Duration) ([]string, error) { return aliveNames, aliveErr }
	lookup := func(_ context.Context, ids []string, _, _ time.Time) (map[string]chstore.TraceFact, error) {
		out := map[string]chstore.TraceFact{}
		for _, id := range ids {
			if id == "t1" {
				out[id] = chstore.TraceFact{Service: "bsa-trace-svc"}
			}
		}
		return out, nil
	}
	r := NewSubjectResolver(&fakeState{kv: map[string][]byte{}}, lookup, aliveFn)
	r.now = func() time.Time { return now }
	src := SourceConfig{ID: "o-p", Name: "master-log"}
	rows := []chstore.OracleErrorRow{
		{OperationCode: "LOGIN", InstanceID: "bsa-mobile-login-prod-7b9949bb74-l4bg5"},
		{OperationCode: "LOGIN", InstanceID: "bsa-mobile-login-prod-5459c84bd9-qnfgc"},
		{OperationCode: "LOGIN", InstanceID: "bsa-mobile-login-prod-5459c84bd9-qnfgc"}, // aynı pod tek oy
		{OperationCode: "PUSH", InstanceID: "bsa-digital-mobile-pushconfirm-prod-5b57d5c754-6c2n9"},
		{OperationCode: "TRACED", TraceID: "t1", InstanceID: "bsa-mobile-login-prod-7b9949bb74-l4bg5"},
		{OperationCode: "HOSTONLY", InstanceID: "WMOBAPPP84"},
	}
	r.Observe(context.Background(), src, rows, now.Add(-15*time.Minute), now)
	ctx := context.Background()
	if got := r.Resolve(ctx, "o-p", []string{"LOGIN"}); got.Service != "bsa-mobile-login" || got.Source != "pod" || got.Note != "pod adından (2/2 pod)" {
		t.Fatalf("pod adından: %+v", got)
	}
	if got := r.Resolve(ctx, "o-p", []string{"PUSH"}); got.Service != "bsa-digital-mobile-pushconfirm-prod" || got.Source != "pod" {
		t.Fatalf("tam -prod adı canlıysa o: %+v", got)
	}
	if got := r.Resolve(ctx, "o-p", []string{"TRACED"}); got.Service != "bsa-trace-svc" || got.Source != "trace" {
		t.Fatalf("trace kazanır: %+v", got)
	}
	if got := r.Resolve(ctx, "o-p", []string{"HOSTONLY"}); got.Service != "" {
		t.Fatalf("pod biçimi olmayan değer servis üretmez: %+v", got)
	}
	if e := r.Learned(ctx, "o-p").Entries["LOGIN"]; e == nil || e.Service != "bsa-mobile-login" || e.Hits != 2 {
		t.Fatalf("harita pod oylarıyla öğrenmeli: %+v", e)
	}
	// Canlılık listesi okunamazsa pod oyu YOK (uydurma servis yok).
	r2 := NewSubjectResolver(&fakeState{kv: map[string][]byte{}}, lookup, aliveFn)
	r2.now = func() time.Time { return now }
	aliveErr = errors.New("CH down")
	r2.Observe(ctx, src, rows[:2], now.Add(-15*time.Minute), now)
	if got := r2.Resolve(ctx, "o-p", []string{"LOGIN"}); got.Service != "" {
		t.Fatalf("doğrulanamayan pod adı servis olmamalı: %+v", got)
	}
}
