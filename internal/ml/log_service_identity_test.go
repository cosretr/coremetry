package ml

// v0.10.2045 — Operator-reported: "Burada Api diyor ama Api diye izlenen bir
// servis yok; logtan buluyor." Log ML olayı yalnız katalogdaki servise yazılır:
// birebir eşleşen → aynen; değilse pod adından tek servise çözülen → o servis
// ("orders-api (logda: Api)"); çözülemeyen → yazılmaz. Fikstür nötr.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestResolveLogService(t *testing.T) {
	known := map[string]bool{"orders-api": true, "billing-worker": true, "Api": false}
	ix := buildPodIndex([]string{"orders-api", "billing-worker"}, map[string]chstore.ServiceMetadata{
		"billing-worker": {Deployment: "billing-wrk"},
	})
	cases := []struct {
		name, svc string
		pods      []string
		want      string
		viaPod    bool
		ok        bool
	}{
		{"katalogda birebir", "orders-api", nil, "orders-api", false, true},
		{"pod'dan tek servise", "Api", []string{"orders-api-7d9f6c-x2k4p", "orders-api-7d9f6c-q8w2z"}, "orders-api", true, true},
		{"deployment adıyla pod", "Api", []string{"billing-wrk-5c7d8-abcde"}, "billing-worker", true, true},
		{"iki servise birden uyuyor → çözülemedi", "Api", []string{"orders-api-7d9f6c-x2k4p", "billing-wrk-5c7d8-abcde"}, "", false, false},
		{"hiç eşleşmeyen pod", "Api", []string{"payments-gw-1"}, "", false, false},
		{"pod bilgisi yok", "Api", nil, "", false, false},
		{"büyük/küçük harf eşleşmez", "Orders-Api", nil, "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, via, ok := resolveLogService(c.svc, known, c.pods, ix)
			if got != c.want || via != c.viaPod || ok != c.ok {
				t.Fatalf("= %q %v %v, istenen %q %v %v", got, via, ok, c.want, c.viaPod, c.ok)
			}
		})
	}
}

func TestLogServiceDisplayAndReads(t *testing.T) {
	st := time.Date(2026, 11, 12, 16, 23, 0, 0, time.UTC)
	ev := chstore.MLEvent{ID: "e", Detector: "log_volume", Signal: "log", Service: "orders-api", Observed: 4400, Baseline: 200, WindowSec: 300,
		Direction: "up", StartedAt: st.UnixNano(), LastSeen: st.Add(5 * time.Minute).UnixNano(), Evidence: `{"logService":"Api"}`}
	if logServiceOf(ev) != "Api" {
		t.Fatal("seri / ES okumaları log adıyla")
	}
	in := CardInputFromEvent(ev, st.Add(5*time.Minute), time.UTC)
	if in.LogService != "Api" || in.SeriesService() != "Api" {
		t.Fatalf("girdi: %+v", in)
	}
	if s := ExplainSummary(in); !strings.HasPrefix(s, "orders-api (logda: Api): log hacmi dakikada") {
		t.Fatalf("anlatım: %s", s)
	}
	c := BuildExplainCard(in)
	if !strings.Contains(c.WhereToLook[0].Href, "service=Api") {
		t.Fatalf("Logs bağlantısı log adıyla: %s", c.WhereToLook[0].Href)
	}
	plain := ev
	plain.Evidence = `{}`
	if logServiceOf(plain) != "orders-api" || CardInputFromEvent(plain, st, time.UTC).LogService != "" {
		t.Fatal("eşleşen olayda log adı yok")
	}
}
