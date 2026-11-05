package anomaly

// v0.10.1802 — Davis paritesi dilim 3: queue öznesinin Kafka consumer lag
// metriği (subjects_kafka_lag.go). Pinlenen:
//   - VM ifadeleri BAYT BAYT (seçim `topk` ortalama lag'e göre, `@` sabit;
//     kova ifadesi max by (svc, topic), seçimle süzülü, her ofsette aynı çapa);
//   - üst-N lag'e göre (hacme değil), deterministik, sıfır lag dışarıda;
//   - karar: yalnız artış + lagMinRecords; örnek cümle düz Türkçe;
//   - aynı servisin iki topic'i ayrı parmak izi; bütçe: endpoint lag payını bırakır;
//   - uçtan uca sahte VM: ısınma → seri → olay; VM yok / metrik yok → "vm_kafka_absent".

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func TestKafkaLagVMQueryGolden(t *testing.T) {
	sel := kafkaLagSelectExpr("service_name", 20, 1_700_000_000)
	// Ad adayları vmetrics.NameCandidates("gauge") — ürünün diğer Kafka
	// okumalarıyla (KafkaQuery → PlainSeries) aynı yazım kuralı.
	const name = `__name__=~"^(kafka\\.consumer\\.records_lag_max|kafka_consumer_records_lag_max|kafka_consumer_records_lag_max_seconds|kafka_consumer_records_lag_max_milliseconds|kafka_consumer_records_lag_max_bytes|kafka_consumer_records_lag_max_ratio)$"`
	wantSel := `topk(20, max by (service_name, topic) (avg_over_time({` + name + `, topic!=""}[1h] @ 1700000000)))`
	if sel != wantSel {
		t.Fatalf("seçim ifadesi:\n got %s\nwant %s", sel, wantSel)
	}
	q := kafkaLagQuery("service_name", sel, []int64{0, 86400})
	want := `label_set((max by (service_name, topic) (max_over_time({` + name + `, topic!=""}[300s]))) and on (service_name, topic) (` + wantSel + `), "cm_off", "0")` +
		` or label_set((max by (service_name, topic) (max_over_time({` + name + `, topic!=""}[300s] offset 86400s))) and on (service_name, topic) (` + wantSel + `), "cm_off", "86400")`
	if q != want {
		t.Fatalf("kova sorgusu:\n got %s\nwant %s", q, want)
	}
	// Çapa her ofsette aynı (mevsimsel ofset geçmişin üst-N'ini seçmez).
	if strings.Count(q, "@ 1700000000") != 2 {
		t.Errorf("çapa sabit değil: %s", q)
	}
	if kafkaLagQuery("service_name", "", []int64{0}) != "" || kafkaLagQuery("", sel, []int64{0}) != "" {
		t.Error("seçimsiz / servis etiketsiz sorgu kurulmamalı")
	}
	// Grup anahtarı → (svc, topic) + ofset.
	k, off, ok := kafkaLagParseKey([]string{`cm_off="86400"`, `service_name="orders-consumer"`, `topic="orders"`}, "service_name")
	if !ok || k != endpointStoreKey("orders-consumer", "orders") || off != 86400 {
		t.Errorf("anahtar: %q %d %v", k, off, ok)
	}
	if _, _, ok := kafkaLagParseKey([]string{`service_name="orders-consumer"`}, "service_name"); ok {
		t.Error("topic'siz seri kabul edildi")
	}
	if kafkaLagSvcLabel(vmetrics.LabelMap{}) != "service_name" || kafkaLagSvcLabel(vmetrics.LabelMap{Service: "svc"}) != "svc" {
		t.Error("servis etiketi")
	}
}

func TestPickTopLagSubjects(t *testing.T) {
	avg := map[string]float64{
		endpointStoreKey("a", "orders"):   50_000, // en geride
		endpointStoreKey("b", "payments"): 1_200,
		endpointStoreKey("c", "audit"):    1_200, // eşit: kimlik artan kazanır
		endpointStoreKey("d", "idle"):     0,     // lag yok → seçime girmez
	}
	got := pickTopLagSubjects(avg, 2)
	if len(got) != 2 || !got[endpointStoreKey("a", "orders")] || !got[endpointStoreKey("b", "payments")] {
		t.Errorf("üst-2: %v", got)
	}
	if all := pickTopLagSubjects(avg, 10); len(all) != 3 || all[endpointStoreKey("d", "idle")] {
		t.Errorf("sıfır lag seçildi: %v", all)
	}
}

func TestConsumerLagEventAllowed(t *testing.T) {
	up := anomalyOutcome{Action: "open", Direction: "spiked", Current: 48_000, Median: 1_200}
	cases := []struct {
		name string
		oc   anomalyOutcome
		min  float64
		want bool
	}{
		{"artış, taban üstü", up, 1000, true},
		{"artış, tam sınırda", anomalyOutcome{Action: "open", Direction: "spiked", Current: 1000, Median: 10}, 1000, true},
		{"artış, taban altı (gürültü)", anomalyOutcome{Action: "open", Direction: "spiked", Current: 999, Median: 3}, 1000, false},
		{"düşüş (yetişiyor)", anomalyOutcome{Action: "open", Direction: "dropped", Current: 5_000, Median: 50_000}, 1000, false},
		{"açılmamış karar", anomalyOutcome{Action: "none", Current: 48_000, Median: 1_200}, 1000, false},
	}
	for _, c := range cases {
		if got := consumerLagEventAllowed(c.oc, c.min); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
	if lagMinRecordsOf(chstore.AnomalySubjectsConfig{}) != 1000 || lagMinRecordsOf(chstore.AnomalySubjectsConfig{Queue: chstore.AnomalySubjectKindConfig{LagMinRecords: 5}}) != 5 {
		t.Error("lagMinRecordsOf")
	}
	// evaluator.subjectMetricConsumerLag ile aynı literal (terfi kapısı okur).
	if subjectMetricConsumerLag != "consumer_lag" {
		t.Error("metrik adı ikizden ayrıştı")
	}
	if directionFor(subjectMetricConsumerLag) != "up" {
		t.Error("consumer_lag yönü yalnız artış olmalı")
	}
}

func TestConsumerLagEventShape(t *testing.T) {
	k := subjectKey{Kind: chstore.AnomalySubjectQueue, Owner: "orders-consumer", ID: chstore.QueueSubjectID("kafka", "orders")}
	oc := anomalyOutcome{Action: "open", Direction: "spiked", Current: 48_000, Median: 1_200, Z: 6.1, Dwell: 3, BaselineKind: "seasonal", BaselineN: 40}
	ev := subjectAnomalyEvent(k, subjectMetricConsumerLag, oc, subjectSeries{Rates: []float64{1, 1, 1}}, time.Unix(1_700_000_000, 0))
	if want := "orders-consumer · topic orders: lag 48.000 kayıt, taban 1.200 (mevsimsel, z=6,1, 3 kova)"; ev.Sample != want {
		t.Errorf("örnek cümle:\n got %q\nwant %q", ev.Sample, want)
	}
	if ev.Service != "orders-consumer" || ev.SubjectKind != "queue" || ev.SubjectID != k.ID || ev.Direction != "up" ||
		ev.CurrentCount != 48_000 || ev.PeakRatio != 40 || ev.Pattern != "consumer_lag @ "+k.ID {
		t.Errorf("olay: %+v", ev)
	}
	// Aynı servisin iki topic'i AYRI parmak izi; db/queue'nun eski deseni aynen.
	k2 := k
	k2.ID = chstore.QueueSubjectID("kafka", "payments")
	if subjectAnomalyEvent(k2, subjectMetricConsumerLag, oc, subjectSeries{}, time.Unix(0, 0)).ID == ev.ID {
		t.Error("iki topic aynı parmak izine çöktü")
	}
	q := subjectKey{Kind: chstore.AnomalySubjectQueue, Owner: k.ID, ID: k.ID}
	if subjectEventPattern(q, "p99_ms") != "p99_ms" {
		t.Error("messaging_summary_5m queue deseni değişti")
	}
	for in, want := range map[float64]string{0: "0", 999: "999", 1000: "1.000", 1234567.4: "1.234.567", -1500: "-1.500"} {
		if got := formatIntTR(in); got != want {
			t.Errorf("formatIntTR(%v) = %q, want %q", in, got, want)
		}
	}
	if displayMetric(subjectMetricConsumerLag) != "Consumer lag" || unitOf(subjectMetricConsumerLag) != "kayıt" {
		t.Error("etiket / birim")
	}
}

func TestPlanSubjectBudgetReservesLag(t *testing.T) {
	on := chstore.AnomalySubjectKindConfig{Enabled: true, TopN: 20, MaxSeriesPerTick: 100}
	reqs := subjectKindsOrdered(chstore.AnomalySubjectsConfig{Endpoint: on, Queue: on})
	p := planSubjectBudget(reqs, 10)
	if p[0].VMBudget != 8 || p[3].Source != subjectSourceKafkaLag || p[3].VMBudget != 2 || p[3].Skipped {
		t.Errorf("endpoint lag payını bırakmadı: %+v", p)
	}
	// Queue kapalıyken endpoint tüm payı alır (eski davranış).
	p = planSubjectBudget(subjectKindsOrdered(chstore.AnomalySubjectsConfig{Endpoint: on}), 10)
	if p[0].VMBudget != 10 || p[3].Enabled {
		t.Errorf("queue kapalı: %+v", p)
	}
	// Pay lag'e bile yetmiyorsa: endpoint atlanır, lag koşar.
	p = planSubjectBudget(reqs, 3)
	if !p[0].Skipped || p[0].Reason != subjectSkipVMBudget || p[3].VMBudget != 3 {
		t.Errorf("dar pay: %+v", p)
	}
}

// ── uçtan uca (sahte VM) ────────────────────────────────────────────────

type fakeLagVM struct {
	notConfigured bool
	keys          [][2]string // (servis, topic)
	val           func(svc, topic string, bucket int64) float64
	queries       atomic.Int64
	sawSel        atomic.Int64
}

func (f *fakeLagVM) Configured() bool                  { return !f.notConfigured }
func (f *fakeLagVM) MetricLabelMap() vmetrics.LabelMap { return vmetrics.LabelMap{} }
func (f *fakeLagVM) MetricLabelNamesIn(context.Context, string, time.Time, time.Time) ([]string, bool, error) {
	return nil, false, nil
}
func (f *fakeLagVM) MetricLabelValuesIn(context.Context, string, string, time.Time, time.Time, int) ([]string, bool, error) {
	return nil, false, nil
}
func (f *fakeLagVM) QueryPromQLInstant(_ context.Context, q string, at time.Time) ([]chstore.SpanMetricSeries, error) {
	f.queries.Add(1)
	var out []chstore.SpanMetricSeries
	for _, k := range f.keys {
		out = append(out, chstore.SpanMetricSeries{GroupKey: []string{`service_name="` + k[0] + `"`, `topic="` + k[1] + `"`},
			Points: []chstore.SpanMetricPoint{{Time: at.UnixNano(), Value: 1500}}})
	}
	return out, nil
}
func (f *fakeLagVM) QueryPromQLRange(_ context.Context, q string, from, to time.Time, step, _ int) ([]chstore.SpanMetricSeries, error) {
	f.queries.Add(1)
	if strings.Contains(q, "and on (service_name, topic) (topk(") {
		f.sawSel.Add(1)
	}
	var out []chstore.SpanMetricSeries
	for _, m := range offRe.FindAllStringSubmatch(q, -1) {
		var off int64
		for _, c := range m[1] {
			off = off*10 + int64(c-'0')
		}
		for _, k := range f.keys {
			s := chstore.SpanMetricSeries{GroupKey: []string{`cm_off="` + m[1] + `"`, `service_name="` + k[0] + `"`, `topic="` + k[1] + `"`}}
			for ts := from.Unix() + int64(step); ts <= to.Unix(); ts += int64(step) {
				b := ts - int64(step)
				s.Points = append(s.Points, chstore.SpanMetricPoint{Time: b * int64(time.Second), Value: f.val(k[0], k[1], b-off)})
			}
			out = append(out, s)
		}
	}
	return out, nil
}

func TestKafkaLagEndToEnd(t *testing.T) {
	now := time.Date(2026, 11, 4, 14, 31, 0, 0, time.UTC)
	sens := chstore.DefaultAnomalySensitivity()
	sens.Subjects.Queue.Enabled = true
	upper := lastCompleteBucketStart(now.Add(-vmIngestLag)).Unix()
	spikeFrom := upper - int64(sens.DwellBuckets)*bucketSeconds
	f := &fakeLagVM{
		keys: [][2]string{{"orders-consumer", "orders"}, {"orders-consumer", "payments"}},
		val: func(svc, topic string, b int64) float64 {
			if topic == "orders" && b >= spikeFrom {
				return 48_000 // son dwell kovaları: lag patladı
			}
			return 1_200 + float64((b/bucketSeconds)%7)*10 // olağan dalga
		},
	}
	d := &Detector{red: newVMREDSource(), kafkaLag: newKafkaLagSource()}
	d.red.wire.Store(&vmREDBox{vm: f})
	r := subjectKindsOrdered(sens.Subjects)[3]
	var tick subjectTick
	var st SubjectKindStats
	for i := 0; i < 30; i++ {
		tick, st = subjectTick{}, SubjectKindStats{}
		left := 24
		d.collectKafkaLag(context.Background(), now, sens, seasonalDays, seasonalNeighborBuckets, r, &left, tick, &st)
		if st.Queries > 24 || 24-left != st.Queries {
			t.Fatalf("tik %d: %d sorgu, kalan %d", i, st.Queries, left)
		}
		if len(tick) > 0 {
			break
		}
	}
	if st.Skipped || st.Series != 2 || f.sawSel.Load() == 0 {
		t.Fatalf("ısınmadı: %+v (seçimli okuma %d)", st, f.sawSel.Load())
	}
	ser := tick[subjectMetricConsumerLag]
	if len(ser) != 2 {
		t.Fatalf("seriler: %v", ser)
	}
	evs := decideSubjects(tick, sens, seasonalMinSamples, now)
	if len(evs) != 1 || evs[0].SubjectID != chstore.QueueSubjectID("kafka", "orders") || evs[0].Service != "orders-consumer" || evs[0].Direction != "up" {
		t.Fatalf("olaylar: %+v", evs)
	}
	if !strings.HasPrefix(evs[0].Sample, "orders-consumer · topic orders: lag 48.000 kayıt, taban ") {
		t.Errorf("örnek: %q", evs[0].Sample)
	}
	// Kararlı durum: sonraki tik (5 dk sonra) yalnız yeni kovaları okur.
	before := f.queries.Load()
	tick, st = subjectTick{}, SubjectKindStats{}
	left := 24
	d.collectKafkaLag(context.Background(), now.Add(5*time.Minute), sens, seasonalDays, seasonalNeighborBuckets, r, &left, tick, &st)
	if n := f.queries.Load() - before; n > 3 {
		t.Errorf("kararlı tik %d sorgu (≤3 beklenir)", n)
	}
	// lagMinRecords yüksekse aynı sıçrama olay değil.
	sens.Subjects.Queue.LagMinRecords = 100_000
	if evs := decideSubjects(tick, sens, seasonalMinSamples, now.Add(5*time.Minute)); len(evs) != 0 {
		t.Errorf("taban altı lag olay üretti: %+v", evs)
	}
}

func TestKafkaLagAbsentSkipsSilently(t *testing.T) {
	now := time.Date(2026, 11, 4, 14, 31, 0, 0, time.UTC)
	sens := chstore.DefaultAnomalySensitivity()
	sens.Subjects.Queue.Enabled = true
	r := subjectKindsOrdered(sens.Subjects)[3]
	for name, vm := range map[string]REDVM{"VM yok": nil, "yapılandırılmamış": &fakeLagVM{notConfigured: true}, "metrik yok": &fakeLagVM{}} {
		d := &Detector{red: newVMREDSource(), kafkaLag: newKafkaLagSource()}
		if vm != nil {
			d.red.wire.Store(&vmREDBox{vm: vm})
		}
		tick, st := subjectTick{}, SubjectKindStats{}
		left := 24
		d.collectKafkaLag(context.Background(), now, sens, seasonalDays, seasonalNeighborBuckets, r, &left, tick, &st)
		if !st.Skipped || st.Reason != subjectSkipVMKafkaAbsent || len(tick) != 0 || st.LastError != "" {
			t.Errorf("%s: %+v", name, st)
		}
	}
}
