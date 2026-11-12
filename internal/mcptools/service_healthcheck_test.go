package mcptools

// service_healthcheck_test.go — v0.10.2039 (servis sağlık kontrolü, dilim 1).
//
// SÖZLEŞME: hüküm KURALLARDAN çıkar (model yok). Her kontrol dört durumdan
// birini verir: ok (İyi) / warn (Dikkat) / crit (Sorun) / unknown
// (Ölçülemedi). "Ölçülemedi" ASLA "İyi" sayılmaz: okunamayan kaynak, boş
// pencere, yapılandırılmamış Thanos unknown döner; özet cümle bunu söyler.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/thanos"
)

func TestHCClassifyRED(t *testing.T) {
	red := func(spans, errs uint64, p95 float64) chstore.ServiceWindowRED {
		return chstore.ServiceWindowRED{Spans: spans, Errors: errs, P95Ms: p95}
	}
	cases := []struct {
		name    string
		cur     chstore.ServiceWindowRED
		prior   chstore.ServiceWindowRED
		priorOK bool
		err     error
		want    string
		seen    string // alt dize
	}{
		{"okuma hatası → ölçülemedi", red(0, 0, 0), red(0, 0, 0), false, errors.New("boom"), HCUnknown, "okunamadı"},
		{"zaman aşımı → ölçülemedi", red(0, 0, 0), red(0, 0, 0), false, context.DeadlineExceeded, HCUnknown, "zaman aşımı"},
		{"span yok → ölçülemedi (İyi değil)", red(0, 0, 0), red(100, 0, 50), true, nil, HCUnknown, "span yok"},
		{"temiz", red(1000, 4, 120), red(1000, 3, 110), true, nil, HCOK, "%0.40"},
		{"hata %2 → dikkat", red(1000, 20, 120), red(1000, 3, 110), true, nil, HCWarn, "%2.00"},
		{"hata %5 → sorun", red(1000, 50, 120), red(1000, 3, 110), true, nil, HCCrit, "%5.00"},
		{"az hata (taban altı) yüksek oran → iyi", red(40, 4, 120), red(40, 0, 110), true, nil, HCOK, "%10.00"},
		{"p95 1.5x → dikkat", red(1000, 0, 300), red(1000, 0, 200), true, nil, HCWarn, "1.5×"},
		{"p95 2x → sorun", red(1000, 0, 400), red(1000, 0, 200), true, nil, HCCrit, "2.0×"},
		{"p95 2x ama mutlak artış küçük → iyi", red(1000, 0, 8), red(1000, 0, 4), true, nil, HCOK, "p95 8 ms"},
		{"önceki pencere yok → yalnız hata oranı", red(1000, 0, 900), red(0, 0, 0), false, nil, HCOK, "önceki pencere okunamadı"},
		{"önceki pencerede az span → kıyas yok", red(1000, 0, 900), red(5, 0, 100), true, nil, HCOK, "p95 900 ms"},
		{"hata sorun + gecikme dikkat → sorun", red(1000, 60, 300), red(1000, 0, 200), true, nil, HCCrit, "%6.00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := hcClassifyRED("orders-api", c.cur, c.prior, c.priorOK, c.err)
			if r.State != c.want {
				t.Fatalf("state = %q, want %q (seen %q)", r.State, c.want, r.Seen)
			}
			if !strings.Contains(r.Seen, c.seen) {
				t.Fatalf("seen = %q, %q içermeli", r.Seen, c.seen)
			}
			if r.Check != HCCheckRED || r.Title == "" || r.Link == "" {
				t.Fatalf("satır alanları eksik: %+v", r)
			}
			if r.State != HCOK && r.State != HCUnknown && r.Short == "" {
				t.Fatalf("sapan satırın kısa adı yok: %+v", r)
			}
		})
	}
}

func TestHCClassifyOpenItems(t *testing.T) {
	row := func(kind, prio, title string) InboxRow {
		return InboxRow{Kind: kind, Priority: prio, Title: title, PageURL: "/problems?x=" + title}
	}
	cases := []struct {
		name          string
		oi            InboxOpenResult
		err           error
		wantProblems  string
		wantException string
	}{
		{"okuma hatası → ikisi de ölçülemedi", InboxOpenResult{}, errors.New("x"), HCUnknown, HCUnknown},
		{"boş → ikisi de iyi", InboxOpenResult{}, nil, HCOK, HCOK},
		{"P3 problem → dikkat", InboxOpenResult{Rows: []InboxRow{row("problem", "P3", "a")}, Total: 1}, nil, HCWarn, HCOK},
		{"P2 problem → sorun", InboxOpenResult{Rows: []InboxRow{row("problem", "P2", "a")}, Total: 1}, nil, HCCrit, HCOK},
		{"P1 incident → sorun", InboxOpenResult{Rows: []InboxRow{row("incident", "P1", "a")}, Total: 1}, nil, HCCrit, HCOK},
		{"P1 exception → exception sorun", InboxOpenResult{Rows: []InboxRow{row("exception", "P1", "NPE")}, Total: 1}, nil, HCOK, HCCrit},
		{"P2 httperror → exception dikkat", InboxOpenResult{Rows: []InboxRow{row("httperror", "P2", "503")}, Total: 1}, nil, HCOK, HCWarn},
		{"yalnız P3 exception → iyi (izlemede)", InboxOpenResult{Rows: []InboxRow{row("exception", "P3", "x")}, Total: 1}, nil, HCOK, HCOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := hcClassifyProblems("orders-api", c.oi, c.err)
			e := hcClassifyExceptions("orders-api", c.oi, c.err)
			if p.State != c.wantProblems || e.State != c.wantException {
				t.Fatalf("problems=%q (%s) exceptions=%q (%s); want %q / %q", p.State, p.Seen, e.State, e.Seen, c.wantProblems, c.wantException)
			}
			if p.Link == "" || e.Link == "" {
				t.Fatalf("bağlantı boş: %+v %+v", p, e)
			}
		})
	}
}

func TestHCClassifyAnomalies(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	ev := func(stage, verdict string, lastAgo time.Duration) chstore.MLEvent {
		return chstore.MLEvent{ID: "e", Detector: "error_rate", Signal: "trace", Stage: stage, Verdict: verdict,
			StartedAt: now.Add(-time.Hour).UnixNano(), LastSeen: now.Add(-lastAgo).UnixNano()}
	}
	cases := []struct {
		name string
		evs  []chstore.MLEvent
		err  error
		want string
	}{
		{"hata → ölçülemedi", nil, errors.New("x"), HCUnknown},
		{"olay yok → iyi", nil, nil, HCOK},
		{"süren gölge olay → dikkat", []chstore.MLEvent{ev(chstore.MLStageShadow, "", 2*time.Minute)}, nil, HCWarn},
		{"süren terfi olay → sorun", []chstore.MLEvent{ev(chstore.MLStagePromote, "", 2*time.Minute)}, nil, HCCrit},
		{"bitmiş (eski last_seen) → iyi", []chstore.MLEvent{ev(chstore.MLStageShadow, "", 40*time.Minute)}, nil, HCOK},
		{"kapanmış → iyi", []chstore.MLEvent{ev(chstore.MLStageClosed, "", time.Minute)}, nil, HCOK},
		{"kronik → iyi", []chstore.MLEvent{ev(chstore.MLStageChronic, "", time.Minute)}, nil, HCOK},
		{"operatör 'anomali değil' dedi → iyi", []chstore.MLEvent{ev(chstore.MLStagePromote, chstore.VerdictNotAnomaly, time.Minute)}, nil, HCOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := hcClassifyAnomalies("orders-api", c.evs, now, c.err)
			if r.State != c.want {
				t.Fatalf("state = %q, want %q (seen %q)", r.State, c.want, r.Seen)
			}
		})
	}
}

func TestHCClassifyDeploys(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) int64 { return now.Add(-ago).UnixNano() }
	cases := []struct {
		name string
		rows []ChangeRow
		err  error
		want string
	}{
		{"hata → ölçülemedi", nil, errors.New("x"), HCUnknown},
		{"deploy yok → iyi", nil, nil, HCOK},
		{"son 30 dk'da deploy → dikkat", []ChangeRow{{Source: "rollout", TimeUnixNs: at(12 * time.Minute), Workload: "orders-api", Version: "42", PrevVersion: "41"}}, nil, HCWarn},
		{"eski deploy → iyi", []ChangeRow{{Source: "inferred", TimeUnixNs: at(50 * time.Minute), Version: "1.4.2"}}, nil, HCOK},
		{"takılmış rollout → sorun", []ChangeRow{{Source: "rollout", TimeUnixNs: at(50 * time.Minute), Status: "stalled", Workload: "orders-api"}}, nil, HCCrit},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := hcClassifyDeploys("orders-api", c.rows, now, c.err)
			if r.State != c.want {
				t.Fatalf("state = %q, want %q (seen %q)", r.State, c.want, r.Seen)
			}
		})
	}
}

func TestHCClassifyPods(t *testing.T) {
	f := func(v float64) map[string]float64 { return map[string]float64{"shop\x00orders-api-1": v} }
	pod := func(memPct, cpuPct float64, term, phase string) thanos.PodRow {
		return thanos.PodRow{Namespace: "shop", Pod: "orders-api-1", MemPct: memPct, CPUPct: cpuPct, LastTermReason: term, Phase: phase, CPUCores: 0.1, MemBytes: 1}
	}
	cl := func(name string, rows []thanos.PodRow, restarts map[string]float64, waiting map[string]string) ServicePodsCluster {
		return ServicePodsCluster{ID: name, Name: name, Rows: rows, Restarts: restarts, RestartsOK: restarts != nil, Waiting: waiting, WaitingOK: waiting != nil}
	}
	type want struct{ restarts, waiting, resources string }
	cases := []struct {
		name    string
		res     ServicePodsResult
		err     error
		skipped bool
		want    []want // cluster başına
	}{
		{"Thanos yok → üçü ölçülemedi", ServicePodsResult{}, nil, true, []want{{HCUnknown, HCUnknown, HCUnknown}}},
		{"okuma hatası → üçü ölçülemedi", ServicePodsResult{Thanos: true}, errors.New("x"), false, []want{{HCUnknown, HCUnknown, HCUnknown}}},
		{"temiz", ServicePodsResult{Thanos: true, Clusters: []ServicePodsCluster{cl("a", []thanos.PodRow{pod(40, 20, "", "Running")}, f(0), map[string]string{})}}, nil, false,
			[]want{{HCOK, HCOK, HCOK}}},
		{"1 restart → dikkat", ServicePodsResult{Thanos: true, Clusters: []ServicePodsCluster{cl("a", []thanos.PodRow{pod(40, 20, "Error", "Running")}, f(1), map[string]string{})}}, nil, false,
			[]want{{HCWarn, HCOK, HCOK}}},
		{"3 restart → sorun", ServicePodsResult{Thanos: true, Clusters: []ServicePodsCluster{cl("a", []thanos.PodRow{pod(40, 20, "Error", "Running")}, f(3), map[string]string{})}}, nil, false,
			[]want{{HCCrit, HCOK, HCOK}}},
		{"OOMKilled + restart → sorun", ServicePodsResult{Thanos: true, Clusters: []ServicePodsCluster{cl("a", []thanos.PodRow{pod(40, 20, "OOMKilled", "Running")}, f(1), map[string]string{})}}, nil, false,
			[]want{{HCCrit, HCOK, HCOK}}},
		{"eski OOM (pencerede restart yok) → iyi", ServicePodsResult{Thanos: true, Clusters: []ServicePodsCluster{cl("a", []thanos.PodRow{pod(40, 20, "OOMKilled", "Running")}, f(0), map[string]string{})}}, nil, false,
			[]want{{HCOK, HCOK, HCOK}}},
		{"restart okunamadı → ölçülemedi", ServicePodsResult{Thanos: true, Clusters: []ServicePodsCluster{cl("a", []thanos.PodRow{pod(40, 20, "", "Running")}, nil, map[string]string{})}}, nil, false,
			[]want{{HCUnknown, HCOK, HCOK}}},
		{"CrashLoop → bekleyen sorun", ServicePodsResult{Thanos: true, Clusters: []ServicePodsCluster{cl("a", []thanos.PodRow{pod(40, 20, "", "Running")}, f(0), map[string]string{"shop\x00orders-api-1": "CrashLoopBackOff"})}}, nil, false,
			[]want{{HCOK, HCCrit, HCOK}}},
		{"Pending → bekleyen dikkat", ServicePodsResult{Thanos: true, Clusters: []ServicePodsCluster{cl("a", []thanos.PodRow{pod(40, 20, "", "Pending")}, f(0), map[string]string{})}}, nil, false,
			[]want{{HCOK, HCWarn, HCOK}}},
		{"bekleme okunamadı → ölçülemedi", ServicePodsResult{Thanos: true, Clusters: []ServicePodsCluster{cl("a", []thanos.PodRow{pod(40, 20, "", "Running")}, f(0), nil)}}, nil, false,
			[]want{{HCOK, HCUnknown, HCOK}}},
		{"bellek %85 → dikkat", ServicePodsResult{Thanos: true, Clusters: []ServicePodsCluster{cl("a", []thanos.PodRow{pod(85, 20, "", "Running")}, f(0), map[string]string{})}}, nil, false,
			[]want{{HCOK, HCOK, HCWarn}}},
		{"bellek %96 → sorun", ServicePodsResult{Thanos: true, Clusters: []ServicePodsCluster{cl("a", []thanos.PodRow{pod(96, 20, "", "Running")}, f(0), map[string]string{})}}, nil, false,
			[]want{{HCOK, HCOK, HCCrit}}},
		{"CPU %92 → dikkat", ServicePodsResult{Thanos: true, Clusters: []ServicePodsCluster{cl("a", []thanos.PodRow{pod(40, 92, "", "Running")}, f(0), map[string]string{})}}, nil, false,
			[]want{{HCOK, HCOK, HCWarn}}},
		{"limit yok → oran ölçülemedi", ServicePodsResult{Thanos: true, Clusters: []ServicePodsCluster{cl("a", []thanos.PodRow{pod(0, 0, "", "Running")}, f(0), map[string]string{})}}, nil, false,
			[]want{{HCOK, HCOK, HCUnknown}}},
		{"cluster hatası → o cluster ölçülemedi, diğeri ölçülür",
			ServicePodsResult{Thanos: true, Clusters: []ServicePodsCluster{
				{ID: "b", Name: "b", Err: "timeout"},
				cl("a", []thanos.PodRow{pod(40, 20, "", "Running")}, f(0), map[string]string{})}}, nil, false,
			[]want{{HCUnknown, HCUnknown, HCUnknown}, {HCOK, HCOK, HCOK}}},
		{"pod'u olmayan cluster düşer (servis orada koşmuyor)",
			ServicePodsResult{Thanos: true, Clusters: []ServicePodsCluster{
				cl("empty", nil, map[string]float64{}, map[string]string{}),
				cl("a", []thanos.PodRow{pod(40, 20, "", "Running")}, f(0), map[string]string{})}}, nil, false,
			[]want{{HCOK, HCOK, HCOK}}},
		{"hiçbir cluster'da pod yok → ölçülemedi",
			ServicePodsResult{Thanos: true, Clusters: []ServicePodsCluster{cl("empty", nil, map[string]float64{}, map[string]string{})}}, nil, false,
			[]want{{HCUnknown, HCUnknown, HCUnknown}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows := hcClassifyPods("orders-api", c.res, c.err, c.skipped)
			if len(rows) != 3*len(c.want) {
				t.Fatalf("satır sayısı %d, want %d: %+v", len(rows), 3*len(c.want), rows)
			}
			// Sıra: kontrol önce (restart ×N, bekleyen ×N, kaynak ×N), cluster sonra.
			n := len(c.want)
			for i, w := range c.want {
				got := want{rows[i].State, rows[n+i].State, rows[2*n+i].State}
				if got != w {
					t.Fatalf("cluster %d: got %+v want %+v\n%+v", i, got, w, rows)
				}
				if rows[i].Check != HCCheckPodRestarts || rows[n+i].Check != HCCheckPodWaiting || rows[2*n+i].Check != HCCheckPodResources {
					t.Fatalf("kontrol sırası bozuk: %+v", rows)
				}
			}
		})
	}
}

func TestHCSummarize(t *testing.T) {
	r := func(state, short string) HealthcheckRow {
		return HealthcheckRow{Check: "x", Title: "X", State: state, Short: short}
	}
	cases := []struct {
		name        string
		rows        []HealthcheckRow
		wantVerdict string
		wantSent    string
	}{
		{"hepsi iyi", []HealthcheckRow{r(HCOK, ""), r(HCOK, "")}, HCOK, "orders-api sağlıklı görünüyor."},
		{"iyi + ölçülemedi → sağlıklı DEMEZ", []HealthcheckRow{r(HCOK, ""), r(HCUnknown, "")}, HCOK,
			"orders-api: ölçülen kontrollerde sorun yok; 1 kontrol ölçülemedi."},
		{"hepsi ölçülemedi", []HealthcheckRow{r(HCUnknown, ""), r(HCUnknown, "")}, HCUnknown,
			"orders-api ölçülemedi: kontrollerin hiçbiri okunamadı."},
		{"boş", nil, HCUnknown, "orders-api ölçülemedi: kontrollerin hiçbiri okunamadı."},
		{"dikkat", []HealthcheckRow{r(HCOK, ""), r(HCWarn, "son 30 dk'da deploy")}, HCWarn,
			"orders-api: sorun yok, 1 noktaya dikkat: son 30 dk'da deploy."},
		{"iki sorun + dikkat + ölçülemedi", []HealthcheckRow{r(HCCrit, "pod restart (cluster-a)"), r(HCWarn, "w"), r(HCCrit, "hata oranı %6"), r(HCUnknown, "")}, HCCrit,
			"orders-api: 2 sorun var: pod restart (cluster-a), hata oranı %6. Ayrıca 1 noktaya dikkat; 1 kontrol ölçülemedi."},
		{"dörtten fazla sorun kısaltılır", []HealthcheckRow{r(HCCrit, "a"), r(HCCrit, "b"), r(HCCrit, "c"), r(HCCrit, "d"), r(HCCrit, "e")}, HCCrit,
			"orders-api: 5 sorun var: a, b, c ve 2 sorun daha."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, s, _ := hcSummarize("orders-api", c.rows)
			if v != c.wantVerdict || s != c.wantSent {
				t.Fatalf("got (%q, %q)\nwant (%q, %q)", v, s, c.wantVerdict, c.wantSent)
			}
		})
	}
}

// TestHCRunnerTimeoutIsUnknown — yavaş kaynak diğerlerini bekletmez; süresi
// dolan kontrol "Ölçülemedi (zaman aşımı)" olur, koşu kısmi işaretlenir.
func TestHCRunnerTimeoutIsUnknown(t *testing.T) {
	r := hcReaders{sweepReaders: sweepReaders{
		windowRED: func(ctx context.Context, _, _ string, _, _ time.Time) (chstore.ServiceWindowRED, error) {
			return chstore.ServiceWindowRED{Spans: 1000, Errors: 1, P95Ms: 100}, nil
		},
		openItems: func(ctx context.Context, _ InboxOpenQuery) (InboxOpenResult, error) {
			<-ctx.Done() // asla dönmeyen kaynak
			return InboxOpenResult{}, ctx.Err()
		},
	}}
	caps := hcCaps{red: time.Second, inbox: 50 * time.Millisecond, anomalies: time.Second, deploys: time.Second, pods: time.Second}
	res, err := runServiceHealthcheck(context.Background(), r, HealthcheckRequest{Service: "orders-api"}, caps, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Partial {
		t.Fatalf("kısmi olmalı")
	}
	got := map[string]string{}
	for _, row := range res.Rows {
		if row.Subject == "" {
			got[row.Check] = row.State
		}
	}
	if got[HCCheckRED] != HCOK {
		t.Fatalf("RED iyi olmalı: %+v", res.Rows)
	}
	if got[HCCheckProblems] != HCUnknown || got[HCCheckExceptions] != HCUnknown {
		t.Fatalf("inbox zaman aşımı → ölçülemedi: %+v", res.Rows)
	}
	// Okuyucusu bağlı olmayan kontroller de ölçülemedi (İyi değil).
	if got[HCCheckAnomalies] != HCUnknown || got[HCCheckDeploys] != HCUnknown {
		t.Fatalf("bağlı olmayan okuyucu → ölçülemedi: %+v", res.Rows)
	}
	if res.Verdict != HCOK || !strings.Contains(res.Sentence, "ölçülemedi") {
		t.Fatalf("hüküm/cümle: %q %q", res.Verdict, res.Sentence)
	}
}

func TestHCNormalizeWindow(t *testing.T) {
	for in, want := range map[string]string{"": "15m", "15m": "15m", "1h": "1h", "6h": "6h", "24h": "15m", "garbage": "15m"} {
		if got := HCNormalizeWindow(in); got != want {
			t.Fatalf("%q → %q, want %q", in, got, want)
		}
	}
}
