package notify

// frequent_shadow_test.go — v0.10.2280 (entity grafı S3): "sık tekrar eden"
// gölge kararı bildirim kararını DEĞİŞTİRMEZ. Kanıt: aynı Problem için
// kaynaklı ve kaynaksız iki Notifier'ın politika gölge satırı bayt bayt
// aynı, enforce kapısının kararı aynı ve kapı kaynağı hiç sormaz; kaynaklı
// olan yalnız AYRI bir `frequent:*` satırı ekler.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

type fakeFreqSource struct {
	mu    sync.Mutex
	calls int
	v     chstore.FrequentVerdict
}

func (f *fakeFreqSource) FrequentShadowVerdict(context.Context, chstore.Problem) (chstore.FrequentVerdict, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.v, true
}

func TestFrequentShadowNeverChangesNotifyDecision(t *testing.T) {
	src := &fakeFreqSource{v: chstore.FrequentVerdict{Signature: "svc:bff-web|error|", Frequent: true, Kind: chstore.FrequentKindHold, Prior24h: 4, Days7d: 2}}
	var muA, muB sync.Mutex
	var rowsA, rowsB []chstore.NotificationLog
	with := shadowTestNotifier(&rowsA, &muA)
	with.SetFrequentShadow(src)
	without := shadowTestNotifier(&rowsB, &muB)

	p := shadowProblem()
	now := time.Now()
	for _, mode := range []string{chstore.NotifyModeShadow, chstore.NotifyModeEnforce} {
		pol := chstore.DefaultNotifyPolicy()
		pol.Mode = mode
		rowsA, rowsB = nil, nil
		with.recordShadowDecision(context.Background(), p, pol, now)
		without.recordShadowDecision(context.Background(), p, pol, now)
		if len(rowsB) != 1 || len(rowsA) != 2 {
			t.Fatalf("%s: rows with=%d without=%d", mode, len(rowsA), len(rowsB))
		}
		ja, _ := json.Marshal(rowsA[0])
		jb, _ := json.Marshal(rowsB[0])
		if string(ja) != string(jb) {
			t.Fatalf("%s: policy shadow row changed by the frequent verdict:\n%s\n%s", mode, ja, jb)
		}
		if rowsA[1].Error != chstore.FrequentShadowHold || rowsA[1].ChannelKind != chstore.NotifyShadowChannelKind || rowsA[1].RelatedID != p.ID {
			t.Fatalf("%s: frequent row = %+v", mode, rowsA[1])
		}
	}

	// enforce kipinde gönderim kapısı: kaynaklı / kaynaksız aynı karar; kaynak sorulmaz.
	prev := chstore.CurrentNotifyPolicy()
	defer chstore.SetNotifyPolicy(prev)
	pol := chstore.DefaultNotifyPolicy()
	pol.Mode = chstore.NotifyModeEnforce
	chstore.SetNotifyPolicy(pol)
	src.mu.Lock()
	before := src.calls
	src.mu.Unlock()
	for i := 0; i < 3; i++ {
		okA, holdA := with.enforceGate(context.Background(), p)
		okB, holdB := without.enforceGate(context.Background(), p)
		if okA != okB || holdA != holdB {
			t.Fatalf("enforce gate differs: with=(%v,%q) without=(%v,%q)", okA, holdA, okB, holdB)
		}
	}
	src.mu.Lock()
	defer src.mu.Unlock()
	if src.calls != before {
		t.Fatalf("enforce gate consulted the frequent source (%d calls)", src.calls-before)
	}
}

func TestFrequentShadowRowNotWrittenWhenNotFrequent(t *testing.T) {
	if _, ok := frequentShadowRow(shadowProblem(), chstore.NotifyDecision{Notifiable: true}, chstore.FrequentVerdict{}, "shadow"); ok {
		t.Fatal("no row for a non-frequent verdict")
	}
	r, ok := frequentShadowRow(shadowProblem(), chstore.NotifyDecision{Hold: chstore.NotifyHoldImpactErr}, chstore.FrequentVerdict{Frequent: true, Kind: chstore.FrequentKindHold}, "shadow")
	if !ok || r.Error != chstore.FrequentShadowEarlier {
		t.Fatalf("earlier gate: %+v", r)
	}
}
