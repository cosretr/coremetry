package chstore

// prior_window_sites_test.go — v0.10.1028 regresyon testleri.
//
// v0.10.1025 /databases ve /messaging listelerinde "önceki pencere"yi
// `[from − dur, from)` diye kurmanın hizasız from'da current'ın ilk
// kovasını İKİ pencereye saydığını bulmuştu (prior_window.go). Aynı biçim
// dört yüzeyde daha vardı; ikisinin okuyucusu 5 dk / `< to` kalıbının
// dışında sınır kuruyor:
//
//   - /endpoints MV yolu (GetEndpointsMV): alt sınır DAKİKAYA iner, kova
//     greni katmana göre 10 sn ya da 1 dk → EndpointsPriorWindow.
//   - topoloji okuyucusu (readServiceTopologyAggFiltered): üst sınır
//     `to`'nun KOVASINI da alarak dışa yuvarlanır → TopologyPriorWindow.
//     Burada eski formül HİZALI from'da bile bir kova paylaşıyordu.
//
// Her test okuyucunun SQL yüklemini Go'da taklit eder (aşağıdaki
// modeller) ve kaynak pini modelin hâlâ okuyucuyu anlattığını doğrular.
// Özellikler: ortak kova YOK, kova sayısı EŞİT.

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// gridBuckets — `time_bucket >= floorA(lo) AND time_bucket < sec(hi)`
// yükleminin `grain` ızgarasında aldığı kova etiketleri. align: okuyucunun
// alt sınırı indirdiği ızgara (grain'in katı); hi saniyeye iner
// (clickhouse-go konumsal bağı).
func gridBuckets(lo, hi time.Time, align, grain time.Duration) []time.Time {
	end := hi.Truncate(time.Second)
	var out []time.Time
	for b := lo.Truncate(align); b.Before(end); b = b.Add(grain) {
		out = append(out, b)
	}
	return out
}

// topoBuckets — readServiceTopologyAggFiltered'ın yüklemi:
// `time_bucket >= toStartOfFiveMinute(lo) AND time_bucket <
// toStartOfFiveMinute(hi) + 5 dk`; lo/hi `.Unix()` ile bağlanır.
func topoBuckets(lo, hi time.Time) []time.Time {
	end := time.Unix(hi.Unix(), 0).UTC().Truncate(mvBucketWidth).Add(mvBucketWidth)
	var out []time.Time
	for b := time.Unix(lo.Unix(), 0).UTC().Truncate(mvBucketWidth); b.Before(end); b = b.Add(mvBucketWidth) {
		out = append(out, b)
	}
	return out
}

// assertDisjointEqual — iki küme ortak kova taşımaz ve eşit boydadır.
func assertDisjointEqual(t *testing.T, label string, cur, prior []time.Time) {
	t.Helper()
	seen := make(map[int64]bool, len(cur))
	for _, b := range cur {
		seen[b.UnixNano()] = true
	}
	for _, b := range prior {
		if seen[b.UnixNano()] {
			t.Fatalf("%s: ORTAK KOVA %s — aynı veri hem current hem prior sayılıyor",
				label, b.UTC().Format("15:04:05"))
		}
	}
	if len(cur) != len(prior) {
		t.Fatalf("%s: kova sayısı eşit değil: current %d, prior %d — sayaç deltası sahte",
			label, len(cur), len(prior))
	}
}

func sharedCount(a, b []time.Time) int {
	seen := make(map[int64]bool, len(a))
	for _, x := range a {
		seen[x.UnixNano()] = true
	}
	n := 0
	for _, y := range b {
		if seen[y.UnixNano()] {
			n++
		}
	}
	return n
}

func psAt(h, m, s, ns int) time.Time {
	return time.Date(2026, 10, 2, h, m, s, ns, time.UTC)
}

// ── PriorWindowGrid ─────────────────────────────────────────────────────

// TestPriorWindowIsGrid5m — tek uygulama: PriorWindow ≡ PriorWindowGrid(5dk).
func TestPriorWindowIsGrid5m(t *testing.T) {
	r := rand.New(rand.NewSource(1028))
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5000; i++ {
		from := base.Add(time.Duration(r.Int63n(int64(30 * 24 * time.Hour)))).Add(time.Duration(r.Intn(1e9)))
		to := from.Add(time.Duration(r.Int63n(int64(72*time.Hour))) - time.Hour).Add(time.Duration(r.Intn(1e9)))
		a1, a2 := PriorWindow(from, to)
		b1, b2 := PriorWindowGrid(from, to, 5*time.Minute)
		if !a1.Equal(b1) || !a2.Equal(b2) {
			t.Fatalf("PriorWindow(%v, %v) = [%v, %v) ≠ PriorWindowGrid 5dk [%v, %v)", from, to, a1, a2, b1, b2)
		}
	}
}

// TestPriorWindowGridTable — 1 dk ızgarası (spanmetrics_1m alt sınırı).
func TestPriorWindowGridTable(t *testing.T) {
	cases := []struct {
		name             string
		from, to         time.Time
		wantFrom, wantTo time.Time
		wantCur          int
	}{
		{"hizasız from ve to — 61 kova", psAt(10, 3, 35, 0), psAt(11, 3, 35, 0), psAt(9, 2, 0, 0), psAt(10, 3, 0, 0), 61},
		{"hizalı 1 saat", psAt(10, 0, 0, 0), psAt(11, 0, 0, 0), psAt(9, 0, 0, 0), psAt(10, 0, 0, 0), 60},
		{"saniye-altı uçlar — to saniyeye iner", psAt(10, 0, 0, 400_000_000), psAt(11, 0, 0, 900_000_000), psAt(9, 0, 0, 0), psAt(10, 0, 0, 0), 60},
		{"1 dk'dan kısa, tek kovanın içinde", psAt(10, 3, 10, 0), psAt(10, 3, 40, 0), psAt(10, 2, 0, 0), psAt(10, 3, 0, 0), 1},
		{"1 dk'dan kısa ama sınırı kesiyor", psAt(10, 3, 50, 0), psAt(10, 4, 10, 0), psAt(10, 1, 0, 0), psAt(10, 3, 0, 0), 2},
		{"boş pencere", psAt(10, 3, 0, 0), psAt(10, 3, 0, 0), psAt(10, 3, 0, 0), psAt(10, 3, 0, 0), 0},
		{"ters pencere — prior boş, panik yok", psAt(10, 7, 0, 0), psAt(10, 2, 0, 0), psAt(10, 7, 0, 0), psAt(10, 7, 0, 0), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pFrom, pTo := PriorWindowGrid(c.from, c.to, time.Minute)
			if !pFrom.Equal(c.wantFrom) || !pTo.Equal(c.wantTo) {
				t.Fatalf("[%s, %s), beklenen [%s, %s)", pFrom.Format("15:04:05"), pTo.Format("15:04:05"),
					c.wantFrom.Format("15:04:05"), c.wantTo.Format("15:04:05"))
			}
			cur := gridBuckets(c.from, c.to, time.Minute, time.Minute)
			if len(cur) != c.wantCur {
				t.Fatalf("model %d kova okudu, beklenen %d", len(cur), c.wantCur)
			}
			assertDisjointEqual(t, c.name, cur, gridBuckets(pFrom, pTo, time.Minute, time.Minute))
		})
	}
}

// TestPriorWindowGridProperties — dört ızgara, rastgele pencereler: uçlar
// ızgarada, ortak kova yok, kova sayısı eşit, prior current'a bitişik.
func TestPriorWindowGridProperties(t *testing.T) {
	r := rand.New(rand.NewSource(10281))
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, g := range []time.Duration{10 * time.Second, time.Minute, 5 * time.Minute, time.Hour} {
		t.Run(g.String(), func(t *testing.T) {
			for i := 0; i < 3000; i++ {
				from := base.Add(time.Duration(r.Int63n(int64(30 * 24 * time.Hour)))).Add(time.Duration(r.Intn(1e9)))
				to := from.Add(time.Second + time.Duration(r.Int63n(int64(72*time.Hour)))).Add(time.Duration(r.Intn(1e9)))
				pFrom, pTo := PriorWindowGrid(from, to, g)
				if !pFrom.Equal(pFrom.Truncate(g)) || !pTo.Equal(pTo.Truncate(g)) {
					t.Fatalf("uçlar %v ızgarasında değil: [%v, %v)", g, pFrom, pTo)
				}
				cur := gridBuckets(from, to, g, g)
				prior := gridBuckets(pFrom, pTo, g, g)
				assertDisjointEqual(t, fmt.Sprintf("from=%v to=%v", from, to), cur, prior)
				if len(cur) > 0 && !prior[len(prior)-1].Add(g).Equal(cur[0]) {
					t.Fatalf("prior bitişik değil: son %v, current ilk %v", prior[len(prior)-1], cur[0])
				}
			}
		})
	}
}

// TestPriorWindowGridNoGrid — bucket ≤ 0 = ham okuma: birebir süre
// kaydırması. v0.10.1028 (inceleme R5): günü bölmeyen kova (7 sn) da aynı
// yola düşer — Go'nun Truncate ızgarası (1. yıla göre) ClickHouse'un epoch
// ızgarasıyla yalnız 86400 sn'yi bölen kovalarda çakışır.
func TestPriorWindowGridNoGrid(t *testing.T) {
	from, to := psAt(10, 3, 17, 5), psAt(10, 18, 17, 5)
	for _, b := range []time.Duration{0, -time.Minute, 7 * time.Second, 7 * time.Minute} {
		pFrom, pTo := PriorWindowGrid(from, to, b)
		if !pTo.Equal(from) || pTo.Sub(pFrom) != to.Sub(from) {
			t.Errorf("bucket=%v: prior [%v, %v) — beklenen birebir kayma [from − %v, from)", b, pFrom, pTo, to.Sub(from))
		}
	}
	// Günü bölen kovalar ızgaralı kalır (uç ızgarada, hizalı prior).
	for _, b := range []time.Duration{10 * time.Second, time.Minute, 5 * time.Minute, time.Hour, 24 * time.Hour} {
		if _, pTo := PriorWindowGrid(from, to, b); !pTo.Equal(from.Truncate(b)) {
			t.Errorf("bucket=%v: pTo %v, beklenen floorB(from) %v", b, pTo, from.Truncate(b))
		}
	}
	// Go ızgarası = epoch ızgarası: 1. yıldan epoch'a TAM gün sayısı.
	// (Saniyeyle: iki uç arası time.Duration'a sığmaz.)
	if sec := -(time.Time{}).Unix(); sec%86400 != 0 {
		t.Fatalf("sıfır zaman → epoch %d sn tam gün değil — PriorWindowGrid'in ızgara varsayımı çöker", sec)
	}
}

// ── EndpointsPriorWindow ────────────────────────────────────────────────

// endpointsTiers — GetEndpointsMV'nin iki katmanı (endpointsSparkGrid).
var endpointsTiers = []struct {
	name  string
	grain time.Duration
}{
	{"spanmetrics_10s", 10 * time.Second},
	{"spanmetrics_1m", time.Minute},
}

func TestEndpointsPriorWindowTable(t *testing.T) {
	cases := []struct {
		name             string
		from, to         time.Time
		wantFrom, wantTo time.Time
	}{
		// BUG BUYDU: eski prior [09:03:35, 10:03:35) — 10:03 dakikası (1m)
		// ve 10:03:00/10/20/30 kovaları (10s) iki pencerede.
		{"hizasız from ve to (varsayılan 1 sa, canlı)", psAt(10, 3, 35, 0), psAt(11, 3, 35, 0), psAt(9, 2, 0, 0), psAt(10, 2, 40, 0)},
		{"hizalı 1 saat — klasik geri kayma", psAt(10, 0, 0, 0), psAt(11, 0, 0, 0), psAt(9, 0, 0, 0), psAt(10, 0, 0, 0)},
		{"saniye-altı uçlar", psAt(10, 0, 0, 500_000_000), psAt(11, 0, 0, 700_000_000), psAt(9, 0, 0, 0), psAt(10, 0, 0, 0)},
		{"30 sn'lik kısa pencere, dakika sınırını kesiyor", psAt(10, 3, 35, 0), psAt(10, 4, 5, 0), psAt(10, 1, 0, 0), psAt(10, 2, 10, 0)},
		{"15 dk hizasız", psAt(10, 3, 35, 0), psAt(10, 18, 35, 0), psAt(9, 47, 0, 0), psAt(10, 2, 40, 0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pFrom, pTo := EndpointsPriorWindow(EndpointsQuery{From: c.from, To: c.to})
			if !pFrom.Equal(c.wantFrom) || !pTo.Equal(c.wantTo) {
				t.Fatalf("[%s, %s), beklenen [%s, %s)", pFrom.Format("15:04:05"), pTo.Format("15:04:05"),
					c.wantFrom.Format("15:04:05"), c.wantTo.Format("15:04:05"))
			}
			for _, tier := range endpointsTiers {
				// Okuyucu prior'un From'unu da dakikaya indirir — model aynı.
				cur := gridBuckets(c.from, c.to, endpointsMVFloor, tier.grain)
				prior := gridBuckets(pFrom, pTo, endpointsMVFloor, tier.grain)
				assertDisjointEqual(t, tier.name, cur, prior)
			}
		})
	}
}

// TestEndpointsPriorWindowProperties — iki katmanda da rastgele pencereler.
func TestEndpointsPriorWindowProperties(t *testing.T) {
	r := rand.New(rand.NewSource(10282))
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5000; i++ {
		from := base.Add(time.Duration(r.Int63n(int64(30 * 24 * time.Hour)))).Add(time.Duration(r.Intn(1e9)))
		to := from.Add(time.Second + time.Duration(r.Int63n(int64(7*time.Hour)))).Add(time.Duration(r.Intn(1e9)))
		pFrom, pTo := EndpointsPriorWindow(EndpointsQuery{From: from, To: to})
		if !pFrom.Equal(pFrom.Truncate(time.Minute)) {
			t.Fatalf("pFrom dakika hizalı değil (okuyucu indirir, kova sayısı kayar): %v", pFrom)
		}
		if pTo.After(from.Truncate(time.Minute)) {
			t.Fatalf("pTo %v current'ın ilk dakikasını (%v) geçiyor", pTo, from.Truncate(time.Minute))
		}
		for _, tier := range endpointsTiers {
			assertDisjointEqual(t, fmt.Sprintf("%s from=%v to=%v", tier.name, from, to),
				gridBuckets(from, to, endpointsMVFloor, tier.grain),
				gridBuckets(pFrom, pTo, endpointsMVFloor, tier.grain))
		}
	}
}

// TestEndpointsPriorWindowRawPath — cluster / env süzgeci ham spans yolunu
// zorlar (forcesRaw, GetEndpoints'in AYNI yüklemi): ızgara yok, prior
// birebir süre kaydırması.
func TestEndpointsPriorWindowRawPath(t *testing.T) {
	from, to := psAt(10, 3, 35, 0), psAt(11, 3, 35, 0)
	for _, q := range []EndpointsQuery{
		{From: from, To: to, Env: "uat"},
		{From: from, To: to, Cluster: "cluster-a"},
	} {
		pFrom, pTo := EndpointsPriorWindow(q)
		if !pTo.Equal(from) || pTo.Sub(pFrom) != to.Sub(from) {
			t.Errorf("ham yol (env=%q cluster=%q) prior [%v, %v) — beklenen [from − %v, from)",
				q.Env, q.Cluster, pFrom, pTo, to.Sub(from))
		}
	}
}

// TestEndpointsPriorWindowMixedTier — v0.10.1028 (inceleme R6), KABUL
// EDİLEN davranışın belgesi. GetEndpointsMV katmanı okumanın kendi
// başlangıç YAŞINA göre seçer (endpointsSparkGrid: ≤ 24 sa → 10 sn). Current
// başı 24 saatten genç, prior başı 24 saatten yaşlıysa current 10 sn, prior
// 1 dk katmanını okur. O durumda da ortak veri YOK; prior current'tan 0–50 sn
// FAZLA kapsar (n1 × 60 − n10 × 10). ≥ 1 sa pencerede ≤ %1,4; yalnız birkaç
// on saniyelik pencerelerde belirgin. 2 sa boy sınırında fark 0'dır (orada
// n10 × 10 = n1 × 60), yani karışıklığın tek kaynağı 24 sa yaş kapısı.
func TestEndpointsPriorWindowMixedTier(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	// tier — GetEndpointsMV'nin seçimi, sabit bir "now" ile.
	tier := func(lo, hi time.Time) time.Duration {
		f := lo.Truncate(time.Minute)
		src, _, _ := endpointsSparkGrid(hi.Unix()-f.Unix(), now.Unix()-f.Unix())
		if src == "spanmetrics_10s" {
			return 10 * time.Second
		}
		return time.Minute
	}
	coverage := func(lo, hi time.Time, grain time.Duration) (time.Time, time.Time, time.Duration) {
		b := gridBuckets(lo, hi, endpointsMVFloor, grain)
		if len(b) == 0 {
			return time.Time{}, time.Time{}, 0
		}
		first, end := b[0], b[len(b)-1].Add(grain)
		return first, end, end.Sub(first)
	}

	// Tablo satırı: 1 sa, current başı 23 sa 50 dk önce (10 sn), prior başı
	// 24 sa 51 dk önce (1 dk) → prior 20 sn fazla.
	from := time.Date(2026, 10, 1, 12, 10, 35, 0, time.UTC)
	to := from.Add(time.Hour)
	pFrom, pTo := EndpointsPriorWindow(EndpointsQuery{From: from, To: to})
	cg, pg := tier(from, to), tier(pFrom, pTo)
	if cg != 10*time.Second || pg != time.Minute {
		t.Fatalf("fikstür katman karışıklığı üretmiyor: current %v, prior %v", cg, pg)
	}
	cFirst, _, cCov := coverage(from, to, cg)
	_, pEnd, pCov := coverage(pFrom, pTo, pg)
	if pEnd.After(cFirst) {
		t.Fatalf("prior verisi %v'e kadar, current %v'den başlıyor — ortak veri", pEnd, cFirst)
	}
	if d := pCov - cCov; d != 20*time.Second {
		t.Fatalf("prior kapsaması current'tan %v fazla, beklenen 20s (current %v, prior %v)", d, cCov, pCov)
	}

	// Özellik: karışık katmanlı her pencerede ortak veri yok, fark [0, 50 sn].
	r := rand.New(rand.NewSource(10284))
	mixed := 0
	for i := 0; i < 5000; i++ {
		f := now.Add(-24*time.Hour + time.Duration(r.Int63n(int64(2*time.Hour)))).Add(time.Duration(r.Intn(1e9)))
		e := f.Add(time.Second + time.Duration(r.Int63n(int64(119*time.Minute)))).Add(time.Duration(r.Intn(1e9)))
		pf, pt := EndpointsPriorWindow(EndpointsQuery{From: f, To: e})
		cg, pg := tier(f, e), tier(pf, pt)
		if cg == pg {
			continue
		}
		mixed++
		cFirst, _, cCov := coverage(f, e, cg)
		_, pEnd, pCov := coverage(pf, pt, pg)
		if pEnd.After(cFirst) {
			t.Fatalf("from=%v to=%v: karışık katmanda ortak veri", f, e)
		}
		if d := pCov - cCov; d < 0 || d > 50*time.Second {
			t.Fatalf("from=%v to=%v: kapsama farkı %v, kabul aralığı [0, 50s]", f, e, d)
		}
	}
	if mixed == 0 {
		t.Fatal("rastgele örneklem hiç karışık katman üretmedi — test bir şey sınamıyor")
	}

	// 2 sa boy sınırı: current x ∈ [7191, 7199] sn → 10 sn katmanı, prior
	// n10 × 10 = 7200 sn → 1 dk katmanı; ama kapsamalar EŞİT (720 × 10 =
	// 120 × 60). Yaş kapısı devre dışı (pencere 3 sa önce).
	base := now.Add(-3 * time.Hour).Truncate(time.Minute).Add(20 * time.Second)
	for x := 7191; x <= 7199; x++ {
		e := base.Truncate(time.Minute).Add(time.Duration(x) * time.Second)
		pf, pt := EndpointsPriorWindow(EndpointsQuery{From: base, To: e})
		cg, pg := tier(base, e), tier(pf, pt)
		_, _, cCov := coverage(base, e, cg)
		_, _, pCov := coverage(pf, pt, pg)
		if cg != 10*time.Second || pg != time.Minute || pCov != cCov {
			t.Errorf("x=%d: katman %v/%v, kapsama current %v / prior %v — sınırda fark 0 beklenir", x, cg, pg, cCov, pCov)
		}
	}
}

// TestEndpointsOldPriorOverlapped — kusurun belgesi: eski `[from − dur,
// from)` hizasız from'da iki katmanda da current'la kova paylaşıyordu.
func TestEndpointsOldPriorOverlapped(t *testing.T) {
	from, to := psAt(10, 3, 35, 0), psAt(11, 3, 35, 0)
	want := map[string]int{"spanmetrics_10s": 4, "spanmetrics_1m": 1}
	for _, tier := range endpointsTiers {
		cur := gridBuckets(from, to, endpointsMVFloor, tier.grain)
		old := gridBuckets(from.Add(-to.Sub(from)), from, endpointsMVFloor, tier.grain)
		if n := sharedCount(cur, old); n != want[tier.name] {
			t.Errorf("%s: eski formülün ortak kova sayısı %d, beklenen %d", tier.name, n, want[tier.name])
		}
	}
}

// TestEndpointsPriorWindowReaderPins — model GetEndpointsMV'yi hâlâ anlatıyor
// mu? Alt sınır dakikaya iniyor, üst `< ?`, katman grenleri 60 / 10 sn.
// Biri değişirse EndpointsPriorWindow da yeniden türetilmeli.
func TestEndpointsPriorWindowReaderPins(t *testing.T) {
	body := funcBody(t, "endpoints.go", "func (s *Store) GetEndpointsMV(")
	for _, want := range []string{
		"from := q.From.Truncate(time.Minute)",
		`where := "time_bucket >= ? AND time_bucket < ?"`,
		"args = append(args, from.Unix(), bucketSec, from, q.To)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GetEndpointsMV %q içermiyor — EndpointsPriorWindow'un modeli kaymış olabilir", want)
		}
	}
	grid := funcBody(t, "endpoints.go", "func endpointsSparkGrid(")
	for _, want := range []string{"grain := int64(60)", "grain = 10"} {
		if !strings.Contains(grid, want) {
			t.Errorf("endpointsSparkGrid %q içermiyor — katman grenleri değişti mi?", want)
		}
	}
	if endpointsMVFloor != time.Minute || endpointsMVFineGrain != 10*time.Second {
		t.Errorf("EndpointsPriorWindow sabitleri okuyucudan ayrışmış: floor=%v fine=%v",
			endpointsMVFloor, endpointsMVFineGrain)
	}
	// Yol seçimi GetEndpoints'in kendi yüklemiyle aynı olmalı.
	disp := funcBody(t, "endpoints.go", "func (s *Store) GetEndpoints(")
	if !strings.Contains(disp, "if q.forcesRaw() {") {
		t.Error("GetEndpoints ham yolu artık q.forcesRaw() ile seçmiyor — EndpointsPriorWindow'un dalı da güncellenmeli")
	}
}

// ── TopologyPriorWindow ─────────────────────────────────────────────────

func TestTopologyPriorWindowTable(t *testing.T) {
	cases := []struct {
		name             string
		from, to         time.Time
		wantFrom, wantTo time.Time // OKUYUCU argümanları
		wantCur          int
		oldShared        int // eski (from − dur, from) argümanının ortak kovası
	}{
		{"hizasız from ve to", psAt(10, 3, 0, 0), psAt(11, 3, 0, 0), psAt(8, 55, 0, 0), psAt(9, 55, 0, 0), 13, 1},
		// Okuyucu `to`'nun kovasını da alır: hizalı 1 saat 13 kova. Eski
		// formül burada BİLE 10:00 kovasını paylaşıyordu.
		{"hizalı 1 saat — eski formül yine de paylaşıyor", psAt(10, 0, 0, 0), psAt(11, 0, 0, 0), psAt(8, 55, 0, 0), psAt(9, 55, 0, 0), 13, 1},
		{"saniye-altı uçlar", psAt(10, 0, 0, 600_000_000), psAt(11, 2, 59, 900_000_000), psAt(8, 55, 0, 0), psAt(9, 55, 0, 0), 13, 1},
		{"5 dk'dan kısa, tek kovanın içinde", psAt(10, 1, 0, 0), psAt(10, 3, 0, 0), psAt(9, 55, 0, 0), psAt(9, 55, 0, 0), 1, 1},
		{"5 dk'dan kısa ama sınırı kesiyor", psAt(10, 4, 0, 0), psAt(10, 6, 0, 0), psAt(9, 50, 0, 0), psAt(9, 55, 0, 0), 2, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pFrom, pTo := TopologyPriorWindow(c.from, c.to)
			if !pFrom.Equal(c.wantFrom) || !pTo.Equal(c.wantTo) {
				t.Fatalf("(%s, %s), beklenen (%s, %s)", pFrom.Format("15:04:05"), pTo.Format("15:04:05"),
					c.wantFrom.Format("15:04:05"), c.wantTo.Format("15:04:05"))
			}
			cur := topoBuckets(c.from, c.to)
			if len(cur) != c.wantCur {
				t.Fatalf("model %d kova okudu, beklenen %d", len(cur), c.wantCur)
			}
			assertDisjointEqual(t, c.name, cur, topoBuckets(pFrom, pTo))
			if n := sharedCount(cur, topoBuckets(c.from.Add(-c.to.Sub(c.from)), c.from)); n != c.oldShared {
				t.Errorf("eski formülün ortak kova sayısı %d, beklenen %d", n, c.oldShared)
			}
		})
	}
}

func TestTopologyPriorWindowProperties(t *testing.T) {
	r := rand.New(rand.NewSource(10283))
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5000; i++ {
		from := base.Add(time.Duration(r.Int63n(int64(30 * 24 * time.Hour)))).Add(time.Duration(r.Intn(1e9)))
		to := from.Add(time.Duration(r.Int63n(int64(72 * time.Hour)))).Add(time.Duration(r.Intn(1e9)))
		pFrom, pTo := TopologyPriorWindow(from, to)
		cur := topoBuckets(from, to)
		prior := topoBuckets(pFrom, pTo)
		assertDisjointEqual(t, fmt.Sprintf("from=%v to=%v", from, to), cur, prior)
		if len(cur) > 0 && !prior[len(prior)-1].Add(mvBucketWidth).Equal(cur[0]) {
			t.Fatalf("prior bitişik değil: son %v, current ilk %v", prior[len(prior)-1], cur[0])
		}
	}
	// Ters pencere: current boş → prior da boş.
	if p := topoBuckets(TopologyPriorWindow(psAt(10, 7, 0, 0), psAt(10, 2, 0, 0))); len(p) != 0 {
		t.Errorf("ters pencerede prior %d kova okudu, beklenen 0", len(p))
	}
}

// TestTopologyPriorWindowReaderPin — model okuyucunun yüklemini anlatıyor
// mu? Üst sınır `to`'nun kovası dahil (dışa yuvarlı), iki uç `.Unix()`.
func TestTopologyPriorWindowReaderPin(t *testing.T) {
	body := funcBody(t, "topology.go", "func (s *Store) readServiceTopologyAggFiltered(")
	for _, want := range []string{
		"args := []any{from.Unix(), to.Unix()}",
		"WHERE time_bucket >= toStartOfFiveMinute(toDateTime(?, 'UTC'))",
		"AND time_bucket <  toStartOfFiveMinute(toDateTime(?, 'UTC')) + INTERVAL 5 MINUTE",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("readServiceTopologyAggFiltered %q içermiyor — TopologyPriorWindow'un modeli kaymış olabilir", want)
		}
	}
	focus := funcBody(t, "topology.go", "func (s *Store) ReadServiceTopologyAggForFocus(")
	if !strings.Contains(focus, "s.readServiceTopologyAggFiltered(ctx, from, to, limit, frontier)") {
		t.Error("ReadServiceTopologyAggForFocus aynı yüklemi okumuyor — servicegraph prior'u ayrı model ister")
	}
}

// ── dbstmt detayı: GERÇEK builder'ın bağ argümanlarıyla ──────────────────

// TestDBStmtDetailPriorWindowRealBuilder — dbStmtDetailWhere'in current ve
// prior için ÜRETTİĞİ alt/üst bağları okunur (args[1], args[2]); model
// yalnız `>=` / `<` operatörü ve saniye bağı. Prior API'deki gibi
// PriorWindow(from, to)'dan.
func TestDBStmtDetailPriorWindowRealBuilder(t *testing.T) {
	bounds := func(from, to time.Time) (time.Time, time.Time) {
		wc := dbStmtDetailWhere(DBStmtDetailQuery{Hash: 0xC0FFEE, From: from, To: to})
		if !strings.Contains(wc.sql(), "time_bucket >= ? AND time_bucket < ?") {
			t.Fatalf("dbStmtDetailWhere yüklemi değişti: %s", wc.sql())
		}
		return wc.args[1].(time.Time), wc.args[2].(time.Time)
	}
	read := func(from, to time.Time) []time.Time {
		lo, hi := bounds(from, to)
		return gridBuckets(lo, hi, mvBucketWidth, mvBucketWidth)
	}
	for _, c := range []struct {
		name      string
		from, to  time.Time
		oldShared int
	}{
		{"hizasız from (BUG BUYDU)", psAt(10, 3, 0, 0), psAt(11, 3, 0, 0), 1},
		{"hizalı", psAt(10, 0, 0, 0), psAt(11, 0, 0, 0), 0},
		{"saniye-altı", psAt(10, 2, 59, 999_000_000), psAt(11, 0, 0, 400_000_000), 1},
		{"5 dk'dan kısa", psAt(10, 1, 0, 0), psAt(10, 3, 0, 0), 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			pFrom, pTo := PriorWindow(c.from, c.to)
			cur := read(c.from, c.to)
			assertDisjointEqual(t, c.name, cur, read(pFrom, pTo))
			if n := sharedCount(cur, read(c.from.Add(-c.to.Sub(c.from)), c.from)); n != c.oldShared {
				t.Errorf("eski formülün ortak kova sayısı %d, beklenen %d", n, c.oldShared)
			}
		})
	}
}
