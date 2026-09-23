package oracle

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.906 — ön-toplanmış bir Oracle satırının patlatılmış parçaları TEK
// seriye düşer: trace'li parçaların exception tipi çoğunluğu artan (trace'siz)
// satıra da uygulanır; tablo kipi satırları ve jenerik olmayan kod değişmez.
func TestUnifyAggregatedQualifier(t *testing.T) {
	t0 := time.Date(2026, 9, 23, 15, 21, 0, 0, time.UTC)
	base := chstore.OracleErrorRow{Time: t0, OperationCode: "OP", ErrorCode: "GEN", ChannelCode: "K", HostName: "H"}
	with := func(trace string, w uint32) chstore.OracleErrorRow {
		r := base
		r.TraceID, r.Weight = trace, w
		return r
	}
	ex := map[string]string{"t1": "TimeoutException", "t2": "TimeoutException", "t3": "SQLException"}
	q := QualifierFor(map[string]bool{"GEN": true}, func(id string) string { return ex[id] })
	rows := []chstore.OracleErrorRow{with("t1", 1), with("t2", 1), with("t3", 1), with("", 7)}
	res := BucketRows("src", rows, t0, t0.Add(2*time.Minute), nil, nil, 0, q)
	got := map[string]float64{}
	for _, p := range res.Points {
		if p.Time.Equal(t0) && p.Value > 0 {
			got[p.AttrValues[3]] += p.Value
		}
	}
	if len(got) != 1 || got["TimeoutException"] != 10 {
		t.Fatalf("tek seri (çoğunluk TimeoutException, 1+1+1+7=10): %v", got)
	}
	// Tablo kipi satırları (Weight 0) parça başına kalır.
	tbl := []chstore.OracleErrorRow{with("t1", 0), with("", 0)}
	res2 := BucketRows("src", tbl, t0, t0.Add(2*time.Minute), nil, nil, 0, q)
	got2 := map[string]float64{}
	for _, p := range res2.Points {
		if p.Time.Equal(t0) && p.Value > 0 {
			got2[p.AttrValues[3]] += p.Value
		}
	}
	if got2["TimeoutException"] != 1 || got2["generic"] != 1 {
		t.Fatalf("tablo kipi değişmemeli: %v", got2)
	}
	// Jenerik olmayan kod: "-" kalır.
	ng := base
	ng.ErrorCode, ng.Weight = "F1", 3
	if unifyAggregatedQualifier([]chstore.OracleErrorRow{ng}, q)(ng) != "-" {
		t.Fatal("jenerik olmayan kod ayrılmaz")
	}
	// Grupta hiç tip yoksa generic.
	solo := with("", 5)
	if unifyAggregatedQualifier([]chstore.OracleErrorRow{solo}, q)(solo) != "generic" {
		t.Fatal("tipsiz grup generic")
	}
}
