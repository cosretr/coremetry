package api

// v0.10.1454 — outbound_call (R1) regresyon testleri.
//
// Operator-reported: servis sayfasında "**/esb/external/…/customer-kvkk/**
// kısmı için grafikleri ortalamayı gösterebilirsin" → 32,8 sn, 75K token,
// "veri bulunamadı". Yol bir CLIENT span'inin url.path'i; katalogda yok.
// Pinler: (1) "…" anmayı öldürmez, (2) sonda HIT → guided outbound_call,
// LLM'siz, chart + table blokları, (3) sonda MISS/hata → eski yol-yalnız,
// (4) kademe sırası ve imzalar dokunulmaz. Adlar sentetik.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

const (
	obTestHost  = "esbprod.example.internal"
	obTestSlow  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	obTestErr   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	obTestPath  = "/esb/external/.../customer-kvkk"
	obTestAsk   = "**/esb/external/…/customer-kvkk/** kısmı için grafikleri ortalamayı gösterebilirsin"
	obTestSvc   = "orders-api"
	obTestOther = "billing-api"
)

func TestCleanEndpointPathEllipsis(t *testing.T) {
	cases := []struct {
		name, msg, want string
	}{
		{"prod şekli (unicode elips, ** sarmalayıcı)", obTestAsk, obTestPath},
		{"ascii elips", "**/esb/external/.../customer-kvkk/** için grafik", obTestPath},
		{"yapışık unicode elips", "/esb/ExternalX…/customer-kvkk ortalama", "/esb/ExternalX.../customer-kvkk"},
		{"sondaki unicode elips korunur", "/esb/ExternalX… gecikmesi", "/esb/ExternalX..."},
		{"elipssiz değişmez", "/esb/external/rest/customer-kvkk/ grafiği", "/esb/external/rest/customer-kvkk"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, p, ok := endpointMention(c.msg)
			if !ok || p != c.want {
				t.Errorf("endpointMention(%q) = %q,%v; want %q", c.msg, p, ok, c.want)
			}
		})
	}
	if got := pathSearchText(obTestPath); got != "/esb/external/ /customer-kvkk" {
		t.Errorf("pathSearchText = %q", got)
	}
}

func TestOutboundCallersFrom(t *testing.T) {
	rows := []chstore.OutboundCaller{
		{Service: obTestOther, Host: obTestHost, Calls: 50},
		{Service: obTestSvc, Host: obTestHost, Calls: 20},
		{Service: obTestSvc, Host: "esbdr.example.internal", Calls: 5},
	}
	cases := []struct {
		name, hint string
		rows       []chstore.OutboundCaller
		want       []string
		peer       string
	}{
		{"ipucu yok: hepsi, iki host → peer yok", "", rows, []string{obTestOther, obTestSvc}, ""},
		{"ipucu satırlarda: yalnız o", obTestSvc, rows, []string{obTestSvc}, ""},
		{"ipucu satırlarda yok: hepsi", "payments", rows[:2], []string{obTestOther, obTestSvc}, obTestHost},
		{"tek host → peer", obTestOther, rows, []string{obTestOther}, obTestHost},
		{"boş", "", nil, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, peer := outboundCallersFrom(c.rows, c.hint)
			if strings.Join(got, ",") != strings.Join(c.want, ",") || peer != c.peer {
				t.Errorf("got %v/%q, want %v/%q", got, peer, c.want, c.peer)
			}
		})
	}
}

func TestOutboundWindowFor(t *testing.T) {
	to := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		rangeS  time.Duration
		callers int
		want    time.Duration
		clamped bool
	}{
		{"1 sa tek çağıran", time.Hour, 1, time.Hour, false},
		{"24 sa → 6 sa", 24 * time.Hour, 1, 6 * time.Hour, true},
		{"6 sa çok çağıran → 1 sa", 6 * time.Hour, 40, time.Hour, true},
		{"6 sa 5 çağıran kırpılmaz", 6 * time.Hour, 5, 6 * time.Hour, false},
	}
	for _, c := range cases {
		f, tt, note := outboundWindowFor(to.Add(-c.rangeS), to, c.callers)
		if tt.Sub(f) != c.want || (note != "") != c.clamped || !tt.Equal(to) {
			t.Errorf("%s: %v %q", c.name, tt.Sub(f), note)
		}
	}
	if _, _, note := outboundWindowFor(to.Add(-24*time.Hour), to, 1); !strings.Contains(note, "6 saat") {
		t.Errorf("kırpma notu pencereyi söylemeli: %q", note)
	}
}

func TestOutboundStepS(t *testing.T) {
	to := time.Now()
	for _, c := range []struct {
		r    time.Duration
		want int64
	}{{time.Hour, 60}, {6 * time.Hour, 180}, {10 * time.Minute, 60}} {
		if got := outboundStepS(to.Add(-c.r), to); got != c.want {
			t.Errorf("%v: step %d want %d", c.r, got, c.want)
		}
		if n := int64(c.r/time.Second) / outboundStepS(to.Add(-c.r), to); n > outboundChartMaxPts {
			t.Errorf("%v: %d nokta > %d", c.r, n, outboundChartMaxPts)
		}
	}
}

func TestOutboundTotal(t *testing.T) {
	rows := []chstore.OutboundAgg{
		{Win: "cur", Calls: 100, Errors: 2, AvgMs: 100, P95Ms: 300},
		{Win: "cur", Calls: 300, Errors: 6, AvgMs: 200, P95Ms: 500},
		{Win: "prev", Calls: 50, Errors: 0, AvgMs: 80, P95Ms: 200},
	}
	cur := outboundTotal(rows, "cur")
	if cur.Calls != 400 || cur.Errors != 8 || cur.AvgMs != 175 || cur.P95Exact || cur.P95Ms != 0 || cur.Groups != 2 {
		t.Errorf("cur = %+v", cur)
	}
	prev := outboundTotal(rows, "prev")
	if !prev.P95Exact || prev.P95Ms != 200 || prev.AvgMs != 80 {
		t.Errorf("prev = %+v", prev)
	}
	if z := outboundTotal(nil, "cur"); z.Calls != 0 || z.AvgMs != 0 {
		t.Errorf("boş = %+v", z)
	}
}

func TestOutboundVerdict(t *testing.T) {
	st := func(calls, errs uint64, avg, p95 float64) outboundStat {
		return outboundStat{Calls: calls, Errors: errs, AvgMs: avg, P95Ms: p95, P95Exact: p95 > 0, Groups: 1}
	}
	none := outboundStat{}
	cases := []struct {
		name            string
		cur, prev, week outboundStat
		want            string
	}{
		{"yetersiz veri", st(19, 0, 900, 0), st(100, 0, 100, 0), none, obVerdictInsufficient},
		{"referans yok", st(100, 0, 900, 0), st(5, 0, 100, 0), none, obVerdictNoRef},
		{"ort artışı (oran + ms)", st(1240, 0, 812, 0), st(1000, 0, 420, 0), st(900, 0, 395, 0), obVerdictRise},
		{"oran var ms yok", st(100, 0, 60, 0), st(100, 0, 40, 0), none, obVerdictFlat},
		{"ms var oran yok", st(100, 0, 1100, 0), st(100, 0, 1000, 0), none, obVerdictFlat},
		{"referans en yüksek kullanılır", st(100, 0, 500, 0), st(100, 0, 300, 0), st(100, 0, 450, 0), obVerdictFlat},
		{"p95 artışı", st(100, 0, 400, 2000), st(100, 0, 380, 900), none, obVerdictRise},
		{"hata artışı", st(1000, 31, 400, 0), st(1000, 4, 400, 0), none, obVerdictErrRise},
		{"hata 2 puan altı", st(1000, 20, 400, 0), st(1000, 4, 400, 0), none, obVerdictFlat},
		{"ikisi birden", st(1000, 31, 812, 0), st(1000, 4, 420, 0), none, obVerdictBoth},
		{"geçen hafta tek referans", st(100, 0, 812, 0), none, st(100, 0, 395, 0), obVerdictRise},
	}
	for _, c := range cases {
		if got, _ := outboundVerdict(c.cur, c.prev, c.week); got != c.want {
			t.Errorf("%s: %s want %s", c.name, got, c.want)
		}
	}
}

func TestRenderOutboundTR(t *testing.T) {
	in := outboundRender{Path: obTestPath, Peer: obTestHost, Callers: []string{obTestSvc}, RangeS: 3600,
		Cur:  outboundStat{Calls: 1240, Errors: 38, AvgMs: 812, P95Ms: 2310, P95Exact: true, Groups: 1},
		Prev: outboundStat{Calls: 1100, Errors: 4, AvgMs: 420, P95Ms: 900, P95Exact: true, Groups: 1},
		Week: outboundStat{Calls: 1000, Errors: 3, AvgMs: 395, P95Exact: true, P95Ms: 850, Groups: 1}}
	got := renderOutboundTR(in)
	for _, w := range []string{
		obTestSvc + " → " + obTestHost + " · " + obTestPath, "son 1 saat", "kaynak: trace (örneklenmiş)",
		"1.240 çağrı", "ort 812 ms", "önceki pencere 420 ms", "geçen hafta 395 ms", "×1,9", "p95 2.310 ms",
		"hata %3,1", "(önceki %0,4)", "**ARTIŞ VAR** (gecikme ve hata oranı)", "Dashboard'a ekle",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("metinde %q yok:\n%s", w, got)
		}
	}
	in.Scan, in.Clamped, in.Env = true, "pencere 1 saat'e kırpıldı", "prod"
	got = renderOutboundTR(in)
	for _, w := range []string{"≤100 servis son 1 saatte tarandı", "Not: pencere 1 saat'e kırpıldı", "ortam prod"} {
		if !strings.Contains(got, w) {
			t.Errorf("beyan %q yok:\n%s", w, got)
		}
	}
	empty := renderOutboundTR(outboundRender{Path: obTestPath, Callers: []string{obTestSvc}, RangeS: 3600})
	if !strings.Contains(empty, "client span YOK") || strings.Contains(empty, "ARTIŞ") {
		t.Errorf("boş küme metni: %s", empty)
	}
	bad := renderOutboundTR(outboundRender{Path: obTestPath, RangeS: 3600, ReadErr: errors.New("timeout")})
	if !strings.Contains(bad, "OKUNAMADI (okunamadı ≠ çağrı yok)") {
		t.Errorf("hata metni: %s", bad)
	}
	many := outboundSubjectTR([]string{"a", "b", "c", "d", "e"}, "", "/x")
	if many != "a, b, c +2 servis → dış hostlar · /x" {
		t.Errorf("özne: %q", many)
	}
}

func TestOutboundChartPayload(t *testing.T) {
	from := time.Unix(1_000_000, 0)
	to := from.Add(time.Hour)
	pts := []chstore.OutboundPoint{
		{Bucket: 1_000_060, Service: obTestSvc, Calls: 10, Errors: 1, AvgMs: 100, P95Ms: 200},
		{Bucket: 1_000_000, Service: obTestSvc, Calls: 10, Errors: 0, AvgMs: 90, P95Ms: 150},
		{Bucket: 1_000_000, Service: obTestOther, Calls: 4, Errors: 2, AvgMs: 50, P95Ms: 60},
		{Bucket: 1_000_120, Service: obTestOther, Calls: 0},
	}
	title := outboundChartTitle([]string{obTestSvc, obTestOther}, obTestHost, obTestPath)
	if !strings.Contains(title, "trace (örneklenmiş)") {
		t.Errorf("başlık kaynağı söylemeli: %q", title)
	}
	c := outboundChartPayload(pts, title, "/traces?x=1", from, to)
	if c == nil || len(c.Series) != 6 {
		t.Fatalf("2 çağıran × 3 seri bekleniyordu: %+v", c)
	}
	units := map[string]int{}
	for _, s := range c.Series {
		units[s.Unit]++
		if s.Group == "" {
			t.Errorf("çağıran başına Group boş: %+v", s)
		}
		if s.Name == "p95 ms" && s.Style != "secondary" {
			t.Errorf("p95 secondary olmalı")
		}
	}
	if units["ms"] != 4 || units["%"] != 2 {
		t.Errorf("birimler: %v", units)
	}
	first := c.Series[0]
	if first.Name != "ort ms" || len(first.Points) != 2 || first.Points[0][0] != 1_000_000_000 || first.Points[1][1] != 100 {
		t.Errorf("ilk seri (sıralı, ms damga): %+v", first)
	}
	for _, s := range c.Series {
		if s.Name == "hata %" && s.Group == obTestOther && s.Points[0][1] != 50 {
			t.Errorf("hata %%: %+v", s.Points)
		}
	}
	if c.Window.FromNs != from.UnixNano() || c.Window.ToNs != to.UnixNano() {
		t.Error("pencere")
	}
	if outboundChartPayload(nil, title, "", from, to) != nil {
		t.Error("boş seri → nil (blok yayınlanmaz)")
	}
}

func TestOutboundTableRows(t *testing.T) {
	rows := []chstore.OutboundAgg{
		{Service: obTestSvc, Peer: obTestHost, Path: "/esb/external/rest/customer-kvkk", Win: "cur", Calls: 200, Errors: 4, AvgMs: 300, P95Ms: 900, SlowTrace: obTestSlow, ErrTrace: obTestErr},
		{Service: obTestSvc, Peer: obTestHost, Path: "/esb/external/rest/customer-kvkk", Win: "prev", Calls: 200, AvgMs: 200},
		{Service: obTestOther, Peer: obTestHost, Path: "/esb/external/rest/customer-kvkk", Win: "cur", Calls: 500, AvgMs: 100},
		{Service: obTestOther, Peer: obTestHost, Path: "/esb/external/rest/customer-kvkk", Win: "prev", Calls: 3, AvgMs: 10},
	}
	week := []chstore.OutboundAgg{{Service: obTestSvc, Peer: obTestHost, Path: "/esb/external/rest/customer-kvkk", Win: "cur", Calls: 100, AvgMs: 150}}
	got, trunc := outboundTableRows(rows, week)
	if len(got) != 2 || trunc {
		t.Fatalf("satırlar: %v", got)
	}
	if got[0][0] != obTestOther || got[0][7] != nil {
		t.Errorf("çağrı sırası + <20 referans → Δ boş: %v", got[0])
	}
	if got[1][7] != 50.0 || got[1][8] != 100.0 || got[1][4] != 2.0 || got[1][9] != obTestSlow || got[1][10] != obTestErr {
		t.Errorf("Δ önceki/geçen hafta, hata%%, trace: %v", got[1])
	}
	if len(got[0]) != len(outboundTableColumns) {
		t.Error("sütun sayısı")
	}
	var many []chstore.OutboundAgg
	for i := 0; i < 15; i++ {
		many = append(many, chstore.OutboundAgg{Service: "s", Path: string(rune('a' + i)), Win: "cur", Calls: uint64(i + 1)})
	}
	if got, trunc := outboundTableRows(many, nil); len(got) != outboundTableMax || !trunc {
		t.Errorf("≤10 satır + truncated: %d %v", len(got), trunc)
	}
	if ids := outboundSampleTraces(rows); strings.Join(ids, ",") != obTestSlow+","+obTestErr {
		t.Errorf("örnek trace: %v", ids)
	}
}

// TestOutboundRouteJSONRoundTrip — "son 6 saate genişlet" LastRoute'u JSON'dan
// klonlar: girdi alanları geri gelmeli (gizli alan takipte düşerdi).
func TestOutboundRouteJSONRoundTrip(t *testing.T) {
	r := outboundHit{Path: obTestPath, Callers: []string{obTestSvc}, Peer: obTestHost, Service: obTestSvc, Env: "prod", Scan: true, Src: "raw"}.route()
	r.OutboundTraces = []string{obTestSlow}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var back guidedRoute
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Intent != guidedOutboundCall || back.OutboundPath != obTestPath || back.OutboundPeer != obTestHost ||
		strings.Join(back.OutboundCallers, ",") != obTestSvc || !back.OutboundScan || back.OutboundSrc != "raw" || back.Env != "prod" {
		t.Errorf("geri gelmedi: %+v", back)
	}
	if len(back.OutboundTraces) != 0 {
		t.Error("çıktı alanı JSON'a girmemeli")
	}
	m, ctxm, next, ok := applyContextMutation(ChatContext{LastRoute: &back, RangeS: 3600}, contextMutation{Kind: "window", RangeS: 6 * 3600})
	if !ok || m.Intent != guidedOutboundCall || ctxm != 6*3600 || !next.RangeExplicit {
		t.Errorf("pencere mutasyonu: %+v %d %v", m, ctxm, ok)
	}
}

func TestOutboundLinksAndChips(t *testing.T) {
	r := guidedRoute{Intent: guidedOutboundCall, Service: obTestSvc, OutboundPath: obTestPath, OutboundPeer: obTestHost,
		OutboundCallers: []string{obTestSvc}, OutboundTraces: []string{obTestSlow, obTestErr}, OutboundRangeS: 3600,
		OutboundFromMs: 1000, OutboundToMs: 2000}
	links := guidedAnswerLinks(r, linkWindowBetween(time.UnixMilli(1000), time.UnixMilli(2000)))
	var hrefs []string
	for _, l := range links {
		hrefs = append(hrefs, l.Href)
	}
	all := strings.Join(hrefs, " ")
	for _, w := range []string{"/traces?", "service=" + obTestSvc, "range=custom:1000-2000", "/external?host=" + obTestHost, "/trace?id=" + obTestSlow, "/trace?id=" + obTestErr} {
		if !strings.Contains(all, w) {
			t.Errorf("linklerde %q yok: %v", w, hrefs)
		}
	}
	fu := guidedFollowups(r, nil)
	if len(fu) != 3 || fu[0].Text != "Son 6 saate genişlet" || !strings.Contains(fu[1].Href, "hasError=true") || !strings.Contains(fu[1].Href, "range=custom") {
		t.Errorf("takip çipleri: %+v", fu)
	}
	if norm := normalizeGuidedMsg(fu[0].Text); func() bool {
		m, ok := detectContextMutation(norm, guidedTokens(norm), ChatContext{LastRoute: &r}, false)
		return !ok || m.Kind != "window" || m.RangeS != 6*3600
	}() {
		t.Error("\"Son 6 saate genişlet\" pencere kipine oturmalı")
	}
	r.OutboundRangeS = 6 * 3600
	if fu := guidedFollowups(r, nil); fu[0].Kind == fuKindMetricWindow {
		t.Error("6 sa'te genişlet çipi yok")
	}
	if s := guidedSuggestions(r); len(s) == 0 || !strings.HasPrefix(s[0], obTestSvc) {
		t.Errorf("öneriler: %v", s)
	}
	for _, tool := range []string{outboundProbeTool, outboundCallsTool, outboundSeriesTool, outboundVerdictTool} {
		if stepPhaseTR(tool) != "Sağlık" {
			t.Errorf("%s evresi", tool)
		}
	}
}

// ── sahte okuyucu ─────────────────────────────────────────────────────

type fakeOutboundReader struct {
	mu        sync.Mutex
	mv        func(needle, svc string) ([]chstore.OutboundCaller, error)
	raw       func(q chstore.OutboundQuery) ([]chstore.OutboundCaller, error)
	callers   []string
	aggs      []chstore.OutboundAgg
	week      []chstore.OutboundAgg
	points    []chstore.OutboundPoint
	aggErr    error
	calls     []string
	lastQuery chstore.OutboundQuery
	// v0.10.1455 (R2)
	codes       []chstore.OutboundCode
	codeQuery   chstore.OutboundQuery
	hostCallers func(from, to time.Time) []chstore.ExternalCaller
	hostErr     error
	hostWins    []time.Duration
	trend       []chstore.ExternalTrendPoint
	aggsFn      func(q chstore.OutboundQuery) []chstore.OutboundAgg
}

func (f *fakeOutboundReader) note(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}

func (f *fakeOutboundReader) OutboundProbeMV(_ context.Context, needle, svc string, _, _ time.Time) ([]chstore.OutboundCaller, error) {
	f.note("mv:" + needle + ":" + svc)
	if f.mv == nil {
		return nil, nil
	}
	return f.mv(needle, svc)
}

func (f *fakeOutboundReader) OutboundExternalCallers(context.Context, time.Time, time.Time) ([]string, error) {
	f.note("callers")
	return f.callers, nil
}

func (f *fakeOutboundReader) OutboundProbeRaw(_ context.Context, q chstore.OutboundQuery) ([]chstore.OutboundCaller, error) {
	f.note("raw:" + strings.Join(q.Services, ","))
	if f.raw == nil {
		return nil, nil
	}
	return f.raw(q)
}

func (f *fakeOutboundReader) OutboundAggs(_ context.Context, q chstore.OutboundQuery, prevFrom time.Time) ([]chstore.OutboundAgg, error) {
	if prevFrom.IsZero() {
		f.note("week")
		return f.week, nil
	}
	f.mu.Lock()
	f.lastQuery = q
	f.mu.Unlock()
	f.note("aggs")
	if f.aggsFn != nil {
		return f.aggsFn(q), f.aggErr
	}
	return f.aggs, f.aggErr
}

func (f *fakeOutboundReader) OutboundSeries(context.Context, chstore.OutboundQuery, int64, bool) ([]chstore.OutboundPoint, error) {
	f.note("series")
	return f.points, nil
}

func (f *fakeOutboundReader) OutboundCodes(_ context.Context, q chstore.OutboundQuery) ([]chstore.OutboundCode, error) {
	f.mu.Lock()
	f.codeQuery = q
	f.mu.Unlock()
	f.note("codes")
	return f.codes, nil
}

func (f *fakeOutboundReader) OutboundHostCallers(_ context.Context, host string, from, to time.Time) ([]chstore.ExternalCaller, error) {
	f.mu.Lock()
	f.hostWins = append(f.hostWins, to.Sub(from))
	f.mu.Unlock()
	f.note("hostcallers:" + host)
	if f.hostCallers == nil {
		return nil, f.hostErr
	}
	return f.hostCallers(from, to), f.hostErr
}

func (f *fakeOutboundReader) OutboundHostTrend(_ context.Context, host, caller string, _, _ time.Time) ([]chstore.ExternalTrendPoint, error) {
	f.note("trend:" + host + ":" + caller)
	return f.trend, nil
}

func installOutboundReader(t *testing.T, f *fakeOutboundReader) {
	t.Helper()
	prev := outboundReadFn
	outboundReadFn = func(*Server) outboundReader { return f }
	t.Cleanup(func() { outboundReadFn = prev })
}

type capturedEvent struct {
	kind    string
	payload any
}

func captureEmit() (*[]capturedEvent, func(string, any)) {
	var mu sync.Mutex
	evs := &[]capturedEvent{}
	return evs, withStepIDs(func(k string, p any) {
		mu.Lock()
		*evs = append(*evs, capturedEvent{k, p})
		mu.Unlock()
	})
}

func TestOutboundProbe(t *testing.T) {
	hit := []chstore.OutboundCaller{{Service: obTestSvc, Host: obTestHost, Calls: 40}}
	cases := []struct {
		name      string
		f         *fakeOutboundReader
		msgSvc    string
		screenSvc string
		ok        bool
		src       string
		scan      bool
		calls     string
	}{
		{"MV HIT (ekran servisi)", &fakeOutboundReader{mv: func(string, string) ([]chstore.OutboundCaller, error) { return hit, nil }},
			"", obTestSvc, true, "mv", false, "mv:customer-kvkk:" + obTestSvc},
		{"MV boş → ham sonda (mesaj servisi)", &fakeOutboundReader{raw: func(chstore.OutboundQuery) ([]chstore.OutboundCaller, error) { return hit, nil }},
			obTestSvc, "", true, "raw", false, "mv:customer-kvkk:" + obTestSvc + " mv:customer-kvkk: raw:" + obTestSvc},
		{"çağıran belirsiz → ≤100 tarama + beyan", &fakeOutboundReader{callers: []string{obTestOther, obTestSvc},
			raw: func(q chstore.OutboundQuery) ([]chstore.OutboundCaller, error) { return hit, nil }},
			"", "", true, "raw", true, "mv:customer-kvkk: callers raw:" + obTestOther + "," + obTestSvc},
		{"ekran servisi çağırmıyor → tarama", &fakeOutboundReader{callers: []string{obTestOther},
			raw: func(q chstore.OutboundQuery) ([]chstore.OutboundCaller, error) {
				if len(q.Services) == 1 && q.Services[0] == obTestOther {
					return []chstore.OutboundCaller{{Service: obTestOther, Host: obTestHost, Calls: 9}}, nil
				}
				return nil, nil
			}},
			"", "payments", true, "raw", true, "mv:customer-kvkk:payments mv:customer-kvkk: raw:payments callers raw:" + obTestOther},
		{"mesaj servisi çağırmıyor → tarama YOK, MISS", &fakeOutboundReader{callers: []string{obTestOther}},
			"payments", "", false, "", false, "mv:customer-kvkk:payments mv:customer-kvkk: raw:payments"},
		{"sonda hatası → MISS (eski yol)", &fakeOutboundReader{raw: func(chstore.OutboundQuery) ([]chstore.OutboundCaller, error) { return nil, errors.New("timeout") }},
			obTestSvc, "", false, "", false, "mv:customer-kvkk:" + obTestSvc + " mv:customer-kvkk: raw:" + obTestSvc},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			installOutboundReader(t, c.f)
			s := &Server{}
			ctx := armOutboundSlot(context.Background(), c.screenSvc, "prod", 3600, time.Now())
			evs, emit := captureEmit()
			h, ok := s.outboundProbe(ctx, emit, outboundSlotFrom(ctx), obTestPath, c.msgSvc, nil)
			if ok != c.ok || h.Src != c.src || h.Scan != c.scan {
				t.Errorf("hit=%+v ok=%v", h, ok)
			}
			if got := strings.Join(c.f.calls, " "); got != c.calls {
				t.Errorf("okumalar %q, want %q", got, c.calls)
			}
			if ok && (h.Path != obTestPath || h.Env != "prod" || len(h.Callers) != 1) {
				t.Errorf("hit alanları: %+v", h)
			}
			step := false
			for _, e := range *evs {
				if m, _ := e.payload.(map[string]any); e.kind == "step" && m["tool"] == outboundProbeTool {
					step = true
				}
			}
			if !step {
				t.Error("outbound_probe çipi yayınlanmadı")
			}
		})
	}
	// okuyucu yok → MISS, okuma yok.
	prev := outboundReadFn
	outboundReadFn = func(*Server) outboundReader { return nil }
	defer func() { outboundReadFn = prev }()
	ctx := armOutboundSlot(context.Background(), "", "", 0, time.Time{})
	if _, ok := (&Server{}).outboundProbe(ctx, func(string, any) {}, outboundSlotFrom(ctx), obTestPath, "", nil); ok {
		t.Error("okuyucusuz HIT olmamalı")
	}
}

// TestOutboundGuidedHandler — sonda HIT'i yuvada → copilotChatGuided rotayı
// outbound_call'a alır: LLM çağrısı yok, outbound_* çipleri, chart + table
// blokları, "ARTIŞ VAR", 2 örnek trace + External linki, LastRoute outbound_call.
func TestOutboundGuidedHandler(t *testing.T) {
	llm := newLoopLLM(t, func(int, map[string]any) map[string]any { return map[string]any{"content": "MODEL"} })
	s, _ := newLoopTestServer(t, llm)
	f := &fakeOutboundReader{
		aggs: []chstore.OutboundAgg{
			{Service: obTestSvc, Peer: obTestHost, Path: "/esb/external/rest/customer-kvkk", Win: "cur", Calls: 1240, Errors: 38, AvgMs: 812, P95Ms: 2310, SlowTrace: obTestSlow, ErrTrace: obTestErr},
			{Service: obTestSvc, Peer: obTestHost, Path: "/esb/external/rest/customer-kvkk", Win: "prev", Calls: 1100, Errors: 4, AvgMs: 420, P95Ms: 900},
		},
		week:   []chstore.OutboundAgg{{Service: obTestSvc, Peer: obTestHost, Path: "/esb/external/rest/customer-kvkk", Win: "cur", Calls: 1000, AvgMs: 395, P95Ms: 850}},
		points: []chstore.OutboundPoint{{Bucket: 1_000_000, Calls: 20, Errors: 1, AvgMs: 700, P95Ms: 2000}, {Bucket: 1_000_060, Calls: 22, AvgMs: 820, P95Ms: 2100}},
	}
	installOutboundReader(t, f)
	ctx := armOutboundSlot(context.Background(), obTestSvc, "", 900, time.Time{})
	outboundSlotFrom(ctx).set(outboundHit{Path: obTestPath, Callers: []string{obTestSvc}, Peer: obTestHost, Service: obTestSvc, Src: "mv"})
	evs, emit := captureEmit()
	msgs := []copilot.ChatMessage{{Role: "user", Text: obTestAsk}}
	handled, ok := s.copilotChatGuided(ctx, emit, msgs, obTestSvc, "", "", 900, "", "", time.Time{}, 0, "")
	if !handled || !ok {
		t.Fatalf("handled=%v ok=%v", handled, ok)
	}
	if n := len(llm.calls()); n != 0 {
		t.Errorf("LLM'siz olmalı, %d çağrı", n)
	}
	if f.lastQuery.PathLike != "/esb/external/%/customer-kvkk" || f.lastQuery.PeerHost != obTestHost || f.lastQuery.To.Sub(f.lastQuery.From) != time.Hour {
		t.Errorf("ölçüm sorgusu (varsayılan 1 sa, ekranın 15 dk'sı değil): %+v", f.lastQuery)
	}
	tools, blockTypes := map[string]bool{}, map[string]int{}
	var answer map[string]any
	var tableRef, chartRef string
	evRefs := map[string]string{}
	for _, e := range *evs {
		switch e.kind {
		case "step":
			if m, _ := e.payload.(map[string]any); m != nil {
				tools[m["tool"].(string)] = true
			}
		case "step-result":
			if m, _ := e.payload.(map[string]any); m != nil {
				if ev, ok := m["evidence"].(mcptools.ChatEvidence); ok {
					evRefs[ev.Tool] = ev.Ref
					if !strings.Contains(ev.Source, "trace (örneklenmiş)") || ev.QueryKind != "sql" {
						t.Errorf("kanıt kaynağı: %+v", ev)
					}
				}
			}
		case "ev_block":
			b := e.payload.(mcptools.EvidenceBlock)
			blockTypes[b.Type]++
			switch p := b.Payload.(type) {
			case mcptools.EvTablePayload:
				tableRef = p.EvidenceRef
			case mcptools.EvChartPayload:
				chartRef = p.EvidenceRef
				if !strings.Contains(p.Title, "trace (örneklenmiş)") {
					t.Errorf("grafik başlığı kaynağı söylemeli: %q", p.Title)
				}
			}
		case "answer":
			answer, _ = e.payload.(map[string]any)
		}
	}
	for _, tool := range []string{outboundCallsTool, outboundSeriesTool, outboundVerdictTool} {
		if !tools[tool] {
			t.Errorf("%s çipi yok (%v)", tool, tools)
		}
	}
	if blockTypes["chart"] != 1 || blockTypes["table"] != 1 {
		t.Errorf("bloklar: %v", blockTypes)
	}
	if tableRef == "" || chartRef == "" || tableRef == chartRef || tableRef != evRefs[outboundCallsTool] || chartRef != evRefs[outboundSeriesTool] {
		t.Errorf("ref'ler ayrı kanıtlara bağlı olmalı (FE ref başına tek blok): table=%q chart=%q ev=%v", tableRef, chartRef, evRefs)
	}
	if answer == nil {
		t.Fatal("answer yok")
	}
	if _, has := answer["exchangeId"]; has {
		t.Error("deterministik cevap oylanmaz (exchangeId yok)")
	}
	text, _ := answer["text"].(string)
	if !strings.Contains(text, "**ARTIŞ VAR**") || !strings.Contains(text, "kaynak: trace (örneklenmiş)") {
		t.Errorf("cevap: %s", text)
	}
	raw, _ := json.Marshal(answer["links"])
	for _, w := range []string{"/trace?id=" + obTestSlow, "/trace?id=" + obTestErr, "/external?host=" + obTestHost} {
		if !strings.Contains(string(raw), w) {
			t.Errorf("linklerde %q yok: %s", w, raw)
		}
	}
}

// TestOutboundGuidedReadError — ölçüm okunamadı: cevap dürüst "OKUNAMADI".
func TestOutboundGuidedReadError(t *testing.T) {
	llm := newLoopLLM(t, func(int, map[string]any) map[string]any { return map[string]any{"content": "MODEL"} })
	s, _ := newLoopTestServer(t, llm)
	installOutboundReader(t, &fakeOutboundReader{aggErr: errors.New("max_execution_time")})
	route := outboundHit{Path: obTestPath, Callers: []string{obTestSvc}}.route()
	evs, emit := captureEmit()
	to := time.Now()
	handled, ok := s.guidedOutboundAnswer(context.Background(), emit, route, nil, to.Add(-time.Hour), to)
	if !handled || !ok {
		t.Fatal("handled")
	}
	last := (*evs)[len(*evs)-1]
	if m, _ := last.payload.(map[string]any); last.kind != "answer" || !strings.Contains(m["text"].(string), "OKUNAMADI") {
		t.Errorf("son olay: %+v", last)
	}
	// v0.10.1455 (R2): yolsuz rota artık host özetine gider; host da aday da
	// yoksa (katalog boş / okunamadı) dürüst "çözülemedi" cevabı, ölçüm yok.
	// Yollu ama çağıransız rota hâlâ sahiplenilmez.
	evs2, emit2 := captureEmit()
	if h, _ := (&Server{}).guidedOutboundAnswer(context.Background(), emit2, guidedRoute{Intent: guidedOutboundCall}, nil, to, to); !h ||
		len(*evs2) != 1 || !strings.Contains((*evs2)[0].payload.(map[string]any)["text"].(string), "Dış host kataloğu boş ya da okunamadı") {
		t.Errorf("yolsuz + hostsuz rota: %+v", *evs2)
	}
	if h, _ := (&Server{}).guidedOutboundAnswer(context.Background(), emit, guidedRoute{Intent: guidedOutboundCall, OutboundPath: obTestPath}, nil, to, to); h {
		t.Error("yollu / çağıransız rota sahiplenilmemeli")
	}
}

// TestOutboundTierSourcePins — kademe kaynak pinleri: yuva ön-getirmeden
// önce kurulur (tek satır); pathOnly'de sonda yol-yalnız hedeften önce döner;
// ön-getirme HIT'te belirteç yoluna geçmez; guided yuvayı sinyal kapısından
// önce okur; runGuidedRoute LLM'siz dalı anlatımdan önce.
func TestOutboundTierSourcePins(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	before := func(src, a, b, what string) {
		t.Helper()
		ia, ib := strings.Index(src, a), strings.Index(src, b)
		if ia < 0 || ib < 0 || ia >= ib {
			t.Errorf("%s: %q (%d) %q (%d)'den önce olmalı", what, a, ia, b, ib)
		}
	}
	chat := read("copilot_chat.go")
	before(chat, "ctx = armOutboundSlot(ctx, req.Context.Service, req.Context.Env, int64(req.Context.RangeS), anchorTo)", "s.chatOperationPrefetch(ctx, emit, req.Messages", "copilot_chat.go yuva")
	if strings.Count(chat, "armOutboundSlot(") != 1 {
		t.Error("copilot_chat.go'da tek yuva satırı")
	}
	pre := read("chat_operation_prefetch.go")
	body := pre[strings.Index(pre, "func (s *Server) chatOperationPrefetch("):]
	before(body, "s.resolveEndpointMention(", "outboundSlotFrom(ctx).get() != nil", "ön-getirme HIT kontrolü")
	before(body, "outboundSlotFrom(ctx).get() != nil", "toks := opTokens(raw)", "HIT belirteç yolundan önce")
	po := pre[strings.Index(pre, "pathOnly := func()"):]
	before(po, "endpointPathOnlyAllowed(raw)", "s.outboundProbe(", "log vetosu sondadan önce")
	before(po, "s.outboundProbe(", "return pt, endpointPathKind, false, true", "sonda yol-yalnız hedeften önce")
	g := read("copilot_guided.go")
	gb := g[strings.Index(g, "func (s *Server) copilotChatGuided("):]
	before(gb, "outboundSlotFrom(ctx).get()", "if !hasGuidedSignal(norm)", "guided yuva sinyal kapısından önce")
	rg := g[strings.Index(g, "func (s *Server) runGuidedRoute("):]
	before(rg, "route.Intent == guidedOutboundCall", "guidedNarrationUserScaled(", "LLM'siz dal anlatımdan önce") // v0.10.2378 — ölçekli kurucu
	if strings.Contains(read("copilot_intent.go"), "guidedOutboundCall") {
		t.Error("R1: outbound_call sınıflandırıcı listesine girmez")
	}
}
