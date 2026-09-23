package oracle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.893 (Aşama 3 dilim A) — sayaç SAF: dakika kovaları, dense sıfır
// (aktif anahtar pencerede görülmese de her dakikaya 0), pencere dışı satır
// atlanır, ignore sayılmaz, tavan → "diğer" serisi, attr sırası = GroupBy,
// parmak izi seri başına kararlı; Handle aktif anahtarları taşır ve yazım
// hatası poll'u düşürmez.
func TestBucketRowsDenseAndCap(t *testing.T) {
	from := time.Date(2026, 9, 23, 12, 0, 30, 0, time.UTC)
	to := from.Add(3 * time.Minute) // dakikalar 12:00..12:03 → 4 kova
	row := func(min int, op, code, ch string) chstore.OracleErrorRow {
		return chstore.OracleErrorRow{Time: time.Date(2026, 9, 23, 12, min, 10, 0, time.UTC), OperationCode: op, ErrorCode: code, ChannelCode: ch}
	}
	rows := []chstore.OracleErrorRow{row(1, "OP_A", "E1", "MOB"), row(1, "OP_A", "E1", "MOB"), row(2, "OP_A", "E1", "MOB"), row(2, "OP_B", "E2", "WEB"), row(9, "OP_Z", "E9", "X"), row(1, "OP_I", "IGN", "MOB")}
	active := map[CounterKey]time.Time{{Op: "OP_OLD", Code: "E0", Channel: "ATM", Qualifier: "-"}: from.Add(-time.Hour)}
	res := BucketRows("oracle-prod", rows, from, to, active, map[string]bool{"IGN": true}, 0)
	if res.Minutes != 4 || res.Keys != 3 || res.Ignored != 1 || res.Overflow != 0 || len(res.Points) != 12 {
		t.Fatalf("şekil: dk=%d keys=%d ignored=%d overflow=%d points=%d", res.Minutes, res.Keys, res.Ignored, res.Overflow, len(res.Points))
	}
	got := map[string]map[int]float64{}
	for _, p := range res.Points {
		if p.Metric != "ext:error_count" || p.Instrument != "gauge" || p.ServiceName != "oracle-prod" || len(p.AttrKeys) != 4 || p.AttrKeys[0] != "operation.code" || p.AttrKeys[3] != "error.qualifier" || p.SeriesFingerprint == 0 {
			t.Fatalf("nokta şekli: %+v", p)
		}
		k := p.AttrValues[0] + "/" + p.AttrValues[1]
		if got[k] == nil {
			got[k] = map[int]float64{}
		}
		got[k][p.Time.Minute()] = p.Value
	}
	if got["OP_A/E1"][1] != 2 || got["OP_A/E1"][2] != 1 || got["OP_A/E1"][0] != 0 || got["OP_A/E1"][3] != 0 {
		t.Fatalf("OP_A sayımı: %v", got["OP_A/E1"])
	}
	if len(got["OP_OLD/E0"]) != 4 || got["OP_OLD/E0"][1] != 0 {
		t.Fatalf("aktif anahtar dense sıfır almalı: %v", got["OP_OLD/E0"])
	}
	if _, ok := got["OP_Z/E9"]; ok {
		t.Fatal("pencere dışı satır sayılmamalı")
	}
	if _, ok := res.Seen[CounterKey{Op: "OP_A", Code: "E1", Channel: "MOB", Qualifier: "-"}]; !ok {
		t.Fatal("seen")
	}
	// Tavan: 1 aktif + 2 yeni, maxKeys 2 → küçük olan "diğer"e katlanır.
	res2 := BucketRows("oracle-prod", rows, from, to, active, map[string]bool{"IGN": true}, 2)
	if res2.Overflow != 1 || res2.Keys != 3 { // aktif + OP_A + diğer
		t.Fatalf("tavan: overflow=%d keys=%d", res2.Overflow, res2.Keys)
	}
	other := 0.0
	for _, p := range res2.Points {
		if p.AttrValues[1] == counterOtherCode {
			other += p.Value
		}
	}
	if other != 1 { // OP_B/E2 tek satır
		t.Fatalf("diğer serisi toplamı %v", other)
	}
	if counterFingerprint("a", CounterKey{"x", "y", "z", "-"}) == counterFingerprint("a", CounterKey{"x", "y", "w", "-"}) || counterFingerprint("a", CounterKey{"x", "y", "z", "-"}) != counterFingerprint("a", CounterKey{"x", "y", "z", "-"}) {
		t.Fatal("parmak izi seri başına kararlı ve ayrık")
	}
	if len(BucketRows("", rows, from, to, nil, nil, 0).Points) != 0 {
		t.Fatal("kaynak adı yoksa yazım yok")
	}
}

type fakeMetricSink struct {
	calls [][]*chstore.MetricPoint
	err   error
}

func (f *fakeMetricSink) InsertMetrics(_ context.Context, pts []*chstore.MetricPoint) error {
	f.calls = append(f.calls, pts)
	return f.err
}

func TestCounterHandleCarriesActiveKeys(t *testing.T) {
	sink := &fakeMetricSink{}
	c := NewCounter(sink)
	now := time.Date(2026, 9, 23, 12, 10, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	src := SourceConfig{ID: "o-1", Name: "oracle-prod"}
	from, to := now.Add(-3*time.Minute), now
	rows := []chstore.OracleErrorRow{{Time: now.Add(-time.Minute), OperationCode: "OP", ErrorCode: "E", ChannelCode: "C"}}
	r1 := c.Handle(context.Background(), src, rows, from, to, nil)
	if r1.Keys != 1 || len(sink.calls) != 1 || c.ActiveKeys("o-1") != 1 {
		t.Fatalf("ilk tik: %+v aktif=%d", r1, c.ActiveKeys("o-1"))
	}
	// İkinci tik: satır yok → aktif anahtara sıfır yazılır (kapanış için).
	now = now.Add(time.Minute)
	r2 := c.Handle(context.Background(), src, nil, from.Add(time.Minute), to.Add(time.Minute), nil)
	if r2.Keys != 1 || len(r2.Points) == 0 || r2.Points[0].Value != 0 {
		t.Fatalf("satırsız tik sıfır yazmalı: %+v", r2)
	}
	// Yazım hatası: poll düşmez, istatistikte görünür.
	sink.err = errors.New("ch down")
	c.Handle(context.Background(), src, rows, from, to, nil)
	if st, ok := c.Stats("o-1"); !ok || st.Error == "" || c.errors.Load() != 1 {
		t.Fatalf("hata istatistiği: %+v", st)
	}
	// 4 saat görülmeyen anahtar düşer.
	now = now.Add(5 * time.Hour)
	c.Handle(context.Background(), src, nil, now.Add(-time.Minute), now, nil)
	if c.ActiveKeys("o-1") != 0 {
		t.Fatal("bayat anahtar düşmeli")
	}
	c.Forget("o-1")
	if _, ok := c.Stats("o-1"); ok {
		t.Fatal("forget")
	}
}
