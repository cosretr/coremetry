package vmetrics

import (
	"sort"
	"strings"
	"testing"
	"time"
)

// v0.10.1097 — "Kafka istemcileri" sekmesi: topic/client_id süzgeci (etiket
// varsa), pod bazlı görünüm, bağlantı paneli, kısa kaynak notu. Sözleşme:
//   - süzgeç/kırılım katalogda yazmayan bir etiketi YALNIZ keşifte görüldüyse
//     (ExtraLabels) kabul eder; keşifsiz hâl bugünkü hata;
//   - üretilen PromQL GOLDEN: adım pencere/mdp'den (sınırlı), süzgeç
//     değerleri PromQL dizesi olarak kaçışlı (tırnak + ters bölü);
//   - keşif ve değer araması TEK birleşim seçicisinden, q regex-kaçışlı.

func kafkaTabWindow() (time.Time, time.Time) {
	to := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	return to.Add(-15 * time.Minute), to
}

func TestKafkaTabPromQLGolden(t *testing.T) {
	from, to := kafkaTabWindow()
	cases := []struct {
		name     string
		metric   string
		scope    KafkaScope
		groupBy  []string
		want     string
		wantStep int
	}{
		{
			name:   "bağlantı · topic + client_id süzgeci (kaçışlı) · pod kırılımı",
			metric: "kafka.consumer.connection_count",
			scope: KafkaScope{Services: []string{"svc-b", "svc-a"}, Topic: `or"ders\x`, ClientID: `cl-1"`,
				ExtraLabels: []string{"topic", "k8s_pod_name"}, From: from, To: to, MaxDataPoints: 60},
			groupBy:  []string{"service.name", "k8s_pod_name"},
			want:     `sum by (service_name, k8s_pod_name) ({__name__=~"^(kafka\\.consumer\\.connection_count|kafka_consumer_connection_count)$", service_name=~"svc-a|svc-b", topic="or\"ders\\x", client_id="cl-1\""})`,
			wantStep: 15,
		},
		{
			name:     "toplam görünüm · yalnız client_id (katalog etiketi, keşif gerekmez)",
			metric:   "kafka.producer.connection_count",
			scope:    KafkaScope{Services: []string{"svc-a"}, ClientID: "producer-1", From: from, To: to, MaxDataPoints: 60},
			groupBy:  []string{"service.name", "client_id"},
			want:     `sum by (service_name, client_id) ({__name__=~"^(kafka\\.producer\\.connection_count|kafka_producer_connection_count)$", service_name="svc-a", client_id="producer-1"})`,
			wantStep: 15,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, ok := KafkaMetricByName(c.metric)
			if !ok {
				t.Fatalf("katalogda yok: %s", c.metric)
			}
			f, err := KafkaQuery(m, c.scope, c.groupBy)
			if err != nil {
				t.Fatal(err)
			}
			got, err := buildPromQL(f, promOpts{})
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("PromQL\n got: %s\nwant: %s", got, c.want)
			}
			if s := promStep(f.From, f.To, f.StepSeconds, f.MaxDataPoints); s != c.wantStep {
				t.Fatalf("adım %d, bekl. %d", s, c.wantStep)
			}
			if s := KafkaStepSeconds(f.From, f.To, f.MaxDataPoints); s != c.wantStep {
				t.Fatalf("KafkaStepSeconds sorgudan sapmış: %d", s)
			}
		})
	}
}

// Adım SINIRLI: mdp kafkaClientsMdp'de 10..300'e kelepçeli, pencere ne olursa
// olsun nokta sayısı mdp'yi aşmaz (≥1 sn).
func TestKafkaStepSeconds(t *testing.T) {
	to := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		win  time.Duration
		mdp  int
		want int
	}{
		{15 * time.Minute, 60, 15},
		{time.Hour, 60, 60},
		{24 * time.Hour, 60, 1440},
		{30 * time.Second, 60, 1},
	} {
		if got := KafkaStepSeconds(to.Add(-c.win), to, c.mdp); got != c.want {
			t.Errorf("%s/%d → %d, bekl. %d", c.win, c.mdp, got, c.want)
		}
	}
}

func TestKafkaLabelSelectorsGolden(t *testing.T) {
	names := KafkaLabelNamesSelector([]string{"kafka.producer.connection_count", "kafka.consumer.connection_count", "kafka.consumer.connection_count"})
	wantNames := `{__name__=~"^(kafka\\.consumer\\.connection_count|kafka\\.producer\\.connection_count|kafka_consumer_connection_count|kafka_producer_connection_count)$"}`
	if names != wantNames {
		t.Fatalf("etiket seçici\n got: %s\nwant: %s", names, wantNames)
	}
	// q regex-kaçışlı (nokta \.), backtick düşer, tırnak raw string içinde zararsız.
	sc := KafkaLabelScope{Consumers: []string{"svc-b"}}
	vals, err := KafkaLabelValuesSelectors([]string{"kafka.consumer.connection_count"}, "client_id", "a.b\"c`d", sc)
	wantVals := "{__name__=~\"^(kafka\\\\.consumer\\\\.connection_count|kafka_consumer_connection_count)$\", service_name=\"svc-b\", client_id=~`(?i).*a\\.b\"cd.*`}"
	if err != nil || len(vals) != 1 || vals[0] != wantVals {
		t.Fatalf("değer seçici (%v)\n got: %v\nwant: %s", err, vals, wantVals)
	}
	// q boş → ad + kapsam eşleştiricisi, alt dize yüklemi yok (limit'li uç).
	got, _ := KafkaLabelValuesSelectors([]string{"kafka.consumer.connection_count"}, "topic", "  ", sc)
	if len(got) != 1 || strings.Contains(got[0], "topic=~") {
		t.Fatalf("boş q süzgeç eklememeli: %v", got)
	}
}

// v0.10.1102 (operatör-onaylı) — seçici önerileri SAYFA kapsamında: taraf
// başına servis kümesi (panellerin service_name=~ eşleştiricisinin aynısı) +
// karşı süzgeç. Golden: VM'e giden match[] listesi bayt bayt.
func TestKafkaScopedLabelValuesSelectorsGolden(t *testing.T) {
	metrics := []string{"kafka.producer.connection_count", "kafka.consumer.connection_count"}
	sc := KafkaLabelScope{
		Producers: []string{"svc-pay", "svc-api", "svc-pay"},
		Consumers: []string{"svc-ledger"},
		Topic:     "orders.v1",
		ClientID:  "consumer-7",
	}
	// client_id aranırken: kapsam + seçili topic; kendi (client_id) süzgeci YOK.
	got, err := KafkaLabelValuesSelectors(metrics, "client_id", "", sc)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`{__name__=~"^(kafka\\.producer\\.connection_count|kafka_producer_connection_count)$", service_name=~"svc-api|svc-pay", topic="orders.v1"}`,
		`{__name__=~"^(kafka\\.consumer\\.connection_count|kafka_consumer_connection_count)$", service_name="svc-ledger", topic="orders.v1"}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("client_id seçicileri\n got: %s\nwant: %s", strings.Join(got, "\n      "), strings.Join(want, "\n      "))
	}
	// topic aranırken: kapsam + seçili client_id; kendi (topic) süzgeci YOK.
	got, _ = KafkaLabelValuesSelectors(metrics, "topic", "ord", sc)
	want = []string{
		"{__name__=~\"^(kafka\\\\.producer\\\\.connection_count|kafka_producer_connection_count)$\", service_name=~\"svc-api|svc-pay\", client_id=\"consumer-7\", topic=~`(?i).*ord.*`}",
		"{__name__=~\"^(kafka\\\\.consumer\\\\.connection_count|kafka_consumer_connection_count)$\", service_name=\"svc-ledger\", client_id=\"consumer-7\", topic=~`(?i).*ord.*`}",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("topic seçicileri\n got: %s\nwant: %s", strings.Join(got, "\n      "), strings.Join(want, "\n      "))
	}
	// Servisi olmayan taraf DÜŞER; hiç servis yoksa seçici yok (filoya açılmaz).
	got, _ = KafkaLabelValuesSelectors(metrics, "client_id", "", KafkaLabelScope{Consumers: []string{"svc-ledger"}})
	if len(got) != 1 || !strings.Contains(got[0], "consumer") {
		t.Fatalf("üreticisiz kapsam tek (tüketici) seçici vermeli: %v", got)
	}
	if got, _ = KafkaLabelValuesSelectors(metrics, "client_id", "", KafkaLabelScope{}); len(got) != 0 {
		t.Fatalf("boş kapsam seçici üretmemeli (filo geneli yasak): %v", got)
	}
}

// Keşif etiketi katalog dışı süzgeci/kırılımı AÇAR; keşifsiz hâl bugünkü hata.
func TestKafkaQueryExtraLabels(t *testing.T) {
	from, to := kafkaTabWindow()
	m, _ := KafkaMetricByName("kafka.consumer.connection_count")
	base := KafkaScope{Services: []string{"a"}, From: from, To: to}
	withTopic := base
	withTopic.Topic = "orders"
	if _, err := KafkaQuery(m, withTopic, nil); err == nil {
		t.Fatal("keşifsiz topic süzgeci reddedilmeli (katalogda topic yok)")
	}
	withTopic.ExtraLabels = []string{"topic"}
	f, err := KafkaQuery(m, withTopic, nil)
	if err != nil {
		t.Fatalf("keşifte görülen topic kabul edilmeli: %v", err)
	}
	if len(f.Filters) != 2 || f.Filters[1].Key != "topic" || f.Filters[1].Values[0] != "orders" {
		t.Fatalf("topic süzgeci: %+v", f.Filters)
	}
	if _, err := KafkaQuery(m, base, []string{"service.name", "pod"}); err == nil {
		t.Fatal("keşifsiz pod kırılımı reddedilmeli")
	}
	pod := base
	pod.ExtraLabels = []string{"pod"}
	if _, err := KafkaQuery(m, pod, []string{"service.name", "pod"}); err != nil {
		t.Fatalf("keşifte görülen pod kırılımı kabul edilmeli: %v", err)
	}
}

func TestKafkaClientsTabMetrics(t *testing.T) {
	got := KafkaClientsTabMetrics()
	if !sort.StringsAreSorted(got) {
		t.Fatalf("sıralı değil: %v", got)
	}
	seen := map[string]bool{}
	for _, m := range got {
		if seen[m] {
			t.Fatalf("tekrar: %s", m)
		}
		seen[m] = true
		if _, ok := KafkaMetricByName(m); !ok {
			t.Fatalf("katalogda yok: %s", m)
		}
	}
	for _, need := range []string{"kafka.producer.connection_count", "kafka.consumer.connection_count"} {
		if !seen[need] {
			t.Fatalf("bağlantı metriği keşif kümesinde yok: %s", need)
		}
	}
}

// Pod görünümü sağlık sorularının TÜRETİMİ: anahtar/metrik/etiket aynı, kırılım
// [service.name, pod]; kaynak liste mutasyona uğramaz.
func TestKafkaClientHealthPodQuestions(t *testing.T) {
	src := KafkaClientHealthQuestions()
	pod := KafkaClientHealthPodQuestions("k8s_pod_name")
	if len(pod) != len(src) {
		t.Fatalf("uzunluk %d, bekl. %d", len(pod), len(src))
	}
	for i := range src {
		if pod[i].Key != src[i].Key || pod[i].Metric != src[i].Metric || pod[i].TR != src[i].TR {
			t.Errorf("%d: soru sapmış: %+v vs %+v", i, pod[i], src[i])
		}
		if strings.Join(pod[i].GroupBy, ",") != "service.name,k8s_pod_name" {
			t.Errorf("%s: kırılım %v", pod[i].Key, pod[i].GroupBy)
		}
	}
	again := KafkaClientHealthQuestions()
	for i := range again {
		if strings.Join(again[i].GroupBy, ",") != strings.Join(src[i].GroupBy, ",") {
			t.Fatalf("kaynak liste mutasyona uğradı: %v", again[i].GroupBy)
		}
	}
	conn := KafkaConnectionPodQuestions("pod")
	if len(conn) != 2 || conn[0].Key != KafkaConnProducerKey || conn[1].Key != KafkaConnConsumerKey {
		t.Fatalf("bağlantı soruları: %+v", conn)
	}
	for _, q := range conn {
		if _, ok := KafkaMetricByName(q.Metric); !ok {
			t.Fatalf("katalogda yok: %s", q.Metric)
		}
	}
	if c := KafkaPodLabelCandidates(); len(c) == 0 || c[0] != "k8s_pod_name" {
		t.Fatalf("pod adayları: %v", c)
	}
}

func TestKafkaLabelWindow(t *testing.T) {
	to := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	f, tt := KafkaLabelWindow(to.Add(-24*time.Hour), to)
	if !tt.Equal(to) || to.Sub(f) != time.Hour {
		t.Fatalf("1 sa'e kırpılmalı: %s..%s", f, tt)
	}
	f, _ = KafkaLabelWindow(to.Add(-10*time.Minute), to)
	if to.Sub(f) != 10*time.Minute {
		t.Fatalf("kısa pencere korunmalı: %s", f)
	}
}
