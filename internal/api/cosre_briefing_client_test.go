package api

// cosre_briefing_client_test.go — v0.10.1712 Login Briefing kalem 2
// (client_metrics_diff): okuma sonucu → satır dönüşümleri. Nötr fikstür.

import (
	"strconv"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosre/briefing"
)

func TestBriefingKafkaQuestionsSubset(t *testing.T) {
	qs := briefingKafkaQuestions()
	var keys []string
	for _, q := range qs {
		keys = append(keys, q.Key)
		if strings.Join(q.GroupBy, ",") != "service.name,topic" {
			t.Fatalf("%s groupBy %v", q.Key, q.GroupBy)
		}
	}
	if strings.Join(keys, ",") != "consumer_lag_max,producer_error_rate" {
		t.Fatalf("sorular %v (katalogda anahtar değişti mi?)", keys)
	}
}

func TestBriefingHTTPCandidates(t *testing.T) {
	cur := []chstore.ServiceSummary{{Name: "a", SpanCount: 10}, {Name: "orders-api", SpanCount: 900}, {Name: "b", SpanCount: 900}, {Name: "idle", SpanCount: 0}}
	cases := []struct {
		k    int
		want string
	}{{1, "b"}, {2, "b,orders-api"}, {9, "b,orders-api,a"}, {0, ""}}
	for _, c := range cases {
		if got := strings.Join(briefingHTTPCandidates(cur, c.k), ","); got != c.want {
			t.Fatalf("k=%d got %q want %q", c.k, got, c.want)
		}
	}
}

func TestBriefingKafkaRows(t *testing.T) {
	const from = 3_000_000 // ms
	ns := func(ms int64) int64 { return ms * 1e6 }
	ser := func(svc, topic string, prev, cur float64) chstore.SpanMetricSeries {
		return chstore.SpanMetricSeries{GroupKey: []string{svc, topic}, Points: []chstore.SpanMetricPoint{{Time: ns(from - 1000), Value: prev}, {Time: ns(from), Value: cur}}}
	}
	cases := []struct {
		name       string
		blocks     map[string]kafkaMetricBlock
		wantRows   string
		wantFailed bool
	}{
		{"iki soru", map[string]kafkaMetricBlock{
			"producer_error_rate": {Series: []chstore.SpanMetricSeries{ser("payments-core", "orders", 0, 0.5)}},
			"consumer_lag_max":    {Series: []chstore.SpanMetricSeries{ser("orders-api", "", 100, 9000)}},
		}, "orders-api/lag//9000/100;payments-core/send_err/topic=orders/0.5/0", false},
		{"blok hatası → failed, diğer blok okunur", map[string]kafkaMetricBlock{
			"producer_error_rate": {Error: "vm 422"},
			"consumer_lag_max":    {Series: []chstore.SpanMetricSeries{ser("orders-api", "orders", 1, 2)}},
		}, "orders-api/lag/topic=orders/2/1", true},
		{"servissiz seri ve bilinmeyen soru atlanır", map[string]kafkaMetricBlock{
			"consumer_lag_max":   {Series: []chstore.SpanMetricSeries{ser("", "orders", 1, 2)}},
			"consumer_last_poll": {Series: []chstore.SpanMetricSeries{ser("orders-api", "orders", 1, 2)}},
		}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows, failed := briefingKafkaRows(c.blocks, from)
			var got []string
			for _, r := range rows {
				if r.Family != briefing.FamilyKafka || !r.HasNow || !r.HasPrev {
					t.Fatalf("satır %+v", r)
				}
				got = append(got, strings.Join([]string{r.Service, r.Metric, r.Label, briefingFtoa(r.Now), briefingFtoa(r.Prev)}, "/"))
			}
			if strings.Join(got, ";") != c.wantRows || failed != c.wantFailed {
				t.Fatalf("got %q failed=%v", strings.Join(got, ";"), failed)
			}
		})
	}
}

func briefingFtoa(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
