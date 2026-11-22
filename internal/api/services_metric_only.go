package api

// services_metric_only.go — v0.10.2451 (operatör onaylı kuyruk maddesi):
// YALNIZ VictoriaMetrics'te görünen servisler (uygulama histogramı /
// spanmetrics connector; upstream tail sampling penceredeki tüm trace'lerini
// düşürmüş ya da servis hiç trace'lenmiyor) Services listesinde ve CoSRE
// servis çözümünde görünür.
//
// KAYNAK: /api/services-metric-rank'in zaten okuduğu 60 sn önbellekli filo
// sıralaması (servicesMetricRankingCached; aile başına TEK anlık sorgu, servis
// seçicisi yok). Yeni sorgu YOK, satır başına sorgu YOK. Kimlik: VM servis
// etiketi = OTel service.name = servis varlığının adı (aynı varlık; ad
// eşleştirmesi değil, aynı anahtar).
//
// LİSTE: sıralı yolda (İstek / Hata % / Avg / P99, pencere ≤ 24 sa, VM kaynak)
// evrene (span servisleri) eklenir; süzgeçler: ad alt-dizesi (büyük/küçük harf
// duyarsız — CH positionCaseInsensitive ile aynı), takım / namespace izin
// listesi, env/cluster (filo sorgusunun kapsamı), tesisat rotası dışlaması
// (filo sorgusu). errorsOnly / minSpans / minP99 SPAN süzgeçleridir —
// açıkken metrik-yalnız servis eklenmez (span değeri yok, süzgeç
// değerlendirilemez). Toplam ≤ servicesMetricRankUniverseMax.

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// servicesMetricOnlyFilter — metrik-yalnız satırlara uygulanan liste süzgeçleri.
type servicesMetricOnlyFilter struct {
	NameMatch   string          // alt-dize, büyük/küçük harf duyarsız; "" = hepsi
	Allow       map[string]bool // takım / namespace izin listesi; nil = süzgeç yok
	SpanDisplay bool            // errorsOnly / minSpans / minP99 açık → metrik-yalnız yok
}

// servicesMetricOnlyRows — SAF: filo sıralamasında olup evrende (span) olmayan
// servisler → satır (MetricOnly, span alanları sıfır). Sıra: istek azalan,
// eşitlikte ad (kararlı; merge'de evrenin ARKASINA eklenir).
func servicesMetricOnlyRows(universe []chstore.ServiceSummary, rank map[string]serviceMetricRankEntry, f servicesMetricOnlyFilter) []chstore.ServiceSummary {
	if f.SpanDisplay || len(rank) == 0 {
		return nil
	}
	inUni := make(map[string]bool, len(universe))
	for _, s := range universe {
		inUni[s.Name] = true
	}
	needle := strings.ToLower(strings.TrimSpace(f.NameMatch))
	type cand struct {
		name string
		req  float64
	}
	var cs []cand
	for name, e := range rank {
		if name == "" || inUni[name] || e.Requests <= 0 {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(name), needle) {
			continue
		}
		if f.Allow != nil && !f.Allow[name] {
			continue
		}
		cs = append(cs, cand{name, e.Requests})
	}
	sort.Slice(cs, func(a, b int) bool {
		if cs[a].req != cs[b].req {
			return cs[a].req > cs[b].req
		}
		return cs[a].name < cs[b].name
	})
	out := make([]chstore.ServiceSummary, len(cs))
	for i, c := range cs {
		out[i] = chstore.ServiceSummary{Name: c.name, MetricOnly: true}
	}
	return out
}

// servicesMetricOnlyAllow — izin listesi kümesi (filtered=false → nil).
func servicesMetricOnlyAllow(serviceIn []string, filtered bool) map[string]bool {
	if !filtered {
		return nil
	}
	m := make(map[string]bool, len(serviceIn))
	for _, n := range serviceIn {
		m[n] = true
	}
	return m
}

// servicesWithMetricOnly — SAF: evren + metrik-yalnız satırlar, toplam tavanla.
// Tavanı aşan metrik-yalnız satırlar (istek azalan sırada sonda olanlar)
// düşer; cut = düşen sayısı. Evren yerinde değişmez.
func servicesWithMetricOnly(universe, extra []chstore.ServiceSummary, max int) (out []chstore.ServiceSummary, cut int) {
	room := max - len(universe)
	if room < 0 {
		room = 0
	}
	if len(extra) > room {
		cut = len(extra) - room
		extra = extra[:room]
	}
	out = make([]chstore.ServiceSummary, 0, len(universe)+len(extra))
	out = append(out, universe...)
	return append(out, extra...), cut
}

// metricServiceCatalogueWindow — CoSRE metrik kataloğunun penceresi (canlı;
// önbellek anahtarı "live:24h", Services sayfasının 24 sa görünümüyle ortak).
const metricServiceCatalogueWindow = 24 * time.Hour

// metricServiceCatalogue — CoSRE servis çözümü için VM filo sıralamasındaki
// servis adları (sıralı, ≤ servicesMetricRankUniverseMax). VM kaynak değilse
// ya da okuma hatasında nil (span kataloğu tek başına kalır — eski davranış).
// mcptools.Deps.MetricServiceNames + guidedServiceNames bunu okur.
func (s *Server) metricServiceCatalogue(ctx context.Context) []string {
	vm, ok := s.metricSource().(vmMetricSource)
	if !ok {
		return nil
	}
	to := time.Now()
	r, _, err := s.servicesMetricRankingCached(ctx, vm, "spanCount", "", "", to.Add(-metricServiceCatalogueWindow), to, false)
	if err != nil {
		return nil
	}
	return metricRankNames(r.Services, servicesMetricRankUniverseMax)
}

// metricRankNames — SAF: istek veren servis adları, sıralı, en çok max.
func metricRankNames(rank map[string]serviceMetricRankEntry, max int) []string {
	names := make([]string, 0, len(rank))
	for n, e := range rank {
		if n != "" && e.Requests > 0 {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) > max {
		names = names[:max]
	}
	return names
}
