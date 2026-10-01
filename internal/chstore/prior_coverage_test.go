package chstore

// prior_coverage_test.go — v0.10.1025 inceleme düzeltmeleri (R1, R3, R4).
//
//   R1 — canlı kenar: `to = now` iken current'ın son 5 dk kovası henüz
//        doluyor, prior ise N tam kova. Sayaç deltası aşağı kayıyordu (düz
//        iş yükünde 5 dk pencerede ortalama −%25). PriorCoverage + ScalePriorCount.
//   R3 — saklama ufku: prior'un başı okunan tablonun TTL'inin dışındaysa
//        okunmaz (PriorReadable; ham yolda span saklaması, MV'de 90 gün).
//   R4 — ileriye dönük MV: prior MV'nin ilk kovasından önce başlıyorsa
//        okunmaz (priorSourceCovers).
//
// Yön notu: eksik bir prior (silinmiş ya da hiç yazılmamış) current'ı FAZLA
// gösterir — sahte KÖTÜLEŞME. Canlı kenar ise current'ı EKSİK gösterir —
// sahte iyileşme / gerçek bir artışın yutulması. İkisi zıt yönlü.

import (
	"math"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestPriorCoverageTable(t *testing.T) {
	m := func(h, mi, s int) time.Time { return time.Date(2026, 9, 30, h, mi, s, 0, time.UTC) }
	cases := []struct {
		name          string
		from, to, now time.Time
		want          float64
	}{
		{"geçmiş pencere (son kova çoktan doldu) → 1", m(10, 0, 0), m(11, 0, 0), m(12, 0, 0), 1},
		{"hizalı canlı pencere, to=now kova sınırında → 1", m(10, 0, 0), m(11, 0, 0), m(11, 0, 0), 1},
		// from 10:02:30 → F=10:00, to=now=10:17:30 → N=4 (20 dk), dolu 17,5 dk.
		{"15 dk canlı, kova ortası → 17,5/20", m(10, 2, 30), m(10, 17, 30), m(10, 17, 30), 0.875},
		// 5 dk: from 10:12:30 → F=10:10, N=2 (10 dk), dolu 7,5 dk.
		{"5 dk canlı, kova ortası → 7,5/10", m(10, 12, 30), m(10, 17, 30), m(10, 17, 30), 0.75},
		// Sayfa açık kalmış: to 10:17:30'da donmuş, sunucu saati 10:19 →
		// son kova (10:15–10:20) 4 dk dolu.
		{"donmuş to, saat ilerlemiş → (19−0)/20", m(10, 2, 30), m(10, 17, 30), m(10, 19, 0), 0.95},
		{"donmuş to, son kova artık tam → 1", m(10, 2, 30), m(10, 17, 30), m(10, 21, 0), 1},
		{"boş pencere → 1 (prior zaten yok)", m(10, 0, 0), m(10, 0, 0), m(10, 0, 0), 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PriorCoverage(c.from, c.to, c.now)
			if math.Abs(got-c.want) > 1e-9 {
				t.Errorf("PriorCoverage = %v, beklenen %v", got, c.want)
			}
		})
	}
	// Gelecekteki pencere: dolu süre negatif → asla 0, alt sınır pozitif.
	if got := PriorCoverage(m(11, 0, 0), m(12, 0, 0), m(10, 0, 0)); !(got > 0 && got < 0.01) {
		t.Errorf("tamamen gelecekteki pencere kapsaması %v — (0, küçük] olmalı", got)
	}
}

// TestPriorCoverageProperties — her pencere için 0 < c ≤ 1; son kovası
// tamamlanmış (now ≥ son kovanın sonu) her pencerede TAM 1.
func TestPriorCoverageProperties(t *testing.T) {
	r := rand.New(rand.NewSource(10251))
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5000; i++ {
		from := base.Add(time.Duration(r.Int63n(int64(10 * 24 * time.Hour)))).Add(time.Duration(r.Intn(1e9)))
		to := from.Add(time.Second + time.Duration(r.Int63n(int64(6*time.Hour))))
		now := to.Add(time.Duration(r.Int63n(int64(20*time.Minute))) - 10*time.Minute)
		c := PriorCoverage(from, to, now)
		if !(c > 0 && c <= 1) {
			t.Fatalf("PriorCoverage(%v, %v, %v) = %v — (0, 1] dışında", from, to, now, c)
		}
		pFrom, pTo := PriorWindow(from, to)
		end5 := pTo.Add(pTo.Sub(pFrom))
		if !now.Before(end5) && c != 1 {
			t.Fatalf("son kova tamamlanmış (now=%v ≥ %v) ama kapsama %v", now, end5, c)
		}
	}
}

// TestLiveEdgeScalingRemovesBias — R1'in kendisi: düz iş yükü, canlı
// pencere (to = now), now'ın kova içindeki her evresi (1 sn adım). Ölçeksiz
// prior sayaç deltasını AŞAĞI çekiyor; ölçekli prior current'la en fazla 1
// span farkla eşleşiyor ve ortalama yanlılık ~0.
//
// Model: 10 saniyede bir span, aralığın ORTASINDA (+5 sn). Span'leri
// dakikanın başına koymak current'ı ortalamada yarım span YUKARI yuvarlar
// (sayım ızgarası ile pencere kenarı aynı fazda) — 5 dk pencerede bu tek
// başına +%7'lik sahte bir "yanlılık" demekti; yöntemin değil modelin izi.
func TestLiveEdgeScalingRemovesBias(t *testing.T) {
	const step = 10 * time.Second
	spansIn := func(lo, hi time.Time) uint64 {
		var n uint64
		for t0 := lo.Truncate(step); t0.Before(hi); t0 = t0.Add(step) {
			ts := t0.Add(step / 2)
			if !ts.Before(lo) && ts.Before(hi) {
				n++
			}
		}
		return n
	}
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	for _, win := range []time.Duration{5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour} {
		var rawBias, scaledBias float64
		samples := 0
		for phase := time.Second; phase < 5*time.Minute; phase += time.Second {
			now := base.Add(2*time.Hour + phase)
			from, to := now.Add(-win), now
			start := alignBucketStart(from)
			// Current okuma: start ≤ kova etiketi < to; veri ancak now'a kadar var.
			cur := spansIn(start, now)
			pFrom, pTo := PriorWindow(from, to)
			prior := spansIn(pFrom, pTo)
			scaled := ScalePriorCount(prior, PriorCoverage(from, to, now))
			if d := int64(scaled) - int64(cur); d < -1 || d > 1 {
				t.Fatalf("pencere %v evre %v: current %d, ölçekli prior %d (ham %d) — fark > 1",
					win, phase, cur, scaled, prior)
			}
			rawBias += float64(cur)/float64(prior) - 1
			scaledBias += float64(cur)/float64(scaled) - 1
			samples++
		}
		rawBias /= float64(samples)
		scaledBias /= float64(samples)
		t.Logf("pencere %v: ortalama sayaç yanlılığı ölçeksiz %+.1f%%, ölçekli %+.1f%%", win, rawBias*100, scaledBias*100)
		if math.Abs(scaledBias) > 0.02 {
			t.Errorf("pencere %v: ölçekli ortalama yanlılık %.3f — ~0 olmalı", win, scaledBias)
		}
		if win == 5*time.Minute && rawBias > -0.15 {
			t.Errorf("5 dk: ölçeksiz yanlılık %.3f — kusurun (≈ −%%25) modeli kaymış", rawBias)
		}
	}
}

func TestRawPriorCoverage(t *testing.T) {
	m := func(h, mi int) time.Time { return time.Date(2026, 9, 30, h, mi, 0, 0, time.UTC) }
	if got := RawPriorCoverage(m(10, 0), m(11, 0), m(11, 0)); got != 1 {
		t.Errorf("hazır aralık (to = now) ham yolda %v, beklenen 1 (pencere birebir)", got)
	}
	if got := RawPriorCoverage(m(10, 0), m(11, 0), m(12, 0)); got != 1 {
		t.Errorf("geçmiş pencere %v, beklenen 1", got)
	}
	if got := RawPriorCoverage(m(10, 0), m(11, 0), m(10, 30)); got != 0.5 {
		t.Errorf("geleceğe uzanan pencere yarı dolu: %v, beklenen 0.5", got)
	}
	if got := RawPriorCoverage(m(11, 0), m(12, 0), m(10, 0)); !(got > 0 && got < 0.01) {
		t.Errorf("tamamen gelecekteki pencere %v — (0, küçük] olmalı", got)
	}
}

func TestScalePriorCount(t *testing.T) {
	cases := []struct {
		n     uint64
		scale float64
		want  uint64
	}{
		{20, 1, 20},     // ölçek yok
		{20, 0.875, 18}, // 17,5 → 18 (en yakın, yarım yukarı)
		{80, 0.875, 70}, // tam
		{0, 0.5, 0},     // ölçülmüş sıfır sıfır kalır
		{3, 0.1, 1},     // 0,3 → TABAN 1: sıfır olmayan prior asla "önce 0" olmaz
		{1, 0.017, 1},   // tek kovalık çok kısa canlı pencere
		{10, 1.5, 10},   // geçersiz ölçek → dokunma
		{10, 0, 10},     // geçersiz ölçek → dokunma
		{10, math.NaN(), 10},
	}
	for _, c := range cases {
		if got := ScalePriorCount(c.n, c.scale); got != c.want {
			t.Errorf("ScalePriorCount(%d, %v) = %d, beklenen %d", c.n, c.scale, got, c.want)
		}
	}
}

// R3 — saklama ufku. Ham yol (env süzgeci) span saklamasını, MV yolu 90
// günü kullanır; 0 = bilinmiyor → okunmaz.
func TestPriorReadableHorizon(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	cases := []struct {
		name       string
		pFrom, pTo time.Time
		horizon    int
		want       bool
	}{
		// env + 7d hazır aralığı + Compare: prior [now−14g, now−7g], span
		// saklaması 7 gün → büyük kısmı silinmiş. Eskiden Calls ↑%500.
		{"ham yol: 7 günlük saklama, 7d pencerenin prior'u → okunmaz", now.Add(-14 * day), now.Add(-7 * day), 7, false},
		{"ham yol: 7 günlük saklama, 1 sa pencere → okunur", now.Add(-2 * time.Hour), now.Add(-time.Hour), 7, true},
		{"ham yol: tam 6 gün önce → sınır dahil", now.Add(-6 * day), now.Add(-5 * day), 7, true},
		{"ham yol: 6 günden bir saniye eski → okunmaz (TTL günü kayabilir)", now.Add(-6*day - time.Second), now.Add(-5 * day), 7, false},
		{"MV yolu: 60 günlük özel aralığın prior'u (120 g) → okunmaz", now.Add(-120 * day), now.Add(-60 * day), 90, false},
		{"MV yolu: 30 günlük aralığın prior'u (60 g) → okunur", now.Add(-60 * day), now.Add(-30 * day), 90, true},
		{"ufuk bilinmiyor (0) → okunmaz", now.Add(-2 * time.Hour), now.Add(-time.Hour), 0, false},
		{"boş prior → okunmaz", now.Add(-time.Hour), now.Add(-time.Hour), 90, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PriorReadable(c.pFrom, c.pTo, now, c.horizon); got != c.want {
				t.Errorf("PriorReadable = %v, beklenen %v", got, c.want)
			}
		})
	}
	// dbPriorReadable PriorReadable'ın db MV ufkuyla bağlanmış hâli.
	if dbPriorReadable(now.Add(-120*day), now.Add(-60*day), now) != PriorReadable(now.Add(-120*day), now.Add(-60*day), now, dbMVHorizonDays) {
		t.Error("dbPriorReadable PriorReadable'dan ayrışmış")
	}
}

// R4 — ileriye dönük MV: ilk kova pFrom'dan KESİNLİKLE önce olmalı.
func TestPriorSourceCovers(t *testing.T) {
	pFrom := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		first time.Time
		ok    bool
		want  bool
	}{
		{"MV prior'dan çok önce dolmaya başlamış → kapsar", pFrom.Add(-72 * time.Hour), true, true},
		// Taze kurulum / temizlik / göç: MV 09:30'da başladı, prior 09:00'da.
		{"MV prior'un ORTASINDA başlamış → kapsamaz (eksik prior = sahte kötüleşme)", pFrom.Add(30 * time.Minute), true, false},
		{"ilk kova TAM pFrom → kapsamaz (ilk kova yarım olabilir)", pFrom, true, false},
		{"ilk kova pFrom'dan bir kova önce → kapsar", pFrom.Add(-5 * time.Minute), true, true},
		{"boş tablo / probe hatası → kapsamaz", pFrom.Add(-72 * time.Hour), false, false},
		{"sıfır zaman → kapsamaz", time.Time{}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := priorSourceCovers(c.first, c.ok, pFrom); got != c.want {
				t.Errorf("priorSourceCovers = %v, beklenen %v", got, c.want)
			}
		})
	}
}

// msgMVHorizonDays elle yazılmış bir 90 — iki messaging MV'sinin CREATE
// TTL'inden ayrışmasın (db_horizon_test.go'daki db MV pininin ikizi).
func TestMsgMVHorizonConstMatchesTheCreateTTL(t *testing.T) {
	b, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	ttlRe := regexp.MustCompile(`TTL toDate\(time_bucket\) \+ INTERVAL (\d+) DAY`)
	found := 0
	for _, mv := range []string{"messaging_summary_5m", "messaging_caller_summary_5m"} {
		i := strings.Index(src, "CREATE MATERIALIZED VIEW IF NOT EXISTS "+mv+"\n")
		if i < 0 {
			t.Errorf("%s CREATE'i bulunamadı", mv)
			continue
		}
		win := src[i:]
		if len(win) > 800 {
			win = win[:800]
		}
		mm := ttlRe.FindStringSubmatch(win)
		if mm == nil {
			t.Errorf("%s CREATE'inde TTL yok", mv)
			continue
		}
		found++
		if mm[1] != "90" || msgMVHorizonDays != 90 {
			t.Errorf("%s TTL'i %s gün, msgMVHorizonDays %d — prior kapısı yanlış ufukla çalışır", mv, mm[1], msgMVHorizonDays)
		}
	}
	if found != 2 {
		t.Fatalf("yalnız %d messaging MV TTL'i okunabildi — tarama bozulmuş", found)
	}
}

// Probe SQL'lerinin şekli: MV probları EnvSummaryCovers deseninde
// (`min(time_bucket), count()` — boş tabloda min() epoch döner, count()
// ayırt eder) ve süre tavanlı; spans probu ZAMAN-SINIRLI + LIMIT (sert
// kısıt) — sınırsız min(time) milyar satırda tarama olurdu.
func TestPriorProbeSQLShape(t *testing.T) {
	for src, q := range priorSourceProbeSQL {
		if !strings.HasPrefix(q, "SELECT min(time_bucket), count() FROM ") || !strings.Contains(q, "max_execution_time") {
			t.Errorf("kaynak %d probu desen dışı: %q", src, q)
		}
	}
	for _, want := range []string{"FROM spans", "WHERE time >= ? AND time < ?", "LIMIT 1", "max_execution_time = 5"} {
		if !strings.Contains(spansLiveBeforeSQL, want) {
			t.Errorf("spans probu %q içermiyor", want)
		}
	}
	if strings.Contains(spansLiveBeforeSQL, "min(time)") {
		t.Error("spans probu sınırsız min(time) taraması yapıyor")
	}
}
