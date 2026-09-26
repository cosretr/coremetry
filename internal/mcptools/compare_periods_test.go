package mcptools

// compare_periods_test.go — v0.10.944: referans penceresi aritmetiği (gün/
// hafta dahil, UTC ISO), pencere doğrulaması, env zorunluluğu (ortamlar
// birleşmez), notlar (örnekleme her zaman; düşük örnek; kapsama; p95 oynarken
// karışım kayması), kaynak durumları (hata = başarılı zarf, iptal = Go
// hatası), şema↔argüman aynası. İnceleme turu: duvar saati denetimi çıpadan
// bağımsız (kaydırma / gelecek to_iso / gecikmeli), 50'lik sayfayı aşan
// alt-dize eşleşmeleri, giriş span'siz servis (yalnız bağımlılık), okuma yolu
// ilanı, search_traces ranked_within.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/sourcestate"
)

var cpT0 = time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)

// cpWall — v0.10.944: testlerin duvar saati (runComparePeriods'a verilir);
// çıpa artık denetimi atlatmak için gerekmiyor.
var cpWall = cpT0.Add(3 * time.Hour)

func cpWin(fromMin, toMin int) chstore.PeriodWindow {
	return chstore.PeriodWindow{From: cpT0.Add(time.Duration(fromMin) * time.Minute), To: cpT0.Add(time.Duration(toMin) * time.Minute)}
}

func TestCPReferenceWindowArithmetic(t *testing.T) {
	cur := cpWin(0, 60) // 10:00–11:00
	cases := []struct {
		name       string
		a          comparePeriodsArgs
		kind       string
		from, to   string
		errContain string
	}{
		{"varsayılan previous", comparePeriodsArgs{}, "previous", "2026-09-26T09:00:00Z", "2026-09-26T10:00:00Z", ""},
		{"previous", comparePeriodsArgs{Reference: "previous"}, "previous", "2026-09-26T09:00:00Z", "2026-09-26T10:00:00Z", ""},
		{"day_before", comparePeriodsArgs{Reference: "day_before"}, "day_before", "2026-09-25T10:00:00Z", "2026-09-25T11:00:00Z", ""},
		{"week_before", comparePeriodsArgs{Reference: "week_before"}, "week_before", "2026-09-19T10:00:00Z", "2026-09-19T11:00:00Z", ""},
		{"custom", comparePeriodsArgs{Reference: "custom", RefFromISO: "2026-09-26T06:00:00+03:00", RefToISO: "2026-09-26T07:30:00+03:00"}, "custom", "2026-09-26T03:00:00Z", "2026-09-26T04:30:00Z", ""},
		{"ref alanları reference'sız → custom", comparePeriodsArgs{RefFromISO: "2026-09-26T03:00:00Z", RefToISO: "2026-09-26T04:00:00Z"}, "custom", "2026-09-26T03:00:00Z", "2026-09-26T04:00:00Z", ""},
		{"custom çakışma", comparePeriodsArgs{Reference: "custom", RefFromISO: "2026-09-26T10:30:00Z", RefToISO: "2026-09-26T11:30:00Z"}, "", "", "", "çakışıyor"},
		{"custom eksik", comparePeriodsArgs{Reference: "custom", RefFromISO: "2026-09-26T03:00:00Z"}, "", "", "", "zorunlu"},
		{"custom kısa", comparePeriodsArgs{Reference: "custom", RefFromISO: "2026-09-26T03:00:00Z", RefToISO: "2026-09-26T03:02:00Z"}, "", "", "", "5 dakika"},
		{"custom uzun", comparePeriodsArgs{Reference: "custom", RefFromISO: "2026-09-24T03:00:00Z", RefToISO: "2026-09-25T04:00:00Z"}, "", "", "", "24 saat"},
		{"bozuk ISO", comparePeriodsArgs{Reference: "custom", RefFromISO: "dün", RefToISO: "2026-09-26T04:00:00Z"}, "", "", "", "RFC3339"},
		{"bilinmeyen tür", comparePeriodsArgs{Reference: "last_month"}, "", "", "", "geçersiz"},
		{"previous + ref alanı", comparePeriodsArgs{Reference: "previous", RefFromISO: "2026-09-26T03:00:00Z", RefToISO: "2026-09-26T04:00:00Z"}, "", "", "", "reference=custom"},
	}
	for _, c := range cases {
		ref, kind, err := cpReferenceWindow(c.a, cur)
		if c.errContain != "" {
			if err == nil || !strings.Contains(err.Error(), c.errContain) {
				t.Errorf("%s: hata %q içermeli, %v", c.name, c.errContain, err)
			} else if cls := mcp.ClassifyToolError(err).Error; cls != mcp.ToolErrBadArgs {
				t.Errorf("%s: sınıf %s, istenen bad_args", c.name, cls)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		j := cpWindowJSON(ref)
		if kind != c.kind || j["from_iso"] != c.from || j["to_iso"] != c.to {
			t.Errorf("%s: %s %v–%v, istenen %s %s–%s", c.name, kind, j["from_iso"], j["to_iso"], c.kind, c.from, c.to)
		}
		if j["from_unix_ns"].(int64) != ref.From.UnixNano() {
			t.Errorf("%s: unix ns pivotu", c.name)
		}
	}
	// 24 saatlik pencere + day_before: bitişik, ÇAKIŞMAZ
	day := chstore.PeriodWindow{From: cpT0.Add(-24 * time.Hour), To: cpT0}
	ref, _, err := cpReferenceWindow(comparePeriodsArgs{Reference: "day_before"}, day)
	if err != nil || !ref.To.Equal(day.From) {
		t.Errorf("24 saat + day_before bitişik olmalı: %v %v", ref, err)
	}
}

func TestCPProblemWindow(t *testing.T) {
	anchor := cpT0.Add(2 * time.Hour)
	ctx := WithAnchor(context.Background(), anchor)
	cases := []struct {
		name       string
		a          comparePeriodsArgs
		from, to   time.Time
		errContain string
	}{
		{"range_s çıpadan", comparePeriodsArgs{RangeS: 3600}, anchor.Add(-time.Hour), anchor, ""},
		{"varsayılan 30 dk", comparePeriodsArgs{}, anchor.Add(-30 * time.Minute), anchor, ""},
		{"ISO (ofsetli → UTC)", comparePeriodsArgs{FromISO: "2026-09-26T13:00:00+03:00", ToISO: "2026-09-26T14:00:00+03:00"}, cpT0, cpT0.Add(time.Hour), ""},
		{"yalnız from", comparePeriodsArgs{FromISO: "2026-09-26T10:00:00Z"}, time.Time{}, time.Time{}, "birlikte"},
		{"ISO + range_s", comparePeriodsArgs{FromISO: "2026-09-26T10:00:00Z", ToISO: "2026-09-26T11:00:00Z", RangeS: 600}, time.Time{}, time.Time{}, "birlikte verilemez"},
		{"range_s kısa", comparePeriodsArgs{RangeS: 60}, time.Time{}, time.Time{}, "300 ile 86400"},
		{"range_s uzun", comparePeriodsArgs{RangeS: 7 * 86400}, time.Time{}, time.Time{}, "300 ile 86400"},
		{"ters pencere", comparePeriodsArgs{FromISO: "2026-09-26T11:00:00Z", ToISO: "2026-09-26T10:00:00Z"}, time.Time{}, time.Time{}, "sonra olmalı"},
		{"25 saat", comparePeriodsArgs{FromISO: "2026-09-25T09:00:00Z", ToISO: "2026-09-26T10:00:00Z"}, time.Time{}, time.Time{}, "24 saat"},
	}
	for _, c := range cases {
		w, err := cpProblemWindow(ctx, c.a)
		if c.errContain != "" {
			if err == nil || !strings.Contains(err.Error(), c.errContain) {
				t.Errorf("%s: hata %q içermeli, %v", c.name, c.errContain, err)
			} else if cls := mcp.ClassifyToolError(err).Error; cls != mcp.ToolErrBadArgs {
				t.Errorf("%s: sınıf %s, istenen bad_args (%v)", c.name, cls, err)
			}
			continue
		}
		if err != nil || !w.From.Equal(c.from) || !w.To.Equal(c.to) || w.From.Location() != time.UTC {
			t.Errorf("%s: %v–%v err %v", c.name, w.From, w.To, err)
		}
	}
}

func TestCPResolveEnv(t *testing.T) {
	cases := []struct {
		req        string
		seen       []string
		want       string
		noteHas    string
		errContain string
	}{
		{"", []string{"prod", "uat"}, "", "", "env zorunlu"},
		{"uat", []string{"prod", "uat"}, "uat", "", ""},
		{"int", []string{"prod", "uat"}, "", "", "görülen ortamlar: prod, uat"},
		{"", []string{"prod"}, "", "tek ortamda (prod)", ""},
		{"", nil, "", "env etiketi taşımıyor", ""},
		{"prod", nil, "", "", "etiketi taşımıyor"},
	}
	for _, c := range cases {
		got, note, err := cpResolveEnv(c.req, c.seen)
		if c.errContain != "" {
			if err == nil || !strings.Contains(err.Error(), c.errContain) {
				t.Errorf("%q/%v: hata %q içermeli, %v", c.req, c.seen, c.errContain, err)
			} else if cls := mcp.ClassifyToolError(err).Error; cls != mcp.ToolErrBadArgs {
				t.Errorf("%q/%v: sınıf %s, istenen bad_args", c.req, c.seen, cls)
			}
			continue
		}
		if err != nil || got != c.want || (c.noteHas != "" && !strings.Contains(note, c.noteHas)) {
			t.Errorf("%q/%v: %q %q %v", c.req, c.seen, got, note, err)
		}
	}
}

func cpCmp(p95Problem, p95Ref float64, reqProblem, reqRef uint64, cartShare [2]uint64) chstore.PeriodComparison {
	raw := chstore.PeriodCompareRaw{
		RED: [2]chstore.PeriodRED{
			{Requests: reqProblem, P95Ms: p95Problem, Buckets: 12},
			{Requests: reqRef, P95Ms: p95Ref, Buckets: 12},
		},
		Ops: []chstore.PeriodPairRow{{Key: "GET /cart", Count: cartShare}},
	}
	return chstore.BuildPeriodComparison(raw, cpWin(0, 60), cpWin(-60, 0))
}

func TestCPNotes(t *testing.T) {
	// p95 +100% VE GET /cart payı %30 → %70: karışım uyarısı
	notes := cpNotes(cpNoteInput{Cmp: cpCmp(800, 400, 1000, 1000, [2]uint64{700, 300})})
	if notes[0] != cpNoteSampling {
		t.Fatalf("örnekleme notu DAİMA ilk ve birebir: %q", notes[0])
	}
	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "trafik karışımı kaydı") || !strings.Contains(joined, "GET /cart payı %30 → %70") {
		t.Errorf("karışım uyarısı eksik:\n%s", joined)
	}
	// p95 oynamadıysa karışım uyarısı YOK
	if j := strings.Join(cpNotes(cpNoteInput{Cmp: cpCmp(410, 400, 1000, 1000, [2]uint64{700, 300})}), "\n"); strings.Contains(j, "karışımı kaydı") {
		t.Errorf("p95 oynamadan karışım uyarısı:\n%s", j)
	}
	// karışım kaymadıysa da YOK
	if j := strings.Join(cpNotes(cpNoteInput{Cmp: cpCmp(800, 400, 1000, 1000, [2]uint64{500, 480})}), "\n"); strings.Contains(j, "karışımı kaydı") {
		t.Errorf("kayma yokken karışım uyarısı:\n%s", j)
	}
	// düşük örnek + kapsama + boş dönem + env/operation/uzunluk notları
	low := cpCmp(800, 0, 12, 0, [2]uint64{12, 0})
	low.Problem.Coverage, low.Problem.BucketsWithData = 0.25, 3
	j := strings.Join(cpNotes(cpNoteInput{Cmp: low, EnvNote: "ENVNOTE", OperationSet: true, LengthsDiffer: true, Delayed: true}), "\n")
	for _, want := range []string{"Düşük örnek: sorun penceresinde 12", "Kapsama düşük: sorun penceresinin 5 dk kovalarının %25'inde veri var (3/12)",
		"referans penceresinde giriş span'i YOK", "ENVNOTE", "operation süzgeci istemci", "uzunlukları farklı", "son 2 dakika"} {
		if !strings.Contains(j, want) {
			t.Errorf("not eksik %q:\n%s", want, j)
		}
	}
	// v0.10.944 — giriş span'i 0 üç ayrı durum: operation süzgeci (önce),
	// yalnız istemci/üretici span'i üreten servis, gerçekten veri yok.
	worker := cpCmp(0, 0, 0, 0, [2]uint64{0, 0})
	worker.Problem.Dependencies = []chstore.PeriodDependency{{Kind: "messaging", Target: "kafka:orders", Calls: 420}}
	j = strings.Join(cpNotes(cpNoteInput{Cmp: worker}), "\n")
	if !strings.Contains(j, "sorun penceresinde giriş span'i YOK ama servisin 420 istemci/üretici çağrısı var") || strings.Contains(j, "sorun penceresinde giriş span'i YOK — istek alınmamış ya da veri yok") {
		t.Errorf("yalnız bağımlılıklı pencere 'veri yok' dememeli:\n%s", j)
	}
	if !strings.Contains(j, "referans penceresinde giriş span'i YOK — istek alınmamış ya da veri yok") {
		t.Errorf("gerçekten boş pencere 'veri yok' demeli:\n%s", j)
	}
	j = strings.Join(cpNotes(cpNoteInput{Cmp: worker, OperationSet: true}), "\n")
	if !strings.Contains(j, "list_operations") || strings.Contains(j, "420 istemci/üretici") {
		t.Errorf("operation süzgeci varken yazım hatası önce söylenmeli:\n%s", j)
	}
	// okuma yolu: yüzdelik yöntemi + sürüm MV notu
	j = strings.Join(cpNotes(cpNoteInput{Cmp: worker, Source: chstore.PeriodSourceSpanmetrics, VersionsSource: chstore.PeriodSourceVersionsMV}), "\n")
	if !strings.Contains(j, cpPercentileMethodMV) || !strings.Contains(j, "servisin TÜM span'leri") || !strings.Contains(j, "(versions service_version_5m'den, tüm span'ler)") {
		t.Errorf("MV yolu notları:\n%s", j)
	}
	j = strings.Join(cpNotes(cpNoteInput{Cmp: worker, Source: chstore.PeriodSourceSpans, SourceReason: chstore.PeriodRawReasonScope}), "\n")
	if !strings.Contains(j, "env/cluster/namespace süzgeci bir MV boyutu değil") {
		t.Errorf("ham yol gerekçesi:\n%s", j)
	}
}

// v0.10.944 — yalnız bağımlılık verisi olan pencere "empty" DEĞİL.
func TestCPSourcesDependencyOnlyWindowIsNotEmpty(t *testing.T) {
	cmp := cpCmp(0, 0, 0, 0, [2]uint64{0, 0})
	cmp.Problem.Dependencies = []chstore.PeriodDependency{{Kind: "db", Target: "postgresql@db-a", Calls: 90}}
	srcs := cpSources(cmp, chstore.PeriodCompareRaw{}, cpWin(0, 60), cpWin(-60, 0), cpSourceOpts{})
	if srcs[0].State != sourcestate.OK || srcs[0].Returned != 90 || !strings.Contains(strings.Join(srcs[0].Notes, ";"), "giriş span'i yok") {
		t.Errorf("bağımlılıklı pencere ok olmalı: %+v", srcs[0])
	}
	if srcs[1].State != sourcestate.Empty {
		t.Errorf("gerçekten boş pencere empty: %+v", srcs[1])
	}
	// pod ve sürüm hataları ayrı adlarla
	raw := chstore.PeriodCompareRaw{PodsErr: context.DeadlineExceeded, VersionsErr: fmt.Errorf("dial tcp: connection refused")}
	srcs = cpSources(cmp, raw, cpWin(0, 60), cpWin(-60, 0), cpSourceOpts{})
	n := strings.Join(srcs[0].Notes, ";")
	if srcs[0].State != sourcestate.Partial || !strings.Contains(n, "pods okunamadı (timeout)") || !strings.Contains(n, "versions okunamadı (unreachable)") {
		t.Errorf("pod/sürüm hataları: %+v", srcs[0])
	}
}

// v0.10.944 — okuma yolu zarfta: read_source / versions_source /
// percentile_method; MV ızgarası pencereyi kaydırdıysa not + kaynak okunan
// pencereyi söyler.
func TestRunComparePeriodsDeclaresReadPath(t *testing.T) {
	cur := chstore.PeriodWindow{From: cpT0.Add(30 * time.Second), To: cpT0.Add(time.Hour + 30*time.Second)}
	f := &cpFakeReader{names: []string{"checkout"}, raw: chstore.PeriodCompareRaw{
		RED:    [2]chstore.PeriodRED{{Requests: 3600, Buckets: 12}, {Requests: 3600, Buckets: 12}},
		Source: chstore.PeriodSourceSpanmetrics, VersionsSource: chstore.PeriodSourceVersionsMV,
		Windows: [2]chstore.PeriodWindow{{From: cpT0, To: cpT0.Add(time.Hour)}, {From: cpT0.Add(-time.Hour), To: cpT0}},
	}}
	a := comparePeriodsArgs{Service: "checkout", FromISO: cur.From.Format(time.RFC3339), ToISO: cur.To.Format(time.RFC3339)}
	out, err := runComparePeriods(context.Background(), Deps{}, f, a, cpWall)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(cpEnvelope)
	if m["read_source"] != "spanmetrics_1m" || m["versions_source"] != "service_version_5m" || m["percentile_method"] != cpPercentileMethodMV {
		t.Errorf("okuma yolu: %v %v %v", m["read_source"], m["versions_source"], m["percentile_method"])
	}
	if j := strings.Join(m["notes"].([]string), "\n"); !strings.Contains(j, "1 dk ızgarası: okunan pencereler sorun 2026-09-26T10:00:00Z–2026-09-26T11:00:00Z") {
		t.Errorf("ızgara notu:\n%s", j)
	}
	if srcs := m["sources"].([]sourcestate.Status); srcs[0].FromISO != "2026-09-26T10:00:00Z" {
		t.Errorf("kaynak okunan pencereyi söylemeli: %+v", srcs[0])
	}
	if w := m["window"].(map[string]any); w["from_iso"] != "2026-09-26T10:00:30Z" {
		t.Errorf("window istenen pencere kalır: %v", w)
	}
}

// ─── sahte okuyucu ile uçtan uca gövde ───────────────────────────────────

type cpFakeReader struct {
	names    []string
	envs     map[time.Time][]string // pencere başı → görülen ortamlar
	envErr   error
	readErr  error
	raw      chstore.PeriodCompareRaw
	gotScope chstore.PeriodScope
	gotCur   chstore.PeriodWindow
	gotRef   chstore.PeriodWindow
	reads    int
	envReads int
}

// ListServiceNames — gerçek okumanın şekli: alt-dize, ada göre sıralı,
// limit/offset sayfası + TÜM eşleşmelerin sayısı (v0.10.944 regresyonu bunu
// ister: 50'lik sayfa ötesindeki tam ad).
func (f *cpFakeReader) ListServiceNames(_ context.Context, pattern string, limit, offset int) ([]string, int, error) {
	var out []string
	for _, n := range f.names {
		if pattern == "" || strings.Contains(n, pattern) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	total := len(out)
	if offset > len(out) {
		offset = len(out)
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, total, nil
}

// ReadPeriodEnvironments — iki pencere TEK okuma (v0.10.944).
func (f *cpFakeReader) ReadPeriodEnvironments(_ context.Context, _ string, cur, ref chstore.PeriodWindow) ([]string, error) {
	f.envReads++
	return append(append([]string(nil), f.envs[cur.From]...), f.envs[ref.From]...), f.envErr
}

func (f *cpFakeReader) ReadComparePeriods(_ context.Context, sc chstore.PeriodScope, cur, ref chstore.PeriodWindow) (chstore.PeriodCompareRaw, error) {
	f.reads++
	f.gotScope, f.gotCur, f.gotRef = sc, cur, ref
	return f.raw, f.readErr
}

func cpArgs() comparePeriodsArgs {
	return comparePeriodsArgs{Service: "checkout", FromISO: "2026-09-26T10:00:00Z", ToISO: "2026-09-26T11:00:00Z"}
}

func cpAnchoredCtx() context.Context {
	return WithAnchor(context.Background(), cpT0.Add(3*time.Hour))
}

func TestRunComparePeriodsEnvRequiredWhenAmbiguous(t *testing.T) {
	f := &cpFakeReader{names: []string{"checkout", "checkout-v2"}, envs: map[time.Time][]string{
		cpT0: {"prod"}, cpT0.Add(-time.Hour): {"uat"},
	}}
	_, err := runComparePeriods(cpAnchoredCtx(), Deps{}, f, cpArgs(), cpWall)
	if err == nil || !strings.Contains(err.Error(), "prod, uat") || mcp.ClassifyToolError(err).Error != mcp.ToolErrBadArgs {
		t.Fatalf("iki ortam → bad_args + liste: %v", err)
	}
	if f.reads != 0 {
		t.Error("env belirsizken kıyas okuması koşmamalı (ortamlar birleşirdi)")
	}
	a := cpArgs()
	a.Env = "prod"
	out, err := runComparePeriods(cpAnchoredCtx(), Deps{}, f, a, cpWall)
	if err != nil {
		t.Fatal(err)
	}
	if f.gotScope.Env != "prod" {
		t.Errorf("env okumaya uygulanmalı: %+v", f.gotScope)
	}
	m := out.(cpEnvelope)
	if m["scope"].(map[string]any)["env"] != "prod" {
		t.Errorf("scope env: %v", m["scope"])
	}
}

func TestRunComparePeriodsEnvelope(t *testing.T) {
	f := &cpFakeReader{names: []string{"checkout"}, envs: map[time.Time][]string{cpT0: {"prod"}},
		raw: chstore.PeriodCompareRaw{
			RED: [2]chstore.PeriodRED{{Requests: 3600, Errors: 36, P95Ms: 900, Buckets: 12}, {Requests: 3600, P95Ms: 450, Buckets: 12}},
			Ops: []chstore.PeriodPairRow{{Key: "GET /cart", Count: [2]uint64{2520, 1080}}},
		}}
	a := cpArgs()
	a.Reference = "week_before"
	a.Operation = "GET /cart"
	a.Cluster = "cluster-a"
	d := Deps{Clusters: func() []ClusterRef {
		return []ClusterRef{{ID: "c1", Name: "cluster-a", SpanValues: []string{"cluster-a-east", "cluster-a-west"}}}
	}}
	out, err := runComparePeriods(cpAnchoredCtx(), d, f, a, cpWall)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(cpEnvelope)
	if !f.gotRef.From.Equal(cpT0.Add(-7*24*time.Hour)) || !f.gotCur.To.Equal(cpT0.Add(time.Hour)) {
		t.Errorf("pencereler: cur=%v ref=%v", f.gotCur, f.gotRef)
	}
	if fmt.Sprint(f.gotScope.Clusters) != "[cluster-a-east cluster-a-west]" || f.gotScope.Operation != "GET /cart" {
		t.Errorf("kapsam: %+v", f.gotScope)
	}
	rw := m["reference_window"].(map[string]any)
	if rw["kind"] != "week_before" || rw["from_iso"] != "2026-09-19T10:00:00Z" || m["window"].(map[string]any)["to_iso"] != "2026-09-26T11:00:00Z" {
		t.Errorf("pencere ISO: %v / %v", m["window"], rw)
	}
	for _, k := range []string{"problem", "reference", "deltas", "traffic_mix_shift", "notes", "sources", "requests_definition", "percentile_method"} {
		if _, ok := m[k]; !ok {
			t.Errorf("zarf alanı eksik: %s", k)
		}
	}
	srcs := m["sources"].([]sourcestate.Status)
	if len(srcs) != 2 || srcs[0].FromISO != "2026-09-26T10:00:00Z" || srcs[1].FromISO != "2026-09-19T10:00:00Z" {
		t.Fatalf("pencere başına kaynak durumu: %+v", srcs)
	}
	// operation süzgeci bağımlılığa uygulanamadı → kısmi + not
	if srcs[0].State != sourcestate.Partial || !strings.Contains(strings.Join(srcs[0].Notes, ";"), "dependencies") {
		t.Errorf("uygulanamayan süzgeç kısmi olmalı: %+v", srcs[0])
	}
	notes := m["notes"].([]string)
	if notes[0] != cpNoteSampling || !strings.Contains(strings.Join(notes, "\n"), "tek ortamda (prod)") {
		t.Errorf("notlar: %v", notes)
	}
	if p := m["problem"].(chstore.PeriodStats); p.P95Ms != 900 || p.RatePerS != 1 {
		t.Errorf("sorun dönemi: %+v", p)
	}
	b, err := json.Marshal(m)
	if err != nil || strings.Contains(string(b), "NaN") {
		t.Errorf("JSON: %v", err)
	}
}

func TestRunComparePeriodsSourceFailuresAndCancel(t *testing.T) {
	base := func() *cpFakeReader {
		return &cpFakeReader{names: []string{"checkout"}, envs: map[time.Time][]string{cpT0: {"prod"}}}
	}
	// erişilemeyen kaynak → Go hatası DEĞİL, durumlu başarılı zarf
	f := base()
	f.readErr = errors.New("dial tcp 10.0.0.9:9000: connect: connection refused")
	out, err := runComparePeriods(cpAnchoredCtx(), Deps{}, f, cpArgs(), cpWall)
	if err != nil {
		t.Fatalf("kaynak hatası başarılı sonuç olmalı: %v", err)
	}
	m := out.(cpEnvelope)
	srcs := m["sources"].([]sourcestate.Status)
	if srcs[0].State != sourcestate.Unreachable || m["problem"] != nil {
		t.Errorf("unreachable zarfı: %+v %v", srcs, m["problem"])
	}
	// iptal → Go hatası, kalan okumalar KOŞMAZ
	f = base()
	f.envErr = context.Canceled
	if _, err := runComparePeriods(cpAnchoredCtx(), Deps{}, f, cpArgs(), cpWall); !errors.Is(err, context.Canceled) || f.reads != 0 {
		t.Errorf("iptal: %v reads=%d", err, f.reads)
	}
	f = base()
	f.readErr = fmt.Errorf("q: %w", context.Canceled)
	if _, err := runComparePeriods(cpAnchoredCtx(), Deps{}, f, cpArgs(), cpWall); !errors.Is(err, context.Canceled) {
		t.Errorf("okuma iptali Go hatası olmalı: %v", err)
	}
	// ikincil okuma hatası → kısmi not, RED yine döner
	f = base()
	f.raw = chstore.PeriodCompareRaw{RED: [2]chstore.PeriodRED{{Requests: 100}, {Requests: 100}}, OpsErr: context.DeadlineExceeded}
	out, _ = runComparePeriods(cpAnchoredCtx(), Deps{}, f, cpArgs(), cpWall)
	srcs = out.(cpEnvelope)["sources"].([]sourcestate.Status)
	if srcs[0].State != sourcestate.Partial || !strings.Contains(strings.Join(srcs[0].Notes, ";"), "top_operations okunamadı (timeout)") {
		t.Errorf("ikincil hata kısmi: %+v", srcs[0])
	}
	// store yok → not_configured, panik yok
	out, err = runComparePeriods(cpAnchoredCtx(), Deps{}, nil, cpArgs(), cpWall)
	if err != nil || out.(cpEnvelope)["sources"].([]sourcestate.Status)[0].State != sourcestate.NotConfigured {
		t.Errorf("store yok: %v %v", out, err)
	}
	// bilinmeyen servis → not_found + adaylar
	f = base()
	a := cpArgs()
	a.Service = "chekout"
	_, err = runComparePeriods(cpAnchoredCtx(), Deps{}, f, a, cpWall)
	if err == nil || mcp.ClassifyToolError(err).Error != mcp.ToolErrNotFound || !strings.Contains(err.Error(), "checkout") {
		t.Errorf("bilinmeyen servis: %v", err)
	}
	// kayıtsız cluster → birebir span değeri + not
	f = base()
	a = cpArgs()
	a.Cluster = "cluster-b"
	out, _ = runComparePeriods(cpAnchoredCtx(), Deps{}, f, a, cpWall)
	if fmt.Sprint(f.gotScope.Clusters) != "[cluster-b]" || !strings.Contains(strings.Join(out.(cpEnvelope)["notes"].([]string), "\n"), "birebir uygulandı") {
		t.Errorf("kayıtsız cluster: %+v", f.gotScope)
	}
}

func TestRunComparePeriodsRejectsFutureWithoutAnchor(t *testing.T) {
	f := &cpFakeReader{names: []string{"checkout"}}
	a := comparePeriodsArgs{Service: "checkout", FromISO: "2999-01-01T00:00:00Z", ToISO: "2999-01-01T01:00:00Z"}
	if _, err := runComparePeriods(context.Background(), Deps{}, f, a, cpWall); err == nil || mcp.ClassifyToolError(err).Error != mcp.ToolErrBadArgs {
		t.Errorf("gelecek pencere bad_args olmalı: %v", err)
	}
}

// v0.10.944 — gelecek/tazelik denetimi ÇIPALI pencerelerde de koşar.
// chatAnchorTime 5 dk'ya kadar gelecek çıpayı kabul eder: range_s penceresi
// uzunluğu korunarak şimdiye kaydırılır; to_iso gelecekteyse bad_args; son 2
// dk'da biten pencere "gecikmeli".
func TestRunComparePeriodsWallClockChecksWithAnchor(t *testing.T) {
	base := func() *cpFakeReader {
		return &cpFakeReader{names: []string{"checkout"}, raw: chstore.PeriodCompareRaw{
			RED: [2]chstore.PeriodRED{{Requests: 600, Buckets: 2}, {Requests: 600, Buckets: 2}},
		}}
	}
	// çıpa = duvar + 4 dk, range_s 600 → pencere duvarda biter, 600 s kalır
	f := base()
	ctx := WithAnchor(context.Background(), cpWall.Add(4*time.Minute))
	out, err := runComparePeriods(ctx, Deps{}, f, comparePeriodsArgs{Service: "checkout", RangeS: 600}, cpWall)
	if err != nil {
		t.Fatal(err)
	}
	if !f.gotCur.To.Equal(cpWall) || f.gotCur.To.Sub(f.gotCur.From) != 600*time.Second {
		t.Errorf("kaydırılmış pencere: %v", f.gotCur)
	}
	if !f.gotRef.To.Equal(f.gotCur.From) {
		t.Errorf("previous kaymış pencereyi izlemeli: cur=%v ref=%v", f.gotCur, f.gotRef)
	}
	m := out.(cpEnvelope)
	joined := strings.Join(m["notes"].([]string), "\n")
	if !strings.Contains(joined, "Çıpa duvar saatinin 4m0s ilerisindeydi") {
		t.Errorf("kaydırma notu yok:\n%s", joined)
	}
	srcs := m["sources"].([]sourcestate.Status)
	if !strings.Contains(strings.Join(srcs[0].Notes, ";"), "şimdiye çekildi") || srcs[0].State != sourcestate.Delayed {
		t.Errorf("sorun penceresi kaynağı: not + gecikmeli olmalı: %+v", srcs[0])
	}
	if strings.Contains(strings.Join(srcs[1].Notes, ";"), "şimdiye çekildi") {
		t.Errorf("kaydırma notu yalnız sorun penceresinde: %+v", srcs[1])
	}

	// çıpalı + gelecek to_iso → bad_args
	f = base()
	a := comparePeriodsArgs{Service: "checkout", FromISO: cpWall.Add(-30 * time.Minute).Format(time.RFC3339), ToISO: cpWall.Add(10 * time.Minute).Format(time.RFC3339)}
	if _, err := runComparePeriods(ctx, Deps{}, f, a, cpWall); err == nil || mcp.ClassifyToolError(err).Error != mcp.ToolErrBadArgs || !strings.Contains(err.Error(), "gelecekte") {
		t.Errorf("çıpalı gelecek to_iso bad_args olmalı: %v", err)
	}

	// çıpalı, pencere sonu duvar − 1 dk → gecikmeli notu + source.state delayed
	f = base()
	a = comparePeriodsArgs{Service: "checkout", FromISO: cpWall.Add(-31 * time.Minute).Format(time.RFC3339), ToISO: cpWall.Add(-time.Minute).Format(time.RFC3339)}
	out, err = runComparePeriods(ctx, Deps{}, f, a, cpWall)
	if err != nil {
		t.Fatal(err)
	}
	m = out.(cpEnvelope)
	if j := strings.Join(m["notes"].([]string), "\n"); !strings.Contains(j, "son 2 dakika") {
		t.Errorf("gecikme notu yok:\n%s", j)
	}
	if srcs := m["sources"].([]sourcestate.Status); srcs[0].State != sourcestate.Delayed || srcs[1].State == sourcestate.Delayed {
		t.Errorf("yalnız sorun penceresi gecikmeli: %+v / %+v", srcs[0], srcs[1])
	}

	// eski pencere (duvar − 2 saat) → gecikme yok
	f = base()
	out, _ = runComparePeriods(ctx, Deps{}, f, cpArgs(), cpWall)
	if srcs := out.(cpEnvelope)["sources"].([]sourcestate.Status); srcs[0].State == sourcestate.Delayed {
		t.Errorf("eski pencere gecikmeli olmamalı: %+v", srcs[0])
	}
}

// v0.10.944 regresyonu: binlerce servisli filoda 'checkout' alt-dizesiyle
// ondan ÖNCE sıralanan 60 servis — var olan servis 50'lik sayfaya girmiyor
// ve not_found alıyordu.
func TestRunComparePeriodsServiceBeyondFirstPage(t *testing.T) {
	var names []string
	for i := 0; i < 60; i++ {
		names = append(names, fmt.Sprintf("a-%02d-checkout", i))
	}
	names = append(names, "checkout")
	f := &cpFakeReader{names: names}
	if page, total, _ := f.ListServiceNames(context.Background(), "checkout", 50, 0); len(page) != 50 || total != 61 || page[len(page)-1] == "checkout" {
		t.Fatalf("kurgu: 'checkout' ilk sayfanın DIŞINDA olmalı (%d/%d)", len(page), total)
	}
	_, err := runComparePeriods(context.Background(), Deps{}, f, cpArgs(), cpWall)
	if err != nil || f.reads != 1 {
		t.Fatalf("var olan servis kıyasa ulaşmalı: err=%v reads=%d", err, f.reads)
	}
	if f.envReads != 1 {
		t.Errorf("ortam keşfi iki pencere için TEK okuma: %d", f.envReads)
	}
}

// "mı?" ipucu yalnız harf-farkı/yakın ad için; tam ad yoksa not_found.
func TestRunComparePeriodsCaseOnlyHint(t *testing.T) {
	f := &cpFakeReader{names: []string{"Checkout"}}
	a := cpArgs()
	a.Service = "checkout"
	_, err := runComparePeriods(context.Background(), Deps{}, f, a, cpWall)
	if err == nil || !strings.Contains(err.Error(), `"Checkout" mı?`) || f.reads != 0 {
		t.Errorf("harf farkı ipucu: %v reads=%d", err, f.reads)
	}
}

// /mcp-tools adım 7: şema özellikleri argüman struct'ının JSON etiketleriyle
// BİREBİR — uyuşmazlık süzgeci sessizce sıfırlar.
func TestComparePeriodsSchemaMirrorsArgs(t *testing.T) {
	tool := comparePeriodsTool(Deps{})
	props := tool.InputSchema["properties"].(map[string]any)
	var schema, tags []string
	for k := range props {
		schema = append(schema, k)
	}
	rt := reflect.TypeOf(comparePeriodsArgs{})
	for i := 0; i < rt.NumField(); i++ {
		tags = append(tags, strings.Split(rt.Field(i).Tag.Get("json"), ",")[0])
	}
	sort.Strings(schema)
	sort.Strings(tags)
	if fmt.Sprint(schema) != fmt.Sprint(tags) {
		t.Errorf("şema %v ≠ argümanlar %v", schema, tags)
	}
	if req := tool.InputSchema["required"].([]string); len(req) != 1 || req[0] != "service" {
		t.Errorf("yalnız service zorunlu: %v", req)
	}
	if desc := props["env"].(map[string]any)["description"].(string); !strings.Contains(desc, "uat") {
		t.Error("env açıklaması int/uat/prep sözlüğünü öğretmeli")
	}
	if tool.MinRole != "" || tool.Name != "compare_periods" {
		t.Errorf("salt-okunur viewer tool: %q %q", tool.Name, tool.MinRole)
	}
	n := len(tool.ShortDescription)
	if n < 60 || n > 400 || n >= len(tool.Description) {
		t.Errorf("kompakt açıklama boyu %d", n)
	}
	for _, want := range []string{"ENTRY spans", "never an average of bucket percentiles", "REQUIRED", "never merged", "sampling",
		"spanmetrics_1m", "it carries kind", "env/cluster/namespace is not an MV dimension", "not 'no traffic'"} {
		if !strings.Contains(tool.Description, want) {
			t.Errorf("açıklama %q taşımalı", want)
		}
	}
	// geçersiz argüman store'a gitmeden döner (Deps{} → Store nil)
	if _, err := tool.Handler(context.Background(), json.RawMessage(`{"service":"checkout","range_s":10}`)); err == nil {
		t.Error("range_s=10 reddedilmeli")
	}
	if _, err := tool.Handler(context.Background(), json.RawMessage(`{"service":`)); err == nil || mcp.ClassifyToolError(err).Error != mcp.ToolErrBadArgs {
		t.Errorf("bozuk JSON bad_args: %v", err)
	}
}

func TestSearchTracesSource(t *testing.T) {
	from, to := cpT0, cpT0.Add(time.Hour)
	cases := []struct {
		name     string
		returned int
		hasMore  bool
		ranked   int
		clamp    string
		narrowed time.Time
		want     sourcestate.State
	}{
		{"boş", 0, false, 0, "", time.Time{}, sourcestate.Empty},
		{"ok", 5, false, 0, "", time.Time{}, sourcestate.OK},
		{"dolu sayfa", 20, true, 0, "", time.Time{}, sourcestate.Truncated},
		{"kapı daralttı", 5, false, 0, "filters clamp window to 6h", time.Time{}, sourcestate.Partial},
		{"kaynak daralttı", 5, true, 0, "", cpT0.Add(30 * time.Minute), sourcestate.Partial},
		// v0.10.944 — sort=duration yalnız en yeni N aday içinde sıraladıysa
		// "pencerenin en yavaşları" DEĞİL: kısmi.
		{"dilimde sıralandı", 12, false, 5000, "", time.Time{}, sourcestate.Partial},
		{"dilimde boş", 0, false, 6000, "", time.Time{}, sourcestate.Partial},
	}
	for _, c := range cases {
		st := searchTracesSource(c.returned, 20, c.hasMore, c.ranked, from, to, c.clamp, c.narrowed)
		if st.State != c.want || st.FromISO != "2026-09-26T10:00:00Z" || st.Limit != 20 {
			t.Errorf("%s: %+v", c.name, st)
		}
		if c.ranked > 0 && !strings.Contains(strings.Join(st.Notes, ";"), fmt.Sprintf("en yeni %d aday", c.ranked)) {
			t.Errorf("%s: ranked_within notu yok: %+v", c.name, st.Notes)
		}
	}
}

// cpSubstringMissReader — alt-dize okuması boş dönen (ör. MV tazelenirken)
// ama kataloğun tamamında tam adı gören okuyucu.
type cpSubstringMissReader struct{ *cpFakeReader }

func (r cpSubstringMissReader) ListServiceNames(ctx context.Context, pattern string, limit, offset int) ([]string, int, error) {
	if pattern != "" {
		return nil, 0, nil
	}
	return r.cpFakeReader.ListServiceNames(ctx, pattern, limit, offset)
}

// v0.10.944 — katalogda TAM ad varsa servis VARDIR: eskiden aynı adı
// öneren bir not_found hatası dönüyordu ("checkout" bulunamadı — "checkout" mı?).
func TestRunComparePeriodsExactHitInCatalogueIsFound(t *testing.T) {
	f := &cpFakeReader{names: []string{"checkout", "payments"}}
	_, err := runComparePeriods(context.Background(), Deps{}, cpSubstringMissReader{f}, cpArgs(), cpWall)
	if err != nil || f.reads != 1 {
		t.Errorf("katalogdaki tam ad bulunmuş sayılmalı: err=%v reads=%d", err, f.reads)
	}
}

// TestCPSourcesWindowLabelInDetail — v0.10.944: çip rozeti (stepSourceState)
// notları ve pencereyi taşımaz; iki traces/clickhouse rozeti ancak Detail'deki
// pencere etiketiyle ayırt edilir. Başarı ve fail() yolu.
func TestCPSourcesWindowLabelInDetail(t *testing.T) {
	srcs := cpSources(cpCmp(800, 400, 1000, 1000, [2]uint64{700, 300}), chstore.PeriodCompareRaw{}, cpWin(0, 60), cpWin(-60, 0), cpSourceOpts{})
	if srcs[0].Detail != "sorun penceresi" || srcs[1].Detail != "referans penceresi" {
		t.Errorf("başarı yolu Detail: %q / %q", srcs[0].Detail, srcs[1].Detail)
	}
	f := &cpFakeReader{names: []string{"checkout"}, envs: map[time.Time][]string{cpT0: {"prod"}},
		readErr: errors.New("code: 159, DB::Exception: Timeout exceeded: elapsed 30.1 seconds")}
	out, err := runComparePeriods(cpAnchoredCtx(), Deps{}, f, cpArgs(), cpWall)
	if err != nil {
		t.Fatal(err)
	}
	fs := out.(cpEnvelope)["sources"].([]sourcestate.Status)
	if !strings.HasPrefix(fs[0].Detail, "sorun penceresi: ") || !strings.HasPrefix(fs[1].Detail, "referans penceresi: ") ||
		fs[0].State != sourcestate.Timeout {
		t.Errorf("fail yolu Detail önekleri: %+v", fs)
	}
}

// TestCPNotesSecondaryReadFailures — v0.10.944: ikincil okuma hatası modelin
// gördüğü notlarda; okunamayan bağımlılık "istek yok / veri yok" dedirtmez.
func TestCPNotesSecondaryReadFailures(t *testing.T) {
	// DepsErr + requests==0 → "veri yok" DEĞİL, "okunamadı"
	empty := cpCmp(0, 0, 0, 0, [2]uint64{0, 0})
	raw := chstore.PeriodCompareRaw{DepsErr: context.DeadlineExceeded}
	j := strings.Join(cpNotes(cpNoteInput{Cmp: empty, SecondaryErrs: cpSecondaryErrs(raw)}), "\n")
	if strings.Contains(j, "veri yok") || !strings.Contains(j, "dependencies okunamadı (timeout)") || !strings.Contains(j, "'istek yok / servis kapalı' DEME") {
		t.Errorf("okunamayan bağımlılıkla boş pencere:\n%s", j)
	}
	// VersionsErr, tam veri → sürüm notu; karışım/uyarılar etkilenmez
	raw = chstore.PeriodCompareRaw{VersionsErr: errors.New("dial tcp 10.0.0.9:9000: connect: connection refused")}
	notes := cpNotes(cpNoteInput{Cmp: cpCmp(800, 400, 1000, 1000, [2]uint64{700, 300}), SecondaryErrs: cpSecondaryErrs(raw)})
	j = strings.Join(notes, "\n")
	if notes[0] != cpNoteSampling || !strings.Contains(j, "versions okunamadı (unreachable) — problem/reference içindeki boş versions listesi") ||
		!strings.Contains(j, "trafik karışımı kaydı") {
		t.Errorf("sürüm hatası notu:\n%s", j)
	}
	// OpsErr → karışım kayması değerlendirilemedi
	j = strings.Join(cpNotes(cpNoteInput{Cmp: empty, SecondaryErrs: cpSecondaryErrs(chstore.PeriodCompareRaw{OpsErr: context.DeadlineExceeded})}), "\n")
	if !strings.Contains(j, "traffic_mix_shift ve p95/karışım-kayması uyarısı değerlendirilemedi") {
		t.Errorf("operasyon hatası notu:\n%s", j)
	}
	// cpSources ile aynı liste, aynı sıra
	all := chstore.PeriodCompareRaw{OpsErr: context.DeadlineExceeded, DepsErr: context.DeadlineExceeded, PodsErr: context.DeadlineExceeded, VersionsErr: context.DeadlineExceeded}
	var names []string
	for _, se := range cpSecondaryErrs(all) {
		names = append(names, se.Name)
	}
	if strings.Join(names, ",") != "top_operations,dependencies,pods,versions" {
		t.Errorf("sıra: %v", names)
	}
}

// TestComparePeriodsFailureNoteWithinModelClamp — v0.10.944: dolu bir kıyas
// (dönem başına 10 operasyon/bağımlılık/pod/sürüm) ve bir başarısız ikincil
// okumada "okunamadı" notu, sohbetin 6000 rune'luk model kırpmasının
// (api.chatToolResultMaxRunes) içinde başlar; sources/notes iri dönem
// nesnelerinden ÖNCE.
func TestComparePeriodsFailureNoteWithinModelClamp(t *testing.T) {
	raw := chstore.PeriodCompareRaw{RED: [2]chstore.PeriodRED{{Requests: 36000, Errors: 400, P95Ms: 900, Buckets: 12}, {Requests: 36000, Errors: 40, P95Ms: 400, Buckets: 12}},
		PodsErr: context.DeadlineExceeded}
	for i := 0; i < 10; i++ {
		raw.Ops = append(raw.Ops, chstore.PeriodPairRow{Key: fmt.Sprintf("GET /api/v1/catalogue/items/{id}/variant-%02d", i), Count: [2]uint64{3000, 3000}, Errors: [2]uint64{40, 4}, P95Ms: [2]float64{900, 400}})
		raw.Deps = append(raw.Deps, chstore.PeriodPairRow{Kind: "service", Key: fmt.Sprintf("svc-downstream-%02d", i), Count: [2]uint64{2000, 2000}, Errors: [2]uint64{10, 1}, P95Ms: [2]float64{120, 80}})
		for p := 0; p < 2; p++ {
			raw.Values = append(raw.Values,
				chstore.PeriodValueRow{Period: p, Dim: "pod", Value: fmt.Sprintf("checkout-7d9f8b6c5d-%05d", i), Count: 3600, Distinct: 10},
				chstore.PeriodValueRow{Period: p, Dim: "version", Value: fmt.Sprintf("1.4.%d-build.%06d", i, i*7919), Count: 3600, Distinct: 10})
		}
	}
	f := &cpFakeReader{names: []string{"checkout"}, envs: map[time.Time][]string{cpT0: {"prod"}}, raw: raw}
	out, err := runComparePeriods(cpAnchoredCtx(), Deps{}, f, cpArgs(), cpWall)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if n := len([]rune(s)); n < 6000 {
		t.Fatalf("test sonucu yeterince iri değil (%d rune)", n)
	}
	i := strings.Index(s, "pods okunamadı (timeout) — problem/reference")
	if i < 0 || len([]rune(s[:i])) >= 6000 {
		t.Fatalf("okunamadı notu model kırpmasının dışında (bayt %d)", i)
	}
	is, ip := strings.Index(s, `"sources":`), strings.Index(s, `"problem":`)
	if is < 0 || ip < is || !strings.HasPrefix(s, `{"service":`) {
		t.Errorf("sıra: sources=%d problem=%d başlangıç=%.40s", is, ip, s)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil || len(back) != len(out.(cpEnvelope)) {
		t.Fatalf("sıralı zarf geçersiz: %v", err)
	}
}
