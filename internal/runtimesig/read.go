package runtimesig

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// read.go — profil çözümleme + sınırlı okuma.
//
// Bütçe (MV-first kuralının metrik ikizi): servis başına TEK katalog okuması
// (ListMetricNames, servis süzgeçli) + çözülen dedektör sinyali başına TEK
// sorgu (≤ len(detectSignals) = 9), pencere 60 dk, adım 60 s (≤ 62 nokta).
// Filo okuması (evaluator) aynı sayıda sorgu, servis groupBy'ın başında.

// MetricReader — api metricSource / mcptools.MetricSource / vmetrics.Service
// ortak yüzü (imza chstore.Store ile aynı).
type MetricReader interface {
	ListMetricNames(ctx context.Context, service, pattern string, limit, offset int) ([]chstore.MetricInfo, int, error)
	QueryMetric(ctx context.Context, f chstore.MetricQueryFilter) ([]chstore.SpanMetricSeries, error)
}

// PodGroupBy — pod kimliği adayları (UI POD_GROUP ile aynı sıra; resource.
// öneki VM'de düşer, CH'de res_keys'e çözülür). Follow-up 2026-11-21
// (operatör, prod gerçeği): VM serileri host_name (= pod adı) TAŞIR — pod
// boyutu önce host_name, sonra k8s_pod_name, son çare instance id. Pod
// etiketi hiç yoksa seri anonimdir (pod "") ve servis toplamı yedeğine düşer.
var PodGroupBy = []string{"resource.host.name", "resource.k8s.pod.name", "resource.service.instance.id"}

// detectSignals — dedektörlerin okuduğu sinyaller (UI'nin çizdiği fazlası
// okunmaz).
var detectSignals = []SignalKey{SigGCTime, SigGCCollections, SigPoolQueue, SigPoolCompleted, SigThreads, SigLockContention, SigExceptions, SigHeapUsed, SigWorkingSet}

// Catalog — servisin runtime metrik adları (tek okuma). service "" = filo.
func Catalog(ctx context.Context, r MetricReader, service string) ([]string, error) {
	infos, _, err := r.ListMetricNames(ctx, service, "", 0, 0)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, 64)
	for _, m := range infos {
		if IsRuntimeMetric(m.Name) {
			out = append(out, m.Name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func podOf(gk []string, off int) string {
	for i := off; i < off+3 && i < len(gk); i++ {
		if v := gk[i]; v != "" {
			if i == off+2 && len([]rune(v)) > 8 {
				return string([]rune(v)[:8])
			}
			return v
		}
	}
	return ""
}

// ReadPods — çözülmüş profilden pod başına seriler. service "" = filo
// (groupBy başına service.name). Tek sinyal okuması düşerse not düşülür, öteki
// sinyaller sürer; hepsi düşerse hata.
func ReadPods(ctx context.Context, r MetricReader, service string, p Profile, now time.Time) ([]PodData, []string, error) {
	return readPodsWith(ctx, r, service, p, now, readOpts{})
}

// readOpts — follow-up 2026-11-21: okuma kapsamı. keys nil = runtime'ın
// dedektör girdileri; filters = ortam kapsamı / pod süzgeci (her sorguya
// eklenir); noServiceFilter = seri service_name taşımaz (HikariCP), servis
// yalnız anahtar olarak kullanılır.
type readOpts struct {
	keys            []SignalKey
	filters         []chstore.FilterExpr
	noServiceFilter bool
}

func readPodsWith(ctx context.Context, r MetricReader, service string, p Profile, now time.Time, o readOpts) ([]PodData, []string, error) {
	from := now.Add(-time.Duration(AnalysisWindowNs))
	keys := o.keys
	if keys == nil {
		keys = detectSignalsFor(p.Runtime)
	}
	type pk struct{ svc, pod string }
	pods := map[pk]*PodData{}
	get := func(svc, pod string) *PodData {
		k := pk{svc, pod}
		if d, ok := pods[k]; ok {
			return d
		}
		d := &PodData{Service: svc, Pod: pod, Series: map[SignalKey][]Point{}}
		pods[k] = d
		return d
	}
	var notes []string
	tried, failed := 0, 0
	for _, key := range keys {
		res, ok := p.Get(key)
		if !ok {
			continue
		}
		tried++
		gb := append([]string{}, PodGroupBy...)
		off := 0
		if service == "" {
			gb = append([]string{"service.name"}, gb...)
			off = 1
		}
		genIdx := -1
		if res.GenLabel != "" {
			genIdx = len(gb)
			gb = append(gb, res.GenLabel)
		}
		f := chstore.MetricQueryFilter{
			Name: res.Metric, Service: service, GroupBy: gb, Aggregation: res.Agg,
			From: from, To: now, StepSeconds: 60, MaxDataPoints: int(AnalysisWindowNs/60e9) + 2,
			PlainSeries: true,
		}
		if o.noServiceFilter {
			f.Service = ""
		}
		f.Filters = append([]chstore.FilterExpr{}, o.filters...)
		if res.FilterKey != "" {
			f.Filters = append(f.Filters, chstore.FilterExpr{Key: res.FilterKey, Op: "=", Values: []string{res.FilterValue}})
		}
		if len(f.Filters) == 0 {
			f.Filters = nil
		}
		series, err := r.QueryMetric(ctx, f)
		if err != nil {
			failed++
			notes = append(notes, fmt.Sprintf("%s (%s) okunamadı: %v", key, res.Metric, err))
			continue
		}
		// Pod başına zaman damgasında toplam (nesil / havuz etiketi toplanır).
		type acc struct{ m map[int64]float64 }
		sum := map[pk]*acc{}
		gen2 := map[pk]*acc{}
		add := func(m map[pk]*acc, k pk, t int64, v float64) {
			a := m[k]
			if a == nil {
				a = &acc{m: map[int64]float64{}}
				m[k] = a
			}
			a.m[t] += v
		}
		for _, s := range series {
			svc := service
			if service == "" && len(s.GroupKey) > 0 {
				svc = s.GroupKey[0]
			}
			pod := podOf(s.GroupKey, off)
			if svc == "" {
				continue
			}
			k := pk{svc, pod}
			isGen2 := genIdx >= 0 && genIdx < len(s.GroupKey) && s.GroupKey[genIdx] == "gen2"
			for _, pt := range s.Points {
				v := pt.Value * res.Scale
				add(sum, k, pt.Time, v)
				if key == SigGCCollections && isGen2 {
					add(gen2, k, pt.Time, v)
				}
			}
		}
		toPts := func(a *acc) []Point {
			out := make([]Point, 0, len(a.m))
			for t, v := range a.m {
				out = append(out, Point{T: t, V: v})
			}
			sort.Slice(out, func(i, j int) bool { return out[i].T < out[j].T })
			return out
		}
		// Pod kimliği yok (host_name / k8s_pod_name / service_instance_id
		// taşımayan seriler; prod VM host_name taşır — bu yalnız yedek) →
		// `sum(rate)` servisin TÜM pod'larının toplamıdır; GC PAYI (mutlak
		// eşik) pod sayısıyla bölünmezse 10 pod × %5 = %50 okunurdu. Aynı
		// gruplamayla tek `count` sorgusu seri sayısını verir, pay ortalamaya
		// iner. Oran tabanlı kontroller (sıçrama, kuyruk büyümesi) etkilenmez.
		if key == SigGCTime {
			anon := false
			for k := range sum {
				if k.pod == "" {
					anon = true
				}
			}
			if anon {
				cf := f
				cf.Aggregation = "count"
				if cs, cerr := r.QueryMetric(ctx, cf); cerr == nil {
					cnt := map[pk]map[int64]float64{}
					for _, s := range cs {
						svc := service
						if service == "" && len(s.GroupKey) > 0 {
							svc = s.GroupKey[0]
						}
						k := pk{svc, podOf(s.GroupKey, off)}
						if cnt[k] == nil {
							cnt[k] = map[int64]float64{}
						}
						for _, pt := range s.Points {
							cnt[k][pt.Time] += pt.Value
						}
					}
					for k, a := range sum {
						if k.pod != "" {
							continue
						}
						for t, v := range a.m {
							if n := cnt[k][t]; n > 1 {
								a.m[t] = v / n
							}
						}
					}
				} else {
					notes = append(notes, fmt.Sprintf("gc_time pod sayısı okunamadı (pay pod toplamı olabilir): %v", cerr))
				}
			}
		}
		for k, a := range sum {
			get(k.svc, k.pod).Series[key] = toPts(a)
		}
		for k, a := range gen2 {
			get(k.svc, k.pod).Gen2 = toPts(a)
		}
	}
	if tried > 0 && failed == tried {
		return nil, notes, fmt.Errorf("runtime sinyalleri okunamadı (%d/%d sorgu düştü)", failed, tried)
	}
	out := make([]PodData, 0, len(pods))
	for _, d := range pods {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].Pod < out[j].Pod
	})
	return out, notes, nil
}

// Report — servisin runtime görünümü (API + CoSRE ortak gövde).
type Report struct {
	Service string   `json:"service"`
	Mode    string   `json:"mode"` // off | shadow | on
	Profile Profile  `json:"profile"`
	Pods    int      `json:"pods"`
	Signals []Signal `json:"signals"`
	Notes   []string `json:"notes,omitempty"`
	AtNs    int64    `json:"atNs"`
	// PodEntities — follow-up 2026-11-21: servisin varlık katmanındaki pod'ları
	// (pod adı = metrikteki host_name → entity kaydı). UI pod çizgisini / pod
	// bağlantısını bununla kurar; ad eşlemesi yok. API doldurur (best-effort).
	PodEntities map[string]PodEntityRef `json:"podEntities,omitempty"`
	// ScopeFilters — follow-up 2026-11-21: uygulanmış ortam / küme kapsamı
	// (UI grafik sorgularına AYNEN ekler — tek çözüm, iki yüz). ScopeNote:
	// istendi ama uygulanamadı (tüm ortamlar okundu).
	ScopeFilters []chstore.FilterExpr `json:"scopeFilters,omitempty"`
	ScopeNote    string               `json:"scopeNote,omitempty"`
}

// Analyze — tek servis: katalog → tespit → çözümleme → okuma → dedektör.
// Kip off ise yalnız profil (grafikler için) döner, sinyal okunmaz.
// Dedektör yalnız .NET'te koşar (JVM'in GC kuralları evaluator'da ayrı).
func Analyze(ctx context.Context, r MetricReader, service, language, runtimeName string, th Thresholds, now time.Time) (Report, error) {
	return AnalyzeWith(ctx, r, service, language, runtimeName, th, now, AnalyzeOpts{})
}

// AnalyzeOpts — follow-up 2026-11-21.
type AnalyzeOpts struct {
	// Pods — servisin varlık pod'ları (pod adı = host_name). Doluysa HikariCP
	// serileri bu pod'larla okunur (service_name taşımazlar).
	Pods []string
	// Filters — uygulanmış ortam / küme kapsamı (her sorguya eklenir).
	Filters []chstore.FilterExpr
	// ScopeFor — follow-up 2026-11-21: ortam / küme kapsamı, çözülen ilk
	// runtime metriğinin KENDİ etiket değerleriyle (API: resolveMetricEnvScope
	// Mapped). Filters'a eklenir; uygulanamazsa note (kapsamsız okunur).
	ScopeFor func(ctx context.Context, probe string) (filters []chstore.FilterExpr, note string)
}

// AnalyzeWith — Analyze + pod kapsamlı HikariCP + ortam kapsamı.
func AnalyzeWith(ctx context.Context, r MetricReader, service, language, runtimeName string, th Thresholds, now time.Time, o AnalyzeOpts) (Report, error) {
	rep := Report{Service: service, Mode: th.Mode, Signals: []Signal{}, AtNs: now.UnixNano()}
	cat, err := Catalog(ctx, r, service)
	if err != nil {
		return rep, err
	}
	rt := DetectRuntime(language, runtimeName, cat)
	rep.Profile = Resolve(rt, cat)
	if o.ScopeFor != nil && len(rep.Profile.Signals) > 0 {
		fs, note := o.ScopeFor(ctx, rep.Profile.Signals[0].Metric)
		if note != "" {
			rep.ScopeNote = note
		}
		if len(fs) > 0 {
			rep.ScopeFilters = fs
			o.Filters = append(append([]chstore.FilterExpr{}, o.Filters...), fs...)
		}
	}
	// HikariCP: katalogda servis süzgeciyle görünmez; varlık pod'larıyla
	// çözülür + okunur (kip off'ta yalnız çözüm — grafikler çizer).
	var hikariPods []PodData
	if len(o.Pods) > 0 {
		hres, hdata, hnotes, herr := readHikariFor(ctx, r, service, o, now, th.Mode != chstore.RuntimeSignalsOff)
		rep.Profile.Signals = append(rep.Profile.Signals, hres...)
		rep.Notes = append(rep.Notes, hnotes...)
		if herr != nil {
			rep.Notes = append(rep.Notes, "HikariCP okunamadı: "+herr.Error())
		}
		hikariPods = hdata
	}
	if th.Mode == chstore.RuntimeSignalsOff || (!hasDetectSignal(rep.Profile) && len(hikariPods) == 0) {
		return rep, nil
	}
	var pods []PodData
	if hasDetectSignal(rep.Profile) {
		var notes []string
		pods, notes, err = readPodsWith(ctx, r, service, rep.Profile, now, readOpts{filters: o.Filters})
		rep.Notes = append(rep.Notes, notes...)
		if err != nil {
			return rep, err
		}
	}
	pods = mergePods(pods, hikariPods)
	rep.Pods = len(pods)
	if sigs := Evaluate(pods, now.UnixNano(), th); sigs != nil {
		rep.Signals = sigs
	}
	return rep, nil
}

// ReadFleetDotnet — filo geneli .NET pod okuması (evaluator, yalnız VM).
// Katalog tek okuma ("dotnet" alt dizesi, ≤ 1000 ad); iki kuşak AYRI çözülür
// ve okunur (≤ 2 × 9 sorgu), pod'lar (servis, pod) anahtarında birleşir.
func ReadFleetDotnet(ctx context.Context, r MetricReader, now time.Time) ([]PodData, []string, error) {
	infos, _, err := r.ListMetricNames(ctx, "", "dotnet", 1000, 0)
	if err != nil {
		return nil, nil, err
	}
	cat := make([]string, 0, len(infos))
	for _, m := range infos {
		cat = append(cat, m.Name)
	}
	type pk struct{ svc, pod string }
	merged := map[pk]*PodData{}
	var notes []string
	read := 0
	for _, gen := range []string{GenSemconv, GenInstrumentation} {
		p := ResolveGen(RuntimeDotnet, cat, gen)
		if len(p.Signals) == 0 {
			continue
		}
		pods, n, err := ReadPods(ctx, r, "", p, now)
		notes = append(notes, n...)
		if err != nil {
			return nil, notes, err
		}
		read++
		for _, d := range pods {
			k := pk{d.Service, d.Pod}
			cur, ok := merged[k]
			if !ok {
				c := d
				merged[k] = &c
				continue
			}
			for s, pts := range d.Series {
				if len(cur.Series[s]) == 0 {
					cur.Series[s] = pts
				}
			}
			if len(cur.Gen2) == 0 {
				cur.Gen2 = d.Gen2
			}
		}
	}
	out := make([]PodData, 0, len(merged))
	for _, d := range merged {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].Pod < out[j].Pod
	})
	return out, notes, nil
}
