package oracle

// exgroups_more_test.go — v0.10.1092 inceleme turu pinleri: imleç HER turda
// kalıcı blob'dan (başka lider ilerlettiyse ikinci sayım yok; kendi imleç
// kaydımız düştüyse bellek ilerideyse o), toplama tavanında imleç yalnız TAM
// okunan dakikaya kadar, saatlik dilimler (Oracle öncelik kuralının girdisi),
// istatistik önbelleği.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// H1 — liderlik el değiştirip geri gelince bellekteki ESKİ imleç kullanılmaz:
// başka liderin yazdığı ileri imleç blob'dan okunur, aralık ikinci kez sayılmaz.
func TestExGroupCursorReReadEachRefresh(t *testing.T) {
	base := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	st := &fakeExStore{rows: steadyRows(base)}
	state := &fakeState{}
	src := exSource(ProblemModeShadow)
	r := NewExGroupRefresher(st, state, nil)
	now := base.Add(46 * time.Minute) // kapanış 10:30
	r.now = func() time.Time { return now }
	if _, err := r.Refresh(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	fpA := chstore.OracleGroupFingerprint(src.ID, "APP_ERR_001", "OP_A")
	if st.total[fpA] != 30*5 {
		t.Fatalf("ilk tur 10:00–10:30: %v", st.total)
	}
	// Başka lider 10:30–10:50'yi saydı ve imleci 10:50'ye yazdı.
	other := DecodeExGroupState(state.kv[ExGroupKey(src.ID)])
	other.CursorNs = base.Add(50 * time.Minute).UnixNano()
	raw, _ := json.Marshal(other)
	state.kv[ExGroupKey(src.ID)] = raw
	st.total[fpA] += 20 * 5
	// Bu lider geri döner, kapanış 11:00: yalnız 10:50–11:00 sayılmalı.
	now = base.Add(76 * time.Minute)
	res, err := r.Refresh(context.Background(), src)
	if err != nil || !res.From.Equal(base.Add(50*time.Minute)) {
		t.Fatalf("blob imleci kazanmalı: %+v %v", res, err)
	}
	if st.total[fpA] != 60*5 {
		t.Fatalf("her dakika bir kez: %d", st.total[fpA])
	}
}

// H1 — kendi imleç kaydımız düştüyse (blob geride) bellek ilerideyse o kazanır.
type failPutState struct{ fakeState }

func (f *failPutState) PutSetting(context.Context, string, []byte) error {
	return errors.New("ch down")
}

func TestExGroupMemoryWinsWhenOwnSaveFailed(t *testing.T) {
	base := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	st := &fakeExStore{rows: steadyRows(base)}
	src := exSource(ProblemModeShadow)
	r := NewExGroupRefresher(st, &failPutState{}, nil)
	now := base.Add(76 * time.Minute)
	r.now = func() time.Time { return now }
	if _, err := r.Refresh(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	calls := st.aggCalls
	if res, _ := r.Refresh(context.Background(), src); res.Skipped == "" || st.aggCalls != calls {
		t.Fatalf("kayıt düştü ama aynı süreç yeniden saymamalı: %+v", res)
	}
	// İmleç okuması düşerse tur düşer (sayım yok).
	r2 := NewExGroupRefresher(st, errState{}, nil)
	r2.now = func() time.Time { return now }
	if _, err := r2.Refresh(context.Background(), src); err == nil || st.aggCalls != calls {
		t.Fatal("imleç okunamazsa toplama koşmamalı")
	}
}

type errState struct{}

func (errState) GetSetting(context.Context, string) ([]byte, error) {
	return nil, errors.New("ch down")
}
func (errState) PutSetting(context.Context, string, []byte) error { return nil }

// L7 — toplama tavanı: son (yarım olabilecek) dakika atılır, üst sınır o
// dakikanın başı; tek dakika tavanı aşıyorsa o dakika sayılır ve ilerlenir.
func TestClipAggsAtLimit(t *testing.T) {
	m0 := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	mk := func(min int) chstore.OracleGroupAgg {
		return chstore.OracleGroupAgg{Code: "APP_ERR_001", Op: "OP_A", Minute: m0.Add(time.Duration(min) * time.Minute), Weight: 1}
	}
	aggs := []chstore.OracleGroupAgg{mk(0), mk(0), mk(1), mk(2), mk(2)}
	to := m0.Add(10 * time.Minute)
	if kept, end, clipped := clipAggsAtLimit(aggs, m0, to, 10); clipped || len(kept) != 5 || !end.Equal(to) {
		t.Fatal("tavan altında dokunulmaz")
	}
	kept, end, clipped := clipAggsAtLimit(aggs, m0, to, 5)
	if !clipped || len(kept) != 3 || !end.Equal(m0.Add(2*time.Minute)) {
		t.Fatalf("son dakika atılmalı: %d %v", len(kept), end)
	}
	one := []chstore.OracleGroupAgg{mk(0), mk(0)}
	if kept, end, clipped := clipAggsAtLimit(one, m0, to, 2); !clipped || len(kept) != 2 || !end.Equal(m0.Add(time.Minute)) {
		t.Fatalf("tek dakika tavanı: %d %v", len(kept), end)
	}
}

// M4 girdisi — saatlik dilimler: kapanış ucundan geriye son 1 sa / önceki 1 sa.
func TestHourStatsFromSlots(t *testing.T) {
	base := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	var rows []minuteRow
	for i := 0; i < 120; i++ { // 09:00–10:59: ilk saat dakikada 1, ikinci saat dakikada 4
		w := uint64(1)
		if i >= 60 {
			w = 4
		}
		rows = append(rows, minuteRow{base.Add(time.Duration(i) * time.Minute), "APP_ERR_001", "OP_A", "MOB", w})
	}
	st := &fakeExStore{rows: rows}
	state := &fakeState{}
	src := exSource(ProblemModeShadow)
	r := NewExGroupRefresher(st, state, nil)
	r.now = func() time.Time { return base.Add(136 * time.Minute) } // kapanış 11:00
	if _, err := r.Refresh(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	blob := DecodeExGroupState(state.kv[ExGroupKey(src.ID)])
	b := blob.Groups[chstore.OracleGroupFingerprint(src.ID, "APP_ERR_001", "OP_A")]
	last, prev := HourStats(b, blob.CursorNs)
	if last != 240 || prev != 60 || len(b.Slots) != 12 {
		t.Fatalf("son 1 sa 240, önceki 60, 12 dilim: %d %d %d", last, prev, len(b.Slots))
	}
	if l, p := HourStats(nil, blob.CursorNs); l != 0 || p != 0 {
		t.Fatal("nil kırılım 0")
	}
}

// İstatistik önbelleği: kaynak çözümü, gecikme, saatlik toplamlar, TTL.
func TestGroupStatsCache(t *testing.T) {
	src := exSource(ProblemModeLive)
	fp := chstore.OracleGroupFingerprint(src.ID, "APP_ERR_001", "OP_A")
	end := time.Date(2026, 10, 3, 11, 0, 0, 0, time.UTC)
	blob := ExGroupState{V: 1, CursorNs: end.UnixNano(), Groups: map[string]*ExGroupBreakdown{
		fp: {Code: "APP_ERR_001", Op: "OP_A", Slots: map[int64]uint64{end.Add(-10 * time.Minute).Unix(): 7000, end.Add(-70 * time.Minute).Unix(): 1000}},
	}}
	raw, _ := json.Marshal(blob)
	reads := 0
	c := NewGroupStatsCache(func(context.Context, string) ([]byte, error) { reads++; return raw, nil },
		func() Settings { return Settings{Sources: []SourceConfig{src}} })
	now := time.Date(2026, 10, 3, 11, 16, 30, 0, time.UTC)
	c.now = func() time.Time { return now }
	g := chstore.ExceptionGroup{Fingerprint: fp, Type: "APP_ERR_001", Message: "OP_A"}
	st, ok := c.Stats(g)
	if !ok || !st.BlobOK || st.LastHour != 7000 || st.PrevHour != 1000 || st.Lag != 16*time.Minute+30*time.Second || st.Source.ID != src.ID {
		t.Fatalf("istatistik: %+v %v", st, ok)
	}
	_, _ = c.Stats(g)
	if reads != 1 {
		t.Fatalf("TTL içinde tek okuma: %d", reads)
	}
	if _, ok := c.Stats(chstore.ExceptionGroup{Fingerprint: "0a1b"}); ok {
		t.Fatal("span grubu istatistik almaz")
	}
}
