package api

// messaging_kafka_tab.go — v0.10.1097 (operatör: "9'u yap" — dördü birden).
// /messaging/topic "Kafka istemcileri" sekmesinin ekleri:
//
//  1. topic süzgeci — YALNIZ sekme metriklerinin serileri `topic` etiketi
//     taşıyorsa. Keşif tek `/api/v1/labels` (vmetrics.KafkaLabelNames), cevap
//     `set=clients` önbelleğinin içinde. Etiket yoksa süzgeç UYGULANMAZ ve
//     cevap `labels.topic=false` der; FE denetimi hiç çizmez (ölü denetim yok).
//  2. pod bazlı görünüm — `view=pod`: sağlık soruları [service.name, <pod>]
//     kırılımıyla; blok başına ilk 12 seri + "diğer N" (blok toplamasıyla
//     birleşik) SUNUCUDA katlanır, tel 13 seriyi aşmaz.
//  3. kısa kaynak notu — `stepSeconds` (sorgunun kullandığı adım, promStep ile
//     aynı saf fonksiyon); FE tek satır yazar, uzun not ipucunda.
//  4. "Bağlantılar" paneli — connection_count (katalogda zaten var; yeni
//     metrik/kardinalite yok) pod başına, üretici + tüketici toplamı; "aktif
//     pod" = son adımda ≥1 bağlantısı olan pod. client_id süzgeci (etiket
//     varsa) sekmenin TÜM panellerini daraltır.
//
// api.go BÜYÜMEYECEK kuralı: değer araması ucu burada, kayıt route_registry
// defteriyle:
//
//	GET /api/messaging/kafka-label-values?label=topic|client_id&q=&from=&to=&limit=
//	    &system=&cluster=&destination=&env=&topicFilter=&clientFilter=   (v0.10.1102)
//
// Rol kapısı YOK (salt-okunur; viewer görür). serveCached 60 s, anahtar TÜM
// girdileri taşır (kafkaLabelValuesKey, test pinli). Pencere ≤ 1 sa'e kırpılır
// (varlık sorusu), limit 1..100, q ≤ 200 karakter — keyfi istek sınırsız
// önbellek girdisi basamaz. v0.10.1102 — öneriler SAYFA kapsamında: istemci
// yalnız sayfa anahtarlarını yollar; üretici/tüketici kümeleri sunucuda
// /api/messaging/clients ile AYNI yoldan türetilir (resolveKafkaServiceScope,
// ≤ 200'er, panellerin service_name=~ eşleştiricisi) + karşı süzgeç; servis
// listesi URL'e binmez (başlık tamponu / virgüllü ad sorunu yok). Türetilen
// kapsam boşsa 400.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func init() {
	registerRoutesExtra("messaging-kafka-tab", (*Server).registerMessagingKafkaTabRoutes)
}

func (s *Server) registerMessagingKafkaTabRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/messaging/kafka-label-values", s.getKafkaLabelValues)
}

const (
	kafkaLabelValuesTTL = 60 * time.Second
	// kafkaPodSeriesCap — pod görünümünde blok başına çizgi tavanı; fazlası
	// tek "diğer N" serisine katlanır (tel ≤ 13 seri).
	kafkaPodSeriesCap = 12
	// kafkaFilterMaxLen — süzgeç değeri / arama metni tavanı (anahtar şişmesin).
	kafkaFilterMaxLen = 200
	kafkaViewPod      = "pod"
)

// kafkaLabelSource — İSTEĞE BAĞLI seam yeteneği (metricNoteSource deseni):
// metrik KÜMESİ üzerinde etiket adı / değer araması. VM tek /api/v1/labels
// ile cevaplar; CH attr_keys'ten temsilî iki metrikle. Yeteneği olmayan
// kaynakta keşif yapılmaz — etiket "yok" sayılır, denetim çizilmez.
type kafkaLabelSource interface {
	KafkaLabelNames(ctx context.Context, metrics []string, from, to time.Time) ([]string, error)
	KafkaLabelValues(ctx context.Context, metrics []string, label, q string, sc vmetrics.KafkaLabelScope, from, to time.Time, limit int) ([]string, error)
}

func (v vmMetricSource) KafkaLabelNames(ctx context.Context, metrics []string, from, to time.Time) ([]string, error) {
	out, err := v.svc.KafkaLabelNames(ctx, metrics, from, to)
	return out, upstream(err)
}

func (v vmMetricSource) KafkaLabelValues(ctx context.Context, metrics []string, label, q string, sc vmetrics.KafkaLabelScope, from, to time.Time, limit int) ([]string, error) {
	out, err := v.svc.KafkaLabelValues(ctx, metrics, label, q, sc, from, to, limit)
	return out, upstream(err)
}

// chKafkaProbeMetrics — CH'de keşif metrik başına bir DISTINCT taraması;
// on üç metrik yerine iki temsilî (bağlantı sayısı her istemcide var).
func chKafkaProbeMetrics(metrics []string) []string {
	out := []string{}
	for _, m := range metrics {
		if strings.HasSuffix(m, ".connection_count") {
			out = append(out, m)
		}
	}
	return out
}

func (c chMetricSource) KafkaLabelNames(ctx context.Context, metrics []string, from, to time.Time) ([]string, error) {
	from, _ = vmetrics.KafkaLabelWindow(from, to)
	seen := map[string]bool{}
	out := []string{}
	for _, m := range chKafkaProbeMetrics(metrics) {
		keys, err := c.store.MetricAttrKeys(ctx, m, "", time.Since(from))
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// v0.10.1102 — CH de sayfa kapsamında: taraf başına (vmetrics.KafkaLabelSides)
// o tarafın temsilî metriği, panellerle AYNI FilterExpr'lerle süzülür.
func (c chMetricSource) KafkaLabelValues(ctx context.Context, metrics []string, label, q string, sc vmetrics.KafkaLabelScope, from, to time.Time, limit int) ([]string, error) {
	from, _ = vmetrics.KafkaLabelWindow(from, to)
	seen := map[string]bool{}
	out := []string{}
	for _, side := range vmetrics.KafkaLabelSides(metrics, label, sc) {
		for _, m := range chKafkaProbeMetrics(side.Metrics) {
			vals, err := c.store.MetricLabelValuesScoped(ctx, m, label, time.Since(from), q, limit, side.Filters)
			if err != nil {
				return nil, err
			}
			for _, v := range vals {
				if v != "" && !seen[v] {
					seen[v] = true
					out = append(out, v)
				}
			}
		}
	}
	sort.Strings(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// kafkaTabLabels — keşfin cevabı. Detected=false: kaynak keşif yapamadı (ya
// da hata) — FE bunu "etiket yok" gibi okur, denetim çizmez.
type kafkaTabLabels struct {
	Detected bool   `json:"detected"`
	Topic    bool   `json:"topic"`
	ClientID bool   `json:"clientId"`
	Pod      string `json:"pod,omitempty"` // kaynağın KENDİ yazımı (k8s_pod_name | pod | …)
}

// kafkaTabFilter — UYGULANAN süzgeç (istenen değil): etiket yoksa boş döner.
type kafkaTabFilter struct {
	Topic    string `json:"topic,omitempty"`
	ClientID string `json:"clientId,omitempty"`
}

// kafkaConnections — "Bağlantılar" paneli.
type kafkaConnections struct {
	PodLabel string                     `json:"podLabel,omitempty"` // boş = pod etiketi yok, seriler servis · istemci
	Series   []chstore.SpanMetricSeries `json:"series"`
	Folded   int                        `json:"folded,omitempty"` // "diğer N" serisine katlanan seri sayısı
	Total    int                        `json:"total"`            // katlamadan önceki seri (pod) sayısı
	// ActivePods — son adımda ≥1 bağlantısı olan pod; yalnız pod etiketi varken.
	ActivePods *int   `json:"activePods,omitempty"`
	Error      string `json:"error,omitempty"`
}

// detectKafkaTabLabels — SAF: keşfedilen etiket adları → denetim kararı.
// Ad karşılaştırması VM yazımında (LabelName): CH `k8s.pod.name` de VM
// `k8s_pod_name` de aynı adayı tutar; dönen Pod kaynağın kendi yazımıdır
// (kırılım o adla sorulur).
func detectKafkaTabLabels(names []string) kafkaTabLabels {
	byProm := map[string]string{}
	for _, n := range names {
		l := vmetrics.LabelName(n)
		if _, ok := byProm[l]; !ok && l != "" {
			byProm[l] = n
		}
	}
	out := kafkaTabLabels{Detected: true}
	_, out.Topic = byProm["topic"]
	_, out.ClientID = byProm["client_id"]
	for _, c := range vmetrics.KafkaPodLabelCandidates() {
		if orig, ok := byProm[c]; ok {
			out.Pod = orig
			break
		}
	}
	return out
}

// kafkaTab — sekmenin ÇÖZÜLMÜŞ hâli: keşif + istenen süzgeç/görünüm.
type kafkaTab struct {
	Labels   kafkaTabLabels
	Topic    string // uygulanan topic süzgeci ("" = yok)
	ClientID string // uygulanan client_id süzgeci
	View     string // "" (toplam) | pod
}

// resolveKafkaTab — SAF: istenen her şey ancak etiketi VARSA uygulanır.
// Etiketsiz bir süzgeci sorguya koymak paneli sessizce boşaltırdı; görünmeyen
// bir süzgeç ise ekranda olmayan bir daraltma vaat ederdi — ikisi de yasak.
func resolveKafkaTab(labels kafkaTabLabels, topic, clientID, view string) kafkaTab {
	t := kafkaTab{Labels: labels}
	if labels.Topic {
		t.Topic = strings.TrimSpace(topic)
	}
	if labels.ClientID {
		t.ClientID = strings.TrimSpace(clientID)
	}
	if view == kafkaViewPod && labels.Pod != "" {
		t.View = kafkaViewPod
	}
	return t
}

// ExtraLabels — KafkaQuery'nin katalog dışı kabul edeceği etiketler.
func (t kafkaTab) ExtraLabels() []string {
	var out []string
	if t.Topic != "" {
		out = append(out, "topic")
	}
	if t.Labels.Pod != "" {
		out = append(out, t.Labels.Pod)
	}
	return out
}

func (t kafkaTab) Questions() []vmetrics.KafkaQuestion {
	if t.View == kafkaViewPod {
		return vmetrics.KafkaClientHealthPodQuestions(t.Labels.Pod)
	}
	return vmetrics.KafkaClientHealthQuestions()
}

// NoteSuffix — uzun notun (FE'de ipucu) süzgeç/görünüm cümleleri.
func (t kafkaTab) NoteSuffix() string {
	var b strings.Builder
	if t.Topic != "" {
		fmt.Fprintf(&b, " topic=%q süzgeci uygulandı (seriler topic etiketi taşıyor); etiketi taşımayan seri panelde görünmez.", t.Topic)
	}
	if t.ClientID != "" {
		fmt.Fprintf(&b, " client_id=%q süzgeci uygulandı.", t.ClientID)
	}
	if t.View == kafkaViewPod {
		fmt.Fprintf(&b, " Pod bazlı görünüm (%s): blok başına ilk %d pod + \"diğer N\".", t.Labels.Pod, kafkaPodSeriesCap)
	}
	return b.String()
}

// prepareKafkaTab — keşif (tek çağrı) + çözüm. Keşif hatası cevabı düşürmez:
// etiketler "yok" sayılır ve log'a yazılır.
func prepareKafkaTab(ctx context.Context, src metricSource, p messagingClientsPlan) kafkaTab {
	labels := kafkaTabLabels{}
	if ks, ok := src.(kafkaLabelSource); ok {
		names, err := ks.KafkaLabelNames(ctx, vmetrics.KafkaClientsTabMetrics(), p.From, p.To)
		if err != nil {
			log.Printf("[messaging] kafka etiket keşfi: %v", err)
		} else {
			labels = detectKafkaTabLabels(names)
		}
	}
	return resolveKafkaTab(labels, p.TopicFilter, p.ClientFilter, p.View)
}

// combineKafkaSeries — SAF: serileri zaman damgası başına tek noktaya indirir
// (sum | avg | max | min; bilinmeyen/rate → sum). Sıra: zaman.
func combineKafkaSeries(in []chstore.SpanMetricSeries, agg string) []chstore.SpanMetricPoint {
	type acc struct {
		sum, max, min float64
		n             int
	}
	byT := map[int64]*acc{}
	for _, s := range in {
		for _, pt := range s.Points {
			a := byT[pt.Time]
			if a == nil {
				a = &acc{max: pt.Value, min: pt.Value}
				byT[pt.Time] = a
			}
			a.sum += pt.Value
			a.n++
			if pt.Value > a.max {
				a.max = pt.Value
			}
			if pt.Value < a.min {
				a.min = pt.Value
			}
		}
	}
	ts := make([]int64, 0, len(byT))
	for t := range byT {
		ts = append(ts, t)
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
	out := make([]chstore.SpanMetricPoint, 0, len(ts))
	for _, t := range ts {
		a := byT[t]
		v := a.sum
		switch agg {
		case "avg":
			v = a.sum / float64(a.n)
		case "max":
			v = a.max
		case "min":
			v = a.min
		}
		out = append(out, chstore.SpanMetricPoint{Time: t, Value: v})
	}
	return out
}

// foldKafkaSeries — SAF: ≤ cap seri olduğu gibi döner; fazlası son değere
// göre sıralanır (capSeriesByLast — partition tablosuyla aynı sıra), ilk cap
// kalır, geri kalan BLOK TOPLAMASIYLA (sum/avg/max/min; rate → sum) tek
// "diğer N" serisine katlanır. folded = katlanan seri sayısı.
func foldKafkaSeries(in []chstore.SpanMetricSeries, limit int, agg string) ([]chstore.SpanMetricSeries, int) {
	if len(in) <= limit {
		return in, 0
	}
	sorted := capSeriesByLast(in, len(in))
	rest := sorted[limit:]
	out := make([]chstore.SpanMetricSeries, 0, limit+1)
	out = append(out, sorted[:limit]...)
	out = append(out, chstore.SpanMetricSeries{
		GroupKey: []string{fmt.Sprintf("diğer %d", len(rest))},
		Points:   combineKafkaSeries(rest, agg),
	})
	return out, len(rest)
}

// mergeSumSeries — SAF: iki listeyi GroupKey'e göre birleştirir, aynı anahtar
// (aynı pod hem üretici hem tüketici) zaman başına TOPLANIR. Sıra: anahtar.
func mergeSumSeries(a, b []chstore.SpanMetricSeries) []chstore.SpanMetricSeries {
	groups := map[string][]chstore.SpanMetricSeries{}
	keys := map[string][]string{}
	for _, s := range append(append([]chstore.SpanMetricSeries(nil), a...), b...) {
		k := strings.Join(s.GroupKey, "\x00")
		groups[k] = append(groups[k], s)
		keys[k] = s.GroupKey
	}
	ids := make([]string, 0, len(groups))
	for k := range groups {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	out := make([]chstore.SpanMetricSeries, 0, len(ids))
	for _, k := range ids {
		g := groups[k]
		if len(g) == 1 {
			out = append(out, g[0])
			continue
		}
		out = append(out, chstore.SpanMetricSeries{GroupKey: keys[k], Points: combineKafkaSeries(g, "sum")})
	}
	return out
}

// countActiveSeries — SAF: SON ADIMDA (tüm serilerin en geç zaman damgası)
// değeri ≥ 1 olan seri sayısı. Son adımda noktası olmayan pod "aktif" sayılmaz
// — durmuş bir pod'un eski değeri onu canlı göstermesin.
func countActiveSeries(in []chstore.SpanMetricSeries) int {
	var last int64
	found := false
	for _, s := range in {
		for _, pt := range s.Points {
			if !found || pt.Time > last {
				last, found = pt.Time, true
			}
		}
	}
	n := 0
	for _, s := range in {
		for i := len(s.Points) - 1; i >= 0; i-- {
			if s.Points[i].Time == last {
				if s.Points[i].Value >= 1 {
					n++
				}
				break
			}
		}
	}
	return n
}

// foldKafkaConnections — SAF: iki taraf → panel. Seri yok + iki taraf da hata
// → Error; seri yok + hata yok → Total 0 (FE "metrik yok" der).
func foldKafkaConnections(prod, cons kafkaMetricBlock, podLabel string) *kafkaConnections {
	merged := mergeSumSeries(prod.Series, cons.Series)
	c := &kafkaConnections{PodLabel: podLabel, Total: len(merged), Series: []chstore.SpanMetricSeries{}}
	if len(merged) == 0 && prod.Error != "" && cons.Error != "" {
		c.Error = prod.Error + " · " + cons.Error
	}
	if podLabel != "" {
		n := countActiveSeries(merged)
		c.ActivePods = &n
	}
	folded, n := foldKafkaSeries(merged, kafkaPodSeriesCap, "sum")
	if folded != nil {
		c.Series = folded
	}
	c.Folded = n
	return c
}

const (
	kafkaProducerConnKey = "producer_connection_count"
	kafkaConsumerConnKey = "consumer_connection_count"
)

// buildKafkaConnections — panelin seri kaynağı, EK SORGUYU en aza indirerek:
//   - pod etiketi + pod görünümü → sağlık bloklarının bağlantı soruları zaten
//     pod kırılımlı: ek sorgu YOK (katlamadan ÖNCE çağrılmalı);
//   - pod etiketi + toplam görünüm → iki ek soru (connection_count × pod);
//   - pod etiketi yok → sağlık blokları (servis · istemci), ek sorgu yok,
//     "aktif pod" hesaplanmaz.
func buildKafkaConnections(ctx context.Context, src metricSource, t kafkaTab, blocks map[string]kafkaMetricBlock, env string,
	scopeFor func(m vmetrics.KafkaMetric) *vmetrics.KafkaScope) *kafkaConnections {
	pod := t.Labels.Pod
	if pod != "" && t.View != kafkaViewPod {
		extra, _, _ := runKafkaQuestions(ctx, src, vmetrics.KafkaConnectionPodQuestions(pod), env, scopeFor)
		return foldKafkaConnections(extra[vmetrics.KafkaConnProducerKey], extra[vmetrics.KafkaConnConsumerKey], pod)
	}
	return foldKafkaConnections(blocks[kafkaProducerConnKey], blocks[kafkaConsumerConnKey], pod)
}

// kafkaLabelValuesReq — seçici aramasının ayrıştırılmış girdileri.
// v0.10.1102 — Page: sayfanın kapsam anahtarları (system / cluster /
// destination / env / pencere — /api/messaging/clients ile AYNI ayrıştırıcı);
// servis kümeleri istemciden GELMEZ, sunucuda türetilir. Topic / ClientID:
// karşı süzgeç (aranan etiketin kendisi düşer).
type kafkaLabelValuesReq struct {
	Label, Q        string
	Limit           int
	Page            messagingClientsPlan
	Topic, ClientID string
}

// kafkaLabelValuesKey — SAF; regresyon testi tüm girdileri pinler (v0.5.187).
// v3 (v0.10.1102): sayfa anahtarları + TÜRETİLMİŞ taraf kümelerinin sıralı
// FNV özeti (uzunluk değil içerik) + karşı süzgeç.
func kafkaLabelValuesKey(src string, p kafkaLabelValuesReq, sc kafkaServiceScope) string {
	return fmt.Sprintf("kafka-label-values:v3:src=%s:sys=%q:clu=%q:dest=%q:env=%q:label=%s:q=%q:lim=%d:p=%s:c=%s:ft=%q:fc=%q:%s",
		src, p.Page.System, p.Page.Cluster, p.Page.Destination, p.Page.Env, p.Label, p.Q, p.Limit,
		blastRadiusSetDigest(sortedCopyOf(sc.Producers)), blastRadiusSetDigest(sortedCopyOf(sc.Consumers)),
		p.Topic, p.ClientID, cacheBucket(p.Page.From, p.Page.To))
}

// kafkaPageScopeKey — sayfa kapsamının (servis kümeleri) kendi önbellek
// anahtarı: her tuş vuruşu caller SQL'i + iki keşif sorgusunu tekrar koşmasın.
// mx: metrik dışlamaları keşif sorgusunu etkiler (messagingClientsKey emsali).
func kafkaPageScopeKey(src, mx string, p messagingClientsPlan) string {
	return fmt.Sprintf("kafka-page-scope:v1:src=%s:sys=%q:clu=%q:dest=%q:env=%q:mx=%s:%s",
		src, p.System, p.Cluster, p.Destination, p.Env, mx, cacheBucket(p.From, p.To))
}

type kafkaLabelValuesResponse struct {
	Label  string   `json:"label"`
	Source string   `json:"source"`
	Values []string `json:"values"`
}

// kafkaLabelValuesParams — SAF ayrıştırıcı: etiket beyaz listesi (yalnız
// sekmenin süzgeçleri), q tavanı, limit kelepçesi. Hata = 400 metni.
// v0.10.1102 — sayfa anahtarları messagingClientsPlanFrom'dan (system +
// destination zorunlu, cluster varsayılanı, env, pencere): panellerin
// kapsamıyla bayt-aynı girdi. Aranan etiketin kendi süzgeci düşer
// (topicFilter yalnız client_id aranırken, clientFilter yalnız topic aranırken).
func kafkaLabelValuesParams(r *http.Request) (kafkaLabelValuesReq, error) {
	v := r.URL.Query()
	p := kafkaLabelValuesReq{Label: strings.TrimSpace(v.Get("label"))}
	if p.Label != "topic" && p.Label != "client_id" {
		return kafkaLabelValuesReq{}, fmt.Errorf("label parametresi geçersiz: topic | client_id")
	}
	p.Q = strings.TrimSpace(v.Get("q"))
	if len(p.Q) > kafkaFilterMaxLen {
		return kafkaLabelValuesReq{}, fmt.Errorf("q en çok %d karakter", kafkaFilterMaxLen)
	}
	p.Limit = parseInt(v.Get("limit"), 50)
	if p.Limit < 1 || p.Limit > 100 {
		p.Limit = 50
	}
	page, err := messagingClientsPlanFrom(r)
	if err != nil {
		return kafkaLabelValuesReq{}, err
	}
	// Seçiciye ait olmayan plan alanları anahtarı parçalamasın.
	p.Page = messagingClientsPlan{System: page.System, Cluster: page.Cluster, Destination: page.Destination,
		Env: page.Env, From: page.From, To: page.To}
	ft, fc := strings.TrimSpace(v.Get("topicFilter")), strings.TrimSpace(v.Get("clientFilter"))
	if len(ft) > kafkaFilterMaxLen || len(fc) > kafkaFilterMaxLen {
		return kafkaLabelValuesReq{}, fmt.Errorf("süzgeç değeri en çok %d karakter", kafkaFilterMaxLen)
	}
	if p.Label == "client_id" {
		p.Topic = ft
	} else {
		p.ClientID = fc
	}
	return p, nil
}

// errKafkaScopeEmpty — türetilen kapsamda servis yok: filo geneline açılmak
// yerine 400 (panellerin "kapsam boş" hâliyle aynı karar).
var errKafkaScopeEmpty = fmt.Errorf("kapsam boş: bu topic için üretici/tüketici servisi yok (span ve topic etiketli metrik)")

// kafkaLabelScopeFor — SAF: türetilen sayfa kapsamı + karşı süzgeç → seçici
// kapsamı. Servis yoksa hata (400).
func kafkaLabelScopeFor(sc kafkaServiceScope, p kafkaLabelValuesReq) (vmetrics.KafkaLabelScope, error) {
	if len(sc.Producers) == 0 && len(sc.Consumers) == 0 {
		return vmetrics.KafkaLabelScope{}, errKafkaScopeEmpty
	}
	return vmetrics.KafkaLabelScope{Producers: sc.Producers, Consumers: sc.Consumers, Topic: p.Topic, ClientID: p.ClientID}, nil
}

// kafkaPageScope — sayfanın servis kümeleri, /api/messaging/clients ile AYNI
// yoldan (MessagingCallerServices + resolveKafkaServiceScope: span ∪ keşif,
// taraf başına ≤ msgScopeServiceCap); kendi anahtarıyla 60 sn önbellekli.
func (s *Server) kafkaPageScope(r *http.Request, src metricSource, p messagingClientsPlan) (kafkaServiceScope, error) {
	key := kafkaPageScopeKey(src.Name(), s.store.MetricExclusions().Digest(), p)
	body, _, err := s.cachedJSON(r.Context(), key, kafkaLabelValuesTTL, false, func(ctx context.Context) (any, error) {
		callers, err := s.store.MessagingCallerServices(ctx, p.System, p.Cluster, p.Destination, p.From, p.To)
		if err != nil {
			return nil, err
		}
		return resolveKafkaServiceScope(ctx, src, p, callers), nil
	})
	if err != nil {
		return kafkaServiceScope{}, err
	}
	var sc kafkaServiceScope
	if err := json.Unmarshal(body, &sc); err != nil {
		return kafkaServiceScope{}, err
	}
	return sc, nil
}

// getKafkaLabelValues — GET /api/messaging/kafka-label-values (seçici araması).
func (s *Server) getKafkaLabelValues(w http.ResponseWriter, r *http.Request) {
	p, err := kafkaLabelValuesParams(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	src, err := s.metricSourceFor(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	page, err := s.kafkaPageScope(r, src, p.Page)
	if err != nil {
		writeErr(w, err)
		return
	}
	sc, err := kafkaLabelScopeFor(page, p)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.serveCached(w, r, kafkaLabelValuesKey(src.Name(), p, page), kafkaLabelValuesTTL, func(ctx context.Context) (any, error) {
		return buildKafkaLabelValues(ctx, src, p, sc)
	})
}

// buildKafkaLabelValues — handler gövdesi (kaynak arayüzü üzerinden; test
// sahte kaynakla kapsamın iletildiğini pinler).
func buildKafkaLabelValues(ctx context.Context, src metricSource, p kafkaLabelValuesReq, sc vmetrics.KafkaLabelScope) (kafkaLabelValuesResponse, error) {
	resp := kafkaLabelValuesResponse{Label: p.Label, Source: src.Name(), Values: []string{}}
	ks, ok := src.(kafkaLabelSource)
	if !ok {
		return resp, nil
	}
	vals, err := ks.KafkaLabelValues(ctx, vmetrics.KafkaClientsTabMetrics(), p.Label, p.Q, sc, p.Page.From, p.Page.To, p.Limit)
	if err != nil {
		return resp, err
	}
	if vals != nil {
		resp.Values = vals
	}
	return resp, nil
}
