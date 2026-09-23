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
	to := from.Add(3 * time.Minute) // to=12:03:30 → tamamlanmış dakikalar 12:00..12:02 → 3 kova (12:03 kısmi, yazılmaz)
	row := func(min int, op, code, ch string) chstore.OracleErrorRow {
		return chstore.OracleErrorRow{Time: time.Date(2026, 9, 23, 12, min, 10, 0, time.UTC), OperationCode: op, ErrorCode: code, ChannelCode: ch}
	}
	rows := []chstore.OracleErrorRow{row(1, "OP_A", "E1", "MOB"), row(1, "OP_A", "E1", "MOB"), row(2, "OP_A", "E1", "MOB"), row(2, "OP_B", "E2", "WEB"), row(9, "OP_Z", "E9", "X"), row(1, "OP_I", "IGN", "MOB")}
	active := map[CounterKey]time.Time{{Op: "OP_OLD", Code: "E0", Channel: "ATM", Qualifier: "-"}: from.Add(-time.Hour)}
	res := BucketRows("oracle-prod", rows, from, to, active, map[string]bool{"IGN": true}, 0, nil)
	if res.Minutes != 3 || res.Keys != 3 || res.Ignored != 1 || res.Overflow != 0 || len(res.Points) != 9 {
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
	if got["OP_A/E1"][1] != 2 || got["OP_A/E1"][2] != 1 || got["OP_A/E1"][0] != 0 {
		t.Fatalf("OP_A sayımı: %v", got["OP_A/E1"])
	}
	if _, partial := got["OP_A/E1"][3]; partial {
		t.Fatal("to'nun kısmi dakikası (12:03) yazılmamalı")
	}
	if len(got["OP_OLD/E0"]) != 3 || got["OP_OLD/E0"][1] != 0 {
		t.Fatalf("aktif anahtar dense sıfır almalı: %v", got["OP_OLD/E0"])
	}
	if _, ok := got["OP_Z/E9"]; ok {
		t.Fatal("pencere dışı satır sayılmamalı")
	}
	if _, ok := res.Seen[CounterKey{Op: "OP_A", Code: "E1", Channel: "MOB", Qualifier: "-"}]; !ok {
		t.Fatal("seen")
	}
	// Tavan: 1 aktif + 2 yeni, maxKeys 2 → küçük olan "diğer"e katlanır.
	res2 := BucketRows("oracle-prod", rows, from, to, active, map[string]bool{"IGN": true}, 2, nil)
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
	if len(BucketRows("", rows, from, to, nil, nil, 0, nil).Points) != 0 {
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
	r1 := c.Handle(context.Background(), src, rows, from, to, nil, nil)
	if r1.Keys != 1 || len(sink.calls) != 1 || c.ActiveKeys("o-1") != 1 {
		t.Fatalf("ilk tik: %+v aktif=%d", r1, c.ActiveKeys("o-1"))
	}
	// İkinci tik: satır yok → aktif anahtara sıfır yazılır (kapanış için).
	now = now.Add(time.Minute)
	r2 := c.Handle(context.Background(), src, nil, from.Add(time.Minute), to.Add(time.Minute), nil, nil)
	if r2.Keys != 1 || len(r2.Points) == 0 || r2.Points[0].Value != 0 {
		t.Fatalf("satırsız tik sıfır yazmalı: %+v", r2)
	}
	// Yazım hatası: poll düşmez, istatistikte görünür.
	sink.err = errors.New("ch down")
	c.Handle(context.Background(), src, rows, from, to, nil, nil)
	if st, ok := c.Stats("o-1"); !ok || st.Error == "" || c.errors.Load() != 1 {
		t.Fatalf("hata istatistiği: %+v", st)
	}
	// 4 saat görülmeyen anahtar düşer.
	now = now.Add(5 * time.Hour)
	c.Handle(context.Background(), src, nil, now.Add(-time.Minute), now, nil, nil)
	if c.ActiveKeys("o-1") != 0 {
		t.Fatal("bayat anahtar düşmeli")
	}
	c.Forget("o-1")
	if _, ok := c.Stats("o-1"); ok {
		t.Fatal("forget")
	}
}

// v0.10.899 (dilim F) — jenerik kod qualifier'ı: external_code → exception tipi
// → "generic"; jenerik olmayan kod "-"; (op,kod,kanal) başına ≤50 qualifier,
// fazlası "_other".
func TestQualifierAndCap(t *testing.T) {
	q := QualifierFor(map[string]bool{"ERR_020": true}, func(id string) string {
		if id == "t-ex" {
			return "java.lang.NullPointerException"
		}
		return ""
	})
	row := func(code, ext, tid string) chstore.OracleErrorRow {
		return chstore.OracleErrorRow{ErrorCode: code, ExternalCode: ext, TraceID: tid}
	}
	if q(row("ERR_020", " x1 ", "")) != "X1" || q(row("err_020", "", "t-ex")) != "java.lang.NullPointerException" || q(row("ERR_020", "", "t-none")) != "generic" || q(row("ERR_028", "x1", "t-ex")) != "-" {
		t.Fatal("qualifier sırası")
	}
	from := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	rows := []chstore.OracleErrorRow{}
	for i := 0; i < counterMaxQualifiers+5; i++ {
		rows = append(rows, chstore.OracleErrorRow{Time: from.Add(10 * time.Second), OperationCode: "OP", ErrorCode: "ERR_020", ChannelCode: "MOB", ExternalCode: "E" + itoa(i)})
	}
	res := BucketRows("src", rows, from, from.Add(time.Minute), nil, nil, 0, q)
	other := 0.0
	for _, p := range res.Points {
		if p.AttrValues[3] == counterOtherCode {
			other += p.Value
		}
	}
	if res.Keys != counterMaxQualifiers+1 || other != 5 {
		t.Fatalf("qualifier tavanı: keys=%d other=%v", res.Keys, other)
	}
}

// v0.10.900 (inceleme) — catch-up kelepçesi (24 s'lik pencere → ≤240 dk), capped
// tikte son satır dakikasından sonrası yazılmaz, ignore küçük harf/boşlukla da
// eşleşir, CHAR dolgusu ayrı seri açmaz.
func TestBucketRowsClampsAndIgnoreNormalize(t *testing.T) {
	to := time.Date(2026, 9, 23, 12, 3, 30, 0, time.UTC)
	from := to.Add(-24 * time.Hour)
	rows := []chstore.OracleErrorRow{{Time: to.Add(-time.Minute), OperationCode: "OP ", ErrorCode: " err_020 ", ChannelCode: "MOB"}}
	res := BucketRows("src", rows, from, to, nil, map[string]bool{"ERR_020": true}, 0, nil)
	if res.Ignored != 1 || len(res.Points) != 0 {
		t.Fatalf("ignore normalize: %+v", res)
	}
	res = BucketRows("src", rows, from, to, nil, nil, 0, nil)
	if res.Minutes != 240 || res.Keys != 1 || res.Points[0].AttrValues[0] != "OP" || res.Points[0].AttrValues[1] != "err_020" {
		t.Fatalf("kelepçe/trim: minutes=%d keys=%d attrs=%v", res.Minutes, res.Keys, res.Points[0].AttrValues)
	}
	// Capped: 5000 satır, sonuncusu 11:50 → m1 = 11:50, sonrası yazılmaz.
	capped := make([]chstore.OracleErrorRow, pollRowCap)
	for i := range capped {
		capped[i] = chstore.OracleErrorRow{Time: to.Add(-13 * time.Minute), OperationCode: "OP", ErrorCode: "E", ChannelCode: "C"}
	}
	res = BucketRows("src", capped, to.Add(-20*time.Minute), to, nil, nil, 0, nil)
	last := res.Points[len(res.Points)-1].Time
	if !last.Equal(to.Add(-13 * time.Minute).Truncate(time.Minute)) {
		t.Fatalf("capped tikte son dakika son satır olmalı: %v", last)
	}
}
