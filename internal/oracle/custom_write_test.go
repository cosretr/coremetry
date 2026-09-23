package oracle

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.905 — özel SQL kipinde her poll aynı 15 dk'yı yeniden okur: değişmemiş
// satır yeniden YAZILMAZ; Adet değişen satır yazılır; kanca her poll tüm
// satırları görür (sayaç idempotent).
func TestPollCustomSkipsUnchangedRows(t *testing.T) {
	src := customSource()
	src.ID, src.Name, src.IntervalSec = "o-eeeeeeee", "master-log", 60
	slice := float64(time.Date(2026, 9, 10, 8, 58, 0, 0, time.UTC).Unix())
	adet := 3.0
	w, sink, _, now := newTestWorker(t, src, &fakeState{}, func(int, []any) ([]map[string]any, error) {
		return []map[string]any{{
			"TIMESLICE": slice, "OPERATIONCODE": "OP", "FUNCTIONCODE": "F", "KANALKOD": "K",
			"ADET": adet, "TRACEIDS": "4bf92f3577b34da6a3ce929d0e0e4736",
		}}, nil
	})
	hooked := 0
	w.SetRowsHook(func(_ context.Context, _ SourceConfig, rows []chstore.OracleErrorRow, _, _ time.Time, _ bool) {
		hooked += len(rows)
	})
	w.Tick(context.Background())
	if len(sink.calls) != 1 || len(sink.calls[0]) != 2 {
		t.Fatalf("ilk poll 2 satır yazmalı: %v", sink.calls)
	}
	*now = now.Add(61 * time.Second)
	w.Tick(context.Background())
	if len(sink.calls) != 1 {
		t.Fatalf("değişmeyen satır yeniden yazıldı: %d çağrı", len(sink.calls))
	}
	if st := w.Status()[0]; st.LastSkipped != 2 || st.LastMapped != 2 {
		t.Fatalf("durum: %+v", st)
	}
	adet = 4 // geç commit: artan satırın ağırlığı değişti
	*now = now.Add(61 * time.Second)
	w.Tick(context.Background())
	if len(sink.calls) != 2 || len(sink.calls[1]) != 1 || sink.calls[1][0].TraceID != "" {
		t.Fatalf("yalnız ağırlığı değişen artan satır yazılmalı: %v", sink.calls)
	}
	if hooked != 6 {
		t.Fatalf("kanca her poll tüm satırları görmeli: %d", hooked)
	}
}

// Patlatma tavanı: bir partide maxExpandedPerBatch'ten fazla satır açılmaz;
// kalan gruplar trace'siz tek satır, ağırlık korunur.
func TestExpandCapPerBatch(t *testing.T) {
	m, _ := NewMapper(aggregatedSrc())
	var list string
	for i := 0; i < 500; i++ {
		if i > 0 {
			list += ","
		}
		list += fmt.Sprintf("%032x", i+1)
	}
	groups := maxExpandedPerBatch/500 + 3
	rows := make([]map[string]any, groups)
	for i := range rows {
		r := aggregatedRow(float64(600), list)
		r["OPERATIONCODE"] = fmt.Sprintf("OP%d", i)
		rows[i] = r
	}
	out, st := m.MapAll(rows)
	if !st.ExpandCapped || st.Expanded > maxExpandedPerBatch+501 {
		t.Fatalf("tavan: capped=%v expanded=%d", st.ExpandCapped, st.Expanded)
	}
	total := 0
	for _, r := range out {
		total += r.EffectiveWeight()
	}
	if total != 600*groups {
		t.Fatalf("ağırlık korunmalı: %d, beklenen %d", total, 600*groups)
	}
}
