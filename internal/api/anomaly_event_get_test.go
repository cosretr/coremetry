package api

// anomaly_event_get_test.go — v0.10.1032 (operatör: "Anomali ve alert
// rule'lara girdiğimde drawer çıkıyor. Exception gibi detay gözükmüyor.").
//
// Problems kuyruğundaki tam sayfa anomali detayı tekil uçtan
// (GET /api/anomalies/event) okuyor; o uç eskiden zenginleştirmesiz satır
// döndürüyordu ve kök-neden çipi hipotez varken bile "no clear cause yet"
// diyordu. Bu dosya iki şeyi çiviler:
//   1. zincirin SIRASI ve parametreleri (cluster 1 sa → deploy 30 dk → kök
//      neden → karar), tek satırlık dilimde de — sahte zenginleştiriciyle;
//   2. iki uç (liste + tekil) AYNI zinciri çağırır — kaynak pini; biri
//      zincirden koparsa ekran yine veri söylemeyen bir şey söyler.

import (
	"context"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

type recordingEnricher struct {
	calls []string
}

func (r *recordingEnricher) mark(step string, evs []chstore.AnomalyEvent) []chstore.AnomalyEvent {
	r.calls = append(r.calls, step)
	out := make([]chstore.AnomalyEvent, len(evs))
	copy(out, evs)
	for i := range out {
		// Her adım satıra izini bırakır: bir sonraki adım ÖNCEKİNİN
		// çıktısını almalı (zincir, paralel değil).
		out[i].Sample += "|" + step
	}
	return out
}

func (r *recordingEnricher) EnrichAnomaliesWithClusters(_ context.Context, evs []chstore.AnomalyEvent, since time.Duration) []chstore.AnomalyEvent {
	return r.mark("clusters:"+since.String(), evs)
}
func (r *recordingEnricher) EnrichAnomaliesWithDeploys(_ context.Context, evs []chstore.AnomalyEvent, lookback time.Duration) []chstore.AnomalyEvent {
	return r.mark("deploys:"+lookback.String(), evs)
}
func (r *recordingEnricher) EnrichAnomaliesWithRootCause(_ context.Context, evs []chstore.AnomalyEvent) []chstore.AnomalyEvent {
	return r.mark("rootcause", evs)
}
func (r *recordingEnricher) EnrichAnomaliesWithVerdicts(_ context.Context, evs []chstore.AnomalyEvent) []chstore.AnomalyEvent {
	return r.mark("verdicts", evs)
}

// *chstore.Store zinciri karşılamak ZORUNDA — derleme zamanı pini.
var _ anomalyEventEnricher = (*chstore.Store)(nil)

func TestEnrichAnomalyEvents_OrderAndParams(t *testing.T) {
	want := []string{"clusters:1h0m0s", "deploys:30m0s", "rootcause", "verdicts"}
	for _, tc := range []struct {
		name string
		rows []chstore.AnomalyEvent
	}{
		{"tekil okuma (tek satır)", []chstore.AnomalyEvent{{ID: "e1"}}},
		{"liste", []chstore.AnomalyEvent{{ID: "e1"}, {ID: "e2"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingEnricher{}
			got := enrichAnomalyEvents(context.Background(), rec, tc.rows)
			if !reflect.DeepEqual(rec.calls, want) {
				t.Fatalf("zincir sırası = %v, beklenen %v", rec.calls, want)
			}
			if len(got) != len(tc.rows) {
				t.Fatalf("satır sayısı değişti: %d → %d", len(tc.rows), len(got))
			}
			for i := range got {
				if got[i].ID != tc.rows[i].ID {
					t.Fatalf("satır %d kimliği değişti: %q", i, got[i].ID)
				}
				if got[i].Sample != "|"+strings.Join(want, "|") {
					t.Fatalf("adımlar birbirinin çıktısını almıyor: %q", got[i].Sample)
				}
			}
		})
	}
}

// Gövde kesici: paketteki funcBody (guided_problem_total_test.go) — adla
// bulur, ilk sütun-0 kapanışa kadar keser.
func mustBody(t *testing.T, src, name string) string {
	t.Helper()
	b := funcBody(src, name)
	if b == "" {
		t.Fatalf("fonksiyon bulunamadı: %s", name)
	}
	return b
}

func TestAnomalyEventReads_ShareTheEnrichmentChain(t *testing.T) {
	apiSrc, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	getSrc, err := os.ReadFile("anomaly_event_get.go")
	if err != nil {
		t.Fatal(err)
	}
	call := regexp.MustCompile(`enrichAnomalyEvents\(ctx, s\.store, `)
	list := mustBody(t, string(apiSrc), "getAnomalyEvents")
	if !call.MatchString(list) {
		t.Error("liste ucu (getAnomalyEvents) ortak zinciri çağırmıyor")
	}
	one := mustBody(t, string(getSrc), "getAnomalyEvent")
	if !call.MatchString(one) {
		t.Error("tekil uç (getAnomalyEvent) ortak zinciri çağırmıyor — kök-neden çipi yine yalan söyler")
	}
	// Zincir dışında elle Enrich çağrısı kalmasın: biri ötekinden ayrışırdı.
	for name, body := range map[string]string{"getAnomalyEvents": list, "getAnomalyEvent": one} {
		if strings.Contains(body, "EnrichAnomaliesWith") {
			t.Errorf("%s zincir dışında elle Enrich çağırıyor", name)
		}
	}
	// Tekil handler api.go'dan taşındı (api.go yalnız küçülür).
	if strings.Contains(string(apiSrc), "func (s *Server) getAnomalyEvent(") {
		t.Error("getAnomalyEvent api.go'ya geri dönmüş")
	}
	// Route kaydı yerinde.
	// (mux.HandleFunc öneki BİLEREK yazılmadı: make audit CHECK 7 test dosyasını da tarar.)
	if !strings.Contains(string(apiSrc), `"GET /api/anomalies/event", s.getAnomalyEvent)`) {
		t.Error("GET /api/anomalies/event route kaydı değişmiş")
	}
}
