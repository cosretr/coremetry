package anomaly

// red_vm_query_test.go — v0.10.1478: VM RED sorgu kurucusunun şekli + tavanları,
// koşu/grup planı, GroupKey çözümü, gerekli kova kümelerinin SQL ikizliği ve
// kova belleği.

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestVMREDExprShape(t *testing.T) {
	http := vmREDFamilies[0]
	sc := vmQueryScope{SvcLabel: "service_name", EnvMatcher: `deployment_environment_name=~"prod-a|prod-b"`,
		StatusLabels: []string{"http_response_status_code", "http_status_code"}}
	count, ok := vmREDExpr(http, vmKindCount, sc, 86400)
	if !ok {
		t.Fatal("count ölçülemez dedi")
	}
	for _, want := range []string{"sum by (service_name) (increase(", "[300s] offset 86400s", "http_server_request_duration_seconds_count", `deployment_environment_name=~"prod-a|prod-b"`} {
		if !strings.Contains(count, want) {
			t.Errorf("count ifadesi %q taşımıyor: %s", want, count)
		}
	}
	errs, _ := vmREDExpr(http, vmKindErr, sc, 0)
	if strings.Count(errs, " or ") != 1 || !strings.Contains(errs, `http_response_status_code=~"5.."`) || !strings.Contains(errs, `http_status_code=~"5.."`) {
		t.Errorf("err ifadesi iki durum yazımını or ile birleştirmeli: %s", errs)
	}
	if strings.Contains(errs, "offset") {
		t.Errorf("ofset 0 yazılmamalı: %s", errs)
	}
	p99, _ := vmREDExpr(http, vmKindP99, sc, 0)
	if !strings.HasPrefix(p99, "(histogram_quantile(0.99, sum by (service_name, le) (increase(") || !strings.HasSuffix(p99, ") * 1000") {
		t.Errorf("HTTP p99 saniye → ms ölçeklenmeli: %s", p99)
	}
	legacy := vmREDFamilies[1]
	lp, _ := vmREDExpr(legacy, vmKindP99, vmQueryScope{SvcLabel: "service_name"}, 0)
	if strings.Contains(lp, "* 1000") || !strings.Contains(lp, `__name__="rpc_server_duration_milliseconds_bucket"`) {
		t.Errorf("gRPC legacy ms ailesi ölçeklenmez, tam ad: %s", lp)
	}
	if _, ok := vmREDExpr(legacy, vmKindErr, vmQueryScope{SvcLabel: "service_name"}, 0); ok {
		t.Error("durum etiketi yokken hata sayısı ölçülemez olmalı (0 sanılmasın)")
	}
	ge, _ := vmREDExpr(legacy, vmKindErr, vmQueryScope{SvcLabel: "service_name", StatusLabels: []string{"rpc_grpc_status_code"}}, 0)
	if !strings.Contains(ge, `rpc_grpc_status_code=~"2|4|8|12|13|14|15"`) {
		t.Errorf("gRPC sunucu-hatası kümesi pinli: %s", ge)
	}
	for _, e := range []string{count, errs, p99, lp} {
		if strings.Contains(e, "spans") || strings.Contains(e, "service_summary") {
			t.Errorf("VM ifadesi span tablosuna değmemeli: %s", e)
		}
	}
}

func TestVMREDQueryUnionAndLabels(t *testing.T) {
	sc := vmQueryScope{SvcLabel: "service_name", StatusLabels: []string{"http_response_status_code"}}
	q, ok := vmREDQuery(vmREDFamilies[0], vmKindCount, sc, []int64{0, 86400, 172800})
	if !ok {
		t.Fatal("sorgu kurulamadı")
	}
	if strings.Count(q, "label_set(") != 3 || strings.Count(q, ") or label_set(") != 2 {
		t.Errorf("üç ofset üç işaretli alt ifade olmalı: %s", q)
	}
	for _, off := range []string{`"cm_off", "0")`, `"cm_off", "86400")`, `"cm_off", "172800")`} {
		if !strings.Contains(q, off) {
			t.Errorf("%s işareti yok", off)
		}
	}
	if _, ok := vmREDQuery(vmREDFamilies[0], vmKindCount, vmQueryScope{}, []int64{0}); ok {
		t.Error("servis etiketi yokken sorgu kurulmamalı")
	}
	if _, ok := vmREDQuery(vmREDFamilies[0], vmKindCount, sc, nil); ok {
		t.Error("ofsetsiz sorgu kurulmamalı")
	}
}

// TestVMREDQueryBounds — en kötü gerçekçi grup (6 ofset, iki durum yazımı,
// 200 karakterlik ortam eşleştiricisi) bölündükten sonra her parça tavanın
// altında; tavan GET + ara katman 8 KiB'ının altında.
func TestVMREDQueryBounds(t *testing.T) {
	if vmMaxQueryLen > 7000 {
		t.Fatalf("sorgu tavanı %d — URL kodlamasıyla 8 KiB'ı aşar", vmMaxQueryLen)
	}
	if vmMaxRunBuckets*vmMaxOffsetsPerQuery > 72 {
		t.Fatalf("tek sorgu en çok 6 saatlik kova okumalı: %d kova", vmMaxRunBuckets*vmMaxOffsetsPerQuery)
	}
	if vmMaxQueriesPerTick > 32 || vmQueryTimeout > 15*time.Second || vmTickBudget > 30*time.Second || vmConcurrency > 8 {
		t.Fatal("tik bütçesi sabitleri sınırı aştı")
	}
	env := "deployment_environment_name=~\"" + strings.Repeat("prod-eu-a|", 18) + "prod\""
	sc := vmQueryScope{SvcLabel: "service_name", EnvMatcher: env, StatusLabels: []string{"http_response_status_code", "http_status_code"}}
	g := vmQueryGroup{RefStart: 1_800_000_000, Len: 12, Offsets: []int64{0, 86400, 2 * 86400, 3 * 86400, 4 * 86400, 5 * 86400}}
	for k := vmREDKind(0); k < vmKindN; k++ {
		build := func(offs []int64) string { q, _ := vmREDQuery(vmREDFamilies[0], k, sc, offs); return q }
		parts := vmSplitForLen(g, build)
		n := 0
		for _, p := range parts {
			n += len(p.Offsets)
			if l := len(build(p.Offsets)); l > vmMaxQueryLen {
				t.Errorf("%s: parça %d karakter > %d", k, l, vmMaxQueryLen)
			}
		}
		if n != len(g.Offsets) {
			t.Errorf("%s: bölme ofset kaybetti (%d/%d)", k, n, len(g.Offsets))
		}
	}
}

func TestVMRunsAndGroups(t *testing.T) {
	b := int64(1_800_000_000) // 5 dk ızgarasında
	b -= b % 300
	missing := []int64{b + 600, b, b + 300, b + 300, b + 3600, b + 86400, b + 86400 + 300}
	runs := vmRuns(missing, 2)
	want := []vmRun{{b, 2}, {b + 600, 1}, {b + 3600, 1}, {b + 86400, 2}}
	if !reflect.DeepEqual(runs, want) {
		t.Fatalf("koşular %+v, beklenen %+v", runs, want)
	}
	groups := vmGroupRuns(runs, 6)
	// uzunluk 2: {b+86400 ref, ofsetler 0, 86400}; uzunluk 1: {b+3600 ref, 0, 3000}
	if len(groups) != 2 {
		t.Fatalf("iki grup beklendi: %+v", groups)
	}
	if groups[0].RefStart != b+86400 || !reflect.DeepEqual(groups[0].Offsets, []int64{0, 86400}) || groups[0].Len != 2 {
		t.Errorf("ilk grup en yeni koşu referanslı olmalı: %+v", groups[0])
	}
	if groups[1].RefStart != b+3600 || !reflect.DeepEqual(groups[1].Offsets, []int64{0, 3000}) {
		t.Errorf("ikinci grup: %+v", groups[1])
	}
	// ofset tavanı
	var many []vmRun
	for i := 0; i < 14; i++ {
		many = append(many, vmRun{Start: b - int64(i)*86400, Len: 1})
	}
	gs := vmGroupRuns(many, vmMaxOffsetsPerQuery)
	if len(gs) != 3 {
		t.Fatalf("14 gün / 6 ofset = 3 sorgu, oldu %d", len(gs))
	}
	for _, g := range gs {
		if len(g.Offsets) > vmMaxOffsetsPerQuery {
			t.Errorf("grup %d ofset taşıyor", len(g.Offsets))
		}
	}
	if vmRuns(nil, 3) != nil || vmRuns([]int64{b}, 0) != nil {
		t.Error("boş girdi koşu üretmemeli")
	}
}

func TestVMNextRunLen(t *testing.T) {
	cases := []struct {
		cur  int
		ok   bool
		want int
	}{{12, false, 6}, {6, false, 3}, {1, false, 1}, {0, false, 6}, {3, true, 6}, {8, true, 12}, {12, true, 12}}
	for _, c := range cases {
		if got := vmNextRunLen(c.cur, c.ok); got != c.want {
			t.Errorf("vmNextRunLen(%d,%v) = %d, beklenen %d", c.cur, c.ok, got, c.want)
		}
	}
}

func TestVMParseGroupKey(t *testing.T) {
	svc, off, ok := vmParseGroupKey([]string{`cm_off="86400"`, `service_name="orders-api"`}, "service_name")
	if !ok || svc != "orders-api" || off != 86400 {
		t.Fatalf("got %q %d %v", svc, off, ok)
	}
	svc, _, ok = vmParseGroupKey([]string{`service_name="a\"b"`}, "service_name")
	if !ok || svc != `a"b` {
		t.Fatalf("kaçışlı değer: %q", svc)
	}
	if _, _, ok := vmParseGroupKey([]string{`job="x"`}, "service_name"); ok {
		t.Error("servis etiketi yokken ok olmamalı")
	}
}

// sqlSeasonalPredicate — buildAllSeasonalQuery WHERE'inin satır satır yeniden
// yazımı (toHour/toMinute/toDayOfWeek UTC; ızgara floor).
func sqlSeasonalPredicate(tb int64, cutoff, upper int64, class string, target, radius int) bool {
	if tb < cutoff || tb >= upper {
		return false
	}
	t := time.Unix(tb, 0).UTC()
	dow := (int(t.Weekday())+6)%7 + 1 // mode 0: 1=Pzt … 7=Paz
	cls := "weekday"
	if dow == 6 {
		cls = "saturday"
	} else if dow == 7 {
		cls = "sunday"
	}
	if cls != class {
		return false
	}
	sod := t.Hour()*3600 + t.Minute()*60
	a := int(math.Abs(float64(sod - target)))
	return int(math.Min(float64(a), float64(86400-a))) <= radius
}

// TestVMSeasonalBucketsMatchSQLPredicate — VM kova kümesi = mevsimsel SQL
// yükleminin seçtiği 5 dk kovaları (gece yarısı sarması, cmt/paz ayrı).
func TestVMSeasonalBucketsMatchSQLPredicate(t *testing.T) {
	for _, at := range []time.Time{
		time.Date(2026, 10, 19, 0, 7, 0, 0, time.UTC),   // Pazartesi 00:07 → Cuma 23:5x dahil
		time.Date(2026, 10, 24, 23, 58, 0, 0, time.UTC), // Cumartesi gece
		time.Date(2026, 10, 25, 12, 3, 0, 0, time.UTC),  // Pazar
		time.Date(2026, 10, 22, 14, 31, 0, 0, time.UTC), // Perşembe
	} {
		got := vmSeasonalBuckets(at, seasonalDays, seasonalNeighborBuckets)
		cutoff, upper, class, target, radius := seasonalBinds(at, seasonalDays, seasonalNeighborBuckets, time.UTC)
		var want []int64
		for tb := cutoff.Unix() - cutoff.Unix()%300; tb < upper.Unix(); tb += 300 {
			if sqlSeasonalPredicate(tb, cutoff.Unix(), upper.Unix(), class, target, radius) {
				want = append(want, tb)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%v: VM kümesi SQL yüklemiyle ayrışıyor (%d ≠ %d)", at, len(got), len(want))
		}
		if len(got) == 0 {
			t.Fatalf("%v: boş küme", at)
		}
	}
	// Pazartesi 00:07 → Cuma 23:50..23:55 (hafta içi) dahil, Pazar 23:5x HARİÇ.
	at := time.Date(2026, 10, 19, 0, 7, 0, 0, time.UTC)
	set := map[int64]bool{}
	for _, b := range vmSeasonalBuckets(at, seasonalDays, seasonalNeighborBuckets) {
		set[b] = true
	}
	if !set[time.Date(2026, 10, 16, 23, 55, 0, 0, time.UTC).Unix()] {
		t.Error("Cuma 23:55 (hafta içi, ±15 dk) dahil olmalı")
	}
	if set[time.Date(2026, 10, 18, 23, 55, 0, 0, time.UTC).Unix()] {
		t.Error("Pazar 23:55 hafta içi tabanına girmemeli")
	}
}

func TestVMBehaviorAndConsecutiveBuckets(t *testing.T) {
	now := time.Date(2026, 10, 22, 14, 31, 0, 0, time.UTC)
	upper, cutoff, recentFrom, target := behaviorBinds(now)
	got := vmBehaviorBuckets(now)
	var want []int64
	for tb := cutoff.Unix(); tb < upper.Unix(); tb += 300 {
		how := hourOfWeek(time.Unix(tb, 0))
		d := int(math.Abs(float64(how - target)))
		if d2 := hoursPerWeek - d; d2 < d {
			d = d2
		}
		if d <= behaviorNeighborHours || tb >= recentFrom.Unix() {
			want = append(want, tb)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("davranış kümesi SQL yüklemiyle ayrışıyor (%d ≠ %d)", len(got), len(want))
	}
	if len(got) > 600 {
		t.Fatalf("davranış kümesi %d kova — SQL LIMIT 600 BY ile aynı tavanın altında olmalı", len(got))
	}
	c := vmConsecutiveBuckets(now)
	if len(c) != 288 && len(c) != 287 {
		t.Fatalf("ardışık 24 sa ≈ 288 kova olmalı, %d", len(c))
	}
	if c[len(c)-1] != lastCompleteBucketStart(now).Unix()-300 {
		t.Error("son ardışık kova tamamlanmış son kova olmalı")
	}
}

func TestVMREDStore(t *testing.T) {
	st := newVMREDStore()
	b := int64(1_800_000_000 - 1_800_000_000%300)
	st.put(0, vmKindCount, b, "a", 10)
	st.put(0, vmKindCount, b, "b", math.NaN()) // yazılmaz
	st.markHave(0, vmKindCount, []int64{b, b + 300})
	if v, ok := st.get(0, vmKindCount, b, "a"); !ok || v != 10 {
		t.Fatalf("get a = %v %v", v, ok)
	}
	if _, ok := st.get(0, vmKindCount, b, "b"); ok {
		t.Error("NaN yazılmamalı")
	}
	if _, ok := st.get(0, vmKindCount, b+300, "a"); ok {
		t.Error("okunmuş ama serisiz kova yok dönmeli")
	}
	if m := st.missing(0, vmKindCount, []int64{b, b + 300, b + 600}); !reflect.DeepEqual(m, []int64{b + 600}) {
		t.Errorf("eksik %v", m)
	}
	if m := st.missing(0, vmKindErr, []int64{b}); len(m) != 1 {
		t.Error("başka tür okunmamış sayılmalı")
	}
	st.retain(map[int64]bool{b + 300: true})
	if st.has(0, vmKindCount, b) {
		t.Error("retain gerekli dışını atmalı")
	}
	st.markHave(1, vmKindP99, []int64{b + 300})
	st.flushFamily(1)
	if st.has(1, vmKindP99, b+300) || !st.has(0, vmKindCount, b+300) {
		t.Error("flushFamily yalnız o aileyi atmalı")
	}
	big := newVMREDStore()
	for i := 0; i < vmMaxServices+5; i++ {
		big.id(fmt.Sprintf("s%d", i))
	}
	if len(big.names) != vmMaxServices || big.dropped != 5 {
		t.Errorf("servis tavanı: %d / düşen %d", len(big.names), big.dropped)
	}
}
