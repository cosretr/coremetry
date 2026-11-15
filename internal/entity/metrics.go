package entity

import (
	"context"
	"errors"
	"log"
	"sort"
	"time"
)

// metrics.go — v0.10.2261 (Entity S6 parça 2, P-c; docs/architecture/
// entity-s6-cost-note.md seçenek C): METRİK KOLU, şemasız.
//
//   - Yalnız metrik basan servis (son SeenWindow'da span'i görülmeyen ama
//     metrik backend'inin servis etiketinde bulunan ad) kalıcı katmanda
//     `svc:<ad>` entity'si olur; satırlar sahte cluster_id MetricClusterID
//     altında, source = "metric" (TopologyClusterID emsali). Span basmaya
//     başlarsa bu aralıktaki ömür ≥ 24 sa sonra kapanır.
//   - Metrik `runs` (pod|host → svc) CH'ye YAZILMAZ: canlı dizine girer
//     (cluster blob'unda pod'un servisleri; host blob'unda host'un).
//
// Kaynak metrik backend'i (VictoriaMetrics / Thanos, vmetrics.EntityMetricPairs):
// lider tick'inde 15 dk'da bir, geçiş başına sabit ≤ 3 istek, seri tavanı
// MaxMetricSeries. Etiket eşlemesi (pod etiketi) yapılandırılmamışsa geçiş
// koşmaz ve bunu süreçte bir kez günlükler. Ham metric_points okunmaz.

// MetricClusterID — yalnız metrik basan servis satırlarının cluster_id'si.
const MetricClusterID = "(metric)"

// SourceMetric — metrik backend'inden gelen entity / kenar.
const SourceMetric = "metric"

// Sabitler (ayar yok, K1).
const (
	// MetricsEvery — geçiş kadansı.
	MetricsEvery = 15 * time.Minute
	// MetricsWindow — metrik backend'inde bakılan pencere.
	MetricsWindow = 15 * time.Minute
	// MetricRunsTTL — son başarılı geçişin runs kümesi bu kadar kullanılır
	// (backend düşerse eski eşleşme dizinde sonsuza dek kalmasın).
	MetricRunsTTL = 2*MetricsEvery + SyncInterval
	// MetricCloseAfter — metrik servis ömrü art arda başarılı geçişlerde bu
	// kadar görülmezse kapanır.
	MetricCloseAfter = 24 * time.Hour
	// MaxMetricServices — servis adı tavanı (etiket değeri isteğinin limit'i).
	MaxMetricServices = 5_000
	// MaxMetricSeries — pod / host eşleşme sorgusu başına seri tavanı (topk).
	MaxMetricSeries = 40_000
)

// ErrMetricLabelsUnconfigured — metrik backend'i ya da etiket eşlemesi
// (pod etiketi) yapılandırılmamış: geçiş sessizce atlanır.
var ErrMetricLabelsUnconfigured = errors.New("metrik etiket eşlemesi yapılandırılmamış")

// MetricRun — metrik backend'inde görülen (servis, pod|host) çifti.
type MetricRun struct {
	Service, Pod, Namespace, Host string
}

// MetricSnapshot — tek geçişin metrik görüntüsü.
type MetricSnapshot struct {
	Services  []string
	Runs      []MetricRun
	Truncated bool // bir sorgu tavana ulaştı ya da backend kısmi döndü
	Queries   int
}

// MetricSource — vmetrics.Service (yapısal tipleme; entity vmetrics'i import etmez).
type MetricSource interface {
	EntityMetricPairs(ctx context.Context) (MetricSnapshot, error)
}

// SetMetricSource — main.go vmetrics servisini bağlar (nil = geçiş yok).
// Run goroutine'i başladıktan sonra da çağrılabilir (kilitli).
func (s *Syncer) SetMetricSource(m MetricSource) {
	s.mu.Lock()
	s.metrics = m
	s.mu.Unlock()
}

// MetricIndex — son başarılı geçişin runs dizini (pod / host adına göre).
type MetricIndex struct {
	at     time.Time
	byPod  map[string][]MetricRun // pod adı → çiftler
	byHost map[string][]string    // host adı → servisler
}

// IndexMetricRuns — SAF: çiftler → pod / host dizini (yinelenenler tekil).
func IndexMetricRuns(at time.Time, runs []MetricRun) *MetricIndex {
	ix := &MetricIndex{at: at, byPod: map[string][]MetricRun{}, byHost: map[string][]string{}}
	seen := map[MetricRun]bool{}
	for _, r := range runs {
		if r.Service == "" || seen[r] {
			continue
		}
		seen[r] = true
		switch {
		case r.Pod != "":
			ix.byPod[r.Pod] = append(ix.byPod[r.Pod], r)
		case r.Host != "":
			ix.byHost[r.Host] = append(ix.byHost[r.Host], r.Service)
		}
	}
	for _, v := range ix.byHost {
		sort.Strings(v)
	}
	return ix
}

// currentMetricIndex — TTL içindeki dizin (yoksa nil).
func (s *Syncer) currentMetricIndex(now time.Time) *MetricIndex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.metricIx == nil || now.Sub(s.metricIx.at) > MetricRunsTTL {
		return nil
	}
	return s.metricIx
}

// MetricPodRuns — SAF: cluster görüntüsünün pod'larına metrik runs
// kenarları (pod adı eşleşir; çiftte namespace varsa o da). Span'in
// zaten bildiği kenar eklenmez (relByKey'de önce gelen kazanır).
func MetricPodRuns(cid string, ents map[string]Entity, ix *MetricIndex) []Relation {
	if ix == nil || len(ix.byPod) == 0 {
		return nil
	}
	var out []Relation
	for id, e := range ents {
		if e.Type != TypePod {
			continue
		}
		for _, r := range ix.byPod[e.Name] {
			if r.Namespace != "" && r.Namespace != e.Namespace {
				continue
			}
			out = append(out, Relation{Type: RelRuns, ClusterID: cid, ParentID: id, ChildID: ServiceID(r.Service), Source: SourceMetric})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ParentID != out[j].ParentID {
			return out[i].ParentID < out[j].ParentID
		}
		return out[i].ChildID < out[j].ChildID
	})
	return out
}

// BuildMetricServices — SAF: span'i görülmeyen metrik servisleri → kalıcı
// entity (MetricClusterID, source=metric), ada göre ilk max.
func BuildMetricServices(names []string, spanServices map[string]bool, max int) map[string]Entity {
	uniq := map[string]bool{}
	for _, n := range names {
		if n != "" && !spanServices[n] {
			uniq[n] = true
		}
	}
	list := make([]string, 0, len(uniq))
	for n := range uniq {
		list = append(list, n)
	}
	sort.Strings(list)
	if len(list) > max {
		list = list[:max]
	}
	out := make(map[string]Entity, len(list))
	for _, n := range list {
		id := ServiceID(n)
		out[id] = Entity{Type: TypeService, ClusterID: MetricClusterID, ID: id, Name: n, Labels: map[string]string{"signal": "metric-only"}, Source: SourceMetric}
	}
	return out
}

// syncMetrics — metrik geçişi (lider tick'inde, MetricsEvery kadansı).
// spanServices: bu tick'in span satırlarındaki servisler; spanOK=false
// (span okuması başarısız) → metrik servisleri yazılmaz (hangisinin
// span'siz olduğu bilinmiyor), yalnız runs dizini tazelenir.
func (s *Syncer) syncMetrics(ctx context.Context, spanServices map[string]bool, spanOK bool) (st clusterTickStats) {
	s.mu.Lock()
	src := s.metrics
	s.mu.Unlock()
	now := s.now()
	if src == nil || now.Sub(time.Unix(0, s.metricsLast.Load())) < MetricsEvery {
		return
	}
	s.metricsLast.Store(now.UnixNano())
	mctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	snap, err := src.EntityMetricPairs(mctx)
	cancel()
	if errors.Is(err, ErrMetricLabelsUnconfigured) {
		if s.metricsSkipLogged.CompareAndSwap(false, true) {
			log.Printf("[entity] metrics: geçiş atlandı — %v (Settings → VictoriaMetrics etiket eşlemesi; bu mesaj süreçte bir kez)", err)
		}
		return
	}
	if err != nil {
		s.recordPassFailure(ctx, MetricClusterID, now, "metrics", err)
		return
	}
	ix := IndexMetricRuns(now, snap.Runs)
	s.mu.Lock()
	s.metricIx = ix
	s.mu.Unlock()
	if !spanOK {
		log.Printf("[entity] metrics: span okuması yok, servis yazımı atlandı; runs=%d queries=%d", len(snap.Runs), snap.Queries)
		return
	}
	open, err := s.store.OpenLifetimes(ctx, MetricClusterID)
	if err != nil {
		s.recordPassFailure(ctx, MetricClusterID, now, "metrics", err)
		return
	}
	ents := BuildMetricServices(snap.Services, spanServices, MaxMetricServices)
	st, err = s.applyStable(ctx, MetricClusterID, now, open, ents, nil, MetricCloseAfter, snap.Truncated)
	if err != nil {
		return st
	}
	log.Printf("[entity] metrics: services=%d metric_only=%d runs=%d pods=%d hosts=%d truncated=%v queries=%d written=%d closed=%d",
		len(snap.Services), len(ents), len(snap.Runs), len(ix.byPod), len(ix.byHost), snap.Truncated, snap.Queries, st.ents, st.closed)
	return st
}

// MergeMetricHosts — SAF: metrik backend'inin host → servis çiftleri canlı
// host blob'una. Span adayı olan host'a eksik servis 0 span'le eklenir;
// yalnız metrikte görülen host Source=metric ile eklenir (kalıcı katmana
// terfi etmez: MV kanıtı yok). Pod şekilli ad ve KSM pod adı atlanır.
func MergeMetricHosts(snap *LiveSnapshot, ix *MetricIndex, knownPods map[string]bool) {
	if ix == nil || len(ix.byHost) == 0 {
		return
	}
	at := map[string]int{}
	for i, h := range snap.Hosts {
		at[h.Name] = i
	}
	for name, svcs := range ix.byHost {
		if knownPods[name] || LooksLikePod(name) {
			continue
		}
		i, ok := at[name]
		if !ok {
			snap.Hosts = append(snap.Hosts, LiveHost{Name: name, Source: SourceMetric})
			i = len(snap.Hosts) - 1
			at[name] = i
		}
		h := &snap.Hosts[i]
		have := map[string]bool{}
		for _, s := range h.Services {
			have[s.Name] = true
		}
		for _, sv := range svcs {
			if !have[sv] {
				h.Services = append(h.Services, LiveService{Name: sv})
			}
		}
		sortLiveServices(h.Services)
	}
	sort.Slice(snap.Hosts, func(i, j int) bool { return snap.Hosts[i].Name < snap.Hosts[j].Name })
}
