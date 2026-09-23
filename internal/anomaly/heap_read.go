package anomaly

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// heap_read.go — v0.10.890 (paritesi #4 dilim 2): heap-after-GC okuyucusunun
// SAF yardımcıları api/heap_baseline_routes.go'dan buraya taşındı ki kart ve
// dedektör (891: halka + doldurma) AYNI okuyucuyu kullansın — iki gerçek yok.
// api paketi tüketici (import yönü api → anomaly). Davranış 887 ile aynı.

const (
	HeapHistory   = 24 * time.Hour // ardışık baseline penceresi (anomaly historyHours)
	HeapBucketSec = int64(300)     // anomaly bucketSeconds — kaynaktan da bu adımla istenir
	HeapMaxPods   = 50             // kart: son değere göre en yüksek N; üstü `truncated`
	// HeapMetricPostGC / HeapMetricLimit — OTel stable jvm.* semconv.
	HeapMetricPostGC = "jvm.memory.used_after_last_gc"
	HeapMetricLimit  = "jvm.memory.limit"
)

// HeapPodGroupBy — pod kimliği: k8s.pod.name → host.name → service.instance.id[:8];
// `resource.` öneki CH'de res_values'a çözülür, VM'de düşürülür (iki kaynakta
// aynı). `cluster` HARİÇ (spec üçlü; çok-cluster çakışması dilim notu).
var HeapPodGroupBy = []string{"resource.k8s.pod.name", "resource.host.name", "resource.service.instance.id"}

// HeapTypeFilter — yalnız heap havuzları.
var HeapTypeFilter = []chstore.FilterExpr{{Key: "jvm.memory.type", Op: "=", Values: []string{"heap"}}}

// HeapSource — kart (api metricSource) ve dedektör (vmetrics.Service /
// chstore.Store) aynı üç metodu taşır; yapısal arayüz.
type HeapSource interface {
	Name() string
	MetricExists(ctx context.Context, name string) (bool, error)
	QueryMetric(ctx context.Context, f chstore.MetricQueryFilter) ([]chstore.SpanMetricSeries, error)
}

// HeapPodKey — SAF: grup anahtarından pod kimliği (üçlü sırayla; instance id 8 rune).
func HeapPodKey(groupKey []string) string {
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

// HeapPctByPod — SAF: dakika/kova → postgc/limit×100 (limit > 0, postgc > 0), pod başına.
func HeapPctByPod(postgc, limit []chstore.SpanMetricSeries) map[string]map[int64]float64 {
	lim := map[string]map[int64]float64{}
	for _, ser := range limit {
		pod := HeapPodKey(ser.GroupKey)
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
		pod := HeapPodKey(ser.GroupKey)
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

// HeapBuckets — SAF: örnekler → 5-dk kova ortalamaları, eskiden yeniye, yalnız
// mevcut kovalar (boşluk atlanır) ve yalnız lastComplete'e kadar (son eksik
// kova atılır — anomaly lastCompleteBucketStart kuralı).
func HeapBuckets(samples map[int64]float64, lastComplete int64) (times []int64, values []float64) {
	sum := map[int64]float64{}
	n := map[int64]int{}
	for sec, v := range samples {
		b := sec / HeapBucketSec * HeapBucketSec
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

// HeapLastComplete — SAF: `to` anındaki son TAM 5-dk kovanın başı (unix s).
func HeapLastComplete(to time.Time) int64 {
	return to.Unix()/HeapBucketSec*HeapBucketSec - HeapBucketSec
}

// HeapWorstPod — SAF: en yüksek z'li bant sahibi pod (bantsızlar sayılmaz); yoksa "".
func HeapWorstPod(bands map[string]HeapBand) string {
	worst, best := "", 0.0
	for pod, b := range bands {
		if b.Status == HeapBandNoBaseline {
			continue
		}
		if worst == "" || b.Z > best || (b.Z == best && pod < worst) {
			worst, best = pod, b.Z
		}
	}
	return worst
}

// HeapRead — bir okuma turunun sonucu.
type HeapRead struct {
	Pct    map[string]map[int64]float64 // pod → unix s → %
	Capped bool                         // kaynak satır tavanına çarpıldı (CH SpanMetricRowCap)
	Reason string                       // Pct boşken neden
}

// ReadHeapPods — servis için [from, to] penceresinde 5-dk adımla iki metrik
// okur (metrik yoksa boş + Reason). Kart (24 s) ve 891 doldurma aynı yol.
func ReadHeapPods(ctx context.Context, src HeapSource, service string, from, to time.Time) (HeapRead, error) {
	ok, err := src.MetricExists(ctx, HeapMetricPostGC)
	if err != nil {
		return HeapRead{}, err
	}
	if !ok {
		return HeapRead{Reason: "metric yok: " + HeapMetricPostGC}, nil
	}
	return ReadHeapPodsNoProbe(ctx, src, service, from, to)
}

// ReadHeapPodsNoProbe — MetricExists probu olmadan (dedektörün parçalı
// okumasında servis başına 2 sorgu; varlık zaten filo okumasından biliniyor).
func ReadHeapPodsNoProbe(ctx context.Context, src HeapSource, service string, from, to time.Time) (HeapRead, error) {
	read := func(metric string) ([]chstore.SpanMetricSeries, error) {
		return src.QueryMetric(ctx, chstore.MetricQueryFilter{
			Name: metric, Service: service, Aggregation: "sum", GroupBy: HeapPodGroupBy, Filters: HeapTypeFilter,
			From: from, To: to, StepSeconds: int(HeapBucketSec), MaxDataPoints: int(to.Sub(from).Seconds()/float64(HeapBucketSec)) + 2,
		})
	}
	postgc, err := read(HeapMetricPostGC)
	if err != nil {
		return HeapRead{}, err
	}
	limit, err := read(HeapMetricLimit)
	if err != nil {
		return HeapRead{}, err
	}
	r := HeapRead{Pct: HeapPctByPod(postgc, limit), Capped: chstore.SeriesRowsCapped(postgc) || chstore.SeriesRowsCapped(limit)}
	if len(r.Pct) == 0 {
		r.Reason = "pencerede satır yok"
	}
	return r, nil
}

// ── v0.10.891 — filo-geneli (servis+pod) okuma, dedektör için ──────────────

// HeapFleetGroupBy — servis ilk yuvada, ardından pod üçlüsü.
var HeapFleetGroupBy = append([]string{"resource.service.name"}, HeapPodGroupBy...)

// HeapPctByPodSvc — SAF: HeapPctByPod'un servisli hâli: svc → pod → unix s → %.
// Grup anahtarının ilk yuvası servis; pod üçlü sıradan (HeapPodKey).
func HeapPctByPodSvc(postgc, limit []chstore.SpanMetricSeries) map[string]map[string]map[int64]float64 {
	key := func(gk []string) (string, string) {
		if len(gk) == 0 || strings.TrimSpace(gk[0]) == "" {
			return "", ""
		}
		return gk[0], HeapPodKey(gk[1:])
	}
	lim := map[string]map[int64]float64{}
	for _, ser := range limit {
		svc, pod := key(ser.GroupKey)
		if svc == "" || pod == "" {
			continue
		}
		k := svc + "\x00" + pod
		if lim[k] == nil {
			lim[k] = map[int64]float64{}
		}
		for _, p := range ser.Points {
			lim[k][p.Time/1e9] = p.Value
		}
	}
	out := map[string]map[string]map[int64]float64{}
	for _, ser := range postgc {
		svc, pod := key(ser.GroupKey)
		if svc == "" || pod == "" || lim[svc+"\x00"+pod] == nil {
			continue
		}
		for _, p := range ser.Points {
			l := lim[svc+"\x00"+pod][p.Time/1e9]
			if l <= 0 || p.Value <= 0 {
				continue
			}
			if out[svc] == nil {
				out[svc] = map[string]map[int64]float64{}
			}
			if out[svc][pod] == nil {
				out[svc][pod] = map[int64]float64{}
			}
			out[svc][pod][p.Time/1e9] = p.Value / l * 100
		}
	}
	return out
}

// HeapVMSeriesCap — promapi.DecodeSeries'in SESSİZ seri tavanı (1000): tek
// sorguda bu kadar seri döndüyse sonrası düşmüş olabilir → parçalı okuma.
const HeapVMSeriesCap = 1000

// HeapFleetRead — filo-geneli okuma sonucu.
type HeapFleetRead struct {
	Pct    map[string]map[string]map[int64]float64
	Capped bool // CH satır tavanı ya da VM seri tavanı → çağıran servis-parçalı okumaya düşer
}

// ReadHeapFleet — [from, to] penceresini TÜM servisler için tek çift sorguyla
// okur (Service süzgeçsiz, GroupBy servis+pod). Tavana çarpınca Capped=true.
func ReadHeapFleet(ctx context.Context, src HeapSource, from, to time.Time) (HeapFleetRead, error) {
	read := func(metric string) ([]chstore.SpanMetricSeries, error) {
		return src.QueryMetric(ctx, chstore.MetricQueryFilter{
			Name: metric, Aggregation: "sum", GroupBy: HeapFleetGroupBy, Filters: HeapTypeFilter,
			From: from, To: to, StepSeconds: int(HeapBucketSec), MaxDataPoints: int(to.Sub(from).Seconds()/float64(HeapBucketSec)) + 2,
		})
	}
	postgc, err := read(HeapMetricPostGC)
	if err != nil {
		return HeapFleetRead{}, err
	}
	limit, err := read(HeapMetricLimit)
	if err != nil {
		return HeapFleetRead{}, err
	}
	capped := chstore.SeriesRowsCapped(postgc) || chstore.SeriesRowsCapped(limit) ||
		len(postgc) >= HeapVMSeriesCap || len(limit) >= HeapVMSeriesCap
	return HeapFleetRead{Pct: HeapPctByPodSvc(postgc, limit), Capped: capped}, nil
}
