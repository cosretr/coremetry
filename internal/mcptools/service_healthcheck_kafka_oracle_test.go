package mcptools

// service_healthcheck_kafka_oracle_test.go — v0.10.2046 (servis sağlık
// kontrolü dilim 4): kontrol 12 Kafka consumer lag (VictoriaMetrics) +
// kontrol 13 Oracle hataları (Oracle kökenli exception grupları). Serviste
// hiç veri yoksa satır "uygulanmadı" listesine gider (İyi DEĞİL, Ölçülemedi
// de değil — servis Kafka tüketicisi / Oracle kullanıcısı değil); okuma hatası
// Ölçülemedi.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func lagSeries(topic string, vals ...float64) chstore.SpanMetricSeries {
	s := chstore.SpanMetricSeries{GroupKey: []string{topic}}
	for i, v := range vals {
		s.Points = append(s.Points, chstore.SpanMetricPoint{Time: int64(i) * int64(time.Minute), Value: v})
	}
	return s
}

func TestHCClassifyKafkaLag(t *testing.T) {
	cases := []struct {
		name   string
		series []chstore.SpanMetricSeries
		err    error
		want   string
		seen   string
	}{
		{"hata → ölçülemedi", nil, errors.New("vm 503"), HCUnknown, "okunamadı"},
		{"seri yok → uygulanmadı", nil, nil, hcStateNA, ""},
		{"boş noktalar → uygulanmadı", []chstore.SpanMetricSeries{{GroupKey: []string{"orders"}}}, nil, hcStateNA, ""},
		{"düşük lag → iyi", []chstore.SpanMetricSeries{lagSeries("orders", 5, 20, 3, 0)}, nil, HCOK, "en yüksek lag 20"},
		{"artıyor → dikkat", []chstore.SpanMetricSeries{lagSeries("orders", 100, 400, 900, 2500)}, nil, HCWarn, "orders"},
		{"tavanda, düşmüyor → sorun", []chstore.SpanMetricSeries{lagSeries("orders", 5000, 5200, 5100, 5300)}, nil, HCCrit, "düşmedi"},
		{"yüksek ama eriyor → iyi", []chstore.SpanMetricSeries{lagSeries("orders", 9000, 6000, 3000, 1200)}, nil, HCOK, "en yüksek lag 9000"},
		{"iki topic, en kötüsü kazanır", []chstore.SpanMetricSeries{lagSeries("a", 1, 2, 3), lagSeries("b", 4000, 4100, 4200)}, nil, HCCrit, "b"},
		{"tek nokta tavanda → kıyas yok, dikkat değil", []chstore.SpanMetricSeries{lagSeries("orders", 8000)}, nil, HCOK, "8000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := hcClassifyKafkaLag("orders-api", c.series, c.err)
			if r.State != c.want || !strings.Contains(r.Seen, c.seen) || r.Check != HCCheckKafkaLag {
				t.Fatalf("got %q (%s), want %q ∋ %q", r.State, r.Seen, c.want, c.seen)
			}
		})
	}
}

func TestHCClassifyOracle(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	from := now.Add(-15 * time.Minute)
	g := func(code string, firstAgo, lastAgo time.Duration, occ uint64) chstore.ExceptionGroup {
		return chstore.ExceptionGroup{Fingerprint: "ora:" + code, Type: code, Message: "OP_" + code,
			FirstSeen: now.Add(-firstAgo).UnixNano(), LastSeen: now.Add(-lastAgo).UnixNano(), Occurrences: occ}
	}
	cases := []struct {
		name   string
		groups []chstore.ExceptionGroup
		err    error
		want   string
		seen   string
	}{
		{"hata → ölçülemedi", nil, errors.New("x"), HCUnknown, "okunamadı"},
		{"hiç grup yok → uygulanmadı", nil, nil, hcStateNA, ""},
		{"7 günde var, pencerede yok → iyi", []chstore.ExceptionGroup{g("ORA-00001", 72*time.Hour, 2*time.Hour, 50)}, nil, HCOK, "pencerede Oracle hatası yok"},
		{"bilinen kod aktif → iyi (bilgi)", []chstore.ExceptionGroup{g("ORA-00001", 72*time.Hour, time.Minute, 5000)}, nil, HCOK, "ORA-00001"},
		{"yeni kod az → dikkat", []chstore.ExceptionGroup{g("ORA-01555", 2*time.Hour, time.Minute, 3)}, nil, HCWarn, "yeni"},
		{"yeni kod ≥10 → sorun", []chstore.ExceptionGroup{g("ORA-00060", 2*time.Hour, time.Minute, 14)}, nil, HCCrit, "ORA-00060"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := hcClassifyOracle("orders-api", c.groups, from, now, c.err)
			if r.State != c.want || !strings.Contains(r.Seen, c.seen) || r.Check != HCCheckOracle {
				t.Fatalf("got %q (%s), want %q ∋ %q", r.State, r.Seen, c.want, c.seen)
			}
		})
	}
}

func TestHCKafkaLagFilter(t *testing.T) {
	to := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	f, err := hcKafkaLagFilter("orders-api", to.Add(-15*time.Minute), to)
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "kafka.consumer.records_lag_max" || strings.Join(f.GroupBy, ",") != "topic" || f.MaxDataPoints != 16 ||
		len(f.Filters) != 1 || f.Filters[0].Key != "service.name" || f.Filters[0].Values[0] != "orders-api" {
		t.Fatalf("süzgeç: %+v", f)
	}
}

// TestHCRunnerNotApplicable — uygulanmayan kontroller satır olmaz, listede
// adıyla görünür ve cümleyi "ölçülemedi" diye bozmaz.
func TestHCRunnerNotApplicable(t *testing.T) {
	r := hcReaders{
		sweepReaders: sweepReaders{
			windowRED: func(context.Context, string, string, time.Time, time.Time) (chstore.ServiceWindowRED, error) {
				return chstore.ServiceWindowRED{Spans: 1000, Errors: 1, P95Ms: 100}, nil
			},
		},
		kafkaLag: func(context.Context, string, time.Time, time.Time) ([]chstore.SpanMetricSeries, error) {
			return nil, nil
		},
		oracleGroups: func(context.Context, string, time.Time) ([]chstore.ExceptionGroup, error) {
			return nil, nil
		},
	}
	res, err := runServiceHealthcheck(context.Background(), r, HealthcheckRequest{Service: "orders-api"}, hcDefaultCaps, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range res.Rows {
		if row.Check == HCCheckKafkaLag || row.Check == HCCheckOracle || row.State == hcStateNA {
			t.Fatalf("uygulanmayan satır tabloda: %+v", row)
		}
	}
	if strings.Join(res.NotApplicable, ",") != "Kafka consumer lag,Oracle hataları" {
		t.Fatalf("uygulanmadı listesi: %v", res.NotApplicable)
	}
}
