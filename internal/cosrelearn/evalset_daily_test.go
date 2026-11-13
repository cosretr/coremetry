package cosrelearn

// evalset_daily_test.go — v0.10.2109 (kalite döngüsü D4): günlük evalset
// koşusunun zamanlaması, acil freni ve günde-bir kuralı.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestEvalsetDailyEnabled(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"", true},                   // anahtar yok → varsayılan açık
		{`{}`, true},                 // alan yok → açık
		{`{"enabled":true}`, true},   //
		{`{"enabled":false}`, false}, // fren
		{`{bozuk`, false},            // bozuk blob → fren niyeti
		{`{"enabled":"no"}`, false},  // yanlış tip da bozuk
	} {
		if got := EvalsetDailyEnabled([]byte(tc.raw)); got != tc.want {
			t.Errorf("EvalsetDailyEnabled(%q) = %v, istenen %v", tc.raw, got, tc.want)
		}
	}
}

func TestEvalsetDailyDue(t *testing.T) {
	day := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	ist := time.FixedZone("TRT", 3*3600)
	for _, tc := range []struct {
		name  string
		now   time.Time
		qdone bool
		want  bool
	}{
		{"gün kapanışı + 1sa59dk, kalite bitti", day.Add(2*time.Hour - time.Minute), true, false},
		{"+2sa tam, kalite bitti", day.Add(2 * time.Hour), true, true},
		{"+2sa, kalite bitmedi → bekle", day.Add(2 * time.Hour), false, false},
		{"+3sa59dk, kalite bitmedi → bekle", day.Add(4*time.Hour - time.Minute), false, false},
		{"+4sa, kalite bitmedi → yine koş", day.Add(4 * time.Hour), false, true},
		{"gün sonu, kalite bitti", day.Add(23*time.Hour + 59*time.Minute), true, true},
		// UTC dışı saat dilimi: 05:30 TRT = 02:30 UTC → vakti geldi.
		{"TRT 05:30 = UTC 02:30", time.Date(2026, 11, 14, 5, 30, 0, 0, ist), true, true},
		// 04:30 TRT = 01:30 UTC → henüz değil (yerel saat 02'yi geçse de).
		{"TRT 04:30 = UTC 01:30", time.Date(2026, 11, 14, 4, 30, 0, 0, ist), true, false},
	} {
		if got := EvalsetDailyDue(tc.now, tc.qdone); got != tc.want {
			t.Errorf("%s: EvalsetDailyDue = %v, istenen %v", tc.name, got, tc.want)
		}
	}
}

func TestEvalsetDailyRanToday(t *testing.T) {
	now := time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC)
	today := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		runs []chstore.EvalRun
		want bool
	}{
		{"boş", nil, false},
		{"bugün elle koşu", []chstore.EvalRun{{StartedBy: "admin@example.com", StartedAt: today.Add(time.Hour)}}, false},
		{"dün günlük koşu", []chstore.EvalRun{{StartedBy: EvalsetDailyStartedBy, StartedAt: today.Add(-time.Second)}}, false},
		{"bugün 00:00 günlük koşu", []chstore.EvalRun{{StartedBy: EvalsetDailyStartedBy, StartedAt: today}}, true},
		{"bugün günlük koşu, elle olanın arkasında", []chstore.EvalRun{
			{StartedBy: "admin@example.com", StartedAt: today.Add(2 * time.Hour)},
			{StartedBy: EvalsetDailyStartedBy, StartedAt: today.Add(time.Hour)},
		}, true},
	} {
		if got := EvalsetDailyRanToday(tc.runs, now); got != tc.want {
			t.Errorf("%s: %v, istenen %v", tc.name, got, tc.want)
		}
	}
}

type fakeDailyStore struct {
	setting  []byte
	qdone    bool
	runs     []chstore.EvalRun
	listErr  error
	listHits int
}

func (f *fakeDailyStore) GetSetting(context.Context, string) ([]byte, error) { return f.setting, nil }
func (f *fakeDailyStore) CosreQualityDayDone(context.Context, time.Time) (bool, error) {
	return f.qdone, nil
}
func (f *fakeDailyStore) ListEvalRuns(context.Context, int) ([]chstore.EvalRun, error) {
	f.listHits++
	return f.runs, f.listErr
}

// TestEvalsetDailyTick — fren, günde-bir, atlama sonrası yeniden deneme.
func TestEvalsetDailyTick(t *testing.T) {
	ctx := context.Background()
	day := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	due := day.Add(2*time.Hour + time.Minute)

	t.Run("fren kapalıyken koşucu çağrılmaz", func(t *testing.T) {
		st := &fakeDailyStore{setting: []byte(`{"enabled":false}`), qdone: true}
		calls := 0
		run := func(context.Context) (bool, string, error) { calls++; return true, "", nil }
		var s evalsetDailyState
		evalsetDailyTick(ctx, st, run, due, &s)
		if calls != 0 || st.listHits != 0 {
			t.Fatalf("fren kapalı: koşucu %d, liste %d çağrıldı", calls, st.listHits)
		}
	})

	t.Run("vakti gelmeden CH'ye gidilmez", func(t *testing.T) {
		st := &fakeDailyStore{qdone: true}
		calls := 0
		run := func(context.Context) (bool, string, error) { calls++; return true, "", nil }
		var s evalsetDailyState
		evalsetDailyTick(ctx, st, run, day.Add(time.Hour), &s)
		if calls != 0 || st.listHits != 0 {
			t.Fatalf("erken tik: koşucu %d, liste %d", calls, st.listHits)
		}
	})

	t.Run("günde bir: başladıysa aynı gün tekrar yok, ertesi gün var", func(t *testing.T) {
		st := &fakeDailyStore{qdone: true}
		calls := 0
		run := func(context.Context) (bool, string, error) { calls++; return true, "", nil }
		var s evalsetDailyState
		evalsetDailyTick(ctx, st, run, due, &s)
		evalsetDailyTick(ctx, st, run, due.Add(15*time.Minute), &s)
		if calls != 1 {
			t.Fatalf("aynı gün %d koşu, istenen 1", calls)
		}
		evalsetDailyTick(ctx, st, run, due.Add(24*time.Hour), &s)
		if calls != 2 {
			t.Fatalf("ertesi gün koşu yok (%d)", calls)
		}
	})

	t.Run("CH'de bugünün günlük satırı varsa koşulmaz (başka pod / yeniden başlatma)", func(t *testing.T) {
		st := &fakeDailyStore{qdone: true, runs: []chstore.EvalRun{{StartedBy: EvalsetDailyStartedBy, StartedAt: day.Add(2 * time.Hour)}}}
		calls := 0
		run := func(context.Context) (bool, string, error) { calls++; return true, "", nil }
		var s evalsetDailyState
		evalsetDailyTick(ctx, st, run, due.Add(time.Hour), &s)
		if calls != 0 {
			t.Fatalf("bugün koşmuş: koşucu %d kez çağrıldı", calls)
		}
	})

	t.Run("atlama (yerel profil yok) sonraki tikte yeniden denenir", func(t *testing.T) {
		st := &fakeDailyStore{qdone: true}
		calls := 0
		run := func(context.Context) (bool, string, error) { calls++; return false, "yerel değil", nil }
		var s evalsetDailyState
		evalsetDailyTick(ctx, st, run, due, &s)
		evalsetDailyTick(ctx, st, run, due.Add(15*time.Minute), &s)
		if calls != 2 {
			t.Fatalf("atlama sonrası yeniden deneme yok (%d)", calls)
		}
	})

	t.Run("koşucu hatası aynı gün tekrar denenmez; liste okunamazsa koşu yok", func(t *testing.T) {
		st := &fakeDailyStore{qdone: true}
		calls := 0
		run := func(context.Context) (bool, string, error) { calls++; return false, "", errors.New("fikstür") }
		var s evalsetDailyState
		evalsetDailyTick(ctx, st, run, due, &s)
		evalsetDailyTick(ctx, st, run, due.Add(15*time.Minute), &s)
		if calls != 1 {
			t.Fatalf("hata sonrası %d çağrı, istenen 1", calls)
		}
		st2 := &fakeDailyStore{qdone: true, listErr: errors.New("ch")}
		calls = 0
		var s2 evalsetDailyState
		evalsetDailyTick(ctx, st2, run, due, &s2)
		if calls != 0 {
			t.Fatal("liste okunamadı ama koşu başladı")
		}
	})
}
