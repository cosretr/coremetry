package ml

// v0.10.1814 — günlük baseline işi: 16 dilim akışla okunur, her satır tek
// kez yazılır ve dilimli koşu tek seferlik hesapla aynı satırları verir;
// iş planı önce elle istek, sonra kaçırılan günler (hata sonrası bekleme).

import (
	"context"
	"hash/fnv"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

type fakeBaselineStore struct {
	rows    []chstore.MLSeriesRow
	written []chstore.MLBaselineObs
	reads   int
}

func (f *fakeBaselineStore) MLSeriesDaySlice(_ context.Context, from, to time.Time, slice, slices int, fn func(chstore.MLSeriesRow) error) (int, bool, error) {
	f.reads++
	var sel []chstore.MLSeriesRow
	for _, r := range f.rows {
		h := fnv.New32a()
		_, _ = h.Write([]byte(r.Service))
		if int(h.Sum32())%slices == slice && !r.Bucket.Before(from) && r.Bucket.Before(to) {
			sel = append(sel, r)
		}
	}
	sort.SliceStable(sel, func(i, j int) bool { return seriesRowLess(sel[i], sel[j]) })
	for _, r := range sel {
		if err := fn(r); err != nil {
			return 0, false, err
		}
	}
	return len(sel), false, nil
}

func (f *fakeBaselineStore) InsertMLBaselineObs(_ context.Context, rows []chstore.MLBaselineObs) error {
	f.written = append(f.written, rows...)
	return nil
}

func sortObs(o []chstore.MLBaselineObs) {
	sort.SliceStable(o, func(i, j int) bool {
		a, b := o[i], o[j]
		if a.Signal+a.Service+a.Part != b.Signal+b.Service+b.Part {
			return a.Signal+a.Service+a.Part < b.Signal+b.Service+b.Part
		}
		return a.Slot < b.Slot
	})
}

func TestRunBaselineDaySlicesEqualWhole(t *testing.T) {
	rows := dayFixture()
	// başka günün satırı okunmamalı
	rows = append(rows, chstore.MLSeriesRow{Signal: "log", Service: "orders-api", Part: "_total", Bucket: time.Date(2026, 11, 5, 0, 30, 0, 0, istanbul), BucketSec: 300, Count: 1})
	st := &fakeBaselineStore{rows: rows}
	in := baselineInput{Loc: istanbul, TargetSec: 300, CfgHash: "h1"}
	res, err := runBaselineDay(context.Background(), st, in, "2026-11-04")
	if err != nil {
		t.Fatal(err)
	}
	if st.reads != baselineSlices || res.Slices != baselineSlices {
		t.Fatalf("16 dilim okunmalı: reads=%d res=%+v", st.reads, res)
	}
	want := BaselineDayObs(dayFixture(), in)
	got := append([]chstore.MLBaselineObs(nil), st.written...)
	sortObs(want)
	sortObs(got)
	if !reflect.DeepEqual(got, want) || res.Rows != len(want) {
		t.Fatalf("dilimli koşu tek seferlik hesaptan farklı: %d vs %d satır", len(got), len(want))
	}
}

func TestPlanBaselineWork(t *testing.T) {
	now := time.Date(2026, 11, 5, 4, 0, 0, 0, istanbul)
	st := chstore.MLBaselineStatus{LastDay: "2026-11-03", ManualDone: "r1"}
	ctl := chstore.MLBaselineControl{Run: &chstore.MLBaselineRunRequest{ID: "r2", Day: "2026-10-20"}}
	got := planBaselineWork(st, ctl, now, istanbul, 0)
	want := []baselineWork{{day: "2026-10-20", trigger: "manual", reqID: "r2"}, {day: "2026-11-04", trigger: "auto"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan %+v", got)
	}
	// işlenmiş istek tekrar koşmaz; hata sonrası bekleme dolmadan otomatik yok
	ctl.Run.ID = "r1"
	if got := planBaselineWork(st, ctl, now, istanbul, now.Add(time.Minute).Unix()); len(got) != 0 {
		t.Fatalf("bekleme içinde iş olmamalı: %+v", got)
	}
}
