package api

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2268 — kod varsayılanı boş; mevcut kurulum göçle eski davranışı korur.

type seedStore struct {
	raw      []byte
	oldest   time.Time
	hasRows  bool
	ageErr   error
	puts     int
	audits   int
	getCalls int
}

func (m *seedStore) GetTopologyHiddenRaw(context.Context) ([]byte, error) {
	m.getCalls++
	return m.raw, nil
}
func (m *seedStore) PutTopologyHiddenRaw(_ context.Context, raw []byte) error {
	m.puts++
	m.raw = raw
	return nil
}
func (m *seedStore) OldestSettingWrite(context.Context) (time.Time, bool, error) {
	return m.oldest, m.hasRows, m.ageErr
}
func (m *seedStore) AppendAudit(context.Context, chstore.AuditEntry) error {
	m.audits++
	return nil
}

func seededPatterns(t *testing.T, raw []byte) []string {
	t.Helper()
	var th topologyHidden
	if err := json.Unmarshal(raw, &th); err != nil {
		t.Fatalf("kayıt çözülemedi: %v (%s)", err, raw)
	}
	return th.Patterns
}

func TestTopologyHiddenCodeDefaultIsEmpty(t *testing.T) {
	if len(defaultTopologyHiddenPatterns) != 0 {
		t.Fatalf("kod varsayılanı boş olmalı, alınan %v", defaultTopologyHiddenPatterns)
	}
	if got := (&Server{}).topologyHiddenPatterns(context.Background()); len(got) != 0 {
		t.Fatalf("store'suz sunucu boş liste dönmeli, alınan %v", got)
	}
}

func TestSeedTopologyHidden(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	t.Run("taze kurulum: boş liste yazılır, ikinci koşu dokunmaz", func(t *testing.T) {
		st := &seedStore{oldest: now.Add(-10 * time.Minute), hasRows: true}
		out, err := seedTopologyHidden(ctx, st, now)
		if err != nil || out != topologyHiddenSeedFresh {
			t.Fatalf("out=%q err=%v", out, err)
		}
		if got := seededPatterns(t, st.raw); len(got) != 0 || got == nil {
			t.Fatalf("taze kurulumda boş (nil olmayan) liste bekleniyordu: %v", got)
		}
		// Sonraki boot'ta kurulum artık "eski" görünse bile karar kalıcı.
		out, err = seedTopologyHidden(ctx, st, now.Add(48*time.Hour))
		if err != nil || out != topologyHiddenSeedPresent || st.puts != 1 || st.audits != 0 {
			t.Fatalf("ikinci koşu: out=%q err=%v puts=%d audits=%d", out, err, st.puts, st.audits)
		}
	})

	t.Run("hiç ayar kaydı yok: taze", func(t *testing.T) {
		st := &seedStore{}
		if out, err := seedTopologyHidden(ctx, st, now); err != nil || out != topologyHiddenSeedFresh {
			t.Fatalf("out=%q err=%v", out, err)
		}
	})

	t.Run("mevcut kurulum, kayıt yok: önceki varsayılan tohumlanır", func(t *testing.T) {
		st := &seedStore{oldest: now.Add(-30 * 24 * time.Hour), hasRows: true}
		out, err := seedTopologyHidden(ctx, st, now)
		if err != nil || out != topologyHiddenSeedSeeded {
			t.Fatalf("out=%q err=%v", out, err)
		}
		if got := seededPatterns(t, st.raw); !reflect.DeepEqual(got, legacyTopologyHiddenSeed) {
			t.Fatalf("tohum %v, beklenen %v", got, legacyTopologyHiddenSeed)
		}
		if st.audits != 1 {
			t.Fatalf("tohumlama audit satırı yazmalı, %d", st.audits)
		}
		// İdempotent.
		if out, _ := seedTopologyHidden(ctx, st, now); out != topologyHiddenSeedPresent || st.puts != 1 {
			t.Fatalf("ikinci koşu yazdı: out=%q puts=%d", out, st.puts)
		}
	})

	t.Run("operatör değeri: dokunulmaz", func(t *testing.T) {
		for _, raw := range []string{`{"patterns":["queue:noise-*"]}`, `{"patterns":[]}`} {
			st := &seedStore{raw: []byte(raw), oldest: now.Add(-30 * 24 * time.Hour), hasRows: true}
			out, err := seedTopologyHidden(ctx, st, now)
			if err != nil || out != topologyHiddenSeedPresent || st.puts != 0 || string(st.raw) != raw {
				t.Fatalf("%s: out=%q err=%v puts=%d raw=%s", raw, out, err, st.puts, st.raw)
			}
		}
	})

	t.Run("kurulum yaşı okunamadı: hiçbir şey yazılmaz", func(t *testing.T) {
		st := &seedStore{ageErr: errors.New("ch down")}
		if _, err := seedTopologyHidden(ctx, st, now); err == nil || st.puts != 0 {
			t.Fatalf("err=%v puts=%d", err, st.puts)
		}
	})
}
