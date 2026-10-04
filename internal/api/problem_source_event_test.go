// problem_source_event_test.go — v0.10.1106: terfi Problem'inin (`anomaly-auto:`)
// detayı kaynak olayın "Desen sayısı" grafiğini çizer (v0.10.1060 ertelemesi
// kalktı, operatör isteği).
//
// Pinler: (1) problem kimliği → olay kimliği ayrıştırması tek kaynaktan
// (chstore.PromotedAnomalyEventID): terfi Problem'i → TEK okuma o parmak iziyle;
// terfi olmayan / bozuk kimlik → okuma YOK, alan yok; (2) olay yok → alan JSON'da
// hiç yok; (3) okuma hatası yutulmaz; (4) önbellek anahtarı yalnız olay kimliğini
// taşır ve olaylar arasında ayrışır; (5) rota kendi dosyasında, defterde.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

type fakeSourceEventReader struct {
	ev    *chstore.PromotedSourceEvent
	err   error
	calls []string
}

func (f *fakeSourceEventReader) GetPromotedSourceEvent(_ context.Context, id string) (*chstore.PromotedSourceEvent, error) {
	f.calls = append(f.calls, id)
	return f.ev, f.err
}

func TestResolveProblemSourceEvent(t *testing.T) {
	fp := chstore.FingerprintAnomaly("log_pattern", "oracle-tns", "svc-orders")
	ev := &chstore.PromotedSourceEvent{ID: fp, Kind: "log_pattern", Pattern: "oracle-tns", Service: "svc-orders",
		StartedAt: 1_000, LastSeen: 2_000, Status: "active"}
	cases := []struct {
		name      string
		problemID string
		found     *chstore.PromotedSourceEvent
		wantRead  string // "" = okuma yok
		wantField bool
	}{
		{"terfi Problem'i (problem kimliği, servis son ekli)", chstore.PromotedAnomalyRulePrefix + fp + ":svc-orders", ev, fp, true},
		{"terfi Problem'i (servissiz son ek)", chstore.PromotedAnomalyRulePrefix + fp + ":", ev, fp, true},
		{"saklanan rule_id şekli", chstore.PromotedAnomalyRulePrefix + fp, ev, fp, true},
		{"terfi Problem'i, olay yok (TTL) → alan yok", chstore.PromotedAnomalyRulePrefix + fp + ":svc-orders", nil, fp, false},
		{"metrik dedektörü", "anomaly:payments-api:p99_ms", ev, "", false},
		{"küme", "anomaly-cluster:payments-api", ev, "", false},
		{"alarm kuralı", "builtin-error-rate-15pct:payments-api", ev, "", false},
		{"bozuk parmak izi (15 hex)", chstore.PromotedAnomalyRulePrefix + fp[:15], ev, "", false},
		{"parmak izinden sonra ':' yok", chstore.PromotedAnomalyRulePrefix + fp + "x", ev, "", false},
		{"boş", "", ev, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rd := &fakeSourceEventReader{ev: c.found}
			got, err := resolveProblemSourceEvent(context.Background(), rd, c.problemID)
			if err != nil {
				t.Fatal(err)
			}
			if c.wantRead == "" {
				if len(rd.calls) != 0 {
					t.Errorf("terfi olmayan kimlikte okuma yapıldı: %v", rd.calls)
				}
			} else if len(rd.calls) != 1 || rd.calls[0] != c.wantRead {
				t.Errorf("okumalar = %v, want tek okuma %q", rd.calls, c.wantRead)
			}
			b, _ := json.Marshal(got)
			if has := strings.Contains(string(b), `"sourceEvent"`); has != c.wantField {
				t.Errorf("JSON = %s, sourceEvent alanı var=%v want %v", b, has, c.wantField)
			}
		})
	}
	t.Run("okuma hatası yutulmaz", func(t *testing.T) {
		rd := &fakeSourceEventReader{err: errors.New("CH down")}
		if _, err := resolveProblemSourceEvent(context.Background(), rd, chstore.PromotedAnomalyRulePrefix+fp+":svc-orders"); err == nil {
			t.Error("hata yutuldu — boş cevap 30 s önbelleğe yazılırdı")
		}
	})
}

func TestProblemSourceEventKey(t *testing.T) {
	a := chstore.FingerprintAnomaly("log_pattern", "oracle-tns", "svc-orders")
	b := chstore.FingerprintAnomaly("log_pattern", "oracle-tns", "payments-api")
	if problemSourceEventKey(a) == problemSourceEventKey(b) {
		t.Error("iki farklı olay aynı önbellek girdisini paylaşıyor (v0.5.187 sınıfı)")
	}
	if got := problemSourceEventKey(a); got != "problem-source-event:v1:"+a {
		t.Errorf("anahtar = %q — yalnız olay kimliğini taşımalı (servis son eki cevabı değiştirmez)", got)
	}
}

// TestProblemSourceEventRouteOwnFile — rota kendi dosyasında, defterden; api.go'da yok.
func TestProblemSourceEventRouteOwnFile(t *testing.T) {
	src, err := os.ReadFile("problem_source_event.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{
		`registerRoutesExtra("problem-source-event"`,
		`"GET /api/problems/{id}/source-event"`,
		`s.serveCached(`,
	} {
		if !strings.Contains(string(src), must) {
			t.Errorf("problem_source_event.go %q içermiyor", must)
		}
	}
	apiSrc, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(apiSrc), "source-event") {
		t.Error("rota api.go'ya yazılmış — kendi dosyasında kalmalı")
	}
}
