package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// messaging_kafka_tab_test.go — v0.10.1097 ("Kafka istemcileri" sekmesi:
// topic/client_id süzgeci etiket varsa, pod bazlı görünüm, bağlantı paneli).
// Sözleşme:
//   - etiket YOKSA istenen süzgeç/görünüm uygulanmaz (ölü denetim ve görünmez
//     daraltma yasak), cevap uygulananı söyler;
//   - pod görünümü blok başına ≤ 12 seri + "diğer N" (blok toplamasıyla);
//   - bağlantı paneli EK SORGUYU en aza indirir; "aktif pod" son adımda ≥ 1;
//   - süzgeç/görünüm cache anahtarına girer (v0.5.187).

// fakeKafkaLabelSource — keşif yeteneği olan sahte kaynak (VM'i taklit eder).
type fakeKafkaLabelSource struct {
	*fakeEPSource
	labels     []string
	labelCalls int
}

func (f *fakeKafkaLabelSource) KafkaLabelNames(context.Context, []string, time.Time, time.Time) ([]string, error) {
	f.labelCalls++
	return f.labels, nil
}

func (f *fakeKafkaLabelSource) KafkaLabelValues(context.Context, []string, string, string, time.Time, time.Time, int) ([]string, error) {
	return []string{"orders"}, nil
}

// podSeries — n pod, her biri iki adım; değer = sıra (son adımda i).
func podSeries(svc string, n int, from time.Time) []chstore.SpanMetricSeries {
	out := make([]chstore.SpanMetricSeries, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, chstore.SpanMetricSeries{
			GroupKey: []string{svc, "pod-" + string(rune('a'+i))},
			Points: []chstore.SpanMetricPoint{
				{Time: from.UnixNano(), Value: 1},
				{Time: from.Add(time.Minute).UnixNano(), Value: float64(i)},
			},
		})
	}
	return out
}

func groupByOf(f chstore.MetricQueryFilter) string { return strings.Join(f.GroupBy, ",") }

func clientFilterValues(f chstore.MetricQueryFilter) []string {
	for _, fe := range f.Filters {
		if fe.Key == "client_id" {
			return fe.Values
		}
	}
	return nil
}

func TestDetectKafkaTabLabels(t *testing.T) {
	cases := []struct {
		name  string
		in    []string
		topic bool
		cid   bool
		pod   string
	}{
		{"VM: hepsi", []string{"client_id", "k8s_pod_name", "service_name", "topic"}, true, true, "k8s_pod_name"},
		{"VM: topic yok (prod tipik)", []string{"client_id", "k8s_pod_name", "service_name"}, false, true, "k8s_pod_name"},
		{"kube-state yazımı", []string{"client_id", "pod"}, false, true, "pod"},
		{"iki yazım: OTel önce", []string{"pod", "k8s_pod_name"}, false, false, "k8s_pod_name"},
		{"CH yazımı (noktalı) kendi adıyla döner", []string{"client_id", "k8s.pod.name"}, false, true, "k8s.pod.name"},
		{"hiçbiri", []string{"service_name"}, false, false, ""},
		{"boş", nil, false, false, ""},
	}
	for _, c := range cases {
		got := detectKafkaTabLabels(c.in)
		if !got.Detected || got.Topic != c.topic || got.ClientID != c.cid || got.Pod != c.pod {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

func TestResolveKafkaTab(t *testing.T) {
	all := kafkaTabLabels{Detected: true, Topic: true, ClientID: true, Pod: "pod"}
	none := kafkaTabLabels{Detected: true}
	for _, c := range []struct {
		name          string
		labels        kafkaTabLabels
		topic, client string
		view          string
		wantT, wantC  string
		wantV         string
	}{
		{"etiketler var → uygulanır", all, " orders ", "c1", "pod", "orders", "c1", "pod"},
		{"etiket yok → hiçbiri uygulanmaz", none, "orders", "c1", "pod", "", "", ""},
		{"keşif yapılamadı → hiçbiri", kafkaTabLabels{}, "orders", "c1", "pod", "", "", ""},
		{"toplam görünüm", all, "", "", "", "", "", ""},
		{"bilinmeyen görünüm toplama düşer", all, "", "", "grid", "", "", ""},
	} {
		got := resolveKafkaTab(c.labels, c.topic, c.client, c.view)
		if got.Topic != c.wantT || got.ClientID != c.wantC || got.View != c.wantV {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
	ex := resolveKafkaTab(all, "orders", "", "").ExtraLabels()
	if strings.Join(ex, ",") != "topic,pod" {
		t.Fatalf("ExtraLabels: %v", ex)
	}
}

func TestFoldKafkaSeries(t *testing.T) {
	from, _ := kafkaFixtureWindow()
	in := podSeries("svc", 15, from)
	out, folded := foldKafkaSeries(in, 12, "sum")
	if len(out) != 13 || folded != 3 {
		t.Fatalf("12 + diğer bekleniyordu: len=%d folded=%d", len(out), folded)
	}
	// İlk 12 son değere göre azalan: pod-o (14) … pod-d (3); katlanan 2,1,0.
	if out[0].GroupKey[1] != "pod-o" || out[11].GroupKey[1] != "pod-d" {
		t.Fatalf("sıra: ilk=%v 12.=%v", out[0].GroupKey, out[11].GroupKey)
	}
	other := out[12]
	if other.GroupKey[0] != "diğer 3" || len(other.Points) != 2 {
		t.Fatalf("diğer serisi: %+v", other)
	}
	if other.Points[0].Value != 3 || other.Points[1].Value != 3 { // sum: 1+1+1, 2+1+0
		t.Fatalf("sum katlaması: %+v", other.Points)
	}
	mx, _ := foldKafkaSeries(in, 12, "max")
	if mx[12].Points[1].Value != 2 {
		t.Fatalf("max katlaması: %+v", mx[12].Points)
	}
	avg, _ := foldKafkaSeries(in, 12, "avg")
	if avg[12].Points[1].Value != 1 {
		t.Fatalf("avg katlaması: %+v", avg[12].Points)
	}
	same, n := foldKafkaSeries(in[:12], 12, "sum")
	if n != 0 || len(same) != 12 {
		t.Fatalf("≤12 seri aynen dönmeli: %d %d", len(same), n)
	}
}

func TestMergeAndActivePods(t *testing.T) {
	from, _ := kafkaFixtureWindow()
	t1, t2 := from.UnixNano(), from.Add(time.Minute).UnixNano()
	prod := []chstore.SpanMetricSeries{
		{GroupKey: []string{"svc", "pod-a"}, Points: []chstore.SpanMetricPoint{{Time: t1, Value: 1}, {Time: t2, Value: 2}}},
	}
	cons := []chstore.SpanMetricSeries{
		{GroupKey: []string{"svc", "pod-a"}, Points: []chstore.SpanMetricPoint{{Time: t1, Value: 3}, {Time: t2, Value: 1}}},
		// son adımda 0 bağlantı → aktif değil
		{GroupKey: []string{"svc", "pod-b"}, Points: []chstore.SpanMetricPoint{{Time: t1, Value: 4}, {Time: t2, Value: 0}}},
		// son adımda noktası yok (durmuş pod) → aktif değil
		{GroupKey: []string{"svc", "pod-c"}, Points: []chstore.SpanMetricPoint{{Time: t1, Value: 9}}},
	}
	merged := mergeSumSeries(prod, cons)
	if len(merged) != 3 || merged[0].GroupKey[1] != "pod-a" || merged[0].Points[1].Value != 3 {
		t.Fatalf("birleşim: %+v", merged)
	}
	if n := countActiveSeries(merged); n != 1 {
		t.Fatalf("aktif pod = %d, bekl. 1", n)
	}
	c := foldKafkaConnections(kafkaMetricBlock{Series: prod}, kafkaMetricBlock{Series: cons}, "pod")
	if c.Total != 3 || c.ActivePods == nil || *c.ActivePods != 1 || c.Error != "" {
		t.Fatalf("panel: %+v", c)
	}
	// Seri yok, hata yok → Total 0 ("metrik yok"); iki taraf hata → Error.
	empty := foldKafkaConnections(kafkaMetricBlock{}, kafkaMetricBlock{}, "")
	if empty.Total != 0 || empty.Error != "" || empty.ActivePods != nil || empty.Series == nil {
		t.Fatalf("boş panel: %+v", empty)
	}
	bad := foldKafkaConnections(kafkaMetricBlock{Error: "x"}, kafkaMetricBlock{Error: "y"}, "pod")
	if bad.Error == "" {
		t.Fatal("iki taraf hata → Error dolu olmalı")
	}
}

func TestBuildMessagingClientsKafkaTab(t *testing.T) {
	from, _ := kafkaFixtureWindow()
	newSrc := func(labels []string) *fakeKafkaLabelSource {
		return &fakeKafkaLabelSource{labels: labels, fakeEPSource: &fakeEPSource{name: "vm",
			queryFn: func(f chstore.MetricQueryFilter) ([]chstore.SpanMetricSeries, error) {
				if isKafkaDiscoveryQuery(f) {
					return nil, nil
				}
				if len(f.GroupBy) == 2 && f.GroupBy[1] == "k8s_pod_name" {
					return podSeries("svc", 15, from), nil
				}
				return kafkaSeries(1), nil
			}}}
	}
	healthN := len(vmetrics.KafkaClientHealthQuestions())

	t.Run("etiketler var · topic + client_id · pod görünümü", func(t *testing.T) {
		src := newSrc([]string{"client_id", "k8s_pod_name", "service_name", "topic"})
		p := msgSetPlan(msgSetClients)
		p.TopicFilter, p.ClientFilter, p.View = "payments", "consumer-1", "pod"
		resp, err := buildMessagingClients(context.Background(), src, p, msgSetCallers())
		if err != nil {
			t.Fatal(err)
		}
		if src.labelCalls != 1 {
			t.Fatalf("keşif TEK çağrı olmalı: %d", src.labelCalls)
		}
		if resp.Labels == nil || !resp.Labels.Topic || resp.Labels.Pod != "k8s_pod_name" {
			t.Fatalf("labels: %+v", resp.Labels)
		}
		if resp.Filter == nil || resp.Filter.Topic != "payments" || resp.Filter.ClientID != "consumer-1" || resp.View != "pod" {
			t.Fatalf("uygulanan: %+v view=%q", resp.Filter, resp.View)
		}
		if resp.Scope != "topic" || strings.Contains(resp.Note, "SÜZÜLEMEZ") {
			t.Fatalf("topic süzgeciyle kapsam topic, uyarı düşmeli: %q %q", resp.Scope, resp.Note)
		}
		// Pod görünümünde bağlantı paneli blokları okur: ek sorgu YOK.
		if len(src.queries) != healthN+2 {
			t.Fatalf("sorgu sayısı %d, bekl. %d (+2 keşif)", len(src.queries), healthN+2)
		}
		for _, f := range src.queries {
			if isKafkaDiscoveryQuery(f) {
				continue
			}
			if tv := topicFilterValues(f); len(tv) != 1 || tv[0] != "payments" {
				t.Errorf("%s: topic süzgeci %v", f.Name, tv)
			}
			if cv := clientFilterValues(f); len(cv) != 1 || cv[0] != "consumer-1" {
				t.Errorf("%s: client_id süzgeci %v", f.Name, cv)
			}
			if groupByOf(f) != "service.name,k8s_pod_name" {
				t.Errorf("%s: kırılım %v", f.Name, f.GroupBy)
			}
		}
		for k, b := range resp.Blocks {
			if len(b.Series) != 13 || b.Folded != 3 {
				t.Errorf("%s: 12 + diğer bekleniyordu: %d seri, folded=%d", k, len(b.Series), b.Folded)
			}
		}
		c := resp.Connections
		// İki taraf aynı 15 pod → birleşim 15 pod; katlama 12 + diğer 3;
		// son adımda değer i+i ≥ 1 olan 14 pod aktif (pod-a 0).
		if c == nil || c.Total != 15 || c.Folded != 3 || len(c.Series) != 13 || c.ActivePods == nil || *c.ActivePods != 14 {
			t.Fatalf("bağlantı paneli: %+v", c)
		}
		if resp.StepSeconds != 30 { // 30 dk / 60
			t.Fatalf("adım: %d", resp.StepSeconds)
		}
	})

	t.Run("pod etiketi var · toplam görünüm → bağlantı için iki ek soru", func(t *testing.T) {
		src := newSrc([]string{"client_id", "k8s_pod_name"})
		resp, err := buildMessagingClients(context.Background(), src, msgSetPlan(msgSetClients), msgSetCallers())
		if err != nil {
			t.Fatal(err)
		}
		if len(src.queries) != healthN+2+2 {
			t.Fatalf("sorgu sayısı %d, bekl. %d", len(src.queries), healthN+4)
		}
		if resp.View != "" || resp.Connections == nil || resp.Connections.Total != 15 || resp.Connections.PodLabel != "k8s_pod_name" {
			t.Fatalf("toplam görünüm: view=%q conn=%+v", resp.View, resp.Connections)
		}
		for k, b := range resp.Blocks {
			if b.Folded != 0 {
				t.Errorf("%s: toplam görünümde katlama yok", k)
			}
		}
		if !strings.Contains(resp.Note, "SÜZÜLEMEZ") || resp.Scope != "services" {
			t.Fatalf("süzgeçsiz kapsam uyarısı kalmalı: %q", resp.Note)
		}
	})

	t.Run("etiket yok → istenen süzgeç/görünüm uygulanmaz", func(t *testing.T) {
		src := newSrc([]string{"service_name"})
		p := msgSetPlan(msgSetClients)
		p.TopicFilter, p.ClientFilter, p.View = "payments", "consumer-1", "pod"
		resp, err := buildMessagingClients(context.Background(), src, p, msgSetCallers())
		if err != nil {
			t.Fatal(err)
		}
		if resp.Filter.Topic != "" || resp.Filter.ClientID != "" || resp.View != "" || resp.Labels.Topic || resp.Labels.Pod != "" {
			t.Fatalf("etiketsiz süzgeç uygulandı: %+v %+v view=%q", resp.Labels, resp.Filter, resp.View)
		}
		for _, f := range src.queries {
			if isKafkaDiscoveryQuery(f) {
				continue
			}
			if topicFilterValues(f) != nil || clientFilterValues(f) != nil {
				t.Errorf("%s: süzgeç gitmemeliydi: %+v", f.Name, f.Filters)
			}
		}
		// Pod etiketi yok: panel sağlık bloklarından (servis · istemci), aktif pod yok.
		if resp.Connections == nil || resp.Connections.PodLabel != "" || resp.Connections.ActivePods != nil {
			t.Fatalf("pod'suz panel: %+v", resp.Connections)
		}
	})

	t.Run("keşif yeteneği olmayan kaynak → labels.detected=false, bugünkü davranış", func(t *testing.T) {
		src := &fakeEPSource{name: "ch", queryFn: func(chstore.MetricQueryFilter) ([]chstore.SpanMetricSeries, error) { return kafkaSeries(1), nil }}
		resp, err := buildMessagingClients(context.Background(), src, msgSetPlan(msgSetClients), msgSetCallers())
		if err != nil {
			t.Fatal(err)
		}
		if resp.Labels == nil || resp.Labels.Detected || resp.StepSeconds != 0 {
			t.Fatalf("CH/yeteneksiz: %+v step=%d", resp.Labels, resp.StepSeconds)
		}
	})

	t.Run("diğer setler sekme alanlarını taşımaz", func(t *testing.T) {
		src := newSrc([]string{"topic", "k8s_pod_name"})
		resp, err := buildMessagingClients(context.Background(), src, msgSetPlan(msgSetChart), msgSetCallers())
		if err != nil {
			t.Fatal(err)
		}
		if resp.Labels != nil || resp.Connections != nil || src.labelCalls != 0 {
			t.Fatalf("chart seti keşif yapmamalı: %+v calls=%d", resp.Labels, src.labelCalls)
		}
	})
}

func TestMessagingClientsPlanKafkaTab(t *testing.T) {
	base := "/api/messaging/clients?system=kafka&destination=orders&from=1&to=2"
	p, err := messagingClientsPlanFrom(httptest.NewRequest(http.MethodGet, base+"&set=clients&topicFilter=pay&clientFilter=c1&view=pod", nil))
	if err != nil || p.TopicFilter != "pay" || p.ClientFilter != "c1" || p.View != "pod" {
		t.Fatalf("clients: %+v %v", p, err)
	}
	p, err = messagingClientsPlanFrom(httptest.NewRequest(http.MethodGet, base+"&set=clients&view=toplam", nil))
	if err != nil || p.View != "" {
		t.Fatalf("toplam boş görünüme normalize olmalı: %+v %v", p, err)
	}
	// Diğer setlerde boşaltılır (anahtar parçalanmasın).
	p, err = messagingClientsPlanFrom(httptest.NewRequest(http.MethodGet, base+"&set=chart&topicFilter=pay&view=pod", nil))
	if err != nil || p.TopicFilter != "" || p.View != "" {
		t.Fatalf("chart: %+v %v", p, err)
	}
	if _, err := messagingClientsPlanFrom(httptest.NewRequest(http.MethodGet, base+"&set=clients&view=grid", nil)); err == nil {
		t.Fatal("geçersiz görünüm 400 olmalı")
	}
	long := strings.Repeat("x", kafkaFilterMaxLen+1)
	if _, err := messagingClientsPlanFrom(httptest.NewRequest(http.MethodGet, base+"&set=clients&topicFilter="+long, nil)); err == nil {
		t.Fatal("uzun süzgeç 400 olmalı")
	}
}

func TestMessagingClientsKeyKafkaTab(t *testing.T) {
	base := msgSetPlan(msgSetClients)
	k0 := messagingClientsKey(base, "vm", "mx0")
	seen := map[string]bool{k0: true}
	for i, m := range []func(p *messagingClientsPlan){
		func(p *messagingClientsPlan) { p.TopicFilter = "pay" },
		func(p *messagingClientsPlan) { p.ClientFilter = "c1" },
		func(p *messagingClientsPlan) { p.View = "pod" },
		// alan sınırı kaydırma denemesi: %q bunu ayırır
		func(p *messagingClientsPlan) { p.TopicFilter = `a":fc="b` },
		func(p *messagingClientsPlan) { p.TopicFilter, p.ClientFilter = "a", "b" },
	} {
		p := base
		m(&p)
		k := messagingClientsKey(p, "vm", "mx0")
		if seen[k] {
			t.Fatalf("mutasyon %d anahtarı değiştirmedi: %s", i, k)
		}
		seen[k] = true
	}
}

func TestKafkaLabelValuesParamsAndKey(t *testing.T) {
	ok := httptest.NewRequest(http.MethodGet, "/api/messaging/kafka-label-values?label=client_id&q=%20pay%20&limit=500", nil)
	label, q, limit, err := kafkaLabelValuesParams(ok)
	if err != nil || label != "client_id" || q != "pay" || limit != 50 {
		t.Fatalf("ayrıştırma: %q %q %d %v", label, q, limit, err)
	}
	for _, bad := range []string{"label=pod", "label=", "label=topic&q=" + strings.Repeat("x", kafkaFilterMaxLen+1)} {
		if _, _, _, err := kafkaLabelValuesParams(httptest.NewRequest(http.MethodGet, "/x?"+bad, nil)); err == nil {
			t.Errorf("%s reddedilmeli", bad)
		}
	}
	from, to := kafkaFixtureWindow()
	k0 := kafkaLabelValuesKey("vm", "topic", "pay", 50, from, to)
	for i, k := range []string{
		kafkaLabelValuesKey("ch", "topic", "pay", 50, from, to),
		kafkaLabelValuesKey("vm", "client_id", "pay", 50, from, to),
		kafkaLabelValuesKey("vm", "topic", "pa", 50, from, to),
		kafkaLabelValuesKey("vm", "topic", "pay", 20, from, to),
		kafkaLabelValuesKey("vm", "topic", "pay", 50, from, to.Add(time.Minute)),
	} {
		if k == k0 {
			t.Fatalf("mutasyon %d anahtarı değiştirmedi", i)
		}
	}
}

// Handler sınırı: geçersiz etiket store'a dokunmadan 400.
func TestGetKafkaLabelValuesRejectsUnknownLabel(t *testing.T) {
	s := &Server{}
	rr := httptest.NewRecorder()
	s.getKafkaLabelValues(rr, httptest.NewRequest(http.MethodGet, "/api/messaging/kafka-label-values?label=partition", nil))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "label") {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}
