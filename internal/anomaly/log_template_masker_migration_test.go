package anomaly

// log_template_masker_migration_test.go — v0.10.1829: maske sürümü geçişinde
// "yeni şablon" seli olmasın.
//
// Operator-reported: "Sadece bazen loglarda GUID, UUID, id değerleri
// oluyor; bunları yeni log pattern'i algılamasın." Düzeltme Drain maskesini
// değiştirdi (logstore.MaskerVersion 2); etkilenen şablonlar yeni kimlikle,
// first_seen = şimdi doğar. İki koruma: (a) bilinen şablon GÜNCEL maskeyle
// yeniden maskelenip karşılaştırılır, (b) sürüm değişince 24 sa sessiz
// pencere (chstore/log_masker_version.go).

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// (a) Defterdeki v1 satırı (v1 maskesi bu değerleri maskelemiyordu) yeni
// maskeyle doğan adayı bastırır — Remask olmadan yönlendirme öneki farklı
// değişmez belirteçlerde ayrışır ve aday "yeni" çıkardı.
func TestDetectNewLogTemplates_OldMaskerRowSuppressesNewCandidate(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	window := 10 * time.Minute
	since := now.Add(-window).UnixNano()
	born := since + int64(time.Minute)
	cases := []struct{ name, v1Known, v2Cand string }{
		{"pod suffix", "pod orders-api-7d9f6-x2k4p ready", "pod orders-api-<*> ready"},
		{"duration + mixed id", "txn TX9F3K2 took 1500ms", "txn <*> took <*>ms"},
		{"format placeholder", "Record not found: {<*>}.", "Record not found: <*>."},
		{"decimal", "heap <*>.45MB used now", "heap <*>MB used now"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeTemplateLister{resp: [][]chstore.LogTemplate{
				{tplRow("new-id", c.v2Cand, born, 9, "orders-api")},
				{tplRow("old-id", c.v1Known, since-int64(24*time.Hour), 120, "orders-api")},
			}}
			got, err := detectNewLogTemplates(context.Background(), fake, window, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 0 {
				t.Fatalf("old-masker row %q must suppress new candidate %q, got %+v", c.v1Known, c.v2Cand, got)
			}
		})
	}
	// Negatif: gerçekten yeni bir mesaj hâlâ çıkar.
	fake := &fakeTemplateLister{resp: [][]chstore.LogTemplate{
		{tplRow("new-id", "payment gateway refused <*> card", born, 9, "orders-api")},
		{tplRow("old-id", "pod orders-api-7d9f6-x2k4p ready", since-int64(24*time.Hour), 120, "orders-api")},
	}}
	if got, err := detectNewLogTemplates(context.Background(), fake, window, now); err != nil || len(got) != 1 {
		t.Fatalf("genuinely new template must still emit: got %+v err %v", got, err)
	}
}

// (b) Sessiz pencere kapısı: SAF karar tablosu.
func TestMaskerQuietGate(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	cases := []struct {
		name      string
		st        chstore.LogMaskerState
		err       error
		wantQuiet bool
		wantErr   bool
	}{
		{"no blob yet (puller not run) → runs", chstore.LogMaskerState{}, nil, false, false},
		{"settled version → runs", chstore.LogMaskerState{Version: 2}, nil, false, false},
		{"inside quiet window → quiet", chstore.LogMaskerState{Version: 2, QuietUntil: now.Add(time.Hour).UnixNano()}, nil, true, false},
		{"quiet window over → runs", chstore.LogMaskerState{Version: 2, QuietUntil: now.Add(-time.Second).UnixNano()}, nil, false, false},
		{"unreadable → fail-closed", chstore.LogMaskerState{}, errors.New("ch down"), true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, err := maskerQuietGateAt(c.st, c.err, now)
			if q != c.wantQuiet || (err != nil) != c.wantErr {
				t.Fatalf("quiet=%v err=%v; want quiet=%v err=%v", q, err, c.wantQuiet, c.wantErr)
			}
		})
	}
}

// Kapı ulaşılabilir: tek girişte (NewLogTemplateFamilies, v0.10.2085 — eski
// kapılı giriş silindi) çekirdekten (aday okuması) ÖNCE (feedback: test
// edilmiş ama ulaşılamaz).
func TestDetectNewLogTemplates_MaskerGateOrder(t *testing.T) {
	src, err := os.ReadFile("log_template_new_shared.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	i := strings.Index(body, "func NewLogTemplateFamilies(")
	body = body[i:]
	body = body[:strings.Index(body, "\n}\n")]
	gate := strings.Index(body, "maskerQuietGate(store.LogMaskerStateCached(ctx))")
	core := strings.Index(body, "detectNewLogTemplates(")
	if gate < 0 || core < 0 || gate >= core {
		t.Fatalf("gate order wrong: masker=%d core=%d", gate, core)
	}
}
