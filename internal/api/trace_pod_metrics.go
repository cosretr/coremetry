package api

// trace_pod_metrics.go — v0.10.968 — Trace › Metrics yeniden tasarımının
// toplu metrik ucu (operatör onayı "3 onay", 2026-09-27; spec §2,
// apiContract A–B).
//
//   GET /api/trace-pods/metrics?cv=&from=&to=&mdp=&pods=<ns>/<pod>,…
//
// Neden böyle: sekme, bir span cluster değeri başına TEK istek atar (≤64
// pod, ≤4000 karakterlik seçici). Sunucu değeri Remote Cluster kaydına
// kendisi eşler (thanos.SpanClusterOwner + Enabled) ve Thanos'a pod
// sayısından bağımsız sabit maliyetle sorar (2 range + 6 instant,
// thanos/trace_pods.go). 64 pod × 2 cluster = 2 HTTP isteği.
//
// Neden öteki türlü DEĞİL:
//   - Pod başına /api/clusters/pods/detail fan-out'u (~67 istek, 40 pod
//     tavanı, "Kesildi" durumu) REDDEDİLDİ: maliyet pod sayısıyla
//     büyüyordu ve tavan operatöre kısmi tablo gösteriyordu.
//   - Eşleme istemcide YAPILMAZ: eskiden sekme entity katmanına
//     (useEntityEnabled + resolveCluster) bağlıydı; katman kapalıyken
//     "Entity katmanı kapalı" yazıyordu — oysa gerçek neden "bu cluster
//     için Remote Cluster kaydı yok". Sunucu gerçek nedeni söyler
//     (unmappedReason), eşlenmemiş değer Thanos'a hiç gitmez ve
//     önbelleğe girmez (ucuz, anlık, ayar değişince hemen doğru).
//   - Zaman aşımı sessiz boşluk DEĞİL: 502 + "zaman aşımı" (errUpstream);
//     istemci "10 sn zaman aşımı" diye yazar. Hata asla önbelleğe girmez.
//
// Rol kapısı YOK — salt-okunur trace drill-down'ı; küresel auth middleware
// kimliksiz isteği 401 yapar, viewer veriyi GÖRMELİ. Audit yok (yazmaz).
// Öz-gözlem: otelhttp rota şablonlu span'i zaten basıyor; yeni sayaç yok.
// api.go BÜYÜMEZ: kayıt init()'te registerRoutesExtra ile (/api-route).

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/thanos"
)

func init() { registerRoutesExtra("trace-pod-metrics", (*Server).registerTracePodMetricsRoutes) }

func (s *Server) registerTracePodMetricsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/trace-pods/metrics", s.getTracePodMetrics)
}

// tracePodMetricsTimeout — v0.10.968 — istek başına Thanos deadline'ı
// (instant zincirinin 6 s alt-süresi bunun içinde). Paket değişkeni: test
// 50 ms'ye indirir.
var tracePodMetricsTimeout = 10 * time.Second

// tracePodMaxWindow — v0.10.968 — pencere tavanı. Sekme trace ±5/15/60 dk
// ister (en çok ~2 sa); 6 sa, uzun trace'lere pay bırakıp bir crafted
// isteğin 30 günlük ham blok taramasını önler.
const tracePodMaxWindow = 6 * time.Hour

// v0.10.968 — Kubernetes ad sınırları: pod DNS-1123 subdomain (253),
// namespace DNS-1123 label (63). Aşan girdi gerçek bir pod olamaz; seçiciye
// ve anahtara girmeden 400.
const (
	tracePodNameMax = 253
	tracePodNSMax   = 63
)

// tracePodCluster — v0.10.968 — eşlenen Remote Cluster kaydı (kimlik + ad);
// pod sayfası bağlantısı ve kapsam açılır penceresi bunu gösterir.
type tracePodCluster struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// tracePodMetricsResponse — v0.10.968 — lib/types.ts TracePodMetricsResponse
// birebir. Eşlenmemiş cevaplarda yalnız thanos/clusterValue/mapped/
// unmappedReason/pods dolar; pods hiçbir dalda null değildir.
type tracePodMetricsResponse struct {
	Thanos         bool                    `json:"thanos"`
	ClusterValue   string                  `json:"clusterValue"`
	Mapped         bool                    `json:"mapped"`
	UnmappedReason string                  `json:"unmappedReason,omitempty"` // "no_cluster_value" | "no_remote_cluster"
	Cluster        *tracePodCluster        `json:"cluster,omitempty"`
	Start          int64                   `json:"start,omitempty"`
	Step           int                     `json:"step,omitempty"`
	Points         int                     `json:"points,omitempty"`
	Instant        string                  `json:"instant,omitempty"`
	Pods           []thanos.TracePodSeries `json:"pods"` // never null
}

// parseTracePodRefs — v0.10.968 — SAF: "ns/pod,ns/pod,…" → ref listesi.
// Girdi etrafındaki boşluk kırpılır, boş girdi (sondaki virgül) atlanır,
// tekrar eden ref ilk görüldüğü sırada tek kalır. Namespace boş olabilir
// ("/pod": span'de k8s.namespace.name yok). Hatalar errBadRequest (400).
func parseTracePodRefs(raw string) ([]thanos.TracePodRef, error) {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: pods: "+format, append([]any{errBadRequest}, a...)...)
	}
	out := make([]thanos.TracePodRef, 0, 8)
	seen := map[thanos.TracePodRef]bool{}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		ns, pod, ok := strings.Cut(entry, "/")
		if !ok {
			return nil, bad("%q ns/pod biçiminde değil", entry)
		}
		if pod == "" {
			return nil, bad("%q içinde pod adı boş", entry)
		}
		if len(pod) > tracePodNameMax {
			return nil, bad("pod adı en çok %d karakter", tracePodNameMax)
		}
		if len(ns) > tracePodNSMax {
			return nil, bad("namespace en çok %d karakter", tracePodNSMax)
		}
		ref := thanos.TracePodRef{Namespace: ns, Pod: pod}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
		if len(out) > thanos.TracePodChunkMax {
			return nil, bad("istek başına en çok %d pod", thanos.TracePodChunkMax)
		}
	}
	if len(out) == 0 {
		return nil, bad("en az bir ns/pod gerekli")
	}
	return out, nil
}

// tracePodMDP — v0.10.968 — SAF: ?mdp= → basamak. Eksik / sayı değil /
// ≤0 → 120 (THANOS_MDP_RUNGS[0]; sekme hep 120 yollar). Aksi
// TrendMaxDataPointsRung (yukarı yuvarlar, aşırı → 480). Basamak anahtara
// girer: serbest mdp sınırsız önbellek girdisi basamaz.
func tracePodMDP(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return thanos.TrendMaxDataPointsRungs[0]
	}
	return thanos.TrendMaxDataPointsRung(n)
}

// tracePodCfgDigest — v0.10.968 — clusterCfgDigest (URL + NamespaceFilter)
// + paylaşımlı querier'ın cluster etiketi + kaydın ADI. Etiket de SONUCU
// değiştirir (doQuery her seçiciye enjekte eder); anahtara girmezse etiket
// düzeltmesi 10 dk'lık kapanmış-pencere TTL'i boyunca eski cluster'ın
// serisini gösterirdi (hard constraint: anahtar TÜM girdileri hash'ler).
// Token bilinçli dışarıda: dönen token aynı veriyi getirir.
//
// v0.10.968 — c.Name gövdeye girer (cluster.name → "Pod sayfasında aç"
// bağlantısının ?cluster=); yeniden adlandırmada ID sabit kalır, yani ad
// anahtarda olmazsa önbellek eski adı 10 dk (+ SWR) taşır ve /pod o adı
// artık çözemez. Etiket yokken de, açık etiket değeri adı gölgelerken de
// ad hash'e girer.
func tracePodCfgDigest(c thanos.ClusterConfig) string {
	label, value := c.EffectiveThanosLabel()
	return clusterCfgDigest(c) + "." + fnvStr(label, value, c.Name)
}

// tracePodMetricsKey — v0.10.968 — SAF önbellek anahtarı:
//
//	trace-pod-metrics:v1:<cid>:<cfgDigest>:<fnv64(sıralı "ns/pod")>:<cacheBucket>:mdp=<rung>:c=<0|1>
//
// Pod KÜMESİ sırasız özetlenir (aynı küme farklı tablo sırasında aynı
// girdi); her ref ayrı parça olarak hash'e girer (fnvStr ayırıcısı) ki
// sınırlar kaybolmasın. Pencere 30 s ızgarada (cacheBucket).
//
// v0.10.968 — `c` kapanış evresi (tracePodWindowClosed): açık pencerede
// yazılan girdi 60 s TTL + SWR (3×) ile 180 s yaşar; istemcinin `to + 90 s`
// TEK yeniden çekmesi aynı anahtara düşseydi kapanıştan ÖNCEKİ gövdeyi STALE
// olarak alırdı ve tamamlanmış pencereyi hiç görmezdi. c=1 girdisi yalnız
// to+60 s'den sonra yazılabilir, yani tüm pencereyi kapsar.
func tracePodMetricsKey(clusterID, cfgDigest string, refs []thanos.TracePodRef, from, to time.Time, mdp int, closed bool) string {
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		parts = append(parts, r.Namespace+"/"+r.Pod)
	}
	sort.Strings(parts)
	c := 0
	if closed {
		c = 1
	}
	return fmt.Sprintf("trace-pod-metrics:v1:%s:%s:%s:%s:mdp=%d:c=%d",
		clusterID, cfgDigest, fnvStr(parts...), cacheBucket(from, to), mdp, c)
}

// tracePodWindowClosed — v0.10.968 — SAF: pencere kapanalı en az 60 s oldu
// mu (now ≥ to + 60 s). Eşik 90 değil 60: istemcinin to+90 s zamanlayıcısına
// karşı ~30 s saat kayması payı.
func tracePodWindowClosed(to, now time.Time) bool {
	return !now.Before(to.Add(60 * time.Second))
}

// tracePodMetricsTTL — v0.10.968 — SAF. Pencere hâlâ açık ya da yeni
// kapanmışsa (to ≥ now−10 dk) scrape'ler gelmeye devam eder: 60 s. Eski
// trace'in penceresi değişmez: 10 dk (limitler "şu an" olsa da 10 dk
// bayatlık kabul — istemci staleTime'ı aynı).
func tracePodMetricsTTL(to, now time.Time) time.Duration {
	if to.Before(now.Add(-10 * time.Minute)) {
		return 10 * time.Minute
	}
	return 60 * time.Second
}

func (s *Server) getTracePodMetrics(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cv := strings.TrimSpace(q.Get("cv"))

	// 1. Parametreler — hepsi 400, Thanos'a gitmeden.
	refs, err := parseTracePodRefs(q.Get("pods"))
	if err != nil {
		writeErr(w, err)
		return
	}
	from, to, err := parseTracePodWindow(q.Get("from"), q.Get("to"))
	if err != nil {
		writeErr(w, err)
		return
	}
	mdp := tracePodMDP(q.Get("mdp"))

	unmapped := func(thanosOn bool, reason string) {
		writeJSON(w, &tracePodMetricsResponse{Thanos: thanosOn, ClusterValue: cv,
			UnmappedReason: reason, Pods: []thanos.TracePodSeries{}})
	}
	// 2. Thanos hiç yok → sekme metrik kolonlarını gizler.
	if s.thanos == nil || !s.thanos.HasEnabledClusters() {
		unmapped(false, "")
		return
	}
	// 3. Span'ler cluster özniteliği taşımıyor.
	if cv == "" {
		unmapped(true, "no_cluster_value")
		return
	}
	// 4. Değer hiçbir kayda bağlı değil ya da bağlı kayıt kapalı.
	c, ok := thanos.SpanClusterOwner(s.thanos.CurrentSettings(), cv)
	if !ok || !c.Enabled {
		unmapped(true, "no_remote_cluster")
		return
	}
	// 5. Seçici uzunluğu — FE parçalayıcısı aynı tavanla böler; buraya
	// düşen istek parçalayıcı hatasıdır, sessizce kırpılmaz.
	if _, truncated := thanos.PodNamesRegex(thanos.TracePodNames(refs)); truncated {
		writeErr(w, fmt.Errorf("%w: pod listesi seçici tavanını (%d karakter) aşıyor", errBadRequest, thanos.TracePodRegexMax))
		return
	}

	// 6. Önbellek + sorgu.
	now := time.Now()
	key := tracePodMetricsKey(c.EffectiveID(), tracePodCfgDigest(c), refs, from, to, mdp, tracePodWindowClosed(to, now))
	s.serveCached(w, r, key, tracePodMetricsTTL(to, now), func(ctx context.Context) (any, error) {
		// Kapalı gelen ctx KULLANILIR (SWR tazelemesi kendi ctx'iyle koşar,
		// v0.8.319).
		qctx, cancel := context.WithTimeout(ctx, tracePodMetricsTimeout)
		defer cancel()
		res, err := s.thanos.TracePodMetrics(qctx, c, refs, from, to, mdp)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(qctx.Err(), context.DeadlineExceeded) {
				secs := int(math.Ceil(tracePodMetricsTimeout.Seconds()))
				return nil, fmt.Errorf("%w: %s: Thanos %d sn içinde yanıt vermedi (zaman aşımı)", errUpstream, c.Name, secs)
			}
			if errors.Is(err, context.Canceled) {
				return nil, err // istemci gitti → writeErr 499
			}
			return nil, fmt.Errorf("%w: %s: %v", errUpstream, c.Name, err)
		}
		pods := res.Pods
		if pods == nil {
			pods = []thanos.TracePodSeries{}
		}
		return &tracePodMetricsResponse{
			Thanos: true, ClusterValue: cv, Mapped: true,
			Cluster: &tracePodCluster{ID: c.EffectiveID(), Name: c.Name},
			Start:   res.Start, Step: res.Step, Points: res.Points, Instant: res.Instant,
			Pods: pods,
		}, nil
	})
}

// parseTracePodWindow — v0.10.968 — SAF: from/to unix ns, İKİSİ de zorunlu
// (parseFromTo'nun "eksikse son 1 sa" varsayılanı burada YANLIŞ olurdu:
// trace penceresi olmayan istek başka bir anın metriğini gösterirdi).
func parseTracePodWindow(rawFrom, rawTo string) (time.Time, time.Time, error) {
	parse := func(name, raw string) (time.Time, error) {
		ns, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || ns <= 0 {
			return time.Time{}, fmt.Errorf("%w: %s zorunlu (unix ns)", errBadRequest, name)
		}
		return time.Unix(0, ns), nil
	}
	from, err := parse("from", rawFrom)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	to, err := parse("to", rawTo)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: from, to'dan önce olmalı", errBadRequest)
	}
	if to.Sub(from) > tracePodMaxWindow {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: pencere en çok %d saat", errBadRequest, int(tracePodMaxWindow.Hours()))
	}
	return from, to, nil
}
