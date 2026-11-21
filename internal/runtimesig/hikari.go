package runtimesig

// hikari.go — follow-up 2026-11-21 (operatör onayı): HikariCP bağlantı
// havuzu doygunluğu. Micrometer hikaricp_connections_* serileri prod'da çoğu
// zaman `application` (Spring uygulama adı) + `host_name` taşır, `service_name`
// TAŞIMAZ — servis süzgeçli katalogda görünmez. Eşleme ad üzerinden DEĞİL,
// varlık katmanından: servis → entity_seen pod'ları (pod adı = host_name) →
// seriler host_name ∈ pod'lar süzgeciyle okunur. Pod listesi API'den gelir
// (runtimePodIndex, ≤ HikariPodsMax); boşsa Hikari okunmaz.
//
// Bütçe: tek katalog okuması (ListMetricNames "hikaricp", ≤ 50 ad) + çözülen
// sinyal başına tek sorgu (≤ 5), pencere 60 dk, adım 60 s.

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// HikariCP sinyal anahtarları (pod düzeyi; havuz etiketi `pool` toplanır).
const (
	SigDBPoolActive   SignalKey = "db_pool_active"   // kullanımdaki bağlantı
	SigDBPoolMax      SignalKey = "db_pool_max"      // havuz tavanı
	SigDBPoolPending  SignalKey = "db_pool_pending"  // bağlantı bekleyen thread
	SigDBAcquireSum   SignalKey = "db_acquire_sum"   // bağlantı alma süresi s/s (rate)
	SigDBAcquireCount SignalKey = "db_acquire_count" // bağlantı alma /s (rate)
)

// CheckDBPoolSaturation — havuz doygunluğu kontrolü.
const CheckDBPoolSaturation = "db-pool-saturation"

const (
	// HikariPodsMax — host_name süzgecindeki pod tavanı (regex boyu sınırlı).
	HikariPodsMax       = 50
	hikariCatalogLimit  = 50
	dbPoolWarnPct       = 90.0
	dbPendingWarn       = 1.0
	dbPendingCrit       = 5.0
	dbAcquireCritMs     = 1000.0
	hikariPodFilterKey  = "resource.host.name"
	hikariCatalogFilter = "hikaricp"
)

// hikariSignals — okuma + dedektör sırası.
var hikariSignals = []SignalKey{SigDBPoolActive, SigDBPoolMax, SigDBPoolPending, SigDBAcquireSum, SigDBAcquireCount}

// hikariMappings — Micrometer HikariCP bağlayıcısının Prometheus adları.
var hikariMappings = map[SignalKey][]Source{
	SigDBPoolActive:   {src(KindGauge, 1, "", "hikaricp_connections_active").withGen("pool")},
	SigDBPoolMax:      {src(KindGauge, 1, "", "hikaricp_connections_max").withGen("pool")},
	SigDBPoolPending:  {src(KindGauge, 1, "", "hikaricp_connections_pending").withGen("pool")},
	SigDBAcquireSum:   {src(KindCounter, 1, "", "hikaricp_connections_acquire_seconds_sum").withGen("pool")},
	SigDBAcquireCount: {src(KindCounter, 1, "", "hikaricp_connections_acquire_seconds_count").withGen("pool")},
}

// ResolveHikari — SAF: Hikari kataloğu → pod kapsamlı çözülmüş sinyaller.
func ResolveHikari(catalog []string) []Resolved {
	have := make(map[string]bool, len(catalog))
	for _, n := range catalog {
		have[n] = true
	}
	var out []Resolved
	for _, k := range hikariSignals {
		for _, s := range hikariMappings[k] {
			for _, n := range s.Names {
				if !have[n] {
					continue
				}
				agg := "avg"
				if s.Kind == KindCounter {
					agg = "rate"
				}
				out = append(out, Resolved{Signal: k, Metric: n, Kind: s.Kind, Agg: agg, Scale: s.Scale, GenLabel: s.GenLabel, PodScoped: true})
				break
			}
		}
	}
	return out
}

// HikariPodFilter — SAF: pod listesi → host_name süzgeci (sıralı, ≤ HikariPodsMax).
func HikariPodFilter(pods []string) (chstore.FilterExpr, bool) {
	ps := make([]string, 0, len(pods))
	for _, p := range pods {
		if p != "" {
			ps = append(ps, p)
		}
	}
	if len(ps) == 0 {
		return chstore.FilterExpr{}, false
	}
	sort.Strings(ps)
	if len(ps) > HikariPodsMax {
		ps = ps[:HikariPodsMax]
	}
	return chstore.FilterExpr{Key: hikariPodFilterKey, Op: "IN", Values: ps}, true
}

// readHikari — servisin pod'larının Hikari serileri (servis süzgeci YOK; pod
// süzgeci var). Sonuç pod anahtarlarıyla (service, host_name) döner.
func readHikari(ctx context.Context, r MetricReader, service string, pods []string, extra []chstore.FilterExpr, now time.Time) ([]Resolved, []PodData, []string, error) {
	pf, ok := HikariPodFilter(pods)
	if !ok {
		return nil, nil, nil, nil
	}
	infos, _, err := r.ListMetricNames(ctx, "", hikariCatalogFilter, hikariCatalogLimit, 0)
	if err != nil {
		return nil, nil, nil, err
	}
	cat := make([]string, 0, len(infos))
	for _, m := range infos {
		cat = append(cat, m.Name)
	}
	res := ResolveHikari(cat)
	if len(res) == 0 {
		return nil, nil, nil, nil
	}
	p := Profile{Signals: res}
	keys := make([]SignalKey, 0, len(res))
	for _, x := range res {
		keys = append(keys, x.Signal)
	}
	data, notes, err := readPodsWith(ctx, r, service, p, now, readOpts{keys: keys, noServiceFilter: true,
		filters: append(append([]chstore.FilterExpr{}, extra...), pf)})
	return res, data, notes, err
}

// readHikariFor — Analyze kancası: read=false (kip off) → yalnız katalog
// çözümü (grafikler), seri okunmaz.
func readHikariFor(ctx context.Context, r MetricReader, service string, o AnalyzeOpts, now time.Time, read bool) ([]Resolved, []PodData, []string, error) {
	if !read {
		if _, ok := HikariPodFilter(o.Pods); !ok {
			return nil, nil, nil, nil
		}
		infos, _, err := r.ListMetricNames(ctx, "", hikariCatalogFilter, hikariCatalogLimit, 0)
		if err != nil {
			return nil, nil, nil, err
		}
		cat := make([]string, 0, len(infos))
		for _, m := range infos {
			cat = append(cat, m.Name)
		}
		return ResolveHikari(cat), nil, nil, nil
	}
	// Ortam kapsamı Hikari'ye UYGULANMAZ: kapsam runtime metriğinin etiketiyle
	// çözülür, Hikari serileri o etiketi taşımayabilir (boş grafik olurdu);
	// pod süzgeci zaten servisin varlık pod'larıdır.
	return readHikari(ctx, r, service, o.Pods, nil, now)
}

// mergePods — SAF: (servis, pod) anahtarında seri birleştirme (ilk gelen kazanır).
func mergePods(a, b []PodData) []PodData {
	if len(b) == 0 {
		return a
	}
	type pk struct{ svc, pod string }
	idx := map[pk]int{}
	out := append([]PodData{}, a...)
	for i, d := range out {
		idx[pk{d.Service, d.Pod}] = i
	}
	for _, d := range b {
		i, ok := idx[pk{d.Service, d.Pod}]
		if !ok {
			idx[pk{d.Service, d.Pod}] = len(out)
			out = append(out, d)
			continue
		}
		if out[i].Series == nil {
			out[i].Series = map[SignalKey][]Point{}
		}
		for k, v := range d.Series {
			if len(out[i].Series[k]) == 0 {
				out[i].Series[k] = v
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].Pod < out[j].Pod
	})
	return out
}

// evaluateHikari — SAF: havuz doygunluğu (aktif/tavan ≥ %90 VE bekleyen ≥ 1,
// son 10 dk ort.) ya da bekleyen ≥ 5 → kritik; bağlantı alma ort. ≥ 1 sn de
// kritik yapar.
func evaluateHikari(d PodData, now int64, mk func(check, level string, value, base, thr float64, unit, summary string) Signal, where string) []Signal {
	pend := d.Series[SigDBPoolPending]
	if len(pend) == 0 {
		return nil
	}
	p, pb, nr, _ := splitWindow(pend, now)
	if nr < minRecentPoints || p < dbPendingWarn {
		return nil
	}
	util := -1.0
	if a, m := d.Series[SigDBPoolActive], d.Series[SigDBPoolMax]; len(a) > 0 && len(m) > 0 {
		av, _, _, _ := splitWindow(a, now)
		mv, _, _, _ := splitWindow(m, now)
		if mv > 0 {
			util = av / mv * 100
		}
	}
	acqMs := -1.0
	if s, c := d.Series[SigDBAcquireSum], d.Series[SigDBAcquireCount]; len(s) > 0 && len(c) > 0 {
		sv, _, _, _ := splitWindow(s, now)
		cv, _, _, _ := splitWindow(c, now)
		if cv > 0 {
			acqMs = sv / cv * 1000
		}
	}
	if util >= 0 && util < dbPoolWarnPct && p < dbPendingCrit {
		return nil // bekleyen var ama havuz dolmamış: kısa sıçrama, doygunluk değil
	}
	level := LevelWarning
	if p >= dbPendingCrit || acqMs >= dbAcquireCritMs {
		level = LevelCritical
	}
	use := "havuz doluluğu bilinmiyor"
	if util >= 0 {
		use = fmt.Sprintf("havuz %%%.0f dolu", util)
	}
	acq := ""
	if acqMs >= 0 {
		acq = fmt.Sprintf(", bağlantı alma ort. %.0f ms", acqMs)
	}
	s := mk(CheckDBPoolSaturation, level, p, pb, dbPendingWarn, "items",
		fmt.Sprintf("DB bağlantı havuzu (HikariCP) doygun: %s ortalama %.1f thread bağlantı bekliyor (önceki 50 dk %.1f), %s%s", where, p, pb, use, acq))
	s.Rising = p > pb
	s.Extra = map[string]float64{}
	if util >= 0 {
		s.Extra["poolUtilPct"] = math.Round(util)
	}
	if acqMs >= 0 {
		s.Extra["acquireMs"] = math.Round(acqMs)
	}
	return []Signal{s}
}
