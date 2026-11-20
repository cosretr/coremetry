package api

// problem_breakdown_core.go — v0.10.2388: "Hata nerede" bölümünün SAF çekirdeği
// (uç ve G/Ç problem_breakdown.go'da; tablo testleri _core_test.go). Burada
// hiçbir okuma yok: hangi Problem / hangi pencere, VM satırlarının katlanması,
// rotaların sırası ve "Diğer N" satırı, sürüm kırılımının ne zaman yazılmadığı,
// exception payları, bağımlılık satırları.

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	// Pencere: açık Problem'de "şimdi" = başlangıçtan beri, en çok son 30 dk;
	// kapanmış Problem'de Problem'in tamamı, en çok son 6 sa. En az 5 dk (yeni
	// açılmış Problem'de bir iki dakikalık sayım oran olarak okunmaz).
	breakdownMinWindow    = 5 * time.Minute
	breakdownOpenWindow   = 30 * time.Minute
	breakdownClosedWindow = 6 * time.Hour
	// breakdownClosedHorizon — "yakın zamanda kapanmış": bundan eski kapanışta
	// bölüm çizilmez (rota tabanı 28 gün, olaylar 30 gün saklanır; bir haftadan
	// eski bir Problem'in "şimdi"si kimsenin sorusu değil).
	breakdownClosedHorizon = 7 * 24 * time.Hour

	breakdownTopRoutes     = 8 // + "Diğer" = en çok 9 satır (statik tablo sınırı 10)
	breakdownTopExceptions = 5
	breakdownTopDeps       = 5
	breakdownTopVersions   = 4
	breakdownMessageMax    = 240 // örnek mesaj rune tavanı
)

// breakdownSubject — SAF: bölüm bu Problem için anlamlı mı. Servis özneli hata
// oranı Problem'i (ML servis kuralı ya da kural tabanlı); başka her tür
// Problem'in sayfası değişmez.
func breakdownSubject(p chstore.Problem) bool {
	return p.Service != "" && p.Metric == "error_rate" && chstore.ProblemSubjectKind(p.Kind) == chstore.ProblemKindService
}

// breakdownWin — kırılım penceresi (dakikaya hizalı).
type breakdownWin struct {
	From, To time.Time
	Open     bool
}

func (w breakdownWin) minutes() float64 { return w.To.Sub(w.From).Minutes() }
func (w breakdownWin) seconds() int     { return int(w.To.Sub(w.From) / time.Second) }

// breakdownWindow — SAF: Problem + şimdi → pencere. lag = metrik deposunun
// yazma gecikmesi (ML ayarı ingestLagSec): pencere, deponun TAM yazdığı son
// dakikada biter — eksik dakika oranı düşük gösterirdi. ok=false: başlangıç
// yok ya da kapanış ufkun dışında.
func breakdownWindow(p chstore.Problem, now time.Time, lag time.Duration) (breakdownWin, bool) {
	if p.StartedAt <= 0 {
		return breakdownWin{}, false
	}
	if lag < 0 {
		lag = 0
	}
	start := time.Unix(0, p.StartedAt).Truncate(time.Minute)
	end := now.Add(-lag).Truncate(time.Minute)
	w := breakdownWin{Open: p.Status != "resolved" || p.ResolvedAt == nil}
	maxSpan := breakdownOpenWindow
	if !w.Open {
		res := time.Unix(0, *p.ResolvedAt)
		if now.Sub(res) > breakdownClosedHorizon {
			return breakdownWin{}, false
		}
		maxSpan = breakdownClosedWindow
		e := res.Truncate(time.Minute)
		if e.Before(res) {
			e = e.Add(time.Minute) // kapanış dakikasının sonu (yukarı hizalı)
		}
		if e.Before(end) {
			end = e
		}
	}
	span := end.Sub(start)
	if span < breakdownMinWindow {
		span = breakdownMinWindow
	}
	if span > maxSpan {
		span = maxSpan
	}
	w.From, w.To = end.Add(-span), end
	return w, true
}

// problemBreakdownKey — SAF: önbellek anahtarı. Ucun TEK girdisi kimliktir
// (pencere Problem'in kendi alanlarından ve sunucu saatinden; kardeş /events,
// /graph duruşu). Sorgu parametresi yok.
func problemBreakdownKey(id string) string { return "problem:breakdown:v1:" + id }

// ── VM satırları ────────────────────────────────────────────────────────

// breakdownCounts — penceredeki istek ve başarısız istek sayısı.
type breakdownCounts struct{ Requests, Failed float64 }

// breakdownVMQueries — SAF: ailenin pencere toplamı için İKİ anlık sorgu
// (istek, başarısız istek), `by` etiketlerine göre. increase(…[pencere]) tek
// noktada değerlendirilir: aralık sorgusu yok, bucket serisi yok, topk yok.
// Seçici metricChartSelector'dan (HTTP sayfalarının yazım kuralı: ad adayları
// + kaçışlı servis eşleşmesi).
func breakdownVMQueries(f redFamily, svcLabel, service, statusLabel string, by []string, winSec int) (count, errq string) {
	in := metricChartQueryInput{Variant: metricChartVariant{Probe: f.Probe, Kind: "count", Names: []string{f.names()}}, Service: service, SvcLabel: svcLabel}
	b := strings.Join(by, ", ")
	count = fmt.Sprintf("sum by (%s) (increase(%s[%ds]))", b, metricChartSelector(in, ""), winSec)
	errq = fmt.Sprintf("sum by (%s) (increase(%s[%ds]))", b, metricChartSelector(in, statusLabel+`=~"`+f.ServerErr+`"`), winSec)
	return count, errq
}

// breakdownLabelsOf — SAF: VM grup anahtarı (`k="v"` sıralı) → etiket haritası.
func breakdownLabelsOf(gk []string) map[string]string {
	out := make(map[string]string, len(gk))
	for _, kv := range gk {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if u, err := strconv.Unquote(v); err == nil {
			v = u
		}
		out[k] = v
	}
	return out
}

// foldBreakdownSeries — SAF: bir ailenin iki anlık sonucunu rota ve sürüm
// sayımlarına ekler. Rota etiketi olmayan satır "" anahtarına düşer (toplamın
// parçası; çağıran onu adsız rota olarak gösterir). versionLabel boşsa sürüm
// sayımı tutulmaz. Sonlu / negatif olmayan değer dışındaki satır atılır.
func foldBreakdownSeries(routes, versions map[string]breakdownCounts, count, errs []chstore.SpanMetricSeries, routeLabel, versionLabel string) {
	add := func(rows []chstore.SpanMetricSeries, failed bool) {
		for _, r := range rows {
			if len(r.Points) == 0 {
				continue
			}
			v := r.Points[len(r.Points)-1].Value
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				continue
			}
			lb := breakdownLabelsOf(r.GroupKey)
			put := func(m map[string]breakdownCounts, k string) {
				c := m[k]
				if failed {
					c.Failed += v
				} else {
					c.Requests += v
				}
				m[k] = c
			}
			put(routes, lb[routeLabel])
			if versionLabel != "" {
				put(versions, lb[versionLabel])
			}
		}
	}
	add(count, false)
	add(errs, true)
}

// ── Endpoint'e göre ─────────────────────────────────────────────────────

// breakdownRouteRow — tablo satırı. Oranlar 0–1; dakikalık değerler pencere
// ortalaması.
type breakdownRouteRow struct {
	Route string `json:"route"`
	// Normal — olağan hata oranı (dedektörün mevsimsel tabanı). Yok = bilinmiyor.
	Normal         *float64 `json:"normal,omitempty"`
	Current        float64  `json:"current"`
	FailedPerMin   float64  `json:"failedPerMin"`
	RequestsPerMin float64  `json:"requestsPerMin"`
	// Deviates — şimdiki oran, olağana göre etki kuralının eşiğini aşıyor
	// (renk yalnız bu hücrede). Olağan bilinmiyorsa iddia edilmez.
	Deviates bool `json:"deviates,omitempty"`
}

// breakdownRoutes — sıralı satırlar + "Diğer N endpoint" + pencere toplamı.
type breakdownRoutes struct {
	Rows           []breakdownRouteRow
	Other          *breakdownRouteRow
	OtherCount     int
	FailedPerMin   float64
	RequestsPerMin float64
}

// rankBreakdownRoutes — SAF: rota sayımları + taban → katkıya göre sıralı
// tablo. Sıra: başarısız istek (çoktan aza), eşitse istek, sonra ad. İlk topN
// BAŞARISIZ isteği olan rota adıyla; trafiği olan kalan her rota (hatasızlar
// dahil) "Diğer"de toplanır. Başarısız istek hiç yoksa satır yok (blok
// çizilmez). Diğer'in olağanı, katlanan rotaların trafik ağırlıklı olağanıdır
// ve yalnız HEPSİNİN tabanı biliniyorsa yazılır.
func rankBreakdownRoutes(counts map[string]breakdownCounts, base map[string]chstore.MLRouteBaseline, winMin float64, minSamples, topN int,
	threshold func(float64) float64) breakdownRoutes {
	type rt struct {
		name string
		c    breakdownCounts
	}
	var all []rt
	for name, c := range counts {
		if math.IsNaN(c.Requests) || math.IsNaN(c.Failed) || c.Failed < 0 {
			continue
		}
		if c.Failed > c.Requests {
			c.Requests = c.Failed // sayaç sıfırlanması / hizalama: oran 1'i aşamaz
		}
		if c.Requests <= 0 {
			continue
		}
		all = append(all, rt{name, c})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].c.Failed != all[j].c.Failed {
			return all[i].c.Failed > all[j].c.Failed
		}
		if all[i].c.Requests != all[j].c.Requests {
			return all[i].c.Requests > all[j].c.Requests
		}
		return all[i].name < all[j].name
	})
	var out breakdownRoutes
	if winMin <= 0 || len(all) == 0 || all[0].c.Failed <= 0 {
		return out
	}
	row := func(name string, c breakdownCounts, normal *float64) breakdownRouteRow {
		r := breakdownRouteRow{Route: name, Normal: normal, Current: c.Failed / c.Requests, FailedPerMin: c.Failed / winMin, RequestsPerMin: c.Requests / winMin}
		if normal != nil && threshold != nil {
			r.Deviates = r.Current >= threshold(*normal)
		}
		return r
	}
	normalOf := func(name string) (float64, float64, bool) { // olağan, ağırlık, biliniyor
		b, ok := base[name]
		if !ok {
			return 0, 0, false
		}
		n, known := chstore.RouteNormalErrRate(b, minSamples)
		return n, b.CountMed, known
	}
	var other breakdownCounts
	var wSum, wNormal float64
	otherKnown := true
	for _, r := range all {
		out.FailedPerMin += r.c.Failed / winMin
		out.RequestsPerMin += r.c.Requests / winMin
		n, w, known := normalOf(r.name)
		if len(out.Rows) < topN && r.c.Failed > 0 {
			var np *float64
			if known {
				np = &n
			}
			out.Rows = append(out.Rows, row(r.name, r.c, np))
			continue
		}
		out.OtherCount++
		other.Requests += r.c.Requests
		other.Failed += r.c.Failed
		if !known {
			otherKnown = false
			continue
		}
		wSum += w
		wNormal += w * n
	}
	if out.OtherCount > 0 {
		var np *float64
		if otherKnown && wSum > 0 {
			v := wNormal / wSum
			np = &v
		}
		o := row("", other, np)
		out.Other = &o
	}
	return out
}

// ── Sürüme göre ─────────────────────────────────────────────────────────

// breakdownVersionRow — sürümün penceredeki oranı (örneklenmemiş seriden).
type breakdownVersionRow struct {
	Version        string  `json:"version"`
	Current        float64 `json:"current"`
	FailedPerMin   float64 `json:"failedPerMin"`
	RequestsPerMin float64 `json:"requestsPerMin"`
	// Deviates — oran, ÖTEKİ sürümlerin en düşüğüne göre etki eşiğini aşıyor.
	Deviates bool `json:"deviates,omitempty"`
}

// breakdownVersionRows — SAF: sürüm sayımları → satırlar (trafiğe göre, en çok
// topN; more = sığmayan sürüm sayısı). nil: kırılım yazılmaz —
//   - trafiği olan sürüm ikiden azsa (kıyas yok),
//   - sürüm etiketi TAŞIMAYAN trafik varsa (kırılım eksik; payda bilinmeden
//     sürüm oranı savunulamaz).
func breakdownVersionRows(counts map[string]breakdownCounts, winMin float64, topN int, threshold func(float64) float64) ([]breakdownVersionRow, int) {
	if winMin <= 0 || counts[""].Requests > 0 || counts[""].Failed > 0 {
		return nil, 0
	}
	var rows []breakdownVersionRow
	for v, c := range counts {
		if v == "" || math.IsNaN(c.Requests) || math.IsNaN(c.Failed) || c.Requests <= 0 || c.Failed < 0 {
			continue
		}
		f := math.Min(c.Failed, c.Requests)
		rows = append(rows, breakdownVersionRow{Version: v, Current: f / c.Requests, FailedPerMin: f / winMin, RequestsPerMin: c.Requests / winMin})
	}
	if len(rows) < 2 {
		return nil, 0
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].RequestsPerMin != rows[j].RequestsPerMin {
			return rows[i].RequestsPerMin > rows[j].RequestsPerMin
		}
		return rows[i].Version < rows[j].Version
	})
	if threshold != nil {
		for i := range rows {
			low := math.Inf(1)
			for j := range rows {
				if j != i && rows[j].Current < low {
					low = rows[j].Current
				}
			}
			rows[i].Deviates = rows[i].Current >= threshold(low)
		}
	}
	more := 0
	if len(rows) > topN {
		more = len(rows) - topN
		rows = rows[:topN]
	}
	return rows, more
}

// ── Exception'a göre ────────────────────────────────────────────────────

type breakdownExceptionRow struct {
	Type  string `json:"type"`
	Count uint64 `json:"count"`
	// Share — penceredeki exception kayıtları içindeki pay (0–1). Toplam
	// bilinmiyorsa (tür tavanı doldu) yok.
	Share *float64 `json:"share,omitempty"`
	// Message / Fingerprint — türün en yeni exception grubu (varsa): en yeni
	// örnek mesaj + grup sayfası. Groups: bu türde görülen grup sayısı.
	Message     string `json:"message,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Groups      uint64 `json:"groups,omitempty"`
}

type breakdownExceptions struct {
	Failed bool                    `json:"failed,omitempty"` // okunamadı
	Rows   []breakdownExceptionRow `json:"rows"`
	// Total — penceredeki toplam kayıt; Capped ise bilinmiyor (0).
	Total      uint64   `json:"total,omitempty"`
	Capped     bool     `json:"capped,omitempty"`
	OtherTypes int      `json:"otherTypes,omitempty"`
	OtherCount uint64   `json:"otherCount,omitempty"`
	OtherShare *float64 `json:"otherShare,omitempty"`
}

// buildBreakdownExceptions — SAF: tür sayımları (çoktan aza) + tür → en yeni
// grup → blok. Pay = sayı / toplam; tür tavanı dolduysa (capped) toplam eksik
// olduğundan pay YAZILMAZ, sayılar kalır. Kayıt yoksa nil (blok çizilmez).
func buildBreakdownExceptions(types []chstore.ExceptionTypeCount, capped bool, groups map[string]chstore.ExceptionTypeGroup, topN int) *breakdownExceptions {
	var total uint64
	var rows []chstore.ExceptionTypeCount
	for _, t := range types {
		if t.Count == 0 {
			continue
		}
		total += t.Count
		rows = append(rows, t)
	}
	if total == 0 {
		return nil
	}
	share := func(n uint64) *float64 {
		if capped {
			return nil
		}
		v := float64(n) / float64(total)
		return &v
	}
	out := &breakdownExceptions{Rows: []breakdownExceptionRow{}, Capped: capped}
	if !capped {
		out.Total = total
	}
	for i, t := range rows {
		if i >= topN {
			out.OtherTypes++
			out.OtherCount += t.Count
			continue
		}
		r := breakdownExceptionRow{Type: t.Type, Count: t.Count, Share: share(t.Count)}
		if g, ok := groups[t.Type]; ok && g.Fingerprint != "" {
			r.Fingerprint, r.Groups = g.Fingerprint, g.Groups
			r.Message = truncRunes(strings.TrimSpace(g.Message), breakdownMessageMax)
		}
		out.Rows = append(out.Rows, r)
	}
	if out.OtherTypes > 0 {
		out.OtherShare = share(out.OtherCount)
	}
	return out
}

// breakdownExceptionTypes — SAF: grup araması yapılacak türler (ilk topN).
func breakdownExceptionTypes(types []chstore.ExceptionTypeCount, topN int) []string {
	var out []string
	for _, t := range types {
		if t.Count == 0 {
			continue
		}
		if len(out) >= topN {
			break
		}
		out = append(out, t.Type)
	}
	return out
}

// ── Bağımlılığa göre ────────────────────────────────────────────────────

const (
	breakdownDepErrorRate = "error_rate"
	breakdownDepLatency   = "latency"
)

type breakdownDependencyRow struct {
	EventID string `json:"eventId"` // ML olayı (ayrıntı sayfası)
	Family  string `json:"family"`  // http | db | messaging
	Target  string `json:"target"`
	Measure string `json:"measure"` // error_rate (oran 0–1) | latency (p95, ms)
	// Baseline / Observed — olayın kendi değerleri (ölçünün biriminde).
	Baseline  float64 `json:"baseline"`
	Observed  float64 `json:"observed"`
	StartedAt int64   `json:"startedAt"` // unix ns
	LastSeen  int64   `json:"lastSeen"`  // unix ns
	Live      bool    `json:"live"`
}

type breakdownDependencies struct {
	Failed bool                     `json:"failed,omitempty"` // okunamadı
	Rows   []breakdownDependencyRow `json:"rows"`
	More   int                      `json:"more,omitempty"`
}

// buildBreakdownDependencies — SAF: bağımlılık olayları (en yeni önce) →
// satırlar. Aynı (ölçü, hedef) için en yeni olay; gözlenen ≤ taban olan ve
// kodu çözülemeyen olay atılır. Sıra: hata oranı satırları önce, sonra
// gecikme; her birinde erken başlayan önce (zamanda en önce bozulan = en olası
// köken ADAYI — neden iddiası değil). Olay yoksa nil (blok çizilmez).
func buildBreakdownDependencies(evs []chstore.DependencyEvent, nowNs int64, topN int) *breakdownDependencies {
	seen := map[string]bool{}
	var rows []breakdownDependencyRow
	for _, e := range evs {
		fam, target := chstore.DependencyTargetOf(e.Code)
		if fam == "" || !(e.Observed > e.Baseline) || math.IsInf(e.Observed, 0) {
			continue
		}
		measure := breakdownDepErrorRate
		if e.Detector == chstore.MLDetDependencyLatency {
			measure = breakdownDepLatency
		} else if e.Detector != chstore.MLDetDependencyErrorRate {
			continue
		}
		k := measure + "\x00" + e.Code
		if seen[k] {
			continue
		}
		seen[k] = true
		rows = append(rows, breakdownDependencyRow{EventID: e.ID, Family: fam, Target: target, Measure: measure,
			Baseline: e.Baseline, Observed: e.Observed, StartedAt: e.StartedAt, LastSeen: e.LastSeen,
			Live: chstore.MLEventOpen(chstore.MLEvent{Stage: e.Stage, WindowSec: e.WindowSec, LastSeen: e.LastSeen}, nowNs)})
	}
	if len(rows) == 0 {
		return nil
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if (rows[i].Measure == breakdownDepErrorRate) != (rows[j].Measure == breakdownDepErrorRate) {
			return rows[i].Measure == breakdownDepErrorRate
		}
		if rows[i].StartedAt != rows[j].StartedAt {
			return rows[i].StartedAt < rows[j].StartedAt
		}
		return rows[i].Target < rows[j].Target
	})
	out := &breakdownDependencies{}
	if len(rows) > topN {
		out.More = len(rows) - topN
		rows = rows[:topN]
	}
	out.Rows = rows
	return out
}
