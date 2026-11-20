package ml

// red_family_spelling_test.go — v0.10.2390. SÖZLEŞME: ML motorunun bir RED
// ailesi için kurduğu her VM sorgusu metriği yazım adaylarıyla seçer
// (vmetrics.NameCandidates; çalışan yollarla — anomaly/red_vm_query.go
// vmSelector, api metricChartSelector — aynı mekanizma). Hata: HTTP ailesinin
// pini mantıksal OTel adını taşır (http.server.request.duration); ML bunu
// çıplak yazıp sonuna _count / _bucket ekliyordu. Prometheus adlandırmalı
// VictoriaMetrics'te (http_server_request_duration_seconds_*) etiket keşfi de
// sorgular da boş dönüyordu: ML trace dedektörleri HTTP servislerini görmedi.
//
// Buradaki sahte VM yalnız Prometheus yazımını bilir; düzeltmeden önce
// keşif, tik, 1 dk'lık etki sorgusu, yoklama ve kayıt sorgusu boş dönerdi.

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

var (
	selAltRe   = regexp.MustCompile(`__name__=~"\^\(([^"]*)\)\$"`)
	selExactRe = regexp.MustCompile(`__name__="([^"]*)"`)
	selBareRe  = regexp.MustCompile(`[A-Za-z_:][A-Za-z0-9_:.]*`)
	promKeyRe  = regexp.MustCompile(`^(sum|by|rate|increase|le|histogram_quantile|histogram_share|clamp_min|clamp_max|topk|or|vector|and|on|s)$`)
)

// queriedNames — SAF: ifadenin seçtiği metrik adları. Alternasyon üyeleri
// (regex kaçışı açılır), tam ad eşleyicisi ve süslü parantez / aralık öncesi
// çıplak adlar. Tırnak içi (etiket değeri) çıplak ad sayılmaz.
func queriedNames(q string) []string {
	names, _ := selectorsOf(q)
	return names
}

// selectorsOf — SAF: seçilen adlar + ifadedeki metrik başvurusu (seçici) sayısı.
func selectorsOf(q string) (names []string, selectors int) {
	for _, m := range selAltRe.FindAllStringSubmatch(q, -1) {
		selectors++
		for _, c := range strings.Split(m[1], "|") {
			names = append(names, strings.ReplaceAll(c, `\\`, ""))
		}
	}
	for _, m := range selExactRe.FindAllStringSubmatch(q, -1) {
		selectors++
		names = append(names, m[1])
	}
	plain := stripQuoted(q)
	for _, idx := range selBareRe.FindAllStringIndex(plain, -1) {
		rest, name := plain[idx[1]:], plain[idx[0]:idx[1]]
		if (strings.HasPrefix(rest, "{") || strings.HasPrefix(rest, "[")) && !promKeyRe.MatchString(name) {
			selectors++
			names = append(names, name)
		}
	}
	return names, selectors
}

// stripQuoted — SAF: çift tırnaklı dizeleri aynı uzunlukta boşlukla örter.
func stripQuoted(q string) string {
	b := []byte(q)
	in := false
	for i := 0; i < len(b); i++ {
		switch {
		case b[i] == '\\' && in && i+1 < len(b):
			b[i], b[i+1] = ' ', ' '
			i++
		case b[i] == '"':
			in = !in
			b[i] = ' '
		case in:
			b[i] = ' '
		}
	}
	return string(b)
}

// promNamedVM — yalnız Prometheus adlandırmalı HTTP histogramını bilen sahte
// VictoriaMetrics. Etiket keşfi VM'in keşif adaylarını taklit eder (düz
// yazımlar + _count kardeşleri); sorgu, seçtiği adlardan biri biliniyorsa
// satır döner.
type promNamedVM struct {
	known   map[string]bool
	labels  []string
	queries []string
}

func newPromNamedVM() *promNamedVM {
	return &promNamedVM{
		known: map[string]bool{"http_server_request_duration_seconds_count": true, "http_server_request_duration_seconds_bucket": true,
			"http_server_request_duration_seconds_sum": true},
		labels: []string{"http_response_status_code", "http_route", "k8s_pod_name", "le", "service_name"},
	}
}

func (f *promNamedVM) Configured() bool                  { return true }
func (f *promNamedVM) MetricLabelMap() vmetrics.LabelMap { return vmetrics.LabelMap{} }
func (f *promNamedVM) MetricLabelValuesIn(context.Context, string, string, time.Time, time.Time, int) ([]string, bool, error) {
	return nil, false, nil
}

func (f *promNamedVM) MetricLabelNamesIn(_ context.Context, metric string, _, _ time.Time) ([]string, bool, error) {
	for _, c := range append(vmetrics.NameCandidates("counter", metric), vmetrics.NameCandidates("count", metric)...) {
		if f.known[c] {
			return f.labels, false, nil
		}
	}
	return nil, false, nil
}

func (f *promNamedVM) hit(q string) bool {
	f.queries = append(f.queries, q)
	for _, n := range queriedNames(q) {
		if f.known[n] {
			return true
		}
	}
	return false
}

func (f *promNamedVM) QueryPromQLInstant(_ context.Context, q string, at time.Time) ([]chstore.SpanMetricSeries, error) {
	if !f.hit(q) {
		return nil, nil
	}
	gk := []string{`http_route="/orders"`, `service_name="orders"`}
	if strings.Contains(q, ", le)") {
		gk = append(gk, `le="+Inf"`)
	}
	return []chstore.SpanMetricSeries{{GroupKey: gk, Points: []chstore.SpanMetricPoint{{Time: at.UnixNano(), Value: 12}}}}, nil
}

func (f *promNamedVM) QueryPromQLRange(_ context.Context, q string, from, to time.Time, step, _ int) ([]chstore.SpanMetricSeries, error) {
	if !f.hit(q) {
		return nil, nil
	}
	if step <= 0 {
		step = 60
	}
	var pts []chstore.SpanMetricPoint
	for t := from.Unix(); t <= to.Unix(); t += int64(step) {
		pts = append(pts, chstore.SpanMetricPoint{Time: t * 1e9, Value: 60})
	}
	return []chstore.SpanMetricSeries{{GroupKey: []string{`service_name="orders"`}, Points: pts}}, nil
}

func httpPin(t *testing.T) anomaly.REDFamilyPin {
	t.Helper()
	for _, p := range anomaly.REDFamilyPins() {
		if p.ID == "http" {
			return p
		}
	}
	t.Fatal("http ailesi pinlerde yok")
	return anomaly.REDFamilyPin{}
}

// Bu hatayı yakalayacak test: yalnız Prometheus yazımını bilen VM'de HTTP
// ailesi keşfedilir ve tik, 1 dk'lık etki sorgusu, yoklama, kayıt sorgusu
// seri döndürür.
func TestHTTPFamilyOnPrometheusNamedVM(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC() // sabit an; saat dilimi / takvim bağımsız
	pin := httpPin(t)
	vm := newPromNamedVM()
	d := NewTraceRED()
	deps := Deps{VM: vm, Backoff: newVMBackoff()}

	fl := d.familyLabels(ctx, vm, pin, now.Add(-15*time.Minute), now)
	if !fl.ok || fl.svc != "service_name" || fl.route != "http_route" || fl.status != "http_response_status_code" {
		t.Fatalf("etiket keşfi HTTP ailesini bulmalı: %+v", fl)
	}

	// Tik: count, err, p95, yavaş pay.
	cq, eq, pq := traceQueries(pin, fl.svc, fl.route, fl.status, 300)
	sq := slowShareQuery(pin, fl.svc, fl.route, 300, 1)
	for name, q := range map[string]string{"count": cq, "err": eq, "p95": pq, "slow": sq} {
		rows, skip, err := vmInstant(ctx, deps, "trace:"+pin.ID, q, now, now)
		if err != nil || skip || len(rows) == 0 {
			t.Errorf("tik %s sorgusu seri döndürmeli: %q (rows=%d skip=%v err=%v)", name, q, len(rows), skip, err)
		}
	}

	// 1 dk'lık etki sorgusu (svc_error_rate).
	end := svcImpactEnd(now, 120)
	agg := newSvcAgg()
	if err := svcImpactFetch(ctx, deps, pin, fl, 0, agg, end, now); err != nil {
		t.Fatal(err)
	}
	minute := map[string]map[int64]float64{}
	agg.emit(minute)
	if len(minute) == 0 {
		t.Errorf("1 dk'lık etki sorgusu seri döndürmeli; sorgular: %q", vm.queries[len(vm.queries)-2:])
	}

	// Yoklama: son tikin keşfettiği aileler (keşif sorgusu yok) + 1 dk bucket.
	fams := d.cachedFamilies()
	if len(fams) != 1 || fams[0].pin.ID != "http" {
		t.Fatalf("yoklama HTTP ailesini görmeli: %+v", fams)
	}
	pagg, err := d.impactMinutes(ctx, deps, fams, end, now)
	pm := map[string]map[int64]float64{}
	pagg.emit(pm)
	if err != nil || len(pm) == 0 {
		t.Errorf("yoklamanın 1 dk'lık okuması seri döndürmeli: err=%v", err)
	}
	w, err := d.rtMinutes(ctx, deps, fams, end, now)
	if err != nil || w.blindFrom != -1 {
		t.Errorf("yanıt süresi yoklaması ölçebilmeli: blindFrom=%d err=%v", w.blindFrom, err)
	}
	if rows, _, _ := vmInstant(ctx, deps, "t", rtBucketQuery(pin, fl.svc, 60), now, now); len(rows) == 0 {
		t.Errorf("1 dk bucket sorgusu seri döndürmeli: %q", rtBucketQuery(pin, fl.svc, 60))
	}

	// Kayıt sorguları (aralık).
	es, ok1 := errSeriesQuery(pin, fl.svc, fl.route, fl.status, "orders", "/orders", 300)
	ls, ok2 := latSeriesQuery(pin, fl.svc, fl.route, "orders", "/orders", "p95", 300, 1)
	rs, ok3 := rtSeriesQuery(pin, fl.svc, "orders", rtMeasures[0])
	if !ok1 || !ok2 || !ok3 {
		t.Fatal("kayıt sorgusu kurulamadı")
	}
	for name, q := range map[string]string{"hata oranı": es.Query, "hata hacmi": es.VolumeQuery, "p95": ls.Query, "p95 hacmi": ls.VolumeQuery,
		"yanıt süresi": rs.Query, "yanıt hacmi": rs.VolumeQuery} {
		rows, err := vm.QueryPromQLRange(ctx, q, now.Add(-30*time.Minute), now, 60, 0)
		if err != nil || len(rows) == 0 {
			t.Errorf("kayıt sorgusu (%s) seri döndürmeli: %q", name, q)
		}
	}
}

// balanced — SAF: tırnak dışı (), {}, [] dengeli ve doğru iç içe.
func balanced(q string) bool {
	var st []byte
	pair := map[byte]byte{')': '(', '}': '{', ']': '['}
	for _, c := range []byte(stripQuoted(q)) {
		switch c {
		case '(', '{', '[':
			st = append(st, c)
		case ')', '}', ']':
			if len(st) == 0 || st[len(st)-1] != pair[c] {
				return false
			}
			st = st[:len(st)-1]
		}
	}
	return len(st) == 0 && strings.Count(q, `"`)%2 == 0
}

var bareDottedRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*\.[A-Za-z_]`)

// famQuery — tablo satırı: kurucunun çıktısı + beklenen metrik başvurusu sayısı.
type famQuery struct {
	name string
	q    string
	refs int
}

// checkFamQueries — her ifade: sözdizimi dengeli, tırnak dışında noktalı ad
// yok, başvuru sayısı beklenen, seçilen her ad ailenin yazım adaylarından ve
// her başvuru ailenin TÜM adaylarını taşıyor (aday seçicisinden geçmiş).
func checkFamQueries(t *testing.T, fam, names string, qs []famQuery) {
	t.Helper()
	cands := map[string]bool{}
	nCount, nBucket := len(vmetrics.NameCandidates("count", names)), len(vmetrics.NameCandidates("bucket", names))
	if nCount != nBucket || nCount == 0 {
		t.Fatalf("%s: aday sayıları count=%d bucket=%d", fam, nCount, nBucket)
	}
	for _, c := range append(vmetrics.NameCandidates("count", names), vmetrics.NameCandidates("bucket", names)...) {
		cands[c] = true
	}
	for _, x := range qs {
		if x.q == "" {
			t.Errorf("%s/%s: ifade boş", fam, x.name)
			continue
		}
		if !balanced(x.q) {
			t.Errorf("%s/%s: parantez / tırnak dengesiz: %s", fam, x.name, x.q)
		}
		if m := bareDottedRe.FindString(stripQuoted(x.q)); m != "" {
			t.Errorf("%s/%s: tırnak dışında noktalı ad (%q): %s", fam, x.name, m, x.q)
		}
		got, sel := selectorsOf(x.q)
		if sel != x.refs {
			t.Errorf("%s/%s: %d metrik başvurusu bekleniyordu, %d: %s", fam, x.name, x.refs, sel, x.q)
		}
		if len(got) != x.refs*nCount {
			t.Errorf("%s/%s: her başvuru %d adayı taşımalı (toplam %d): %s", fam, x.name, nCount, len(got), x.q)
		}
		for _, n := range got {
			if !cands[n] {
				t.Errorf("%s/%s: aday olmayan ad %q: %s", fam, x.name, n, x.q)
			}
		}
	}
}

// TÜM RED aileleri × ML'in her sorgu kurucusu.
func TestREDFamilyQueriesUseCandidateSelector(t *testing.T) {
	const svcL, routeL, statusL, podL = "service_name", "http_route", "status_code", "k8s_pod_name"
	for _, pin := range anomaly.REDFamilyPins() {
		fl := traceFamilyLabels{svc: svcL, route: routeL, status: statusL, ok: true}
		var qs []famQuery
		add := func(name, q string, refs int) { qs = append(qs, famQuery{name, q, refs}) }
		cq, eq, pq := traceQueries(pin, svcL, routeL, statusL, 300)
		add("tik count", cq, 1)
		add("tik err", eq, 1)
		add("tik p95", pq, 1)
		add("tik yavaş pay", slowShareQuery(pin, svcL, routeL, 300, 1), 1)
		ic, ie := svcImpactQueries(pin, svcL, statusL)
		add("etki count 1dk", ic, 1)
		add("etki err 1dk", ie, 1)
		add("yanıt süresi bucket 1dk", rtBucketQuery(pin, svcL, 60), 1)
		add("yanıt süresi bucket 5dk", rtBucketQuery(pin, svcL, 300), 1)
		pt, pe, pp := tracePodQueries(pin, svcL, podL, statusL, 600)
		add("pod toplam", pt, 1)
		add("pod err", pe, 1)
		add("pod p95", pp, 1)
		for _, route := range []string{"/orders/{id}", ""} {
			es, ok := errSeriesQuery(pin, svcL, routeL, statusL, "orders", route, 300)
			if !ok {
				t.Fatalf("%s: errSeriesQuery route=%q", pin.ID, route)
			}
			add("kayıt hata oranı route="+route, es.Query, 2)
			add("kayıt hata hacmi route="+route, es.VolumeQuery, 1)
		}
		for _, m := range []string{"p95", "slow_ratio"} {
			ls, ok := latSeriesQuery(pin, svcL, routeL, "orders", "/orders/{id}", m, 300, 1)
			if !ok {
				t.Fatalf("%s: latSeriesQuery %s", pin.ID, m)
			}
			add("kayıt gecikme "+m, ls.Query, 1)
			add("kayıt gecikme hacmi "+m, ls.VolumeQuery, 1)
		}
		tp, ok := tpSeriesQuery(pin, svcL, routeL, "orders", "/orders/{id}", 300)
		if !ok {
			t.Fatalf("%s: tpSeriesQuery", pin.ID)
		}
		add("kayıt throughput", tp.Query, 1)
		for _, m := range rtMeasures {
			rs, ok := rtSeriesQuery(pin, svcL, "orders", m)
			if !ok {
				t.Fatalf("%s: rtSeriesQuery", pin.ID)
			}
			add("kayıt yanıt süresi "+m.agg, rs.Query, 1)
			add("kayıt yanıt hacmi "+m.agg, rs.VolumeQuery, 1)
		}
		pqe, ok := podSeriesQueries(pin, svcL, podL, statusL, "orders", "orders-7c9f-x2", "err_ratio", 600)
		if !ok {
			t.Fatalf("%s: podSeriesQueries err_ratio", pin.ID)
		}
		add("pod kayıt toplam", pqe.Total, 1)
		add("pod kayıt ölçü (err)", pqe.Meas, 1)
		add("pod kayıt çağrı", pqe.Calls, 1)
		add("pod kayıt seri (err)", pqe.Series, 2)
		pqp, ok := podSeriesQueries(pin, svcL, podL, statusL, "orders", "orders-7c9f-x2", "p95", 600)
		if !ok {
			t.Fatalf("%s: podSeriesQueries p95", pin.ID)
		}
		add("pod kayıt ölçü (p95)", pqp.Meas, 1)
		add("pod kayıt seri (p95)", pqp.Series, 1)
		ve, vl, ok := volCtxQueries(pin, fl, "orders")
		if !ok {
			t.Fatalf("%s: volCtxQueries", pin.ID)
		}
		add("hacim yan seri hata", ve, 2)
		add("hacim yan seri p95", vl, 1)
		for m, refs := range map[string]int{"err_ratio": 2, "p95": 1} {
			sq, ok := TracePodSourceQuery(pin, TracePodLabels{Svc: svcL, Pod: podL, Status: statusL}, "orders", m)
			if !ok {
				t.Fatalf("%s: TracePodSourceQuery %s", pin.ID, m)
			}
			add("kaynak pod "+m, sq.Expr, refs)
		}
		for m, refs := range map[string]int{"p95": 1, "slow_ratio": 1, "err_ratio": 2, "rate": 1} {
			sq, ok := TraceREDSourceQuery(pin, TraceREDLabels{Svc: svcL, Route: routeL, Status: statusL}, "orders", "/orders/{id}", m, 300, 1)
			if !ok {
				t.Fatalf("%s: TraceREDSourceQuery %s", pin.ID, m)
			}
			add("kaynak endpoint "+m, sq.Expr, refs)
		}
		checkFamQueries(t, pin.ID, pin.Names, qs)
		// Keşif sorguların okuduğu aileyi sorar: VM'in keşif adayları (düz
		// yazımlar + _count kardeşleri) count adaylarının hepsini kapsar.
		disc := map[string]bool{}
		for _, c := range append(vmetrics.NameCandidates("counter", REDDiscoveryMetric(pin)), vmetrics.NameCandidates("count", REDDiscoveryMetric(pin))...) {
			disc[c] = true
		}
		for _, c := range vmetrics.NameCandidates("count", pin.Names) {
			if !disc[c] {
				t.Errorf("%s: keşif %q yazımını sormuyor", pin.ID, c)
			}
		}
	}
}

// Client (bağımlılık) aileleri de aynı seçiciden geçer.
func TestDependencyFamilyQueriesUseSelector(t *testing.T) {
	bt := svcBatches(map[string][]string{"orders": {"db-main", "db.replica"}, "pay.api": {"ledger"}}, 50)[0]
	for _, f := range depFamilies {
		var qs []famQuery
		add := func(name, q string, refs int) { qs = append(qs, famQuery{name, q, refs}) }
		c, p := depQueries(f, "service_name", f.Targets[0], 300)
		add("count", c, 1)
		add("p95", p, 1)
		add("err", depErrQuery(f, "service_name", f.Targets[0], 300), 1)
		add("tik topk", depTopkQuery(f, "service_name", f.Targets[0], 20, 300), 1)
		add("tik grup p95", depBatchP95Query(f, "service_name", f.Targets[0], bt, 300), 1)
		ls, _, ok := depSeriesQuery(detDependencyLatency, f.ID+"|db-main", "service_name", f.Targets[0], "orders", 300)
		if !ok {
			t.Fatalf("%s: depSeriesQuery gecikme", f.ID)
		}
		add("kayıt p95", ls.Query, 1)
		add("kayıt hacim", ls.VolumeQuery, 1)
		sq, ok := dependencySourceQuery(f.ID+"|db-main", "orders", "service_name")
		if !ok {
			t.Fatalf("%s: dependencySourceQuery", f.ID)
		}
		add("kaynak p95", sq.Expr, len(f.Targets))
		if f.ErrorRate {
			es, _, ok := depSeriesQuery(detDependencyErrorRate, f.ID+"|db-main", "service_name", f.Targets[0], "orders", 300)
			if !ok {
				t.Fatalf("%s: depSeriesQuery hata", f.ID)
			}
			add("kayıt hata oranı", es.Query, 2)
			eq, ok := dependencyErrSourceQuery(f.ID+"|db-main", "orders", "service_name")
			if !ok {
				t.Fatalf("%s: dependencyErrSourceQuery", f.ID)
			}
			add("kaynak hata oranı", eq.Expr, 2*len(f.Targets))
		}
		checkFamQueries(t, "dep:"+f.ID, f.Metric, qs)
	}
}

// Seçicinin kendisi: HTTP = çapalı literal alternasyon (çalışan yolla aynı
// eşleyici metni), tek adaylı aile çıplak ve bayt bayt eski biçim.
func TestFamSelector(t *testing.T) {
	const httpCount = `{__name__=~"^(http\\.server\\.request\\.duration_count|http_server_request_duration_count|http_server_request_duration_seconds_count|http_server_request_duration_milliseconds_count|http_server_request_duration_bytes_count|http_server_request_duration_ratio_count)$"`
	const httpBucket = `{__name__=~"^(http\\.server\\.request\\.duration_bucket|http_server_request_duration_bucket|http_server_request_duration_seconds_bucket|http_server_request_duration_milliseconds_bucket|http_server_request_duration_bytes_bucket|http_server_request_duration_ratio_bucket)$"`
	http := httpPin(t)
	grpc := anomaly.REDFamilyPin{ID: "grpc_legacy", Names: "rpc_server_duration_milliseconds", Unit: "ms", ServerErr: "2|4|8|12|13|14|15"}
	for _, c := range []struct{ name, got, want string }{
		{"http count", redSel(http, partCount), httpCount + "}"},
		{"http bucket", redSel(http, partBucket), httpBucket + "}"},
		{"http count + eşleyiciler", redSel(http, partCount, mRe("http_response_status_code", "5.."), mEq("http_route", "/orders/{id}"), mEq("service_name", "orders")),
			httpCount + `,http_response_status_code=~"5..",http_route="/orders/{id}",service_name="orders"}`},
		{"boş eşleyici atlanır", redSel(http, partBucket, "", mEq("service_name", "orders"), mEq("", "x")), httpBucket + `,service_name="orders"}`},
		{"gRPC çıplak", redSel(grpc, partCount), "rpc_server_duration_milliseconds_count"},
		{"gRPC çıplak bucket", redSel(grpc, partBucket), "rpc_server_duration_milliseconds_bucket"},
		{"gRPC + durum", redSel(grpc, partCount, mRe("rpc_grpc_status_code", grpc.ServerErr)), `rpc_server_duration_milliseconds_count{rpc_grpc_status_code=~"2|4|8|12|13|14|15"}`},
		{"gRPC + kapsam", redSel(grpc, partBucket, mEq("rpc_method", "getQuote"), mEq("service_name", "quotes")), `rpc_server_duration_milliseconds_bucket{rpc_method="getQuote",service_name="quotes"}`},
		{"client dolu etiket", depSel(depFamilies[1], partCount, mSet(depErrorLabel)), `db_client_operation_duration_seconds_count{error_type!=""}`},
		{"değer kaçışı", mEq("service_name", "a\"b\\c\nd"), `service_name="a\"b\\c\nd"`},
		{"regex kaçışı", mRe("service_name", `orders\.api|pay`), `service_name=~"orders\\.api|pay"`},
		{"noktalı etiket adı tırnaklı", mEq("k8s.pod.name", "p-1"), `"k8s.pod.name"="p-1"`},
		{"boş etiket", mEq("", "x"), ""},
		{"boş ad hiçbir şeyi seçmez", famSelector("", partCount), `{__name__=""}`},
	} {
		if c.got != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.name, c.got, c.want)
		}
	}
	// Tek tanım: ad eşleyicisi çalışan yolun ürettiği metnin aynısı.
	if want := vmetrics.NameSelector(vmetrics.NameCandidates("count", http.Names)); !strings.HasPrefix(redSel(http, partCount), "{"+want) {
		t.Errorf("ad eşleyicisi vmetrics.NameSelector ile aynı olmalı: %s", redSel(http, partCount))
	}
	// gRPC ve client ailelerinin aday kümesi yalnız bugünkü addır: aynı seriler.
	for _, p := range anomaly.REDFamilyPins() {
		if p.ID == "http" {
			continue
		}
		if c := vmetrics.NameCandidates("count", p.Names); len(c) != 1 || c[0] != p.Names+"_count" {
			t.Errorf("%s: count adayları %v, yalnız %s_count olmalı", p.ID, c, p.Names)
		}
		if c := vmetrics.NameCandidates("bucket", p.Names); len(c) != 1 || c[0] != p.Names+"_bucket" {
			t.Errorf("%s: bucket adayları %v, yalnız %s_bucket olmalı", p.ID, c, p.Names)
		}
	}
	for _, f := range depFamilies {
		if c := vmetrics.NameCandidates("count", f.Metric); len(c) != 1 || c[0] != f.Metric+"_count" {
			t.Errorf("dep %s: count adayları %v", f.ID, c)
		}
	}
}

// Birim: ölçek YAZIMDAN değil pinden (çalışan yol: anomaly vmREDExpr, Unit
// "ms" değilse × 1000). HTTP ailesi saniye; gRPC legacy milisaniye.
func TestREDFamilyUnitScale(t *testing.T) {
	http := httpPin(t)
	grpcMs := anomaly.REDFamilyPin{ID: "grpc_legacy", Names: "rpc_server_duration_milliseconds", Unit: "ms", ServerErr: "2"}
	if http.Unit != "s" || pinMsScale(http) != 1000 || pinMsScale(grpcMs) != 1 {
		t.Fatalf("ms ölçeği: http %q × %g, gRPC legacy × %g", http.Unit, pinMsScale(http), pinMsScale(grpcMs))
	}
	if pinSlowLe(http, 0.5) != 0.5 || pinSlowLe(grpcMs, 0.5) != 500 {
		t.Fatalf("yavaş eşiği: http le=%g, gRPC legacy le=%g", pinSlowLe(http, 0.5), pinSlowLe(grpcMs, 0.5))
	}
	type lq struct{ name, q, suffix, share string }
	build := func(pin anomaly.REDFamilyPin, suffix, share string) []lq {
		p95, _ := latSeriesQuery(pin, "service_name", "http_route", "orders", "/o", "p95", 300, 0.5)
		slow, _ := latSeriesQuery(pin, "service_name", "http_route", "orders", "/o", "slow_ratio", 300, 0.5)
		rt, _ := rtSeriesQuery(pin, "service_name", "orders", rtMeasures[1])
		pod, _ := podSeriesQueries(pin, "service_name", "k8s_pod_name", "status_code", "orders", "p-1", "p95", 600)
		_, vol, _ := volCtxQueries(pin, traceFamilyLabels{svc: "service_name", status: "status_code"}, "orders")
		srcP, _ := TraceREDSourceQuery(pin, TraceREDLabels{Svc: "service_name", Route: "http_route"}, "orders", "/o", "p95", 300, 0.5)
		srcS, _ := TraceREDSourceQuery(pin, TraceREDLabels{Svc: "service_name", Route: "http_route"}, "orders", "/o", "slow_ratio", 300, 0.5)
		srcPod, _ := TracePodSourceQuery(pin, TracePodLabels{Svc: "service_name", Pod: "k8s_pod_name"}, "orders", "p95")
		return []lq{
			{"kayıt p95", p95.Query, suffix, ""}, {"kayıt yanıt süresi", rt.Query, suffix, ""}, {"pod ölçü", pod.Meas, suffix, ""},
			{"pod seri", pod.Series, suffix, ""}, {"hacim yan seri", vol, suffix, ""}, {"kaynak p95", srcP.Expr, suffix, ""},
			{"kaynak pod p95", srcPod.Expr, suffix, ""},
			{"kayıt yavaş oran", slow.Query, "", share}, {"kaynak yavaş oran", srcS.Expr, "", share},
			{"tik yavaş pay", slowShareQuery(pin, "service_name", "http_route", 300, 0.5), "", share},
		}
	}
	for _, x := range append(build(http, " * 1000", "histogram_share(0.5, "), build(grpcMs, " * 1", "histogram_share(500, ")...) {
		if x.suffix != "" && !strings.HasSuffix(x.q, x.suffix) {
			t.Errorf("%s: ms'ye çevrim %q ile bitmeli: %s", x.name, x.suffix, x.q)
		}
		if x.share != "" && !strings.Contains(x.q, x.share) {
			t.Errorf("%s: yavaş eşiği %q olmalı: %s", x.name, x.share, x.q)
		}
	}
	// Tikin p95 / yanıt süresi dönüşümü sorguda değil kodda: aynı ölçek.
	rows := []chstore.SpanMetricSeries{
		{GroupKey: []string{`le="0.25"`, `service_name="orders"`}, Points: []chstore.SpanMetricPoint{{Time: 1, Value: 100}}},
		{GroupKey: []string{`le="+Inf"`, `service_name="orders"`}, Points: []chstore.SpanMetricPoint{{Time: 1, Value: 100}}},
	}
	if p := rtFromBuckets(rows, "service_name", pinMsScale(http))["orders"]; p.Q[0] <= 0 || p.Q[0] > 250 || p.Q[1] <= p.Q[0] || p.Q[1] > 250 {
		t.Errorf("saniye bucket'ı ms'ye: p50=%g p90=%g (0..250 ms)", p.Q[0], p.Q[1])
	}
}

// Öz-gözlem: aile keşfedildi ama count sorgusu art arda famEmptyBuckets kova
// boş → açık trace dedektörlerinde not; tek seri dönünce temizlenir.
func TestFamEmptyCounter(t *testing.T) {
	var e famEmpty
	for i, c := range []struct {
		bucket int64
		rows   int
		n      int
		flag   bool
	}{
		{300, 0, 1, false},
		{300, 0, 1, false}, // erken tik: aynı kova yeniden sayılmaz
		{600, 0, 2, false},
		{900, 0, 3, true},
		{900, 0, 3, true},
		{1200, 0, 4, true},
		{1500, 7, 0, false}, // seri döndü: temiz
		{1500, 0, 1, false},
		{1800, 0, 2, false},
	} {
		e = e.next(c.bucket, c.rows)
		if e.n != c.n || e.flagged() != c.flag {
			t.Fatalf("adım %d: n=%d flag=%v (want n=%d flag=%v)", i, e.n, e.flagged(), c.n, c.flag)
		}
	}
	if famEmptyNote(nil, true) != "" ||
		famEmptyNote([]string{"http"}, true) != "kısmen ölçülüyor: HTTP ailesi keşfedildi ama sorgular seri döndürmüyor" ||
		famEmptyNote([]string{"http"}, false) != "ölçülemedi: HTTP ailesi keşfedildi ama sorgular seri döndürmüyor" {
		t.Fatalf("not: %q | %q", famEmptyNote([]string{"http"}, true), famEmptyNote([]string{"http"}, false))
	}
}

func TestTraceREDEmptyFamilyNote(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	cfg.Enabled = true
	cfg.Detectors[detOpErrorRate] = true
	cfg.Detectors[detThroughputDrop] = false
	d := NewTraceRED()
	seen := map[string]bool{"http": true, "grpc_legacy": true}
	for i := int64(1); i <= famEmptyBuckets; i++ {
		if u := d.Unmeasured(); len(u) != 0 {
			t.Fatalf("eşikten önce not olmamalı (kova %d): %v", i, u)
		}
		d.observeFamily("http", i*300, 0)
		d.observeFamily("http", i*300, 0) // erken tik
		d.observeFamily("grpc_legacy", i*300, 4)
		d.setEmptyNote(seen, cfg)
	}
	u := d.Unmeasured()
	const partly = "kısmen ölçülüyor: HTTP ailesi keşfedildi ama sorgular seri döndürmüyor"
	for _, det := range []string{detOpLatency, detOpErrorRate, detSvcErrorRate, detSvcResponseTime} {
		if u[det] != partly {
			t.Errorf("%s notu: %q", det, u[det])
		}
	}
	if _, ok := u[detThroughputDrop]; ok {
		t.Errorf("kapalı dedektöre not yazılmaz: %v", u)
	}
	// Sorgu hatası notu daha özeldir: kendi dedektöründe o görünür.
	d.setImpactNote(context.DeadlineExceeded)
	if u := d.Unmeasured(); !strings.HasPrefix(u[detSvcErrorRate], "ölçülemedi: ") || u[detOpLatency] != partly {
		t.Errorf("sorgu hatası notu öncelikli olmalı: %v", u)
	}
	d.setImpactNote(nil)
	// Hiçbir aile seri döndürmüyorsa "ölçülemedi".
	d2 := NewTraceRED()
	for i := int64(1); i <= famEmptyBuckets; i++ {
		d2.observeFamily("http", i*300, 0)
		d2.setEmptyNote(map[string]bool{"http": true}, cfg)
	}
	if got := d2.Unmeasured()[detOpLatency]; got != "ölçülemedi: HTTP ailesi keşfedildi ama sorgular seri döndürmüyor" {
		t.Errorf("tek aile boş: %q", got)
	}
	// Tek seri: temizlenir.
	d.observeFamily("http", 9000, 1)
	d.setEmptyNote(seen, cfg)
	if u := d.Unmeasured(); len(u) != 0 {
		t.Errorf("seri dönünce not kalkmalı: %v", u)
	}
	// Keşiften düşen ailenin sayacı silinir (yeniden keşfedilince sıfırdan).
	for i := int64(1); i <= famEmptyBuckets; i++ {
		d2.observeFamily("http", 9000+i*300, 0)
	}
	d2.setEmptyNote(map[string]bool{}, cfg)
	if u := d2.Unmeasured(); len(u) != 0 || len(d2.empty) != 0 {
		t.Errorf("keşiften düşen aile: not %v sayaç %v", u, d2.empty)
	}
}
