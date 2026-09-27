package thanos

// trace_pods.go — v0.10.968 — Trace › Metrics yeniden tasarımının Thanos
// yarısı (GET /api/trace-pods/metrics; spec §2, apiContract A–B).
//
// Neden böyle: bir trace'in pod'ları TEK istekte, pod sayısından BAĞIMSIZ
// sabit maliyetle okunur — iki query_range (R1 CPU, R2 bellek; zorunlu) +
// altı instant (I1 limit, I2 istek, I3 restart, I4 son sonlanma sebebi,
// I5 faz, I6 son sonlanma zamanı; best-effort). 64 pod × 2 cluster = 2
// HTTP isteği; seçici hedefli pod=~"^(a|b|…)$" (PodNamesRegex), yani
// topk kesmesi yok ve düşük trafikli pod cluster-geneli sıralamada
// kaybolmaz (v0.9.536 dersi).
//
// Neden öteki türlü DEĞİL:
//   - Pod başına fan-out (bugünkü /api/clusters/pods/detail × N, ~67
//     istek) REDDEDİLDİ: maliyet pod sayısıyla büyüyordu, 40 pod tavanı
//     ve "Kesildi" durumu bu yüzden vardı; tavan kalkınca ne sınır ne
//     kesme kalır.
//   - deploy-trend yolu KULLANILMAZ: deployment kapsamı trace'teki pod
//     kümesiyle aynı değil (trace dışı replikalar, ad-öneki sezgiseli).
//   - Kısmi range verisi DÖNMEZ: R1 ya da R2 düşerse çağrı hata döner —
//     yarım tablo "bu pod'da CPU yok" diye yalan söylerdi (v0.9.363
//     görünür-hata sözleşmesi). Instant ailesi ise bilinçli best-effort:
//     kube-state-metrics'siz stack'te grafikler yine gelir, Instant
//     "partial"/"failed" ile İLAN edilir, envanter "unknown" olur.
//   - Cluster eşlemesi burada YAPILMAZ: çağıran (api katmanı) span cluster
//     değerini SpanClusterOwner + Enabled ile eşler; bu dosya yalnız
//     verilmiş ClusterConfig'e sorar. Her istek doQuery'den geçer, yani
//     paylaşımlı querier'da cluster matcher enjeksiyonu otomatiktir.
//
// Limitler "şu an" değeridir (instant) — trace anındaki limit ertelendi
// (spec §1 "Limits are now values").

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// v0.10.968 — istek başına tavanlar. FE parçalayıcısı (traceMetricsModel.ts
// TRACE_POD_CHUNK_MAX / TRACE_POD_CHUNK_REGEX_MAX) aynı sayılarla böler;
// api/trace_pod_metrics_test.go iki dili çiviler (route_pins emsali).
const TracePodChunkMax = 64   // FE: TRACE_POD_CHUNK_MAX
const TracePodRegexMax = 4000 // FE: TRACE_POD_CHUNK_REGEX_MAX (== podNamesRegexMaxLen)

// tracePodInstantBudget — v0.10.968 — instant zincirinin kendi alt-süresi.
// Handler deadline'ı 10 s; zincir R1/R2 ile paralel koşar ve en geç 6 s'de
// kesilir ki yavaş kube-state-metrics grafikleri rehin tutmasın. Testler
// kısaltır (paket değişkeni).
var tracePodInstantBudget = 6 * time.Second

// TracePodRef — v0.10.968 — istenen bir pod. Namespace boş olabilir (span'de
// k8s.namespace.name yoksa); o zaman ad ile eşlenir (buildTracePodRows).
type TracePodRef struct{ Namespace, Pod string }

// TracePodSeries — v0.10.968 — ref başına bir çıktı satırı (istek sırası).
type TracePodSeries struct {
	Namespace      string     `json:"ns"`
	NsFilled       bool       `json:"nsFilled,omitempty"`
	Pod            string     `json:"pod"`
	State          string     `json:"state"`         // "ok" | "no_samples" | "ambiguous"
	CPU            []*float64 `json:"cpu,omitempty"` // cores; len == Points; nil = gap
	Mem            []*float64 `json:"mem,omitempty"` // bytes
	CPULimit       *float64   `json:"cpuLimit,omitempty"`
	MemLimit       *float64   `json:"memLimit,omitempty"`
	CPURequest     *float64   `json:"cpuRequest,omitempty"`
	MemRequest     *float64   `json:"memRequest,omitempty"`
	Inventory      string     `json:"inventory"` // "present" | "absent" | "unknown"
	Phase          string     `json:"phase,omitempty"`
	Restarts       *int       `json:"restarts,omitempty"`
	LastTermReason string     `json:"lastTermReason,omitempty"`
	LastTermAt     int64      `json:"lastTermAt,omitempty"` // unix s
}

// TracePodMetrics — v0.10.968 — bir (cluster, pod kümesi, pencere) cevabı.
type TracePodMetrics struct {
	Start   int64            `json:"start"` // unix s, bucket 0, aligned to Step
	Step    int              `json:"step"`  // s
	Points  int              `json:"points"`
	Instant string           `json:"instant"` // "ok" | "partial" | "failed"
	Pods    []TracePodSeries `json:"pods"`
}

// TracePodNames — v0.10.968 — ref'lerin TEKİL pod adları, ilk görülme
// sırasıyla. Aynı ad iki namespace'te olabilir (StatefulSet "x-0"); regex'e
// bir kez girer. SAF; handler'ın seçici-tavan denetimi de bunu kullanır ki
// iki taraf aynı uzunluğu ölçsün.
func TracePodNames(refs []TracePodRef) []string {
	out := make([]string, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, r := range refs {
		if r.Pod == "" || seen[r.Pod] {
			continue
		}
		seen[r.Pod] = true
		out = append(out, r.Pod)
	}
	return out
}

// tracePodQuerySet — v0.10.968 — sekiz ifadenin tamamı; hepsi AYNI seçiciyi
// taşır.
type tracePodQuerySet struct {
	CPU, Mem                                              string // R1, R2 (range)
	Limits, Requests, Restarts, TermReason, Phase, TermAt string // I1–I6 (instant)
}

// tracePodQueries — v0.10.968 — SAF sorgu kurucu (tablo-testli).
//
// Seçici: pod=~"<PodNamesRegex>" + NS + F.
//   - NS: HER ref namespace taşıyorsa `,namespace=~"a|b"` (QuoteMeta'lı,
//     tekil, ilk görülme sırası). Tek bir namespace'siz ref bile varsa NS
//     YOK — o ref'in pod'u hangi namespace'teyse bulunmalı; tam (ns,pod)
//     eşlemesi Go tarafında yapılır.
//   - F: cluster'ın NamespaceFilter kalkanı (nsMatcher) — her zaman.
//
// İkinci dönüş: seçici TracePodRegexMax'ı aştı (PodNamesRegex truncated);
// çağıran bu durumda Thanos'a GİTMEZ.
func tracePodQueries(refs []TracePodRef, nsFilter string) (tracePodQuerySet, bool) {
	re, truncated := PodNamesRegex(TracePodNames(refs))
	allNS := len(refs) > 0
	nss := make([]string, 0, len(refs))
	seenNS := map[string]bool{}
	for _, r := range refs {
		if r.Namespace == "" {
			allNS = false
			break
		}
		if !seenNS[r.Namespace] {
			seenNS[r.Namespace] = true
			nss = append(nss, regexp.QuoteMeta(r.Namespace))
		}
	}
	sel := fmt.Sprintf(`pod=~"%s"`, escapeLabelValue(re))
	if allNS {
		sel += fmt.Sprintf(`,namespace=~"%s"`, escapeLabelValue(strings.Join(nss, "|")))
	}
	sel += nsMatcher(nsFilter)
	return tracePodQuerySet{
		CPU:        `sum by (namespace, pod) (rate(container_cpu_usage_seconds_total{container!="",` + sel + `}[5m]))`,
		Mem:        `sum by (namespace, pod) (container_memory_working_set_bytes{container!="",` + sel + `})`,
		Limits:     `sum by (namespace, pod, resource) (kube_pod_container_resource_limits{resource=~"cpu|memory",` + sel + `})`,
		Requests:   `sum by (namespace, pod, resource) (kube_pod_container_resource_requests{resource=~"cpu|memory",` + sel + `})`,
		Restarts:   `sum by (namespace, pod) (kube_pod_container_status_restarts_total{` + sel + `})`,
		TermReason: `max by (namespace, pod, reason) (kube_pod_container_status_last_terminated_reason{` + sel + `} == 1)`,
		Phase:      `max by (namespace, pod, phase) (kube_pod_status_phase{` + sel + `} == 1)`,
		TermAt:     `max by (namespace, pod) (kube_pod_container_status_last_terminated_timestamp{` + sel + `})`,
	}, truncated
}

// tracePodWindow — v0.10.968 — SAF kova ızgarası. step = stepForWindowMDP
// (±5/±15 dk → 15 s, ±60 dk → 60 s @ mdp 120); start = from adıma AŞAĞI
// hizalı; son kova = to adıma aşağı hizalı; points = (son-start)/step + 1.
// query_range start'tan başlayıp end'i geçmeyen her adımda değerlendirir,
// yani dönen zaman damgaları tam olarak bu ızgaradır.
func tracePodWindow(from, to time.Time, mdp int) (start int64, step int, points int) {
	step = stepForWindowMDP(from, to, mdp)
	st := int64(step)
	start = from.Unix() - from.Unix()%st
	last := to.Unix() - to.Unix()%st
	if last < start {
		last = start
	}
	return start, step, int((last-start)/st) + 1
}

// tracePodAlign — v0.10.968 — SAF: [ts,"v"] çiftlerini ızgaraya yerleştirir.
// İndeks (ts-start)/step; ızgara dışı ve sayı olmayan (NaN/Inf) örnekler
// atılır, boş kova nil kalır. Hiç örnek yerleşmezse nil döner — "seri var
// ama boş" ile "seri yok" ayrımı no_samples'a böyle ulaşır. round nil ise
// değer aynen.
func tracePodAlign(values [][]json.RawMessage, start int64, step, points int, round func(float64) float64) []*float64 {
	var out []*float64
	for _, pair := range values {
		v, ts, ok := samplePair(pair)
		if !ok || ts < start {
			continue
		}
		idx := (ts - start) / int64(step)
		if idx >= int64(points) {
			continue
		}
		if round != nil {
			v = round(v)
		}
		if out == nil {
			out = make([]*float64, points)
		}
		vv := v
		out[idx] = &vv
	}
	return out
}

// round4Sig — v0.10.968 — CPU çekirdeği 4 anlamlı haneye (0,4123 c); yanıt
// boyutu 64 pod × 121 kova × 2 seride kayda değer, 17 hanelik float gereksiz.
func round4Sig(v float64) float64 {
	if v == 0 {
		return 0
	}
	f, err := strconv.ParseFloat(strconv.FormatFloat(v, 'g', 4, 64), 64)
	if err != nil {
		return v
	}
	return f
}

// tracePodRange — v0.10.968 — bir (ns,pod)'un hizalı range serileri.
type tracePodRange struct{ cpu, mem []*float64 }

// tracePodInstant — v0.10.968 — instant zincirinin sonuçları, anahtar
// ns+"\x00"+pod. Başarısız sorgunun haritası nil kalır (okuma sıfır döner);
// envanter kararı yalnız I5'in başarısına bakar (phaseOK).
type tracePodInstant struct {
	okCount                            int
	phaseOK                            bool
	cpuLimit, memLimit, cpuReq, memReq map[string]float64
	restarts                           map[string]int
	reason, phase                      map[string]string
	termAt                             map[string]int64
}

// tracePodKey — v0.10.968 — seri etiketlerinden ns+"\x00"+pod anahtarı.
func tracePodKey(m map[string]string) string { return m["namespace"] + "\x00" + m["pod"] }

// TracePodMetrics — v0.10.968 — R1 ‖ R2 ‖ (I1→I6 sıralı zincir): en çok 3
// Thanos isteği aynı anda uçuşta. R1/R2'den biri düşerse diğerleri iptal
// edilir ve çağrı hata döner (kısmi veri yok); instant zinciri kendi 6 s
// alt-süresiyle best-effort. Deadline hatası errors.Is(…, DeadlineExceeded)
// ile görünür kalır — handler "zaman aşımı" metnini buna bağlar.
func (s *Service) TracePodMetrics(ctx context.Context, c ClusterConfig, refs []TracePodRef, from, to time.Time, mdp int) (TracePodMetrics, error) {
	if len(refs) == 0 {
		return TracePodMetrics{}, errors.New("en az bir pod gerekli")
	}
	if len(refs) > TracePodChunkMax {
		return TracePodMetrics{}, fmt.Errorf("istek başına en çok %d pod", TracePodChunkMax)
	}
	qs, truncated := tracePodQueries(refs, c.NamespaceFilter)
	if truncated {
		return TracePodMetrics{}, fmt.Errorf("pod listesi seçici tavanını (%d karakter) aşıyor", TracePodRegexMax)
	}
	start, step, points := tracePodWindow(from, to, mdp)
	rangeParams := func(q string) url.Values {
		return url.Values{
			"query": {q},
			"start": {strconv.FormatInt(start, 10)},
			"end":   {strconv.FormatInt(to.Unix(), 10)},
			"step":  {strconv.Itoa(step)},
		}
	}

	gctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg             sync.WaitGroup
		cpuSer, memSer []promSeries
		cpuErr, memErr error
		inst           *tracePodInstant
	)
	wg.Add(3)
	go func() {
		defer wg.Done()
		if cpuSer, cpuErr = s.doQuery(gctx, c, "/api/v1/query_range", rangeParams(qs.CPU)); cpuErr != nil {
			cancel() // kardeş sorgular boşuna beklemesin
		}
	}()
	go func() {
		defer wg.Done()
		if memSer, memErr = s.doQuery(gctx, c, "/api/v1/query_range", rangeParams(qs.Mem)); memErr != nil {
			cancel()
		}
	}()
	go func() {
		defer wg.Done()
		inst = s.tracePodInstantChain(gctx, c, qs)
	}()
	wg.Wait()
	if err := tracePodRangeErr(c, cpuErr, memErr); err != nil {
		return TracePodMetrics{}, err
	}

	rng := map[string]*tracePodRange{}
	get := func(k string) *tracePodRange {
		r := rng[k]
		if r == nil {
			r = &tracePodRange{}
			rng[k] = r
		}
		return r
	}
	for _, ser := range cpuSer {
		if a := tracePodAlign(ser.Values, start, step, points, round4Sig); a != nil {
			get(tracePodKey(ser.Metric)).cpu = a
		}
	}
	for _, ser := range memSer {
		if a := tracePodAlign(ser.Values, start, step, points, math.Round); a != nil {
			get(tracePodKey(ser.Metric)).mem = a
		}
	}

	status := "partial"
	switch inst.okCount {
	case 6:
		status = "ok"
	case 0:
		status = "failed"
	}
	return TracePodMetrics{
		Start: start, Step: step, Points: points, Instant: status,
		Pods: buildTracePodRows(refs, rng, inst, points),
	}, nil
}

// tracePodRangeErr — v0.10.968 — iki range hatasından GERÇEK olanı seçer:
// biri düşünce diğerini biz iptal ederiz ve o context.Canceled döner; operatöre
// gösterilecek metin ilk düşenin metnidir. Sorgu adı öne eklenir ("CPU
// sorgusu: …") — hangi ailenin düştüğü teşhisin yarısıdır.
//
// v0.10.968 — SIZINTI KURALI (console.go) bu uca da uygulanır: istemciye
// giden metin ham doQuery hatası DEĞİL, tracePodQueryError.reason'dır
// (taşıma hatası kaba sebebe iner, upstream gövdesi yalnız JSON ise
// errorType + error olarak geçer, uç nokta maskelenir). Ham hata yalnız
// sunucu loguna; Unwrap ham hatayı verir ki handler'ın DeadlineExceeded /
// Canceled dalları çalışsın.
func tracePodRangeErr(c ClusterConfig, cpuErr, memErr error) error {
	wrap := func(name string, err error) error {
		if !errors.Is(err, context.Canceled) {
			log.Printf("[thanos] trace-pods %s %s sorgusu: %v", c.Name, name, err)
		}
		return &tracePodQueryError{family: name, reason: tracePodErrReason(c, err), err: err}
	}
	switch {
	case cpuErr != nil && !errors.Is(cpuErr, context.Canceled):
		return wrap("CPU", cpuErr)
	case memErr != nil && !errors.Is(memErr, context.Canceled):
		return wrap("bellek", memErr)
	case cpuErr != nil:
		return wrap("CPU", cpuErr)
	case memErr != nil:
		return wrap("bellek", memErr)
	}
	return nil
}

// tracePodQueryError — v0.10.968 — range sorgusu hatasının İSTEMCİYE
// gidebilen yüzü: "<aile> sorgusu: <temiz sebep>". Ham hata yalnız Unwrap
// ile (errors.Is nöbetçileri için), metne hiç girmez.
type tracePodQueryError struct {
	family string
	reason string
	err    error
}

func (e *tracePodQueryError) Error() string { return e.family + " sorgusu: " + e.reason }
func (e *tracePodQueryError) Unwrap() error { return e.err }

// v0.10.968 — doQuery'nin "HTTP <kod>: <gövde≤200>" biçimi. Gövde 200
// baytta kesildiği için JSON bozuk gelebilir; alanlar o zaman düzenli
// ifadeyle (kesik "error" dahil) okunur.
var (
	tracePodHTTPErrRe  = regexp.MustCompile(`^HTTP (\d{3}): (.*)$`)
	tracePodErrTypeRe  = regexp.MustCompile(`"errorType"\s*:\s*"([^"]*)"`)
	tracePodErrFieldRe = regexp.MustCompile(`"error"\s*:\s*"((?:[^"\\]|\\.)*)`)
)

// tracePodErrReason — v0.10.968 — SAF: ham doQuery hatası → istemciye
// gidebilen kısa sebep (SIZINTI KURALI, console.go):
//   - taşıma hatası (*url.Error: uç nokta URL'si + tam PromQL taşır) →
//     transportReason ("connection failed", "DNS lookup failed", "TLS …");
//   - "thanos <ad>: " öneki atılır (handler cluster adını zaten yazar);
//   - "HTTP NNN: {json}" → "HTTP NNN <errorType>: <error>"; JSON değilse
//     gövde YANKILANMAZ (ingress HTML'i host taşıyabilir) → "HTTP NNN";
//   - 200 + status≠success → "<errorType>: <error>";
//   - çözme hatası → "yanıt çözülemedi" (ham gövde parçası taşımaz);
//   - sonuç scrubEndpoint'ten geçer (upstream metni host yankılayabilir).
func tracePodErrReason(c ClusterConfig, err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return transportReason(err)
	}
	msg := strings.TrimSpace(err.Error())
	if strings.HasPrefix(msg, "thanos "+c.Name+" decode: ") {
		return "yanıt çözülemedi"
	}
	msg = strings.TrimPrefix(msg, "thanos "+c.Name+": ")
	if m := tracePodHTTPErrRe.FindStringSubmatch(msg); m != nil {
		code, body := m[1], strings.TrimSpace(m[2])
		var e struct {
			ErrorType string `json:"errorType"`
			Error     string `json:"error"`
		}
		if json.Unmarshal([]byte(body), &e) != nil {
			if strings.HasPrefix(body, "{") {
				if t := tracePodErrTypeRe.FindStringSubmatch(body); t != nil {
					e.ErrorType = t[1]
				}
				if f := tracePodErrFieldRe.FindStringSubmatch(body); f != nil {
					e.Error = strings.ReplaceAll(f[1], `\"`, `"`) + "…"
				}
			}
		}
		out := "HTTP " + code
		switch {
		case e.ErrorType != "" && e.Error != "":
			out += " " + e.ErrorType + ": " + e.Error
		case e.Error != "":
			out += ": " + e.Error
		case e.ErrorType != "":
			out += " " + e.ErrorType
		}
		return scrubEndpoint(c, out)
	}
	return scrubEndpoint(c, msg)
}

// tracePodInstantChain — v0.10.968 — I1…I6 SIRALI (aynı anda tek instant
// istek), tracePodInstantBudget alt-süresi altında. Her hata yutulur ve
// yalnız okCount'u eksik bırakır; alt-süre dolunca kalan sorgular anında
// düşer (ctx), zincir takılmaz.
func (s *Service) tracePodInstantChain(ctx context.Context, c ClusterConfig, qs tracePodQuerySet) *tracePodInstant {
	ictx, cancel := context.WithTimeout(ctx, tracePodInstantBudget)
	defer cancel()
	out := &tracePodInstant{}
	run := func(q string) ([]promSeries, bool) {
		ser, err := s.doQuery(ictx, c, "/api/v1/query", url.Values{"query": {q}})
		if err != nil {
			return nil, false
		}
		out.okCount++
		return ser, true
	}
	// I1 + I2 — resource etiketiyle cpu / memory ayrılır.
	resourceMaps := func(q string) (cpu, mem map[string]float64) {
		ser, ok := run(q)
		if !ok {
			return nil, nil
		}
		cpu, mem = map[string]float64{}, map[string]float64{}
		for _, x := range ser {
			v, ok := sampleValue(x.Value)
			if !ok {
				continue
			}
			switch x.Metric["resource"] {
			case "cpu":
				cpu[tracePodKey(x.Metric)] = v
			case "memory":
				mem[tracePodKey(x.Metric)] = v
			}
		}
		return cpu, mem
	}
	out.cpuLimit, out.memLimit = resourceMaps(qs.Limits)
	out.cpuReq, out.memReq = resourceMaps(qs.Requests)
	// I3 — restart toplamı. Gerçek 0 da seridir (v0.9.371: 0 ≠ bilinmiyor).
	if ser, ok := run(qs.Restarts); ok {
		out.restarts = map[string]int{}
		for _, x := range ser {
			if v, ok := sampleValue(x.Value); ok {
				out.restarts[tracePodKey(x.Metric)] = int(v)
			}
		}
	}
	// I4 — container başına sebep; pod'a en kötüsü çıkar (worseTermReason).
	if ser, ok := run(qs.TermReason); ok {
		out.reason = map[string]string{}
		for _, x := range ser {
			if r := x.Metric["reason"]; r != "" {
				k := tracePodKey(x.Metric)
				out.reason[k] = worseTermReason(out.reason[k], r)
			}
		}
	}
	// I5 — faz; envanter kararının tek kaynağı. HA'lı KSM ikizleri max by ile
	// düzleşir; yine de iki faz gelirse sözlük sırası kararlı kılar.
	if ser, ok := run(qs.Phase); ok {
		out.phaseOK = true
		out.phase = map[string]string{}
		for _, x := range ser {
			p := x.Metric["phase"]
			if p == "" {
				continue
			}
			k := tracePodKey(x.Metric)
			if cur := out.phase[k]; cur == "" || p < cur {
				out.phase[k] = p
			}
		}
	}
	// I6 — son sonlanma zamanı (metrik eski KSM'lerde yok → boş harita).
	if ser, ok := run(qs.TermAt); ok {
		out.termAt = map[string]int64{}
		for _, x := range ser {
			if v, ok := sampleValue(x.Value); ok && v > 0 {
				out.termAt[tracePodKey(x.Metric)] = int64(v)
			}
		}
	}
	return out
}

// buildTracePodRows — v0.10.968 — SAF ref eşleme (tablo-testli). Ref başına
// TAM bir satır, istek sırasıyla.
//   - Namespace'li ref: (ns,pod) tam eşleşme; başka namespace'e kaymaz.
//   - Namespace'siz ref: aday namespace'ler önce range serilerinden (trace
//     penceresinde koşan pod), yoksa instant serilerinden (şu anki
//     envanter). Tek aday → doldurulur + nsFilled; çok aday → ambiguous,
//     seri ve şu-an alanı YOK (hangisi olduğunu bilmiyoruz), envanter
//     unknown. Range önce gelir: StatefulSet adları ("x-0") namespace'ler
//     arasında tekrarlar; bugün başka namespace'te aynı adla koşan pod,
//     trace anındaki tekil eşleşmeyi belirsizleştirmemeli.
//   - Range serisi yok → no_samples. ok satırda cpu ve mem İKİSİ de
//     points uzunluğunda (eksik aile tümü-nil dizi).
func buildTracePodRows(refs []TracePodRef, rng map[string]*tracePodRange, inst *tracePodInstant, points int) []TracePodSeries {
	if inst == nil {
		inst = &tracePodInstant{}
	}
	rangeNS := nsCandidates(func(yield func(string)) {
		for k := range rng {
			yield(k)
		}
	})
	instNS := nsCandidates(func(yield func(string)) {
		for _, m := range []map[string]float64{inst.cpuLimit, inst.memLimit, inst.cpuReq, inst.memReq} {
			for k := range m {
				yield(k)
			}
		}
		for k := range inst.restarts {
			yield(k)
		}
		for _, m := range []map[string]string{inst.reason, inst.phase} {
			for k := range m {
				yield(k)
			}
		}
		for k := range inst.termAt {
			yield(k)
		}
	})

	out := make([]TracePodSeries, 0, len(refs))
	for _, ref := range refs {
		row := TracePodSeries{Namespace: ref.Namespace, Pod: ref.Pod, Inventory: "unknown"}
		ns := ref.Namespace
		if ns == "" {
			cands := rangeNS[ref.Pod]
			if len(cands) == 0 {
				cands = instNS[ref.Pod]
			}
			if len(cands) > 1 {
				row.State = "ambiguous"
				out = append(out, row)
				continue
			}
			if len(cands) == 1 {
				ns = cands[0]
				row.Namespace, row.NsFilled = ns, true
			}
		}
		key := ns + "\x00" + ref.Pod
		if r := rng[key]; r != nil && (r.cpu != nil || r.mem != nil) {
			row.State = "ok"
			row.CPU, row.Mem = r.cpu, r.mem
			if row.CPU == nil {
				row.CPU = make([]*float64, points)
			}
			if row.Mem == nil {
				row.Mem = make([]*float64, points)
			}
		} else {
			row.State = "no_samples"
		}
		applyTracePodInstant(&row, key, inst)
		out = append(out, row)
	}
	return out
}

// applyTracePodInstant — v0.10.968 — şu-an alanları; bilinmeyen alan boş
// kalır (omitempty). Sıfır/negatif limit "limit yok" demektir, oran
// paydasına girmesin diye yazılmaz.
func applyTracePodInstant(row *TracePodSeries, key string, inst *tracePodInstant) {
	if inst.phaseOK {
		if p := inst.phase[key]; p != "" {
			row.Inventory, row.Phase = "present", p
		} else {
			row.Inventory = "absent" // pod artık cluster'da yok (eski trace)
		}
	}
	pos := func(m map[string]float64) *float64 {
		if v, ok := m[key]; ok && v > 0 {
			return &v
		}
		return nil
	}
	row.CPULimit, row.MemLimit = pos(inst.cpuLimit), pos(inst.memLimit)
	row.CPURequest, row.MemRequest = pos(inst.cpuReq), pos(inst.memReq)
	if n, ok := inst.restarts[key]; ok {
		row.Restarts = &n
	}
	row.LastTermReason = inst.reason[key]
	row.LastTermAt = inst.termAt[key]
}

// nsCandidates — v0.10.968 — ns\x00pod anahtarlarından pod → sıralı tekil
// namespace listesi.
func nsCandidates(keys func(yield func(string))) map[string][]string {
	set := map[string]map[string]bool{}
	keys(func(k string) {
		ns, pod, ok := strings.Cut(k, "\x00")
		if !ok || pod == "" || ns == "" {
			return
		}
		if set[pod] == nil {
			set[pod] = map[string]bool{}
		}
		set[pod][ns] = true
	})
	out := make(map[string][]string, len(set))
	for pod, nss := range set {
		l := make([]string, 0, len(nss))
		for ns := range nss {
			l = append(l, ns)
		}
		sort.Strings(l)
		out[pod] = l
	}
	return out
}
