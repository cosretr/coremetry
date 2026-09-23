package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
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

func init() { registerRoutesExtra("heap-baseline", (*Server).registerHeapBaselineRoutes) }

func (s *Server) registerHeapBaselineRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services/{name}/heap-baseline", s.getServiceHeapBaseline)
}

// (v0.10.890) Okuyucu sabitleri/yardımcıları anomaly/heap_read.go'da — kart ve
// dedektör aynı okuyucu, aynı politika (HeapPolicyFrom canlı bloktan).

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
	// Verdict — v0.10.891: dedektörün gölge/canlı hükmü (Redis, 15 dk; lider
	// yazar, her pod okur). Yoksa kip off / Noop cache / henüz hüküm yok.
	Verdict *anomaly.HeapVerdict `json:"verdict,omitempty"`
	Mode    string               `json:"mode"` // anomaly_sensitivity.runtime.heapMode (normalize)
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
	sens := s.store.AnomalySensitivity()
	pol, mode := anomaly.HeapPolicyFrom(sens), sens.HeapMode() // v0.10.890 — canlı vida, kartla dedektör aynı bant
	key := fmt.Sprintf("heap-baseline:%s:%s:%d:%d:%v:%s", src.Name(), name, from.Unix()/anomaly.HeapBucketSec, to.Unix()/anomaly.HeapBucketSec, pol, mode)
	s.serveCached(w, r, key, 60*time.Second, func(ctx context.Context) (any, error) {
		res, err := buildHeapBaseline(ctx, src, name, from, to, pol)
		if err != nil {
			return nil, err
		}
		res.Mode = mode
		if mode != chstore.HeapModeOff { // off'ta bayat verdict (TTL 15 dk) servis edilmez
			res.Verdict = s.heapVerdictFor(ctx, name)
		}
		return res, nil
	})
}

// heapVerdictFor — dedektörün önbelleğe yazdığı hüküm; yoksa nil (dürüst).
func (s *Server) heapVerdictFor(ctx context.Context, service string) *anomaly.HeapVerdict {
	if s.cache == nil {
		return nil
	}
	b, ok, err := s.cache.Get(ctx, anomaly.HeapVerdictKey(service))
	if err != nil || !ok || len(b) == 0 {
		return nil
	}
	var v anomaly.HeapVerdict
	if json.Unmarshal(b, &v) != nil {
		return nil
	}
	return &v
}

func buildHeapBaseline(ctx context.Context, src anomaly.HeapSource, service string, from, to time.Time, pol anomaly.HeapBandPolicy) (*HeapBaselineResponse, error) {
	res := &HeapBaselineResponse{
		Source: src.Name(), Metric: anomaly.HeapBandMetric, HistoryHours: int(anomaly.HeapHistory.Hours()),
		BucketSec: int(anomaly.HeapBucketSec), NeedBuckets: anomaly.HeapBandMinBuckets(),
		FromNs: from.UnixNano(), ToNs: to.UnixNano(), Pods: []HeapBaselinePod{},
	}
	rd, err := anomaly.ReadHeapPods(ctx, src, service, to.Add(-anomaly.HeapHistory), to)
	if err != nil {
		return nil, err
	}
	res.Capped, res.Reason = rd.Capped, rd.Reason
	if len(rd.Pct) == 0 {
		return res, nil
	}
	lastComplete := anomaly.HeapLastComplete(to) // son TAM kovanın başı
	pods := make([]HeapBaselinePod, 0, len(rd.Pct))
	bands := make(map[string]anomaly.HeapBand, len(rd.Pct))
	for pod, minutes := range rd.Pct {
		bucketTimes, buckets := anomaly.HeapBuckets(minutes, lastComplete)
		p := HeapBaselinePod{Pod: pod, Band: anomaly.ComputeHeapBandWith(buckets, pol), Series: []chstore.Point{}}
		bands[pod] = p.Band
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
	res.Worst = anomaly.HeapWorstPod(bands) // kesimden ÖNCE
	silentBefore := lastComplete - int64(heapSilentBuckets)*anomaly.HeapBucketSec
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
	if len(pods) > anomaly.HeapMaxPods {
		res.Truncated = len(pods) - anomaly.HeapMaxPods
		pods = pods[:anomaly.HeapMaxPods]
	}
	res.Pods = pods
	return res, nil
}

// heapSilentBuckets — son kova bundan eskiyse pod "sessiz" (canlılar önce sıralanır).
const heapSilentBuckets = 3
