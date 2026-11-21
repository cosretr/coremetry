package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
)

// Prompt'un andığı araç her sohbet kataloğunda (v1 full/simple, v2) sunuluyor.
func TestCompareBaselineOfferedInEveryChatCatalog(t *testing.T) {
	has := func(ts []mcp.Tool) bool {
		for _, x := range ts {
			if x.Name == CompareBaselineName {
				return true
			}
		}
		return false
	}
	for name, ts := range map[string][]mcp.Tool{
		"chat/full": ChatToolListFor(Deps{}, "full"), "chat/simple": ChatToolListFor(Deps{}, "simple"),
		"v2/full": V2ToolList(Deps{}, "full"), "v2/simple": V2ToolList(Deps{}, "simple"),
	} {
		if !has(ts) {
			t.Errorf("%s: compare_baseline sunulmuyor", name)
		}
	}
}

var updateBaselineGolden = flag.Bool("update-baseline", false, "compare_baseline altınını yeniden yaz")

type fakeBaselineReader struct {
	raw     chstore.BaselineRaw
	errs    map[string]error
	err     error
	deploys []chstore.Deploy
	exc     []chstore.ExceptionGroup
	gotQ    chstore.BaselineQuery
}

func (f *fakeBaselineReader) ReadBaseline(_ context.Context, q chstore.BaselineQuery) (chstore.BaselineRaw, map[string]error, error) {
	f.gotQ = q
	f.raw.SQL = chstore.BaselineSQL{RED: chstore.BaselineREDSQL(q.Operation), Series: chstore.BaselineSeriesSQL(q.Operation, q.StepS), Ops: chstore.BaselineOpsSQL(), Edges: chstore.BaselineEdgesSQL()}
	return f.raw, f.errs, f.err
}
func (f *fakeBaselineReader) BaselineDeploys(context.Context, string, time.Time, time.Time) ([]chstore.Deploy, bool, error) {
	return f.deploys, true, nil
}
func (f *fakeBaselineReader) ListExceptionGroups(context.Context, chstore.ExceptionGroupFilter) ([]chstore.ExceptionGroup, error) {
	return f.exc, nil
}

var cbEnd = time.Date(2026, 11, 21, 10, 2, 30, 0, time.UTC) // → pencere sonu 10:00

// cbFixture — checkout son 1 saatte yavaşladı: p95 40 → 180 ms, yeni hata tipi,
// arada deploy, db bağımlılığı yavaşladı. Adlar kurgusal.
func cbFixture() *fakeBaselineReader {
	to := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	var series []chstore.BaselinePoint
	for i := 0; i < 24; i++ { // 2 sa × 5 dk; ilk saat taban
		p95 := 40.0
		if i >= 12 {
			p95 = 180
		}
		series = append(series, chstore.BaselinePoint{TimeMs: to.Add(-2*time.Hour + time.Duration(i)*5*time.Minute).UnixMilli(), Calls: 600, Errors: 3, P95Ms: p95})
	}
	return &fakeBaselineReader{
		raw: chstore.BaselineRaw{
			Cur:    chstore.BaselineRED{Calls: 7200, Errors: 36, P50Ms: 20, P95Ms: 180, P99Ms: 420, Buckets: 12},
			Ref:    chstore.BaselineRED{Calls: 7200, Errors: 36, P50Ms: 18, P95Ms: 40, P99Ms: 90, Buckets: 12},
			Series: series,
			Ops: []chstore.BaselineOp{
				{Name: "GET /cart", Cur: chstore.BaselineCell{Calls: 3000, Errors: 3, P95Ms: 300}, Ref: chstore.BaselineCell{Calls: 3000, Errors: 3, P95Ms: 50}},
				{Name: "GET /health", Cur: chstore.BaselineCell{Calls: 3000, P95Ms: 2}, Ref: chstore.BaselineCell{Calls: 3000, P95Ms: 2}},
				{Name: "POST /checkout/v2", Cur: chstore.BaselineCell{Calls: 100, P95Ms: 90}},
			},
			Edges: []chstore.BaselineEdge{
				{Child: "db:orders", Kind: "db", CurCalls: 5000, RefCalls: 5000, CurAvgMs: 60, RefAvgMs: 8},
				{Child: "svc:pricing", Kind: "service", CurCalls: 1000, RefCalls: 1000, CurAvgMs: 10, RefAvgMs: 10},
			},
		},
		deploys: []chstore.Deploy{{Service: "checkout", Version: "1.42.0", TimeUnixNs: to.Add(-55 * time.Minute).UnixNano(), Source: "inferred"}},
		exc: []chstore.ExceptionGroup{
			{Fingerprint: "fp-new", Type: "java.sql.SQLTimeoutException", Message: "query timed out", FirstSeen: to.Add(-50 * time.Minute).UnixNano(), Occurrences: 42},
			{Fingerprint: "fp-old", Type: "java.io.IOException", FirstSeen: to.Add(-48 * time.Hour).UnixNano(), Occurrences: 900},
		},
	}
}

func TestCompareBaselinePlan(t *testing.T) {
	q, kind, err := cbPlan(compareBaselineArgs{Service: " checkout ", RangeS: 3500}, cbEnd)
	if err != nil || kind != "previous" {
		t.Fatal(err, kind)
	}
	if q.Cur.To != time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC) || q.Cur.To.Sub(q.Cur.From) != time.Hour || q.Ref.To != q.Cur.From || q.Overlay != 0 {
		t.Fatalf("pencere: %+v", q)
	}
	if q.StepS != 300 || q.ViewFrom != q.Ref.From {
		t.Fatalf("grafik planı: step=%d view=%v", q.StepS, q.ViewFrom)
	}
	q, _, _ = cbPlan(compareBaselineArgs{Service: "checkout", RangeS: 86400, Reference: "week_before"}, cbEnd)
	if q.Overlay != 7*24*time.Hour || q.StepS != 3600 {
		t.Fatalf("week_before: overlay=%v step=%d", q.Overlay, q.StepS)
	}
	if _, _, err := cbPlan(compareBaselineArgs{}, cbEnd); err == nil {
		t.Error("servissiz çağrı hata vermeli")
	}
	if _, _, err := cbPlan(compareBaselineArgs{Service: "x", Reference: "custom"}, cbEnd); err == nil {
		t.Error("custom desteklenmiyor")
	}
}

// Altın: tool sözleşmesi (tam JSON). -update-baseline ile yeniden yazılır.
func TestCompareBaselineGolden(t *testing.T) {
	res, err := runCompareBaseline(context.Background(), cbFixture(), compareBaselineArgs{Service: "checkout"}, cbEnd)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.MarshalIndent(res, "", "  ")
	path := filepath.Join("testdata", "compare_baseline_golden.json")
	if *updateBaselineGolden {
		if err := os.WriteFile(path, append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("altın yok (-update-baseline): %v", err)
	}
	if strings.TrimSpace(string(want)) != strings.TrimSpace(string(got)) {
		t.Errorf("altın farklı:\n%s", got)
	}
}

func TestCompareBaselineFindings(t *testing.T) {
	res, _ := runCompareBaseline(context.Background(), cbFixture(), compareBaselineArgs{Service: "checkout"}, cbEnd)
	if !res.OK || res.Partial || res.RED == nil || *res.RED.Delta.P95Pct != 350 {
		t.Fatalf("RED: %+v", res.RED)
	}
	if len(res.TopEndpoints) != 2 || res.TopEndpoints[0].Endpoint != "POST /checkout/v2" || res.TopEndpoints[0].Change != "new" ||
		res.TopEndpoints[1].Endpoint != "GET /cart" || res.TopEndpoints[1].Change != "latency" {
		t.Fatalf("endpoint sırası: %+v", res.TopEndpoints)
	}
	if len(res.NewErrorTypes) != 1 || res.NewErrorTypes[0].Fingerprint != "fp-new" {
		t.Fatalf("yeni hata: %+v", res.NewErrorTypes)
	}
	if len(res.Deploys) != 1 || !res.Deploys[0].Between {
		t.Fatalf("deploy: %+v", res.Deploys)
	}
	if len(res.Dependencies) != 1 || res.Dependencies[0].Target != "db:orders" {
		t.Fatalf("bağımlılık: %+v", res.Dependencies)
	}
	if res.ChartSignal != "p95" {
		t.Fatalf("sinyal %q", res.ChartSignal)
	}
}

// Grafik YALNIZ araç yükünden: her nokta okumanın kovası, bölgeler pencereler.
// Uçtan uca: aracın JSON'u → ToolResultEvidenceList → ToolResultBlocks.
func TestCompareBaselineChartFromToolData(t *testing.T) {
	fx := cbFixture()
	res, _ := runCompareBaseline(context.Background(), fx, compareBaselineArgs{Service: "checkout"}, cbEnd)
	body, _ := json.Marshal(res)
	evs, ok := ToolResultEvidenceList(CompareBaselineName, string(body))
	if !ok || len(evs) != 2 || evs[1].Hop != cbHopChart {
		t.Fatalf("kanıtlar: %+v", evs)
	}
	for i := range evs {
		evs[i].Ref = "ev1." + evs[i].Hop
	}
	bl := ToolResultBlocks(CompareBaselineName, string(body), evs)
	if len(bl) != 2 || bl[0].Type != blocks.TypeTable || bl[1].Type != blocks.TypeChart {
		t.Fatalf("bloklar: %+v", bl)
	}
	p := bl[1].Payload.(map[string]any)
	if p["evidenceRef"] != "ev1."+cbHopChart {
		t.Fatalf("çip bağı: %v", p["evidenceRef"])
	}
	series := p["series"].([]any)
	pts := series[0].(map[string]any)["points"].([]any)
	if len(pts) != len(fx.raw.Series) {
		t.Fatalf("nokta sayısı %d ≠ okuma %d", len(pts), len(fx.raw.Series))
	}
	for i, raw := range fx.raw.Series {
		pt := pts[i].([]any)
		if int64(pt[0].(float64)) != raw.TimeMs || pt[1].(float64) != raw.P95Ms {
			t.Fatalf("nokta %d: %v ≠ okuma (%d, %v)", i, pt, raw.TimeMs, raw.P95Ms)
		}
	}
	regions := p["regions"].([]any)
	if len(regions) != 2 {
		t.Fatalf("bölgeler: %v", regions)
	}
	insp := regions[1].(map[string]any)
	if int64(insp["fromMs"].(float64)) != fx.gotQ.Cur.From.UnixMilli() || int64(insp["toMs"].(float64)) != fx.gotQ.Cur.To.UnixMilli() || insp["tone"] != "warn" {
		t.Fatalf("inceleme bölgesi pencere değil: %v", insp)
	}
}

// Dün kıyasında taban kesikli çizgi, şimdiye kaydırılmış (değerler okumadan).
func TestCompareBaselineOverlayShift(t *testing.T) {
	fx := cbFixture()
	day := 24 * time.Hour
	fx.raw.OverlaySeries = []chstore.BaselinePoint{{TimeMs: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC).UnixMilli(), Calls: 600, P95Ms: 41}}
	res, _ := runCompareBaseline(context.Background(), fx, compareBaselineArgs{Service: "checkout", Reference: "day_before"}, cbEnd)
	var c EvChartPayload
	for _, b := range res.Data.Blocks {
		if b.Type == blocks.TypeChart {
			c = b.Payload.(EvChartPayload)
		}
	}
	if len(c.Series) != 2 || c.Series[1].Style != evStyleDashed {
		t.Fatalf("overlay yok: %+v", c.Series)
	}
	if got := int64(c.Series[1].Points[0][0]); got != fx.raw.OverlaySeries[0].TimeMs+day.Milliseconds() || c.Series[1].Points[0][1] != 41 {
		t.Fatalf("overlay kaydırması: %v", c.Series[1].Points[0])
	}
	if len(c.Regions) != 1 || c.Regions[0].Tone != "warn" {
		t.Fatalf("dün kıyasında yalnız inceleme bölgesi: %+v", c.Regions)
	}
}

func TestCompareBaselineDegrades(t *testing.T) {
	fx := cbFixture()
	fx.errs = map[string]error{"series": errors.New("timeout"), "edges": errors.New("boom")}
	res, _ := runCompareBaseline(context.Background(), fx, compareBaselineArgs{Service: "checkout"}, cbEnd)
	if !res.Partial || len(res.Unread) != 2 {
		t.Fatalf("kısmi: %+v", res.Unread)
	}
	for _, b := range res.Data.Blocks {
		if b.Type == blocks.TypeChart {
			t.Fatal("seri okunamadıysa grafik olmamalı")
		}
	}
	empty := &fakeBaselineReader{}
	res, _ = runCompareBaseline(context.Background(), empty, compareBaselineArgs{Service: "ghost"}, cbEnd)
	if !res.Empty || len(res.Data.Blocks) != 0 {
		t.Fatalf("boş: %+v", res)
	}
	fail := &fakeBaselineReader{err: errors.New("connection refused")}
	res, _ = runCompareBaseline(context.Background(), fail, compareBaselineArgs{Service: "checkout"}, cbEnd)
	if res.OK || res.Error == nil {
		t.Fatalf("okuma hatası: %+v", res)
	}
}

func TestCompareBaselineCacheKeyHashesInputs(t *testing.T) {
	q1, _, _ := cbPlan(compareBaselineArgs{Service: "a", Operation: "x"}, cbEnd)
	q2, _, _ := cbPlan(compareBaselineArgs{Service: "a", Operation: "y"}, cbEnd)
	q3, _, _ := cbPlan(compareBaselineArgs{Service: "a", Operation: "x", RangeS: 600}, cbEnd)
	k := map[string]bool{cbCacheKey(q1, "previous"): true, cbCacheKey(q2, "previous"): true, cbCacheKey(q3, "previous"): true, cbCacheKey(q1, "day_before"): true}
	if len(k) != 4 {
		t.Fatalf("anahtar çakışması: %v", k)
	}
}

func TestCompareBaselineSchemaMirrorsArgs(t *testing.T) {
	tl := compareBaselineTool(Deps{})
	props := tl.InputSchema["properties"].(map[string]any)
	for _, k := range []string{"service", "operation", "range_s", "reference"} {
		if _, ok := props[k]; !ok {
			t.Errorf("şemada %q yok", k)
		}
	}
	if len(props) != 4 {
		t.Errorf("şema argüman struct'ından fazla alan taşıyor: %v", props)
	}
	found := false
	for _, x := range ChatToolList(Deps{}) {
		if x.Name == CompareBaselineName {
			found = true
		}
	}
	if !found {
		t.Error("compare_baseline sohbet kataloğunda yok")
	}
}
