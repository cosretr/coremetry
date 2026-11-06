package templater

// masker_version_test.go — v0.10.1829: puller'ın maske sürümü geçişi
// (ensureMaskerVersion) — sürüm değişince blob + sessiz pencere yazılır,
// aynı sürümde yazılmaz, okuma/yazma hatası tiki durdurur (defter yazılmaz).

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

type fakeMaskerStore struct {
	st     chstore.LogMaskerState
	found  bool
	getErr error
	putErr error
	puts   []chstore.LogMaskerState
}

func (f *fakeMaskerStore) GetLogMaskerState(context.Context) (chstore.LogMaskerState, bool, error) {
	return f.st, f.found, f.getErr
}

func (f *fakeMaskerStore) PutLogMaskerState(_ context.Context, st chstore.LogMaskerState) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.puts = append(f.puts, st)
	f.st, f.found = st, true
	return nil
}

func TestEnsureMaskerVersion(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	f := &fakeMaskerStore{}
	if err := ensureMaskerVersion(context.Background(), f, 2, now); err != nil {
		t.Fatal(err)
	}
	if len(f.puts) != 1 || f.puts[0].Version != 2 || f.puts[0].From != 1 || !f.puts[0].QuietAt(now.Add(23*time.Hour)) {
		t.Fatalf("upgrade must write v2 + 24h quiet, got %+v", f.puts)
	}
	// İkinci süreç / sonraki boot: aynı sürüm → yazım yok, pencere uzamaz.
	if err := ensureMaskerVersion(context.Background(), f, 2, now.Add(time.Hour)); err != nil || len(f.puts) != 1 {
		t.Fatalf("same version must not rewrite: puts=%d err=%v", len(f.puts), err)
	}
	if err := ensureMaskerVersion(context.Background(), &fakeMaskerStore{getErr: errors.New("ch")}, 2, now); err == nil {
		t.Fatal("read error must surface (tick skips ledger write)")
	}
	if err := ensureMaskerVersion(context.Background(), &fakeMaskerStore{putErr: errors.New("ch")}, 2, now); err == nil {
		t.Fatal("write error must surface (tick skips ledger write)")
	}
}

// Sıra pini: sürüm kontrolü defter yazımından (UpsertLogTemplates) ÖNCE ve
// hata dalı return eder.
func TestPullerChecksMaskerBeforeUpsert(t *testing.T) {
	src, err := os.ReadFile("puller.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	i := strings.Index(body, "func (p *Puller) tick(")
	body = body[i:]
	body = body[:strings.Index(body, "\n}\n")]
	chk := strings.Index(body, "ensureMaskerVersion(ctx, ms, logstore.MaskerVersion, time.Now())")
	up := strings.Index(body, "p.store.UpsertLogTemplates(")
	if chk < 0 || up < 0 || chk > up {
		t.Fatalf("masker check must precede upsert (check=%d upsert=%d)", chk, up)
	}
	if !strings.Contains(body[chk:up], "return") {
		t.Fatal("masker check error branch must return before the upsert")
	}
}
