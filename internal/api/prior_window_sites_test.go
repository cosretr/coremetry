package api

// prior_window_sites_test.go — v0.10.1028 regresyon testleri.
//
// v0.10.1025 /databases ve /messaging'de bulduğu sınıf: "önceki pencere"
// `[from − dur, from)` diye kuruluyor, current okuma ise alt sınırı MV
// kovasına indiriyordu (`time_bucket >= floor5(from)`). Hizasız from'da
// current'ın ilk kovası İKİ pencereye giriyor, delta sıfıra doğru
// sulanıyordu. Bu dosya aynı biçimi taşıyan kalan yüzeyleri sınar:
//
//	/services MV yolu          servicesPriorWindow         (api.go)
//	/databases/statements/…    chstore.PriorWindow         (dbstmt_detail.go)
//	AI analiz baseline'ı       chstore.PriorWindow         (copilot_aianalyze.go)
//	deploy raporu + Rollouts   redComparisonPlan           (deployment_report.go, rollout_detail.go)
//	/endpoints                 chstore.EndpointsPriorWindow (api.go)
//	/topology/service + graf   chstore.TopologyPriorWindow  (topology.go, servicegraph.go)
//
// İlk dördü düz 5 dk okuyucu (`>= alignBucketStart(from) AND < to`); son
// ikisinin okuyucuya özgü modeli ve testleri chstore/prior_window_sites_test.go'da.
// Burada: türetimin tablosu (hizasız / hizalı / saniye-altı / kısa) +
// handler'ın paylaşılan fonksiyonu çağırdığını söyleyen kaynak pini.

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// mv5Read — düz 5 dk okuyucunun yüklemi: `time_bucket >= floor5(lo) AND
// time_bucket < sec(hi)` (clickhouse-go time.Time'ı saniyeyle bağlar).
func mv5Read(lo, hi time.Time) []time.Time {
	end := hi.Truncate(time.Second)
	var out []time.Time
	for b := lo.Truncate(5 * time.Minute); b.Before(end); b = b.Add(5 * time.Minute) {
		out = append(out, b)
	}
	return out
}

func mv5Shared(a, b []time.Time) int {
	seen := map[int64]bool{}
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

// mv5Windows — dört düz-okuyucu yüzeyinin ortak tablosu. oldShared: eski
// `[from − dur, from)` formülünün current'la paylaştığı kova sayısı
// (kusurun belgesi).
var mv5Windows = []struct {
	name      string
	from, to  time.Time
	oldShared int
}{
	{"hizasız from — BUG BUYDU", time.Date(2026, 10, 2, 10, 3, 0, 0, time.UTC), time.Date(2026, 10, 2, 10, 18, 0, 0, time.UTC), 1},
	{"hizasız from ve to, 1 saat", time.Date(2026, 10, 2, 10, 2, 17, 0, time.UTC), time.Date(2026, 10, 2, 11, 2, 17, 0, time.UTC), 1},
	{"hizalı 1 saat", time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC), time.Date(2026, 10, 2, 11, 0, 0, 0, time.UTC), 0},
	{"saniye-altı uçlar", time.Date(2026, 10, 2, 10, 4, 59, 900_000_000, time.UTC), time.Date(2026, 10, 2, 10, 35, 0, 300_000_000, time.UTC), 1},
	{"5 dk'dan kısa", time.Date(2026, 10, 2, 10, 6, 0, 0, time.UTC), time.Date(2026, 10, 2, 10, 8, 30, 0, time.UTC), 1},
}

func assertMV5Prior(t *testing.T, from, to, pFrom, pTo time.Time, oldShared int) {
	t.Helper()
	cur := mv5Read(from, to)
	prior := mv5Read(pFrom, pTo)
	if n := mv5Shared(cur, prior); n != 0 {
		t.Fatalf("ORTAK KOVA: %d kova hem current hem prior (from=%v to=%v prior=[%v, %v))", n, from, to, pFrom, pTo)
	}
	if len(cur) != len(prior) {
		t.Fatalf("kova sayısı eşit değil: current %d, prior %d — sayaç deltası sahte", len(cur), len(prior))
	}
	if n := mv5Shared(cur, mv5Read(from.Add(-to.Sub(from)), from)); n != oldShared {
		t.Errorf("eski formülün ortak kova sayısı %d, beklenen %d — tablo kusuru belgelemiyor", n, oldShared)
	}
}

// TestServicesPriorWindow — /services: MV yolu PriorWindow, ham yol birebir
// süre kaydırması (handler'daki aynı useMV kararı).
func TestServicesPriorWindow(t *testing.T) {
	for _, c := range mv5Windows {
		t.Run("MV/"+c.name, func(t *testing.T) {
			pFrom, pTo := servicesPriorWindow(c.from, c.to, true)
			wFrom, wTo := chstore.PriorWindow(c.from, c.to)
			if !pFrom.Equal(wFrom) || !pTo.Equal(wTo) {
				t.Fatalf("[%v, %v) ≠ PriorWindow [%v, %v)", pFrom, pTo, wFrom, wTo)
			}
			assertMV5Prior(t, c.from, c.to, pFrom, pTo, c.oldShared)
		})
		t.Run("ham/"+c.name, func(t *testing.T) {
			pFrom, pTo := servicesPriorWindow(c.from, c.to, false)
			if !pTo.Equal(c.from) || pTo.Sub(pFrom) != c.to.Sub(c.from) {
				t.Fatalf("ham prior [%v, %v) — beklenen [from − %v, from)", pFrom, pTo, c.to.Sub(c.from))
			}
		})
	}
}

// TestDBStmtDetailAndAIBaselinePriorWindow — dbstmt detayı ve AI analiz
// baseline'ı satır içinde chstore.PriorWindow çağırıyor; ikisinin okuyucusu
// da düz 5 dk (dbStmtDetailWhere: From.Truncate(5m) / ServiceWindowRED:
// alignBucketStart). Gerçek builder'la sınama: chstore
// TestDBStmtDetailPriorWindowRealBuilder.
func TestDBStmtDetailAndAIBaselinePriorWindow(t *testing.T) {
	for _, c := range mv5Windows {
		t.Run(c.name, func(t *testing.T) {
			pFrom, pTo := chstore.PriorWindow(c.from, c.to)
			assertMV5Prior(t, c.from, c.to, pFrom, pTo, c.oldShared)
		})
	}
}

// TestRedComparisonPlanNoSharedBucket — deploy raporu + Rollouts çekmecesi:
// before/after iki service_summary_5m okuması. Deploy anı neredeyse hiç
// kova sınırına oturmaz; eski before floor5(since) kovasını after'la
// paylaşıyordu.
func TestRedComparisonPlanNoSharedBucket(t *testing.T) {
	for _, c := range mv5Windows {
		t.Run(c.name, func(t *testing.T) {
			p := redComparisonPlan(c.from.UnixNano(), c.to.UnixNano(), c.to.UnixNano())
			if !p.AfterFrom.Equal(c.from) || !p.AfterTo.Equal(c.to) {
				t.Fatalf("after [%v, %v] değişti — current okuma DEĞİŞMEMELİ", p.AfterFrom, p.AfterTo)
			}
			assertMV5Prior(t, p.AfterFrom, p.AfterTo, p.BeforeFrom, p.BeforeTo, c.oldShared)
		})
	}
	// Bir deploy 40 sn önce, 10:03:17'de: after yalnız 10:00 kovası; before
	// onun hemen öncesindeki TEK kova (09:55), 10:00 değil.
	since := time.Date(2026, 10, 2, 10, 3, 17, 0, time.UTC)
	now := since.Add(40 * time.Second)
	p := redComparisonPlan(since.UnixNano(), now.UnixNano(), now.UnixNano())
	if want := time.Date(2026, 10, 2, 9, 55, 0, 0, time.UTC); !p.BeforeFrom.Equal(want) || !p.BeforeTo.Equal(want.Add(5*time.Minute)) {
		t.Errorf("taze deploy before [%v, %v), beklenen [09:55, 10:00)", p.BeforeFrom, p.BeforeTo)
	}
}

// flatLoadCount — DÜZ YÜK modeli: since'ten çok önce başlayıp now'da biten,
// saniyede 1 birimlik sürekli trafik (span zamanları nanosaniye; sürekli
// model gerçeğe en yakın soyutlama). Okuyucunun kovalamasıyla sayılır:
// `time_bucket >= floor5(lo) AND time_bucket < sec(hi)`, kova L'nin içeriği
// [L, min(L + 5 dk, now)) — canlı son kova now'a kadar doludur.
func flatLoadCount(lo, hi, now time.Time) float64 {
	end := hi.Truncate(time.Second)
	var sum time.Duration
	for b := lo.Truncate(5 * time.Minute); b.Before(end); b = b.Add(5 * time.Minute) {
		top := b.Add(5 * time.Minute)
		if now.Before(top) {
			top = now
		}
		if top.After(b) {
			sum += top.Sub(b)
		}
	}
	return sum.Seconds()
}

// TestRedComparisonPlanFlatLoad — v0.10.1028 (inceleme R1). Düz yükte
// önce/sonra throughput HER AN eşit olmalı (delta %0): sayı ÷ o tarafın
// gerçekten kapsadığı süre. İki çağıranın da kelepçesiyle koşar: rapor
// kelepçesiz (end = now), çekmece rolloutCompareEnd (6 sa).
//
// Eski paydalarla (iki tarafta `end − since`; çekmecede before için pencere
// boyu) "2 dk sonra" satırı 100 rps düz yükte "250 → 264" ya da v0.10.1028'in
// ilk hâlinde "100 → 264" (+%164) basıyordu; bu test o satırı da yazar.
func TestRedComparisonPlanFlatLoad(t *testing.T) {
	at := func(h, m, s, ns int) time.Time { return time.Date(2026, 10, 2, h, m, s, ns, time.UTC) }
	unaligned := at(10, 3, 17, 0)
	cases := []struct {
		name       string
		since, now time.Time
	}{
		{"hizasız rollout, 30 sn sonra", unaligned, unaligned.Add(30 * time.Second)},
		{"hizasız rollout, 2 dk sonra", unaligned, unaligned.Add(2 * time.Minute)},
		{"hizasız rollout, 10 dk sonra", unaligned, unaligned.Add(10 * time.Minute)},
		{"hizasız rollout, 1 sa sonra", unaligned, unaligned.Add(time.Hour)},
		{"hizasız rollout, 7 sa sonra (çekmece 6 sa kelepçesi)", unaligned, unaligned.Add(7 * time.Hour)},
		{"hizasız rollout, 6 sa 1 dk sonra (kelepçe + canlı kenar)", unaligned, unaligned.Add(6*time.Hour + time.Minute)},
		{"hizalı rollout, 2 dk sonra", at(10, 0, 0, 0), at(10, 2, 0, 0)},
		{"saniye-altı now", unaligned, at(10, 5, 17, 400_000_000)},
		{"now yeni kovanın ilk saniyesinde (sürücü 10:05:00'a keser)", unaligned, at(10, 5, 0, 400_000_000)},
	}
	callers := []struct {
		name string
		end  func(since, now time.Time) time.Time
	}{
		{"deploy raporu", func(_, now time.Time) time.Time { return now }},
		{"rollout çekmecesi", func(since, now time.Time) time.Time {
			e, _ := rolloutCompareEnd(since.UnixNano(), now.UnixNano())
			return time.Unix(0, e)
		}},
	}
	const rps = 100.0
	for _, c := range cases {
		for _, k := range callers {
			t.Run(c.name+"/"+k.name, func(t *testing.T) {
				end := k.end(c.since, c.now)
				p := redComparisonPlan(c.since.UnixNano(), end.UnixNano(), c.now.UnixNano())
				before := rps * flatLoadCount(p.BeforeFrom, p.BeforeTo, c.now) / p.BeforeSec
				after := rps * flatLoadCount(p.AfterFrom, p.AfterTo, c.now) / p.AfterSec
				if math.Abs(before-rps) > 1e-9 || math.Abs(after-rps) > 1e-9 {
					t.Fatalf("düz %v rps yükte before %.6f / after %.6f rps (paydalar %v / %v sn) — sahte delta",
						rps, before, after, p.BeforeSec, p.AfterSec)
				}
				if c.name == "hizasız rollout, 2 dk sonra" {
					// Eski paydalar: iki tarafta end − since (rapor) / before
					// pencere boyu (çekmece, v0.10.1028 ilk hâli).
					oldB, oldA := redComparisonOldWindow(c.since, end)
					dur := end.Sub(c.since).Seconds()
					t.Logf("%s — yeni: %.1f → %.1f rps | eski pencere+payda: %.1f → %.1f | ilk 1028 pencere, eski çekmece paydası: %.1f → %.1f",
						k.name, before, after,
						rps*flatLoadCount(oldB[0], oldB[1], c.now)/dur, rps*flatLoadCount(oldA[0], oldA[1], c.now)/dur,
						rps*flatLoadCount(p.BeforeFrom, p.BeforeTo, c.now)/p.BeforeTo.Sub(p.BeforeFrom).Seconds(),
						rps*flatLoadCount(p.AfterFrom, p.AfterTo, c.now)/dur)
				}
			})
		}
	}
}

// redComparisonOldWindow — v0.10.1028 ÖNCESİ pencereler (yalnız belge için):
// before = [since − dur, since], after = [since, end].
func redComparisonOldWindow(since, end time.Time) ([2]time.Time, [2]time.Time) {
	return [2]time.Time{since.Add(-end.Sub(since)), since}, [2]time.Time{since, end}
}

// codeOnlySrc — dosyanın yorum satırları süzülmüş hâli (eski formül
// şerhlerde TARİHÇE olarak anılıyor; pin yalnız kodu sınar).
func codeOnlySrc(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%s okunamadı: %v", file, err)
	}
	var code strings.Builder
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		code.WriteString(line + "\n")
	}
	return code.String()
}

// TestCompareSitesUseSharedPriorWindow — kaynak pini: her yüzey prior'u
// paylaşılan türetimden alıyor; ad-hoc `from.Add(-dur), from` geri gelmesin.
func TestCompareSitesUseSharedPriorWindow(t *testing.T) {
	cases := []struct {
		file   string
		want   []string
		banned []string
	}{
		{"dbstmt_detail.go",
			[]string{"pq.From, pq.To = chstore.PriorWindow(from, to)"},
			[]string{"from.Add(-dur)", "pq.To = from"}},
		{"api.go",
			[]string{
				"prior.From, prior.To = chstore.EndpointsPriorWindow(eq)",
				"pfrom, pto := servicesPriorWindow(from, to, useMV)",
			},
			[]string{"prior.From = from.Add(-dur)", "pfrom, pto := from.Add(-dur), from"}},
		{"topology.go",
			[]string{
				"pFrom, pTo := chstore.TopologyPriorWindow(from, to)",
				"s.store.ReadServiceTopologyAgg(ctx, pFrom, pTo, edgeCap)",
			},
			[]string{"from.Add(-dur)"}},
		{"servicegraph.go",
			[]string{
				"pfrom, pto := chstore.TopologyPriorWindow(from, to)",
				// pfrom/pto okuyucu ARGÜMANI, zaman aralığı değil: prior grafın
				// dakika paydası current'ınki (aynı kova sayısı).
				"pg := buildServiceGraph(pedges, focus, scope, hops, dbNames, serviceGraphWindowMinutes(from, to))",
			},
			[]string{"from.Add(-dur)", "serviceGraphWindowMinutes(pfrom, pto)"}},
		{"copilot_aianalyze.go",
			[]string{
				"bFrom, bTo := chstore.PriorWindow(from, to)",
				"s.store.ServiceWindowRED(ctx, service, bFrom, bTo)",
			},
			[]string{"from.Add(-span), from"}},
		{"deployment_report.go",
			[]string{
				"bFrom, bTo := chstore.PriorWindow(since, end)",
				// İki çağıran da pencereyi VE paydayı ortak plandan alır.
				"plan := redComparisonPlan(sinceNs, nowNs, nowNs)",
				"redStatsFor(beforeBySvc[svc], plan.BeforeSec)",
				"redStatsFor(afterSv, plan.AfterSec)",
			},
			[]string{"since.Add(-dur)", "beforeTo.Sub(beforeFrom).Seconds()", "afterTo.Sub(afterFrom).Seconds()", "beforeWindowSec", "afterWindowSec"}},
		{"rollout_detail.go",
			[]string{
				"endNs, clamped := rolloutCompareEnd(sinceNs, nowNs)",
				"plan := redComparisonPlan(sinceNs, endNs, nowNs)",
				"redStatsFor(beforeBySvc[svc], plan.BeforeSec)",
				"redStatsFor(afterSv, plan.AfterSec)",
			},
			[]string{"beforeTo.Sub(beforeFrom).Seconds()", "afterTo.Sub(afterFrom).Seconds()", "redComparisonWindow("}},
		{"services_delta.go",
			[]string{"return chstore.PriorWindow(from, to)"},
			nil},
	}
	for _, c := range cases {
		src := codeOnlySrc(t, c.file)
		for _, w := range c.want {
			if !strings.Contains(src, w) {
				t.Errorf("%s %q içermiyor", c.file, w)
			}
		}
		for _, b := range c.banned {
			if strings.Contains(src, b) {
				t.Errorf("%s: eski hizasız prior biçimi %q geri gelmiş — ortak kova (v0.10.1028)", c.file, b)
			}
		}
	}
	// /services prior okuması, MV/ham dalını current'ı seçen AYNI useMV'den
	// almalı; ayrışırsa MV okumasına ham prior (ya da tersi) bağlanır.
	src := codeOnlySrc(t, "api.go")
	if i, j := strings.Index(src, "useMV := servicesUseMV("), strings.Index(src, "servicesPriorWindow(from, to, useMV)"); i < 0 || j < 0 || i > j {
		t.Error("servicesPriorWindow handler'daki useMV kararından SONRA ve onunla çağrılmalı")
	}
}
