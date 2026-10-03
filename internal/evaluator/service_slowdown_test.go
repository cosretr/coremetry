package evaluator

// v0.10.1091 — yaygın yavaşlama hızlı yolu (`svc-slowdown:<servis>`). Operatör:
// "Dün söylediğim CRM sorunu yine oldu, bir sürü anomali geldi ama P1 problem
// gelmedi" → "Onay". Saf sınıflayıcı tablosu (her kapı, iki kol, batch),
// yaşam döngüsü (tek kovada açılış, tazeleme, 2 temiz kovada kapanış, okuma
// hatasında tazele, kesik okuma, fırtına tavanı), öncelik (critical 15 s / 5 s
// → P1), gerekçe cümlesi, kural kapatma ve tik sırası pinleri.

import (
	"errors"
	"math"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Kova 22:10–22:15 (UTC); tik 22:16 → en yeni tamamlanmış kova 22:10.
var (
	sslBucket = time.Date(2026, 10, 3, 22, 10, 0, 0, time.UTC)
	sslNow    = sslBucket.Add(6 * time.Minute)
)

const sslSvc = "crm-svc"

// sslOp — operasyon satırı; yavaşlama yaygın (kova p95'i = p99: çağrıların
// büyük kısmı yavaş). base = 24 sa p95 tabanı.
func sslOp(svc, op string, cur, base float64, calls uint64) chstore.SvcSlowOpRow {
	return chstore.SvcSlowOpRow{Service: svc, Operation: op, CurP99Ms: cur, CurP95Ms: cur, BaseP95Ms: base, CurCalls: calls, BaseCalls: 5000}
}

// sslSvcRow — servis özeti: cari çağrı, önceki saatin kova başı ortalaması,
// cari p99 / taban p95.
func sslSvcRow(svc string, cur, hourAvg uint64, p99, base float64) chstore.SvcSlowServiceRow {
	return chstore.SvcSlowServiceRow{Service: svc, CurCalls: cur, HourCalls: hourAvg * chstore.SvcSlowHourBuckets, CurP99Ms: p99, BaseP95Ms: base}
}

// sslQuantile — doğrusal enterpolasyonlu kuantil (tDigest'in küçük örneklemdeki
// davranışının modeli; SQL'in arrayElement(quantilesTDigestMerge(…)) ikizi değil,
// kararın ÖRNEKLEM düzeyinde neden doğru olduğunun kanıtı).
func sslQuantile(xs []float64, q float64) float64 {
	v := append([]float64(nil), xs...)
	sort.Float64s(v)
	pos := q * float64(len(v)-1)
	lo := int(math.Floor(pos))
	if lo+1 >= len(v) {
		return v[len(v)-1]
	}
	return v[lo] + (pos-float64(lo))*(v[lo+1]-v[lo])
}

// sslSample — n çağrı: slow tanesi slowMs, kalanı normalMs.
func sslSample(n, slow int, normalMs, slowMs float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = normalMs
		if i < slow {
			out[i] = slowMs
		}
	}
	return out
}

func sslOps(n int, p99, base float64, calls uint64) []chstore.SvcSlowOpRow {
	var out []chstore.SvcSlowOpRow
	for i := 0; i < n; i++ {
		out = append(out, sslOp(sslSvc, "GET /op-"+string(rune('a'+i)), p99-float64(i)*100, base, calls))
	}
	return out
}

func TestSvcSlowClassify(t *testing.T) {
	cfg := chstore.DefaultServiceSlowdown()
	isBatch := chstore.DefaultAnomalySensitivity().IsBatchService
	healthySvc := sslSvcRow(sslSvc, 600, 1000, 9000, 150) // trafik −%40, p99 60× → çöküş kolu da tutar
	quietSvc := sslSvcRow(sslSvc, 1000, 1000, 9000, 150)  // trafik düz → çöküş yok
	cases := []struct {
		name               string
		ops                []chstore.SvcSlowOpRow
		svcs               []chstore.SvcSlowServiceRow
		fire, op, collapse bool
	}{
		{"3 operasyon tabanların üstünde → açılır", sslOps(3, 15000, 120, 50), []chstore.SvcSlowServiceRow{quietSvc}, true, true, false},
		{"yalnız 2 operasyon → açılmaz", sslOps(2, 15000, 120, 50), []chstore.SvcSlowServiceRow{quietSvc}, false, false, false},
		{"oran ≥ 20× ama p99 < 5 s → açılmaz", sslOps(3, 4000, 50, 50), []chstore.SvcSlowServiceRow{quietSvc}, false, false, false},
		{"p99 ≥ 5 s ama oran < 20× → açılmaz", sslOps(3, 15000, 1000, 50), []chstore.SvcSlowServiceRow{quietSvc}, false, false, false},
		{"operasyon başına < 30 çağrı → açılmaz", sslOps(3, 15000, 120, 29), []chstore.SvcSlowServiceRow{quietSvc}, false, false, false},
		{"servis toplamı < 100 çağrı → açılmaz", sslOps(3, 15000, 120, 30), []chstore.SvcSlowServiceRow{sslSvcRow(sslSvc, 95, 90, 9000, 150)}, false, false, false},
		{"servis özeti yok ama operasyon toplamı ≥ 100 (alt sınır) → açılır", sslOps(3, 15000, 120, 40), nil, true, true, false},
		{"trafik çöküşü kolu: −%40 + p99 ≥ 3× → açılır (operasyon kolu olmadan)", nil, []chstore.SvcSlowServiceRow{healthySvc}, true, false, true},
		{"trafik −%30 → çöküş yok", nil, []chstore.SvcSlowServiceRow{sslSvcRow(sslSvc, 700, 1000, 9000, 150)}, false, false, false},
		{"trafik −%40, p99 ≥ 5 s ama < 3× taban → çöküş yok", nil, []chstore.SvcSlowServiceRow{sslSvcRow(sslSvc, 600, 1000, 6000, 2500)}, false, false, false},
		{"taban saat ortalaması < 100 → çöküş yok", nil, []chstore.SvcSlowServiceRow{sslSvcRow(sslSvc, 50, 90, 9000, 150)}, false, false, false},
		// İnceleme B: sessiz servis 150 → 60 çağrı, tek 300 ms'lik istek p99'u
		// 3× yapıyordu (−%60) — cari çağrı < 100 VE p99 < 5 s → açılmaz.
		{"150 → 60 çağrı + tek 300 ms istek → çöküş yok", nil, []chstore.SvcSlowServiceRow{sslSvcRow(sslSvc, 60, 150, 300, 80)}, false, false, false},
		{"yeterli hacim, −%60 ama p99 300 ms (< 5 s) → çöküş yok", nil, []chstore.SvcSlowServiceRow{sslSvcRow(sslSvc, 400, 1000, 300, 80)}, false, false, false},
		{"CRM benzeri: p99 15 s, trafik −%40 → çöküş kolu açılır", nil, []chstore.SvcSlowServiceRow{sslSvcRow(sslSvc, 600, 1000, 15000, 140)}, true, false, true},
		{"iki kol birden", sslOps(4, 18200, 120, 80), []chstore.SvcSlowServiceRow{healthySvc}, true, true, true},
	}
	for _, c := range cases {
		got := svcSlowClassify(sslBucket, c.ops, c.svcs, cfg, isBatch)
		v := got[sslSvc]
		if v.Fire != c.fire || v.OpFire != c.op || v.CollapseFire != c.collapse {
			t.Errorf("%s: fire=%v op=%v collapse=%v, istenen %v/%v/%v", c.name, v.Fire, v.OpFire, v.CollapseFire, c.fire, c.op, c.collapse)
		}
	}
	// Batch servis: iki kol da susar (okumalar zaten SQL'de düşürür; Go ikizi).
	batchOps := []chstore.SvcSlowOpRow{}
	for _, o := range sslOps(3, 15000, 120, 50) {
		o.Service = "billing-batch"
		batchOps = append(batchOps, o)
	}
	bs := sslSvcRow("billing-batch", 600, 1000, 9000, 150)
	if v := svcSlowClassify(sslBucket, batchOps, []chstore.SvcSlowServiceRow{bs}, cfg, isBatch)["billing-batch"]; v.Fire {
		t.Errorf("batch servis açılmamalı: %+v", v)
	}
	// Ölçü: operasyon kolunda Value = en yüksek operasyon p99'u, Threshold = O
	// operasyonun kendi tabanı (oran ≥ 20× → daima P1); yalnız çöküş kolunda
	// servis p99'u / servis tabanı.
	v := svcSlowClassify(sslBucket, sslOps(3, 15000, 120, 50), []chstore.SvcSlowServiceRow{quietSvc}, cfg, isBatch)[sslSvc]
	if v.Value != 15000 || v.Threshold != 120 || v.Ops[0].CurP99Ms != 15000 {
		t.Errorf("operasyon kolu ölçüsü: %v / %v", v.Value, v.Threshold)
	}
	v = svcSlowClassify(sslBucket, nil, []chstore.SvcSlowServiceRow{healthySvc}, cfg, isBatch)[sslSvc]
	if v.Value != 9000 || v.Threshold != 150 {
		t.Errorf("çöküş kolu ölçüsü: %v / %v", v.Value, v.Threshold)
	}
}

// Kural ateşlediğinde DAİMA P1 (operatör): Threshold = en yavaş operasyonun
// kendi tabanı → oran ≥ RiseFactor (20) ≥ 2. 5.5 s / taban 120 ms (45.8×) → P1
// (eski Threshold = 5 s biçiminde 1.1× → P2 olurdu); 15 s / 120 ms → P1; yalnız
// çöküş kolu (servis p99 / servis tabanı ≥ 3) → P1.
func TestSvcSlowProblemIsP1(t *testing.T) {
	cfg := chstore.DefaultServiceSlowdown()
	for _, c := range []struct {
		name string
		ops  []chstore.SvcSlowOpRow
		svc  chstore.SvcSlowServiceRow
	}{
		{"5.5 s / 120 ms", sslOps(3, 5500, 120, 50), sslSvcRow(sslSvc, 1000, 1000, 900, 150)},
		{"15 s / 120 ms", sslOps(3, 15000, 120, 50), sslSvcRow(sslSvc, 1000, 1000, 900, 150)},
		{"yalnız çöküş: 9 s / 300 ms", nil, sslSvcRow(sslSvc, 450, 1000, 9000, 300)},
	} {
		v := svcSlowClassify(sslBucket, c.ops, []chstore.SvcSlowServiceRow{c.svc}, cfg, nil)[sslSvc]
		if !v.Fire {
			t.Fatalf("%s: ateşlemeli", c.name)
		}
		got := chstore.EnrichProblemsWithPriority([]chstore.Problem{svcSlowProblem(v, time.UTC)})
		if got[0].Priority != "P1" {
			t.Errorf("%s: %s (%s), Value=%v Threshold=%v", c.name, got[0].Priority, got[0].PriorityReason, got[0].Value, got[0].Threshold)
		}
	}
	v := svcSlowClassify(sslBucket, sslOps(3, 5500, 120, 50), []chstore.SvcSlowServiceRow{sslSvcRow(sslSvc, 600, 1000, 9000, 150)}, cfg, nil)[sslSvc]
	if v.Value != 5500 || v.Threshold != 120 {
		t.Fatalf("ölçü = en yavaş op p99 / o op'un tabanı: %v / %v", v.Value, v.Threshold)
	}
	p := svcSlowProblem(v, time.UTC)
	if p.ID != "svc-slowdown:crm-svc" || p.RuleID != p.ID || p.Severity != "critical" || p.Metric != "p99_ms" ||
		p.Kind != chstore.ProblemKindService || p.Service != sslSvc || p.StartedAt != sslBucket.UnixNano() {
		t.Fatalf("problem satırı: %+v", p)
	}
	got := chstore.EnrichProblemsWithPriority([]chstore.Problem{p})
	if got[0].Priority != "P1" || got[0].Category != chstore.CategorySlowdown {
		t.Fatalf("öncelik/kategori: %s %s (%s)", got[0].Priority, got[0].Category, got[0].PriorityReason)
	}
}

func TestSvcSlowReasonSentence(t *testing.T) {
	cfg := chstore.DefaultServiceSlowdown()
	ops := []chstore.SvcSlowOpRow{
		sslOp(sslSvc, "POST /orders", 18200, 120, 80), sslOp(sslSvc, "GET /customer", 16000, 90, 60),
		sslOp(sslSvc, "GET /search", 15100, 200, 50), sslOp(sslSvc, "GET /cart", 9000, 100, 40),
		sslOp(sslSvc, "GET /a", 8000, 100, 40), sslOp(sslSvc, "GET /b", 7000, 100, 40), sslOp(sslSvc, "GET /c", 6000, 100, 40),
	}
	v := svcSlowClassify(sslBucket, ops, []chstore.SvcSlowServiceRow{sslSvcRow(sslSvc, 600, 1000, 9000, 150)}, cfg, nil)[sslSvc]
	got := svcSlowReason(v, time.UTC)
	want := "crm-svc servisinde yaygın yavaşlama: 7 operasyon, p99 en çok 18.2 s (taban 120 ms), trafik −%40 — 03.10 22:10 kovası." +
		" En yavaş: POST /orders 18.2 s (taban 120 ms), GET /customer 16.0 s (taban 90 ms), GET /search 15.1 s (taban 200 ms)."
	if got != want {
		t.Errorf("gerekçe:\n got %s\nwant %s", got, want)
	}
	ist, _ := time.LoadLocation("Europe/Istanbul")
	if !strings.Contains(svcSlowReason(v, ist), "04.10 01:10 kovası") { // 22:10 UTC = 01:10 (+03)
		t.Errorf("damga operatör diliminde olmalı: %s", svcSlowReason(v, ist))
	}
	c := svcSlowClassify(sslBucket, nil, []chstore.SvcSlowServiceRow{sslSvcRow(sslSvc, 450, 1000, 9000, 300)}, cfg, nil)[sslSvc]
	if r := svcSlowReason(c, time.UTC); r != "crm-svc servisinde yaygın yavaşlama: trafik −%55, servis p99 9.0 s (taban 300 ms, 30.0×) — 03.10 22:10 kovası." {
		t.Errorf("çöküş gerekçesi: %s", r)
	}
	// İnceleme E: yalnız çöküş kolu ayrı kural adı taşır (grafik op tabanı
	// çizgisini çizmez); operasyon kolu temel ad. Ad yapışkan.
	if n := svcSlowProblem(c, time.UTC).RuleName; n != svcSlowRuleNameCollapse {
		t.Errorf("çöküş adı: %q", n)
	}
	if n := svcSlowProblem(v, time.UTC).RuleName; n != svcSlowRuleName {
		t.Errorf("op adı: %q", n)
	}
	opened := svcSlowProblem(v, time.UTC)
	if n := svcSlowRefreshed(opened, c, time.UTC).RuleName; n != svcSlowRuleName {
		t.Errorf("op koluyla açılmış satır çöküşle tazelenince adı korunmalı: %q", n)
	}
	if n := svcSlowRefreshed(svcSlowProblem(c, time.UTC), v, time.UTC).RuleName; n != svcSlowRuleName {
		t.Errorf("çöküşle açılan satırda op kolu ateşleyince temel ada geçmeli: %q", n)
	}
}

// İnceleme A: iç içe span'leri olan TEK yavaş trace (sunucu + iç + istemci
// operasyonu, her biri ~40 çağrı) üç operasyonun p99'unu birden "max"a çeker;
// yavaş pay tabanı (kova p95 ≥ MinP99Ms/2) bunu ayırır. ≥ ~%6 yavaş çağrı (3/40)
// açar; 2/40 (%5) enterpolasyonlu p95'i tabana taşımaz.
func TestSvcSlowSlowShareFloor(t *testing.T) {
	cfg := chstore.DefaultServiceSlowdown()
	mk := func(slow int) []chstore.SvcSlowOpRow {
		var out []chstore.SvcSlowOpRow
		for _, op := range []string{"GET /checkout", "checkout.process", "POST payment-svc"} {
			xs := sslSample(40, slow, 120, 20000)
			out = append(out, chstore.SvcSlowOpRow{Service: sslSvc, Operation: op, CurCalls: 40, BaseCalls: 5000, BaseP95Ms: 120,
				CurP99Ms: sslQuantile(xs, 0.99), CurP95Ms: sslQuantile(xs, 0.95)})
		}
		return out
	}
	one := mk(1)
	if one[0].CurP99Ms < cfg.MinP99Ms || one[0].CurP99Ms < cfg.RiseFactor*120 {
		t.Fatalf("model: tek aykırı p99'u tabanların üstüne çekmeli (p99 %.0f)", one[0].CurP99Ms)
	}
	svc := []chstore.SvcSlowServiceRow{sslSvcRow(sslSvc, 120, 120, 12000, 130)}
	if v := svcSlowClassify(sslBucket, one, svc, cfg, nil)[sslSvc]; v.OpFire {
		t.Errorf("tek 20 s'lik aykırı × 3 operasyon (n=40) açmamalı: p95 %.0f", one[0].CurP95Ms)
	}
	// Taban olmasaydı açardı (kapının sebebi bu).
	noFloor := append([]chstore.SvcSlowOpRow(nil), one...)
	for i := range noFloor {
		noFloor[i].CurP95Ms = noFloor[i].CurP99Ms
	}
	if v := svcSlowClassify(sslBucket, noFloor, svc, cfg, nil)[sslSvc]; !v.OpFire {
		t.Error("model: yavaş pay tabanı olmadan aynı satırlar açardı")
	}
	if v := svcSlowClassify(sslBucket, mk(2), svc, cfg, nil)[sslSvc]; v.OpFire {
		t.Error("2/40 (%5) yavaş: enterpolasyonlu p95 tabanın altında → açmaz")
	}
	if v := svcSlowClassify(sslBucket, mk(3), svc, cfg, nil)[sslSvc]; !v.OpFire {
		t.Error("3 operasyonda 3/40 (%7.5) yavaş çağrı açmalı")
	}
}

// İnceleme D: taban 24 sa'in HAVUZLANMIŞ p95'i. Dünkü 10 dk'lık olay (zirvede, 2
// kova × 400 çağrı 15 s; günün geri kalanı 100 çağrı / kova ~120 ms) taban p99'unu
// olay seviyesine çeker ve bugünkü AYNI olayı "20× değil" diye susturur; p95 tabanı
// kıpırdamaz → bugünkü olay açılır.
func TestSvcSlowBaselineIgnoresYesterdaysIncident(t *testing.T) {
	cfg := chstore.DefaultServiceSlowdown()
	var base []float64
	for b := 0; b < 286; b++ {
		for i := 0; i < 100; i++ {
			base = append(base, 100+float64(i%40)) // 100–139 ms
		}
	}
	base = append(base, sslSample(800, 800, 0, 15000)...)
	p99, p95 := sslQuantile(base, 0.99), sslQuantile(base, 0.95)
	if p99 < 15000/cfg.RiseFactor {
		t.Fatalf("model: dünkü olay taban p99'unu şişirmeli (p99 %.0f)", p99)
	}
	if p95 > 200 {
		t.Fatalf("model: taban p95'i kıpırdamamalı (p95 %.0f)", p95)
	}
	today := func(b float64) []chstore.SvcSlowOpRow {
		var out []chstore.SvcSlowOpRow
		for _, op := range []string{"GET /a", "GET /b", "GET /c"} {
			out = append(out, chstore.SvcSlowOpRow{Service: sslSvc, Operation: op, CurCalls: 200, BaseCalls: 29400,
				CurP99Ms: 15000, CurP95Ms: 15000, BaseP95Ms: b})
		}
		return out
	}
	svc := []chstore.SvcSlowServiceRow{sslSvcRow(sslSvc, 600, 1000, 15000, p95)}
	if v := svcSlowClassify(sslBucket, today(p99), svc, cfg, nil)[sslSvc]; v.OpFire {
		t.Error("model: p99 tabanıyla bugünkü olay susardı (testin karşı örneği)")
	}
	if v := svcSlowClassify(sslBucket, today(p95), svc, cfg, nil)[sslSvc]; !v.OpFire {
		t.Error("p95 tabanıyla bugünkü aynı olay açılmalı")
	}
}

// sslReads — sahte okuyucu; çağrı sayısı tutulur.
type sslReads struct {
	ops      []chstore.SvcSlowOpRow
	svcs     []chstore.SvcSlowServiceRow
	opsErr   error
	svcErr   error
	trunc    bool
	opCalls  int
	svcCalls int
	gotSvcs  []string
}

func (f *sslReads) reads() svcSlowReads {
	return svcSlowReads{
		ops: func() ([]chstore.SvcSlowOpRow, bool, error) {
			f.opCalls++
			return f.ops, f.trunc, f.opsErr
		},
		svcs: func(s []string) ([]chstore.SvcSlowServiceRow, bool, error) {
			f.svcCalls++
			f.gotSvcs = s
			return f.svcs, false, f.svcErr
		},
	}
}

func sslOpen(p chstore.Problem) []*chstore.Problem { return []*chstore.Problem{&p} }

func TestSvcSlowLifecycle(t *testing.T) {
	cfg := chstore.DefaultServiceSlowdown()
	breach := &sslReads{ops: sslOps(3, 15000, 120, 50), svcs: []chstore.SvcSlowServiceRow{sslSvcRow(sslSvc, 600, 1000, 9000, 150)}}
	clean := &sslReads{svcs: []chstore.SvcSlowServiceRow{sslSvcRow(sslSvc, 1000, 1000, 160, 150)}}
	var m svcSlowMemo

	// 1) TEK kovada açılış (sürdürme yok).
	act, err := m.step(sslNow, cfg, nil, nil, breach.reads(), time.UTC)
	if err != nil || len(act.Open) != 1 || act.Open[0].ID != "svc-slowdown:crm-svc" {
		t.Fatalf("tek kovada açılmalı: %+v %v", act, err)
	}
	opened := act.Open[0]

	// 2) Aynı kovanın sonraki tiki: yeniden okuma YOK, açık satır değişmeden tazelenir.
	act, _ = m.step(sslNow.Add(time.Minute), cfg, nil, sslOpen(opened), breach.reads(), time.UTC)
	if breach.opCalls != 1 || len(act.Keep) != 1 || len(act.Open) != 0 {
		t.Fatalf("aynı kova: okuma=%d keep=%d open=%d", breach.opCalls, len(act.Keep), len(act.Open))
	}

	// 3) Yeni kova hâlâ ihlalde → tazelenir (ölçü yükselirse yükselir).
	worse := &sslReads{ops: sslOps(3, 20000, 120, 50), svcs: breach.svcs}
	act, _ = m.step(sslNow.Add(5*time.Minute), cfg, nil, sslOpen(opened), worse.reads(), time.UTC)
	if len(act.Refresh) != 1 || act.Refresh[0].Value != 20000 || act.Refresh[0].Severity != "critical" {
		t.Fatalf("ihlalde tazele: %+v", act)
	}
	refreshed := act.Refresh[0]
	// Ölçü düşerse Value tepe kalır (öncelik olay ortasında P1'den inmez), açıklama güncel.
	act, _ = m.step(sslNow.Add(10*time.Minute), cfg, nil, sslOpen(refreshed), breach.reads(), time.UTC)
	if len(act.Refresh) != 1 || act.Refresh[0].Value != 20000 || !strings.Contains(act.Refresh[0].Description, "15.0 s") {
		t.Fatalf("tepe ölçü korunmalı, açıklama güncel: %+v", act.Refresh)
	}

	// 4) İlk temiz kova → açık kalır (histerezis); ikinci temiz kova → kapanır.
	act, _ = m.step(sslNow.Add(15*time.Minute), cfg, nil, sslOpen(refreshed), clean.reads(), time.UTC)
	if len(act.Keep) != 1 || len(act.Resolve) != 0 {
		t.Fatalf("1. temiz kova açık kalmalı: %+v", act)
	}
	// Araya ihlalli kova girerse sayaç sıfırlanır.
	act, _ = m.step(sslNow.Add(20*time.Minute), cfg, nil, sslOpen(refreshed), breach.reads(), time.UTC)
	if len(act.Refresh) != 1 {
		t.Fatalf("ihlal sayacı sıfırlamalı: %+v", act)
	}
	act, _ = m.step(sslNow.Add(25*time.Minute), cfg, nil, sslOpen(refreshed), clean.reads(), time.UTC)
	if len(act.Resolve) != 0 {
		t.Fatalf("sıfırlanan sayaçta tek temiz kova kapatmamalı: %+v", act)
	}
	act, _ = m.step(sslNow.Add(30*time.Minute), cfg, nil, sslOpen(refreshed), clean.reads(), time.UTC)
	if len(act.Resolve) != 1 || act.Resolve[0].Status != "resolved" || act.Resolve[0].ResolvedAt == nil || act.Resolve[0].Value != 20000 {
		t.Fatalf("2 ardışık temiz kovada kapanmalı (Value ezilmeden): %+v", act)
	}
}

// Okuma hatası → açıklar DEĞİŞMEDEN tazelenir, karar yok, bellek ilerlemez
// (sonraki tik yeniden okur); bayat süpürme olay ortasında P1'i kapatmaz.
func TestSvcSlowReadErrorKeepsOpen(t *testing.T) {
	cfg := chstore.DefaultServiceSlowdown()
	open := chstore.Problem{ID: "svc-slowdown:crm-svc", RuleID: "svc-slowdown:crm-svc", Status: "open", Severity: "critical", Value: 15000, Threshold: 5000}
	for _, r := range []*sslReads{{opsErr: errors.New("code: 159, timeout")}, {svcErr: errors.New("code: 241, memory")}} {
		var m svcSlowMemo
		for i := 0; i < 3; i++ {
			act, err := m.step(sslNow.Add(time.Duration(i)*5*time.Minute), cfg, nil, sslOpen(open), r.reads(), time.UTC)
			if err == nil || len(act.Keep) != 1 || len(act.Resolve)+len(act.Refresh)+len(act.Open) != 0 || act.Keep[0].Value != 15000 {
				t.Fatalf("okuma hatası: %+v %v", act, err)
			}
		}
		if !m.bucket.IsZero() {
			t.Error("okuma hatasında bellek ilerlememeli")
		}
	}
	// İnceleme C: kova başına en çok İKİ deneme (ilk + bir yeniden deneme);
	// sonra aynı kovanın tiklerinde okuma YOK, açıklar tazelenir; yeni kovada
	// hak yenilenir.
	r := &sslReads{opsErr: errors.New("context deadline exceeded")}
	var m0 svcSlowMemo
	for i := 0; i < 4; i++ {
		act, _ := m0.step(sslNow.Add(time.Duration(i)*time.Minute/2), cfg, nil, sslOpen(open), r.reads(), time.UTC)
		if len(act.Keep) != 1 {
			t.Fatalf("tik %d: açık satır tazelenmeli", i)
		}
	}
	if r.opCalls != 2 {
		t.Errorf("aynı kovada okuma denemesi = %d, istenen 2", r.opCalls)
	}
	if _, err := m0.step(sslNow.Add(time.Minute), cfg, nil, sslOpen(open), r.reads(), time.UTC); !errors.Is(err, errSvcSlowGaveUp) {
		t.Errorf("hak bitince sessiz hata: %v", err)
	}
	_, _ = m0.step(sslNow.Add(5*time.Minute), cfg, nil, sslOpen(open), r.reads(), time.UTC)
	if r.opCalls != 3 {
		t.Errorf("yeni kovada yeniden okunmalı: %d", r.opCalls)
	}
	// Kesik okuma (satır tavanı): görünmeyen servis temiz sayılmaz.
	var m svcSlowMemo
	tr := &sslReads{trunc: true}
	for i := 0; i < 3; i++ {
		act, _ := m.step(sslNow.Add(time.Duration(i)*5*time.Minute), cfg, nil, sslOpen(open), tr.reads(), time.UTC)
		if len(act.Resolve) != 0 || len(act.Keep) != 1 {
			t.Fatalf("kesik okumada kapanış yok: %+v", act)
		}
	}
	// Açık servis servis özeti okumasına her zaman girer (kapanış kararı için).
	if len(tr.gotSvcs) != 1 || tr.gotSvcs[0] != "crm-svc" {
		t.Errorf("servis okuması açık servisleri içermeli: %v", tr.gotSvcs)
	}
}

func TestSvcSlowStormCap(t *testing.T) {
	cfg := chstore.DefaultServiceSlowdown()
	cfg.MaxNewPerTick = 2
	var ops []chstore.SvcSlowOpRow
	for i, s := range []string{"svc-a", "svc-b", "svc-c"} {
		for j := 0; j < 3; j++ {
			ops = append(ops, sslOp(s, "op"+string(rune('a'+j)), 10000+float64(i)*5000, 120, 50))
		}
	}
	var m svcSlowMemo
	r := &sslReads{ops: ops}
	act, _ := m.step(sslNow, cfg, nil, nil, r.reads(), time.UTC)
	if len(act.Open) != 2 || act.Open[0].Service != "svc-c" || act.Open[1].Service != "svc-b" {
		t.Fatalf("tavan 2, en kötü önce: %+v", act.Open)
	}
	// Kalan aday aynı kovanın sonraki tikinde, yeniden okumadan açılır.
	act, _ = m.step(sslNow.Add(time.Minute), cfg, nil, nil, r.reads(), time.UTC)
	if r.opCalls != 1 || len(act.Open) != 1 || act.Open[0].Service != "svc-a" {
		t.Fatalf("kalan aday: okuma=%d %+v", r.opCalls, act.Open)
	}
}

// Kural kapatılınca açık/onaylı satırlar "rule disabled" gerekçesiyle kapanır.
func TestSvcSlowDisabledResolutions(t *testing.T) {
	all := []*chstore.Problem{
		{ID: "svc-slowdown:crm-svc", RuleID: "svc-slowdown:crm-svc", Status: "open", Description: "x", Value: 15000},
		{ID: "svc-slowdown:b", RuleID: "svc-slowdown:b", Status: "acknowledged"},
		{ID: "svc-slowdown:c", RuleID: "svc-slowdown:c", Status: "resolved"},
		{ID: "db-health:oracle@h/d", RuleID: "db-health:oracle@h/d", Status: "open"},
		nil,
	}
	open := svcSlowOpenRows(all)
	if len(open) != 2 {
		t.Fatalf("yalnız açık/onaylı svc-slowdown satırları: %d", len(open))
	}
	res := svcSlowDisabledResolutions(open, sslNow.UnixNano())
	for _, q := range res {
		if q.Status != "resolved" || !strings.Contains(q.Description, "rule disabled") {
			t.Errorf("kapanış: %+v", q)
		}
	}
	if res[0].Value != 15000 || all[0].Status != "open" {
		t.Error("Value ezilmemeli, anlık görüntü satırı değişmemeli")
	}
}

func TestSvcSlowLatestBucket(t *testing.T) {
	for _, c := range []struct{ now, want string }{
		{"22:15:40", "22:10"}, // 30 sn payı geçti → 22:10 kovası tamam
		{"22:15:10", "22:05"}, // pay dolmadı → bir önceki
		{"22:19:59", "22:10"},
	} {
		n, _ := time.Parse("15:04:05", c.now)
		w, _ := time.Parse("15:04", c.want)
		if got := svcSlowLatestBucket(n); !got.Equal(w) {
			t.Errorf("%s → %s, istenen %s", c.now, got.Format("15:04"), c.want)
		}
	}
}

// Yumuşak-hata ve sıra pinleri (kaynak okuması): doğrulanmamış ayar → tazele;
// kural kapalı → rule-disabled kapanışı; pas db-health'ten SONRA, runtime'dan
// ÖNCE (eskalasyon + bayat süpürmeden önce); açılışta incident + bildirim.
func TestSvcSlowWiring(t *testing.T) {
	b, err := os.ReadFile("service_slowdown.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, frag := range []string{
		"if !e.store.AnomalySensitivityConfirmed() {\n\t\te.applySvcSlow(ctx, keepAll(open))\n\t\treturn",
		"rctx, cancel := context.WithTimeout(ctx, chstore.SvcSlowReadTimeout)",
		"if !cfg.On() {\n\t\te.applySvcSlow(ctx, svcSlowActions{Resolve: svcSlowDisabledResolutions(open, now.UnixNano())})",
		"e.store.AttachProblemToIncident(ctx, p)",
		"go e.notifier.SendProblemAlert(context.Background(), p)",
	} {
		if !strings.Contains(s, frag) {
			t.Errorf("kaynakta yok: %q", frag)
		}
	}
	src, err := os.ReadFile("evaluator.go")
	if err != nil {
		t.Fatal(err)
	}
	ev := string(src)
	health := strings.Index(ev, "e.evaluateDBHealth(ctx)")
	slow := strings.Index(ev, "e.evaluateServiceSlowdown(ctx)")
	runtime := strings.Index(ev, "e.evaluateRuntimePods(ctx)")
	sweep := strings.Index(ev, "\te.sweepStaleProblems(ctx)")
	if health < 0 || slow < 0 || !(health < slow && slow < runtime && slow < sweep) {
		t.Errorf("sıra bozuk: health=%d slow=%d runtime=%d sweep=%d", health, slow, runtime, sweep)
	}
	if strings.Count(ev, "e.evaluateServiceSlowdown(ctx)") != 1 {
		t.Error("tik başına bir kez çağrılmalı")
	}
}
