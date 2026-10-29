package api

// red_family_test.go — v0.10.1429 (operator context: coremetry-ingest gRPC
// RED — Service sayfası metrik kipi yalnız gRPC sunan serviste boştu).
//
// Pinler: aile kataloğu (sıra, VM adları, birimler, hata kümeleri — FE
// lib/redFamily.ts ile bayt bayt), çözüm tablosu (http → http; http yok +
// legacy → grpc ms; yalnız stable; ikisi → http + both; etiket hatası →
// unknown; seri yok → none + gerekçe; rpc/dup → legacy birincil), aile
// başına PromQL (gRPC /1000, hata kümesi; HTTP bayt bayt eski —
// TestServiceHTTPREDPromQLGolden), önbellek anahtarı tüm girdiler, durum
// yoklaması aile anahtarlarıyla, CoSRE yalnız gRPC → gRPC RED (başlık / head /
// rapor etiketi), /metric-red?family=grpc gövdesi, rota defteri + VM-only 400,
// aday sırası (gRPC HTTP'den sonra).

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func TestREDFamilyErrorSetsPinned(t *testing.T) {
	if grpcServerFaultMatch != "2|4|8|12|13|14|15" || grpcClientFaultMatch != "1|3|5|6|7|9|10|11|16" || httpServerFaultMatch != "5.." {
		t.Fatalf("hata kümeleri pinli: %q %q %q", grpcServerFaultMatch, grpcClientFaultMatch, httpServerFaultMatch)
	}
	grpc, _ := redFamilyByID(redFamilyGRPCLegacy)
	httpF, _ := redFamilyByID(redFamilyHTTP)
	for code, want := range map[string]bool{"0": false, "2": true, "4": true, "5": false, "7": false, "8": true, "12": true, "13": true, "14": true, "15": true, "16": false, "1": false, "": false, "x": false} {
		if got := grpc.IsServerError(code); got != want {
			t.Errorf("gRPC %q sunucu hatası: got %v want %v", code, got, want)
		}
	}
	for code, want := range map[string]bool{"500": true, "503": true, "404": false, "200": false, "5": false, "14": false} {
		if got := httpF.IsServerError(code); got != want {
			t.Errorf("HTTP %q: got %v want %v", code, got, want)
		}
	}
}

func TestREDFamilyCatalogueShape(t *testing.T) {
	if len(redFamilies) != 3 || redFamilies[0].ID != redFamilyHTTP || redFamilies[1].ID != redFamilyGRPCLegacy || redFamilies[2].ID != redFamilyGRPCStable {
		t.Fatalf("sıra: HTTP, legacy, stable — %+v", redFamilies)
	}
	want := map[string][3]string{
		redFamilyHTTP:       {"http_server_request_duration_seconds_count", "s", mcHTTPMetric},
		redFamilyGRPCLegacy: {"rpc_server_duration_milliseconds_count", "ms", "rpc.server.duration"},
		redFamilyGRPCStable: {"rpc_server_call_duration_seconds_count", "s", "rpc.server.call.duration"},
	}
	for _, f := range redFamilies {
		w := want[f.ID]
		if f.vmCount() != w[0] || f.Unit != w[1] || f.Probe != w[2] {
			t.Errorf("%s: count=%s unit=%s probe=%s", f.ID, f.vmCount(), f.Unit, f.Probe)
		}
		if f.Kind == redKindGRPC {
			// ms ↔ s aileleri tek alternasyonda toplanmaz: __name__ TEK ve tam.
			if n := vmetrics.NameCandidates("count", f.names()); len(n) != 1 {
				t.Errorf("%s: gRPC ailesinin yazımı tek olmalı: %v", f.ID, n)
			}
			if f.RouteLabel != "rpc_method" || f.MethodLabel != "rpc_service" || f.ServerErr != grpcServerFaultMatch {
				t.Errorf("%s: etiketler/hata kümesi: %+v", f.ID, f)
			}
		}
	}
	if redFamilies[0].ServerErr != httpServerFaultMatch || redFamilies[0].RouteLabel != "http_route" || redFamilies[0].StatusLabels[0] != "http_response_status_code" {
		t.Errorf("HTTP ailesi bugünkü etiketleri taşımalı: %+v", redFamilies[0])
	}
	if _, ok := redFamilyByID("nope"); ok {
		t.Error("bilinmeyen aile ok=false")
	}
	if q := redFamilyPresenceQuery(redFamilies[1], "service_name", "grpc.example.test"); q != `sum(increase({__name__="rpc_server_duration_milliseconds_count", service_name="grpc.example.test"}[1h]))` {
		t.Errorf("yoklama sorgusu: %s", q)
	}
}

// famReader — aile başına yoklama cevabı veren sahte VM (hangi ailenin
// _count'u sorguda geçiyorsa onun serisi).
type famReader struct {
	fakeChartReader
	present map[string]bool // aile ID → servisin serisi var
	instErr error
	rate    []chstore.SpanMetricSeries
	mu      sync.Mutex // v0.10.1432 — readServiceHTTPRED sorguları paralel koşar; -race yakaladı
	qs      []string
}

func (r *famReader) QueryPromQLInstant(_ context.Context, q string, _ time.Time) ([]chstore.SpanMetricSeries, error) {
	if r.instErr != nil {
		return nil, r.instErr
	}
	for _, f := range redFamilies {
		if strings.Contains(q, `__name__="`+f.vmCount()+`"`) || (f.Kind == redKindHTTP && strings.Contains(q, "http_server_request_duration_seconds_count")) {
			if r.present[f.ID] {
				return []chstore.SpanMetricSeries{{Points: []chstore.SpanMetricPoint{{Value: 1}}}}, nil
			}
			return nil, nil
		}
	}
	return nil, nil
}

func (r *famReader) QueryPromQLRange(_ context.Context, q string, _, _ time.Time, _, _ int) ([]chstore.SpanMetricSeries, error) {
	r.mu.Lock()
	r.qs = append(r.qs, q)
	r.mu.Unlock()
	return r.rate, nil
}

func famLabels(ids ...string) map[string][]string {
	out := map[string][]string{}
	for _, id := range ids {
		f, _ := redFamilyByID(id)
		out[f.Probe] = append([]string{"service_name", "deployment_environment_name", f.RouteLabel, f.MethodLabel}, f.StatusLabels...)
	}
	return out
}

func TestResolveServiceREDFamilyTable(t *testing.T) {
	to := time.Date(2026, 10, 20, 12, 0, 30, 0, time.UTC)
	s := &Server{}
	cases := []struct {
		name    string
		labels  map[string][]string
		present map[string]bool
		labelEr error
		instErr error
		primary string
		httpID  string
		grpcID  string
		both    bool
		state   string
		tried   int
	}{
		{"http → http", famLabels(redFamilyHTTP), map[string]bool{redFamilyHTTP: true}, nil, nil, redKindHTTP, redFamilyHTTP, "", false, "known", 2},
		{"http yok + legacy → grpc ms", famLabels(redFamilyHTTP, redFamilyGRPCLegacy), map[string]bool{redFamilyGRPCLegacy: true}, nil, nil, redKindGRPC, "", redFamilyGRPCLegacy, false, "known", 2},
		{"yalnız stable → grpc s", famLabels(redFamilyGRPCStable), map[string]bool{redFamilyGRPCStable: true}, nil, nil, redKindGRPC, "", redFamilyGRPCStable, false, "known", 2},
		{"ikisi → http birincil + both", famLabels(redFamilyHTTP, redFamilyGRPCLegacy), map[string]bool{redFamilyHTTP: true, redFamilyGRPCLegacy: true}, nil, nil, redKindHTTP, redFamilyHTTP, redFamilyGRPCLegacy, true, "known", 1},
		{"rpc/dup → legacy birincil, stable tried", famLabels(redFamilyGRPCLegacy, redFamilyGRPCStable), map[string]bool{redFamilyGRPCLegacy: true, redFamilyGRPCStable: true}, nil, nil, redKindGRPC, "", redFamilyGRPCLegacy, false, "known", 2},
		{"metrik yok → none", map[string][]string{}, nil, nil, nil, redKindNone, "", "", false, "known", 3},
		{"metrik var, servisin serisi yok → none", famLabels(redFamilyHTTP, redFamilyGRPCLegacy), map[string]bool{}, nil, nil, redKindNone, "", "", false, "known", 3},
		{"etiket hatası → unknown (okunamadı ≠ yok)", famLabels(redFamilyHTTP), nil, errors.New("vm down"), nil, redKindHTTP, "", "", false, "unknown", 0},
		// v0.10.1559 — tek okunabilir aile (HTTP) yoklamada düşer, gRPC
		// aileleri "metrik yok" diye KESİN cevap verir: unknown değil; HTTP
		// varsayılan olarak çizilir, gerekçe reason'da (okunamadı ≠ yok).
		{"yoklama hatası → HTTP varsayılan, kısmi", famLabels(redFamilyHTTP), nil, nil, errors.New("timeout"), redKindHTTP, redFamilyHTTP, "", false, "known", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rd := &famReader{fakeChartReader: fakeChartReader{labels: c.labels, labelErr: c.labelEr}, present: c.present, instErr: c.instErr}
			res, err := s.resolveServiceREDFamily(context.Background(), rd, "grpc.example.test", nil, to)
			if (err != nil) != (c.state == "unknown") {
				t.Fatalf("err=%v state=%s", err, res.State)
			}
			gotHTTP, gotGRPC := "", ""
			if res.HTTP != nil {
				gotHTTP = res.HTTP.ID
			}
			if res.GRPC != nil {
				gotGRPC = res.GRPC.ID
			}
			if res.State != c.state || res.Primary != c.primary || gotHTTP != c.httpID || gotGRPC != c.grpcID || res.Both != c.both || len(res.Tried) != c.tried {
				t.Errorf("got state=%s primary=%s http=%q grpc=%q both=%v tried=%v (%s)", res.State, res.Primary, gotHTTP, gotGRPC, res.Both, res.Tried, res.Reason)
			}
			if (c.state == "unknown" || c.instErr != nil) && !strings.Contains(res.Reason, "okunamadı ≠ yok") {
				t.Errorf("unknown gerekçesi: %s", res.Reason)
			}
			if res.Tried == nil {
				t.Error("tried JSON'da [] olmalı, null değil")
			}
		})
	}
	// Ortam terimi → aile başına kendi metriğine karşı çözüm.
	rd := &famReader{fakeChartReader: fakeChartReader{labels: famLabels(redFamilyGRPCLegacy), envVals: map[string][]string{"deployment_environment_name": {"prod-eu-a"}}}, present: map[string]bool{redFamilyGRPCLegacy: true}}
	res, _ := s.resolveServiceREDFamily(context.Background(), rd, "grpc.example.test", map[string]string{vmetrics.RoleEnv: "eu-a"}, to)
	if res.GRPC == nil || res.GRPC.EnvScope == nil || !res.GRPC.EnvScope.Applied || res.GRPC.EnvScope.Matcher != `deployment_environment_name="prod-eu-a"` || res.GRPC.StatusLabel != "rpc_grpc_status_code" {
		t.Errorf("gRPC ailesinin ortam kapsamı + durum etiketi: %+v", res.GRPC)
	}
	// Çözüm yapısı / birincil aile yardımcıları.
	f, p := res.primaryFamily()
	if f.ID != redFamilyGRPCLegacy || p == nil || !res.anyMetricPresent() {
		t.Errorf("primaryFamily: %s %v", f.ID, p)
	}
	if f, p := (redFamilyResolution{Primary: redKindNone}).primaryFamily(); f.ID != redFamilyHTTP || p != nil {
		t.Error("none → HTTP, presence nil")
	}
}

func TestREDFamilyKeyHashesAllInputs(t *testing.T) {
	to := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	base := redFamilyKey(vmetrics.LabelMap{}, "orders-api", "", "", to, "vm")
	variants := []string{
		redFamilyKey(vmetrics.LabelMap{}, "grpc.example.test", "", "", to, "vm"),
		redFamilyKey(vmetrics.LabelMap{}, "orders-api", "eu-a", "", to, "vm"),
		redFamilyKey(vmetrics.LabelMap{}, "orders-api", "", "eu-a", to, "vm"),
		redFamilyKey(vmetrics.LabelMap{Env: "environment"}, "orders-api", "", "", to, "vm"),
		redFamilyKey(vmetrics.LabelMap{Cluster: "cl"}, "orders-api", "", "", to, "vm"),
		redFamilyKey(vmetrics.LabelMap{Service: "job"}, "orders-api", "", "", to, "vm"),
		redFamilyKey(vmetrics.LabelMap{}, "orders-api", "", "", to.Add(10*time.Minute), "vm"),
		redFamilyKey(vmetrics.LabelMap{}, "orders-api", "", "", to, "ch"),
	}
	seen := map[string]bool{base: true}
	for i, k := range variants {
		if seen[k] {
			t.Errorf("girdi %d anahtarı değiştirmedi: %s", i, k)
		}
		seen[k] = true
	}
	if base != redFamilyKey(vmetrics.LabelMap{}, "orders-api", "", "", to.Add(time.Minute), "vm") || !strings.HasPrefix(base, "red-family:v1:") {
		t.Errorf("10 dk içinde aynı, ad alanlı: %s", base)
	}
}

// TestREDFamilyPromQLGolden — gRPC aileleri: tam __name__, hata kümesi,
// legacy'de gecikme /1000. HTTP: svcHTTPREDQueries ile bayt bayt aynı.
func TestREDFamilyPromQLGolden(t *testing.T) {
	legacy, _ := redFamilyByID(redFamilyGRPCLegacy)
	stable, _ := redFamilyByID(redFamilyGRPCStable)
	sel := `service_name="grpc.example.test"`
	got := svcREDFamilyQueries(legacy, "grpc.example.test", "service_name", "", "rpc_grpc_status_code", 60)
	cnt := `{__name__="rpc_server_duration_milliseconds_count", ` + sel + `}`
	want := []svcHTTPREDQuery{
		{Kind: svcHTTPREDRate, Title: "grpc.example.test · gRPC istek hızı · VictoriaMetrics (örneklenmemiş)", Unit: "req/s", Name: "req/s",
			Query: `sum (rate(` + cnt + `[60s]))`},
		{Kind: svcHTTPRED5xx, Title: "grpc.example.test · gRPC sunucu-hatası oranı · VictoriaMetrics (örneklenmemiş)", Unit: "%", Name: "hata %",
			Query: `100 * (sum (rate({__name__="rpc_server_duration_milliseconds_count", ` + sel + `, rpc_grpc_status_code=~"2|4|8|12|13|14|15"}[60s])) or 0 * sum (rate(` + cnt + `[60s]))) / sum (rate(` + cnt + `[60s]))`},
		{Kind: svcHTTPREDLatency, Title: "grpc.example.test · gRPC gecikme p95/p99 · VictoriaMetrics (örneklenmemiş)", Unit: "s", Name: "gecikme",
			Query: `(histogram_quantiles("phi", 0.95, 0.99, sum by (le) (rate({__name__="rpc_server_duration_milliseconds_bucket", ` + sel + `}[60s])))) / 1000`},
	}
	if len(got) != len(want) {
		t.Fatalf("%d grafik, beklenen %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("legacy grafik %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
	st := svcREDFamilyQueries(stable, "grpc.example.test", "service_name", `deployment_environment_name="prod-eu-a"`, "rpc_response_status_code", 120)
	if st[2].Query != `histogram_quantiles("phi", 0.95, 0.99, sum by (le) (rate({__name__="rpc_server_call_duration_seconds_bucket", `+sel+`, deployment_environment_name="prod-eu-a"}[120s])))` {
		t.Errorf("stable (s) ölçeklenmez, ortam seçicide: %s", st[2].Query)
	}
	if !strings.Contains(st[1].Query, `rpc_response_status_code=~"2|4|8|12|13|14|15"`) {
		t.Errorf("stable hata kümesi: %s", st[1].Query)
	}
	for _, q := range append(got, st...) {
		if strings.Contains(q.Query, "seconds_count|") || strings.Contains(q.Query, "__name__=~") {
			t.Errorf("gRPC ailesinde __name__ alternasyonu olmamalı: %s", q.Query)
		}
	}
	// HTTP: wrapper ile aynı (TestServiceHTTPREDPromQLGolden bayt pinini taşır).
	h1 := svcHTTPREDQueries("orders-api", "service_name", "", "http_response_status_code", 60)
	h2 := svcREDFamilyQueries(redFamilies[0], "orders-api", "service_name", "", "http_response_status_code", 60)
	for i := range h1 {
		if h1[i] != h2[i] {
			t.Errorf("HTTP %d ayrıştı: %+v / %+v", i, h1[i], h2[i])
		}
	}
}

// TestREDFamilyLabelsMatchFrontend — Go kataloğu == lib/redFamily.ts (VM
// adları, etiketler, hata kümeleri, birim) — iki dil ayrışamaz.
func TestREDFamilyLabelsMatchFrontend(t *testing.T) {
	src := readRepoFile(t, "../../frontend/src/lib/redFamily.ts")
	blocks := regexp.MustCompile(`(?s)export const (\w+)_FAMILY: RedFamilySpec = \{(.*?)\n\};`).FindAllStringSubmatch(src, -1)
	if len(blocks) != 3 {
		t.Fatalf("üç aile bloğu bekleniyordu: %d", len(blocks))
	}
	field := func(block, key string) string {
		m := regexp.MustCompile(`\b` + key + `:\s*'([^']*)'`).FindStringSubmatch(block)
		if m == nil {
			t.Fatalf("%s bloğunda %s yok", block[:40], key)
		}
		return m[1]
	}
	for _, b := range blocks {
		body := b[2]
		f, ok := redFamilyByID(field(body, "id"))
		if !ok {
			t.Fatalf("FE ailesi %s Go kataloğunda yok", field(body, "id"))
		}
		cnt := vmetrics.NameCandidates("count", f.VMBase)[0]
		bkt := vmetrics.NameCandidates("bucket", f.VMBase)[0]
		sum := vmetrics.NameCandidates("sum", f.VMBase)[0]
		// HTTP: pinli VM yazımı bugünkü seçici alternasyonunun İÇİNDE (seçici değişmedi).
		if !f.Exact {
			in := false
			for _, c := range vmetrics.NameCandidates("count", f.Probe) {
				in = in || c == cnt
			}
			if !in {
				t.Errorf("%s: %s seçici adayları arasında değil", f.ID, cnt)
			}
		}
		checks := map[string][2]string{
			"count": {field(body, "count"), cnt}, "bucket": {field(body, "bucket"), bkt}, "sum": {field(body, "sum"), sum},
			"unit": {field(body, "unit"), f.Unit}, "probe": {field(body, "probe"), f.Probe}, "kind": {field(body, "kind"), f.Kind},
			"route": {field(body, "route"), f.RouteLabel}, "method": {field(body, "method"), f.MethodLabel},
			"status":    {field(body, "status"), vmetrics.PromLabel(f.StatusKeys[0])},
			"service":   {field(body, "service"), vmetrics.PromLabel("service.name")},
			"serverErr": {field(body, "serverErr"), f.ServerErr}, "clientErr": {field(body, "clientErr"), f.ClientErr},
		}
		for k, v := range checks {
			if v[0] != v[1] {
				t.Errorf("%s.%s: FE %q, Go %q", f.ID, k, v[0], v[1])
			}
		}
		// SLI kovası ailenin biriminde 500 ms.
		if want := map[string]string{"s": "0.5", "ms": "500"}[f.Unit]; field(body, "sliLe") != want {
			t.Errorf("%s.sliLe: %q, beklenen %q", f.ID, field(body, "sliLe"), want)
		}
	}
	if !strings.Contains(src, "'5': 'NOT_FOUND'") || !strings.Contains(src, "'14': 'UNAVAILABLE'") {
		t.Error("GRPC_CODE_NAMES kod adlarını taşımalı")
	}
}

func TestThroughputCandidatesGRPCAfterHTTP(t *testing.T) {
	c := chstore.ThroughputMetricCandidates
	iHTTP, iLegacy, iStable := -1, -1, -1
	for i, n := range c {
		switch n {
		case "http_server_requests_seconds_count":
			iHTTP = i
		case "rpc.server.duration":
			iLegacy = i
		case "rpc.server.call.duration":
			iStable = i
		}
	}
	if iHTTP < 0 || iLegacy < 0 || iStable < 0 || !(iHTTP < iLegacy && iLegacy < iStable) {
		t.Errorf("gRPC adayları HTTP'den SONRA, legacy stable'dan önce: %v", c)
	}
}

func TestProbeStatusLabelKeysFamily(t *testing.T) {
	grpc, _ := redFamilyByID(redFamilyGRPCLegacy)
	var asked []string
	pr := probeStatusLabelKeys(func(key string) ([]chstore.SpanMetricSeries, error) {
		asked = append(asked, key)
		return []chstore.SpanMetricSeries{{GroupKey: []string{"0"}}, {GroupKey: []string{"14"}}}, nil
	}, 0, grpc.StatusKeys)
	if pr.State != statusLabelPresent || pr.Key != "rpc.grpc.status_code" || strings.Join(asked, ",") != "rpc.grpc.status_code" {
		t.Errorf("gRPC durum yoklaması: %+v asked=%v", pr, asked)
	}
	// Eski sarmalayıcı HTTP adaylarıyla aynen.
	asked = nil
	probeStatusLabel(func(key string) ([]chstore.SpanMetricSeries, error) { asked = append(asked, key); return nil, nil }, 0)
	if strings.Join(asked, ",") != strings.Join(httpStatusKeyCandidates, ",") {
		t.Errorf("HTTP adayları: %v", asked)
	}
	n := statusLabelNoteFamily(statusLabelPresent, "rpc.grpc.status_code", "", "vm", "Failure rate", "", grpc)
	if !strings.Contains(n, "rpc_grpc_status_code sunucu-hatası kodları 2|4|8|12|13|14|15") || strings.Contains(n, "5xx") {
		t.Errorf("gRPC notu: %s", n)
	}
	a := statusLabelNoteFamily(statusLabelAbsent, "", "", "vm", "Failure rate", "", grpc)
	if !strings.Contains(a, "(rpc.grpc.status_code)") {
		t.Errorf("gRPC yok notu adayları aileden: %s", a)
	}
	if statusLabelNote(statusLabelPresent, "http.response.status_code", "", "vm", "Failure rate", "") != " Failure rate = http_response_status_code 5xx (4xx hata sayılmaz)." {
		t.Error("HTTP notu bayt bayt eski olmalı")
	}
}

// TestServiceREDGRPCOnlyCoSRE — yalnız gRPC sunan servis: servis sağlığı +
// raporu gRPC RED'i çizer (aynı okuyucu); başlık / head / rapor etiketi gRPC
// der, HTTP ile toplanmaz; ikisi varsa HTTP + not.
func TestServiceREDGRPCOnlyCoSRE(t *testing.T) {
	to := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	pts := func(vs ...float64) []chstore.SpanMetricPoint {
		var out []chstore.SpanMetricPoint
		for i, v := range vs {
			out = append(out, chstore.SpanMetricPoint{Time: from.Add(time.Duration(i) * time.Minute).UnixNano(), Value: v})
		}
		return out
	}
	s := &Server{}
	rd := &famReader{fakeChartReader: fakeChartReader{labels: famLabels(redFamilyHTTP, redFamilyGRPCLegacy)},
		present: map[string]bool{redFamilyGRPCLegacy: true}, rate: []chstore.SpanMetricSeries{{Points: pts(10, 12, 11)}}}
	res := s.readServiceHTTPRED(context.Background(), rd, true, "grpc.example.test", "", from, to, 3600)
	if !res.UseVM || res.Family != redKindGRPC || res.FamilyID != redFamilyGRPCLegacy || res.Both || len(res.Charts) != 3 {
		t.Fatalf("gRPC-only: %+v", res)
	}
	for _, c := range res.Charts {
		if !strings.Contains(c.Query, "rpc_server_duration_milliseconds_") || strings.Contains(c.Query, "http_server") || !strings.Contains(c.Title, "gRPC") {
			t.Errorf("grafik %s: %s / %s", c.Kind, c.Title, c.Query)
		}
	}
	ev := &mcEvents{}
	text := s.emitServiceHTTPRED(ev.emit(), "grpc.example.test", 3600, res, time.UTC)
	for _, w := range []string{"SERVİS gRPC RED GRAFİKLERİ", "rpc_server_duration_milliseconds_*", "sunucu-hatası kodları 2|4|8|12|13|14|15", "servis HTTP sunucu histogramı yazmıyor"} {
		if !strings.Contains(text, w) {
			t.Errorf("head %q içermeli:\n%s", w, text)
		}
	}
	if !strings.Contains(svcHTTPREDSourcesTR(res), "gRPC RED") {
		t.Errorf("kaynak satırı: %s", svcHTTPREDSourcesTR(res))
	}
	views := reportHTTPViews("grpc.example.test", 3600, res, time.UTC)
	charts, note := reportHTTPCharts(res, views)
	rep := renderReportHTTPTR("grpc.example.test", 3600, res, views)
	if note != "" || len(charts) != 3 || charts[0].Family != redKindGRPC || !strings.Contains(rep, "gRPC throughput") || !strings.Contains(rep, "gRPC sunucu-hatası oranı") || strings.Contains(rep, "HTTP 5xx") {
		t.Errorf("rapor: note=%q charts=%d\n%s", note, len(charts), rep)
	}
	if !strings.Contains(reportHTTPSourcesTR(charts), "gRPC throughput") {
		t.Errorf("rapor kaynak satırı: %s", reportHTTPSourcesTR(charts))
	}
	// İkisi de var → HTTP çizilir, gRPC NOT olarak (toplanmaz).
	rd = &famReader{fakeChartReader: fakeChartReader{labels: famLabels(redFamilyHTTP, redFamilyGRPCLegacy)},
		present: map[string]bool{redFamilyHTTP: true, redFamilyGRPCLegacy: true}, rate: []chstore.SpanMetricSeries{{Points: pts(10, 12, 11)}}}
	res = s.readServiceHTTPRED(context.Background(), rd, true, "orders-api", "", from, to, 3600)
	if !res.UseVM || res.Family != redKindHTTP || !res.Both || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "TOPLANMADI") {
		t.Fatalf("both: %+v", res)
	}
	for _, c := range res.Charts {
		if strings.Contains(c.Query, "rpc_") || !strings.Contains(c.Query, "http_server_request_duration_seconds_") {
			t.Errorf("both → HTTP sorguları: %s", c.Query)
		}
	}
	if strings.Contains(s.emitServiceHTTPRED(ev.emit(), "orders-api", 3600, res, time.UTC), "SERVİS gRPC") {
		t.Error("both → HTTP başlığı")
	}
	// Hiçbiri yok (gRPC de yoklandı) → gerekçe gRPC'yi de söyler; sorgu yok.
	rd = &famReader{fakeChartReader: fakeChartReader{labels: famLabels(redFamilyHTTP, redFamilyGRPCLegacy)}, present: map[string]bool{}}
	res = s.readServiceHTTPRED(context.Background(), rd, true, "orders-api", "", from, to, 3600)
	if res.UseVM || !strings.Contains(res.Reason, "gRPC serisi de yok") || len(rd.qs) != 0 {
		t.Errorf("none: %+v q=%v", res, rd.qs)
	}
}

// grpcEPSource — /metric-red?family=grpc için sahte kaynak: legacy aile var,
// durum kırılımı rpc.grpc.status_code (0 / 14 / 5).
type grpcEPSource struct {
	*fakeEPSource
	exists map[string]bool
}

func (g *grpcEPSource) MetricExists(_ context.Context, name string) (bool, error) {
	return g.exists[name], nil
}
func (g *grpcEPSource) MetricInstrument(context.Context, string, string) string { return "histogram" }

func TestServiceMetricREDGRPCFamily(t *testing.T) {
	from, to := epFixtureWindow()
	base := redFixtureSource("ms", nil)
	base.rateFn = func(f chstore.MetricQueryFilter, mode string) ([]chstore.SpanMetricSeries, error) {
		if len(f.GroupBy) == 1 && f.GroupBy[0] == "rpc.grpc.status_code" {
			ok := epConst([]string{"0"}, from, 60, 60, 9)
			srv := epConst([]string{"14"}, from, 60, 60, 1) // UNAVAILABLE → hata
			cli := epConst([]string{"5"}, from, 60, 60, 10) // NOT_FOUND → hata DEĞİL
			return []chstore.SpanMetricSeries{ok, srv, cli}, nil
		}
		return []chstore.SpanMetricSeries{epConst([]string{""}, from, 60, 60, 20)}, nil
	}
	src := &grpcEPSource{fakeEPSource: base, exists: map[string]bool{"rpc.server.duration": true}}
	id, ok := grpcMetricIdentity(context.Background(), src, "grpc.example.test")
	if !ok || id.Family == nil || id.Family.ID != redFamilyGRPCLegacy || id.Metric != "rpc.server.duration" || id.MatchedBy != "family:grpc_legacy" || id.Instrument != "histogram" {
		t.Fatalf("kimlik: %+v ok=%v", id, ok)
	}
	p := serviceMetricREDPlan{Service: "grpc.example.test", From: from, To: to, Mdp: 300, RateWin: 180, Family: redKindGRPC}
	resp, err := buildServiceMetricRED(context.Background(), src, id, p)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Family != redKindGRPC || resp.FamilyID != redFamilyGRPCLegacy || resp.RouteLabel != "rpc.method" || resp.StatusKey != "rpc.grpc.status_code" || resp.ErrorsUnknown {
		t.Fatalf("zarf: %+v", resp)
	}
	// hata % = 14 kodu / toplam (0+14+5 = 20) = %5; NOT_FOUND hata sayılmadı.
	er := resp.Series["error_rate"]
	if len(er) != 1 || len(er[0].Points) == 0 || er[0].Points[0].Value < 4.99 || er[0].Points[0].Value > 5.01 {
		t.Fatalf("error_rate: %+v", er)
	}
	if resp.LatencyUnit != "ms" || !resp.LatencyUnitKnown {
		t.Errorf("birim kaynaktan ms: %+v", resp)
	}
	if !strings.Contains(resp.Note, "Aile: gRPC") || !strings.Contains(resp.Note, "sunucu-hatası kodları 2|4|8|12|13|14|15") || strings.Contains(resp.Note, "5xx") {
		t.Errorf("not: %s", resp.Note)
	}
	// Aile yoksa → kimlik yok + denenenler.
	src2 := &grpcEPSource{fakeEPSource: base, exists: map[string]bool{}}
	if id2, ok2 := grpcMetricIdentity(context.Background(), src2, "x"); ok2 || len(id2.Tried) != 2 {
		t.Errorf("aile yok: %+v %v", id2, ok2)
	}
	// Anahtar aileyi taşır (HTTP gövdesi gRPC isteğine servis edilmesin).
	if serviceMetricREDKey(p, "vm", "0", "-") == serviceMetricREDKey(serviceMetricREDPlan{Service: "grpc.example.test", From: from, To: to, Mdp: 300, RateWin: 180}, "vm", "0", "-") {
		t.Error("fam= anahtarda olmalı")
	}
	if !strings.HasPrefix(serviceMetricREDKey(p, "vm", "0", "-"), "svc-metric-red:v4:") {
		t.Error("zarf büyüdü → v4")
	}
	// breakdown=rpc_method → rpc.method; bilinmeyen kapalı.
	if g := breakdownGroupBy("rpc_method"); len(g) != 1 || g[0] != "rpc.method" {
		t.Errorf("rpc_method kırılımı: %v", g)
	}
	if breakdownGroupBy("pod") != nil || breakdownGroupBy("route")[0] != "http.route" {
		t.Error("route aynen, bilinmeyen nil")
	}
}

func TestServiceREDFamilyRoute(t *testing.T) {
	if _, ok := extraRouteRegistrars["service-red-family"]; !ok {
		t.Fatal("service-red-family defterde değil")
	}
	src := readRepoFile(t, "service_red_family_route.go")
	if !strings.Contains(src, "mux.HandleFunc(") || !strings.Contains(src, `"GET /api/services/{name}/red-family", s.getServiceREDFamily)`) {
		t.Fatal("rota metot önekli ve kapısız (viewer görür) kayıtlı olmalı")
	}
	if strings.Contains(readRepoFile(t, "api.go"), "red-family") {
		t.Fatal("api.go büyümez")
	}
	s := &Server{}
	// Servis zorunlu; VM yoksa 400 (yüzeyler metricsrc=vm'e sabit).
	w := httptest.NewRecorder()
	s.getServiceREDFamily(w, httptest.NewRequest(http.MethodGet, "/api/services/x/red-family", nil))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "service required") {
		t.Errorf("ad yok: %d %s", w.Code, w.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/api/services/orders-api/red-family?env=eu-a", nil)
	req.SetPathValue("name", "orders-api")
	w = httptest.NewRecorder()
	s.getServiceREDFamily(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "yalnız VictoriaMetrics kaynağında (metricsrc=vm)") {
		t.Errorf("VM yok: %d %s", w.Code, w.Body.String())
	}
	// Salt-okur: yazma / denetim / model çağrısı yok (çözücü + rota).
	both := stripGoCommentsAPI(src) + stripGoCommentsAPI(readRepoFile(t, "red_family.go"))
	for _, w := range []string{"Upsert", "Insert", "audit(", "s.copilot", "http.Post", "s.store.", "Exec(", "serveCached("} {
		if strings.Contains(both, w) {
			t.Errorf("%q içermemeli (salt-okur; önbellek çözücünün içinde)", w)
		}
	}
	if n := strings.Count(stripGoCommentsAPI(readRepoFile(t, "red_family.go")), ".QueryPromQLInstant("); n != 1 {
		t.Errorf("TEK anlık yoklama çağrı yeri bekleniyordu, %d", n)
	}
}

// labelErrReader — v0.10.1559: yalnız verilen probe'ların etiket okuması düşer.
type labelErrReader struct {
	*famReader
	errFor map[string]error
}

func (r labelErrReader) MetricLabelNamesIn(ctx context.Context, metric string, from, to time.Time) ([]string, bool, error) {
	if e := r.errFor[metric]; e != nil {
		return nil, false, e
	}
	return r.famReader.MetricLabelNamesIn(ctx, metric, from, to)
}

// TestResolveREDFamilyOneFamilyFailureDoesNotHideOthers — v0.10.1559
// (operatör, prod bsa-crm-contentmanagement-prod: rpc_server_duration_
// milliseconds_* VM'de var, HTTP/RPC sekmesinde gRPC bölümü yok;
// /red-family → state unknown, reason timeout, tried []). Kök neden: HTTP
// etiket okumasının zaman aşımı çözücüyü İLK ailede bitiriyordu, gRPC hiç
// denenmiyordu. Beklenen: HTTP okunamadı diye işaretli ama çizilir, gRPC
// bulunur, ikisi birlikte (both), kısmi sonuç önbelleğe yazılmaz.
func TestResolveREDFamilyOneFamilyFailureDoesNotHideOthers(t *testing.T) {
	to := time.Date(2026, 10, 29, 12, 0, 30, 0, time.UTC)
	s := &Server{}
	httpF, _ := redFamilyByID(redFamilyHTTP)
	rd := labelErrReader{
		famReader: &famReader{fakeChartReader: fakeChartReader{labels: famLabels(redFamilyHTTP, redFamilyGRPCLegacy)},
			present: map[string]bool{redFamilyGRPCLegacy: true}},
		errFor: map[string]error{httpF.Probe: context.DeadlineExceeded},
	}
	res, err := s.resolveServiceREDFamily(context.Background(), rd, "bsa-crm-contentmanagement-prod", nil, to)
	if err != nil {
		t.Fatalf("tek ailenin hatası çözümü düşürmemeli: %v", err)
	}
	if res.State != "known" || res.GRPC == nil || res.GRPC.ID != redFamilyGRPCLegacy {
		t.Fatalf("gRPC bulunmalı: state=%s grpc=%+v tried=%v", res.State, res.GRPC, res.Tried)
	}
	if res.HTTP == nil || res.Primary != redKindHTTP || !res.Both {
		t.Errorf("okunamayan HTTP varsayılan olarak çizilmeli: http=%+v primary=%s both=%v", res.HTTP, res.Primary, res.Both)
	}
	if !strings.Contains(res.Reason, "okunamadı ≠ yok") || !strings.Contains(strings.Join(res.Tried, "|"), "okunamadı") {
		t.Errorf("kısmi okuma ilan edilmeli: reason=%q tried=%v", res.Reason, res.Tried)
	}
	// İptal hâlâ tüm çözümü durdurur (istemci gitti — sonuç yazılmaz).
	rd.errFor[httpF.Probe] = context.Canceled
	if _, err := s.resolveServiceREDFamily(context.Background(), rd, "x", nil, to); !errors.Is(err, context.Canceled) {
		t.Errorf("iptal yayılmalı, got %v", err)
	}
}
