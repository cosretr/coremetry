package notify

// merge_hold_test.go — v0.10.2341 (entity grafı S4): sonradan bağlanan üyenin
// tutması mevcut enforce kapısından geçer; S4 kapalıyken kaynak hiç sorulmaz
// ve karar / gölge satırı bugünküyle aynı.

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

type fakeMergeSrc struct {
	mu    sync.Mutex
	calls int
	hold  chstore.MergeHold
	ok    bool
}

func (f *fakeMergeSrc) MergeHold(context.Context, chstore.Problem) (chstore.MergeHold, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.hold, f.ok
}

func excProblem(fp, prio string) chstore.Problem {
	g := chstore.ExceptionGroup{Fingerprint: fp, Service: "checkout-api", Type: "java.lang.IllegalStateException", Occurrences: 900,
		FirstSeen: time.Now().Add(-10 * time.Minute).UnixNano(), LastSeen: time.Now().UnixNano()}
	p := exceptionGroupProblem(g, chstore.ExStateNew, prio, "patlama")
	p.Status = "open"
	return p
}

func mergeNotifier(src *fakeMergeSrc, ownerOpen bool) (*Notifier, chstore.Problem) {
	n := enforceNotifier()
	owner := excProblem("fp-owner", "P2")
	src.hold = chstore.MergeHold{Rule: chstore.GraphRuleCall, OwnerID: owner.ID, RefPriority: "P2", Owner: &owner}
	src.ok = true
	n.SetMergeHold(src)
	st := chstore.NotifyThreadResolved
	if ownerOpen {
		st = chstore.NotifyThreadOpen
	}
	n.threads.m = map[string]*chstore.NotifyThread{threadKey(owner): {State: st}}
	return n, owner
}

func TestMergeHoldOffIsByteIdentical(t *testing.T) {
	defer chstore.SetProblemGraphMergeEnforceForTest(false)()
	ctx := context.Background()
	for _, mode := range []string{chstore.NotifyModeEnforce, chstore.NotifyModeShadow} {
		withPolicyMode(t, mode)
		src := &fakeMergeSrc{}
		withSrc, _ := mergeNotifier(src, true)
		without := enforceNotifier()
		without.threads.m = withSrc.threads.m
		for _, prio := range []string{"P1", "P2"} {
			p := excProblem("fp-member-"+prio, prio)
			ok1, h1 := withSrc.enforceGate(ctx, p)
			ok2, h2 := without.enforceGate(ctx, p)
			if ok1 != ok2 || h1 != h2 {
				t.Fatalf("%s %s: kapalı S4 kararı değiştirdi: %v %q vs %v %q", mode, prio, ok1, h1, ok2, h2)
			}
			// Gölge satırı da aynı.
			var rows [2][]chstore.NotificationLog
			for i, n := range []*Notifier{withSrc, without} {
				i := i
				n.shadow.sink = func(_ context.Context, e chstore.NotificationLog) error { rows[i] = append(rows[i], e); return nil }
				n.recordShadowDecision(ctx, p, chstore.CurrentNotifyPolicy(), time.Now())
			}
			if !reflect.DeepEqual(rows[0], rows[1]) {
				t.Fatalf("gölge satırı değişti: %+v vs %+v", rows[0], rows[1])
			}
		}
		if src.calls != 0 {
			t.Fatalf("S4 kapalıyken kaynak sorulmamalı (%d)", src.calls)
		}
	}
}

func TestMergeHoldEnforce(t *testing.T) {
	defer chstore.SetProblemGraphMergeEnforceForTest(true)()
	withPolicyMode(t, chstore.NotifyModeEnforce)
	ctx := context.Background()
	src := &fakeMergeSrc{}
	n, _ := mergeNotifier(src, true)
	member := excProblem("fp-member", "P2")
	if ok, hold := n.enforceGate(ctx, member); ok || hold != chstore.NotifyHoldMerged {
		t.Fatalf("sonradan bağlanan P2 üye tutulmalı: %v %q", ok, hold)
	}
	// Aynı üye P1'e çıkar: yükseliş bildirimi gider (baypas).
	esc := excProblem("fp-member", "P1")
	if ok, hold := n.enforceGate(ctx, esc); !ok {
		t.Fatalf("P1'e çıkan üye bildirilmeli: %q", hold)
	}
	// Sahibin açılışı gitmemişse (konu açık değil) üye tutulmaz: kümeden kimse
	// bildirilmemiş olmasın.
	n2, _ := mergeNotifier(&fakeMergeSrc{}, false)
	if ok, hold := n2.enforceGate(ctx, excProblem("fp-member2", "P2")); !ok {
		t.Fatalf("sahip bildirilmemişken tutma yok: %q", hold)
	}
	// Kaynak karar veremezse tutma yok.
	n3 := enforceNotifier()
	n3.SetMergeHold(&fakeMergeSrc{ok: false})
	if ok, hold := n3.enforceGate(ctx, excProblem("fp-member3", "P2")); !ok {
		t.Fatalf("üye değil: gider (%q)", hold)
	}
}

func TestMergeHoldShadowRow(t *testing.T) {
	defer chstore.SetProblemGraphMergeEnforceForTest(true)()
	withPolicyMode(t, chstore.NotifyModeShadow)
	n, _ := mergeNotifier(&fakeMergeSrc{}, true)
	var rows []chstore.NotificationLog
	n.shadow.sink = func(_ context.Context, e chstore.NotificationLog) error { rows = append(rows, e); return nil }
	n.recordShadowDecision(context.Background(), excProblem("fp-member", "P2"), chstore.CurrentNotifyPolicy(), time.Now())
	if len(rows) == 0 || rows[0].Error != "hold:"+chstore.NotifyHoldMerged {
		t.Fatalf("gölge kipinde politika satırı hold:merged: %+v", rows)
	}
	// Gölge kipinde gönderim değişmez (enforce kapısı no-op).
	if ok, _ := n.enforceGate(context.Background(), excProblem("fp-member", "P2")); !ok {
		t.Fatal("gölge kipinde gönderim değişmemeli")
	}
}
