package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// heap_baseline_routes.go — v0.10.887 (Dynatrace paritesi #4, dilim 1;
// spec Onay 2026-09-23). GET /api/services/{name}/heap-baseline?from&to:
// JVM heap-after-GC'nin limit yüzdesi, pod başına 5-dk kova serisi + 24 s
// ardışık pencereden adaptif bant (anomaly.ComputeHeapBand). Yalnız
// görünürlük — Problem/bildirim yok.
//
// Kaynak: metricSource dikişi (VM yapılandırılıysa VM, değilse CH) —
// RuntimeCharts POD_GROUP'un ilk üç anahtarı (resource.k8s.pod.name →
// host.name → service.instance.id; `cluster` HARİÇ — spec üçlü, çok-cluster'da
// aynı adlı pod birleşir, dilim 2 notu). Kova doğrudan 5 dk istenir
// (StepSeconds 300): CH ham yolu `LIMIT SpanMetricRowCap` (50k satır) taşır —
// 60 s adımda 24 s × pod ~34 pod'da sessizce ısırırdı; 300 s'de ~170 pod.
// Yine de tavana çarpılırsa SeriesRowsCapped → `capped: true`, FE söyler.
// CH'de Aggregation=sum kovadaki zaman-içi örnekleri de toplar; used/limit
// oranı iki metrikte örnek sayısı eşit olduğu sürece sadeleşir (VM'de anlık).
//
// Dürüstlük: metrik hiç yoksa pods boş (kart kaybolur, sıfır çizmez);
// n < 15 kova → status no_baseline, bant null; ölmüş pod'un son kovası
// `lastTs` ile gelir (FE "sessiz · N dk önce"); en kötü pod 50 kesiminden
// ÖNCE hesaplanır; cevap kaynağı ve pencereyi söyler. api.go'ya sıfır satır.

const (
	heapBaselineHistory   = 24 * time.Hour // ardışık baseline penceresi (anomaly historyHours)
	heapBaselineBucketSec = 300            // anomaly bucketSeconds — kaynaktan da bu adımla istenir
	heapSilentBuckets     = 3              // son kova bundan eskiyse pod "sessiz" (canlılar önce sıralanır)
	heapBaselineMaxPods   = 50             // son değere göre en yüksek N; üstü `truncated`
	heapMetricPostGC      = "jvm.memory.used_after_last_gc"
	heapMetricLimit       = "jvm.memory.limit"
)

var heapPodGroupBy = []string{"resource.k8s.pod.name", "resource.host.name", "resource.service.instance.id"}
var heapTypeFilter = []chstore.FilterExpr{{Key: "jvm.memory.type", Op: "=", Values: []string{"heap"}}}

func init() { registerRoutesExtra("heap-baseline", (*Server).registerHeapBaselineRoutes) }

func (s *Server) registerHeapBaselineRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services/{name}/heap-baseline", s.getServiceHeapBaseline)
}

// HeapBaselinePod — bir pod: bant + görüntü penceresindeki kova serisi.
type HeapBaselinePod struct {
	Pod    string           `json:"pod"`
	Band   anomaly.HeapBand `json:"band"`
	Series []chstore.Point  `json:"series"`
	// LastTs — verisi olan son TAM kovanın başı (unix s); FE bayatlığı söyler.
	LastTs int64 `json:"lastTs"`
}

// HeapBaselineResponse — kaynak/pencere/metrik açık; pods boşsa kart yok.
type HeapBaselineResponse struct {
	Source       string            `json:"source"`
	Metric       string            `json:"metric"`
	HistoryHours int               `json:"historyHours"`
	BucketSec    int               `json:"bucketSec"`
	NeedBuckets  int               `json:"needBuckets"`
	FromNs       int64             `json:"from"`
	ToNs         int64             `json:"to"`
	Pods         []HeapBaselinePod `json:"pods"`
	Truncated    int               `json:"truncated"`
	// Capped — kaynak satır tavanına çarpıldı (CH SpanMetricRowCap): bazı
	// pod'lar eksik/kısmi olabilir; FE rozet basar, sayılara güvenmez.
	Capped bool   `json:"capped"`
	Worst  string `json:"worst,omitempty"`
	// Reason — pods boşken neden (metrik yok / satır yok); FE göstermez, log/teşhis.
	Reason string `json:"reason,omitempty"`
}

func (s *Server) getServiceHeapBaseline(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "service name required", http.StatusBadRequest)
		return
	}
	from, to := parseFromTo(r, time.Hour)
	src, err := s.metricSourceFor(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	// Anahtar 5-dk ızgarada: veri yalnız tam kova ilerledikçe değişir (son eksik
	// kova atılır); 30 s tik / zoom aynı kovada aynı cevabı okur.
	key := fmt.Sprintf("heap-baseline:%s:%s:%d:%d", src.Name(), name, from.Unix()/heapBaselineBucketSec, to.Unix()/heapBaselineBucketSec)
	s.serveCached(w, r, key, 60*time.Second, func(ctx context.Context) (any, error) {
		return buildHeapBaseline(ctx, src, name, from, to)
	})
}

func buildHeapBaseline(ctx context.Context, src metricSource, service string, from, to time.Time) (*HeapBaselineResponse, error) {
	res := &HeapBaselineResponse{
		Source: src.Name(), Metric: anomaly.HeapBandMetric, HistoryHours: int(heapBaselineHistory.Hours()),
		BucketSec: heapBaselineBucketSec, NeedBuckets: anomaly.HeapBandMinBuckets(),
		FromNs: from.UnixNano(), ToNs: to.UnixNano(), Pods: []HeapBaselinePod{},
	}
	ok, err := src.MetricExists(ctx, heapMetricPostGC)
	if err != nil {
		return nil, err
	}
	if !ok {
		res.Reason = "metric yok: " + heapMetricPostGC
		return res, nil
	}
	histFrom := to.Add(-heapBaselineHistory)
	read := func(metric string) ([]chstore.SpanMetricSeries, error) {
		return src.QueryMetric(ctx, chstore.MetricQueryFilter{
			Name: metric, Service: service, Aggregation: "sum", GroupBy: heapPodGroupBy, Filters: heapTypeFilter,
			From: histFrom, To: to, StepSeconds: heapBaselineBucketSec, MaxDataPoints: int(heapBaselineHistory.Seconds())/heapBaselineBucketSec + 2,
		})
	}
	postgc, err := read(heapMetricPostGC)
	if err != nil {
		return nil, err
	}
	limit, err := read(heapMetricLimit)
	if err != nil {
		return nil, err
	}
	res.Capped = chstore.SeriesRowsCapped(postgc) || chstore.SeriesRowsCapped(limit)
	pct := heapPctByPod(postgc, limit)
	if len(pct) == 0 {
		res.Reason = "pencerede satır yok"
		return res, nil
	}
	lastComplete := to.Unix()/heapBaselineBucketSec*heapBaselineBucketSec - heapBaselineBucketSec // son TAM kovanın başı
	pods := make([]HeapBaselinePod, 0, len(pct))
	for pod, minutes := range pct {
		bucketTimes, buckets := heapBuckets(minutes, lastComplete)
		p := HeapBaselinePod{Pod: pod, Band: anomaly.ComputeHeapBand(buckets), Series: []chstore.Point{}}
		if n := len(bucketTimes); n > 0 {
			p.LastTs = bucketTimes[n-1]
		}
		for i, t := range bucketTimes {
			if t >= from.Unix() && t <= to.Unix() {
				p.Series = append(p.Series, chstore.Point{TimeNs: t * 1e9, Value: math.Round(buckets[i]*100) / 100})
			}
		}
		pods = append(pods, p)
	}
	res.Worst = heapWorstPod(pods) // kesimden ÖNCE
	silentBefore := lastComplete - heapSilentBuckets*heapBaselineBucketSec
	sort.Slice(pods, func(i, j int) bool {
		li, lj := pods[i].LastTs >= silentBefore, pods[j].LastTs >= silentBefore
		if li != lj {
			return li // canlı pod'lar önce
		}
		if pods[i].Band.Current != pods[j].Band.Current {
			return pods[i].Band.Current > pods[j].Band.Current
		}
		return pods[i].Pod < pods[j].Pod
	})
	if len(pods) > heapBaselineMaxPods {
		res.Truncated = len(pods) - heapBaselineMaxPods
		pods = pods[:heapBaselineMaxPods]
	}
	res.Pods = pods
	return res, nil
}

// heapPodKey — SAF: grup anahtarından pod kimliği (k8s.pod.name → host.name →
// service.instance.id[:8]); vmetrics.PodFromTuple ile aynı sıra, servis
// yuvası yok (Service süzgeci ayrı).
func heapPodKey(groupKey []string) string {
	for i, v := range groupKey {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if i == 2 {
			if r := []rune(v); len(r) > 8 {
				return string(r[:8])
			}
		}
		return v
	}
	return ""
}

// heapPctByPod — SAF: dakika → postgc/limit×100 (limit > 0), pod başına.
func heapPctByPod(postgc, limit []chstore.SpanMetricSeries) map[string]map[int64]float64 {
	lim := map[string]map[int64]float64{}
	for _, ser := range limit {
		pod := heapPodKey(ser.GroupKey)
		if pod == "" {
			continue
		}
		m := lim[pod]
		if m == nil {
			m = map[int64]float64{}
			lim[pod] = m
		}
		for _, p := range ser.Points {
			m[p.Time/1e9] = p.Value
		}
	}
	out := map[string]map[int64]float64{}
	for _, ser := range postgc {
		pod := heapPodKey(ser.GroupKey)
		if pod == "" || lim[pod] == nil {
			continue
		}
		for _, p := range ser.Points {
			l := lim[pod][p.Time/1e9]
			if l <= 0 || p.Value <= 0 {
				continue
			}
			if out[pod] == nil {
				out[pod] = map[int64]float64{}
			}
			out[pod][p.Time/1e9] = p.Value / l * 100
		}
	}
	return out
}

// heapBuckets — SAF: dakika değerleri → 5-dk kova ortalamaları, eskiden
// yeniye, yalnız mevcut kovalar (boşluk atlanır) ve yalnız lastComplete'e
// kadar (son eksik kova atılır — anomaly lastCompleteBucketStart kuralı).
func heapBuckets(minutes map[int64]float64, lastComplete int64) (times []int64, values []float64) {
	sum := map[int64]float64{}
	n := map[int64]int{}
	for sec, v := range minutes {
		b := sec / heapBaselineBucketSec * heapBaselineBucketSec
		if b > lastComplete {
			continue
		}
		sum[b] += v
		n[b]++
	}
	times = make([]int64, 0, len(sum))
	for b := range sum {
		times = append(times, b)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	values = make([]float64, len(times))
	for i, b := range times {
		values[i] = sum[b] / float64(n[b])
	}
	return times, values
}

// heapWorstPod — SAF: en yüksek z'li bant sahibi pod (bantsızlar sayılmaz);
// hiç bant yoksa "".
func heapWorstPod(pods []HeapBaselinePod) string {
	worst, best := "", 0.0
	for _, p := range pods {
		if p.Band.Status == anomaly.HeapBandNoBaseline {
			continue
		}
		if worst == "" || p.Band.Z > best {
			worst, best = p.Pod, p.Band.Z
		}
	}
	return worst
}
