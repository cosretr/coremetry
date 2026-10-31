package briefing

// v0.10.1728 — üretici tik maliyeti: sayaç → tik özeti → son + toplamlar,
// tik log satırı. Nil sayaç (talep üzerine üretim) sıfır sayar, çökmez.

import (
	"sync"
	"testing"
)

func TestCostRecord(t *testing.T) {
	ticks := []TickCost{
		{AtUnix: 100, Teams: 3, VMQueries: 18, CHQueries: 40, ESQueries: 3, DurationMs: 900},
		{AtUnix: 400, Teams: 2, VMQueries: 8, CHQueries: 25, ESQueries: 0, DurationMs: 1500},
		{AtUnix: 700, Teams: 0, DurationMs: 5},
	}
	want := []CostTotals{
		{Ticks: 1, Teams: 3, VMQueries: 18, CHQueries: 40, ESQueries: 3, DurationMs: 900, MaxDurationMs: 900},
		{Ticks: 2, Teams: 5, VMQueries: 26, CHQueries: 65, ESQueries: 3, DurationMs: 2400, MaxDurationMs: 1500},
		{Ticks: 3, Teams: 5, VMQueries: 26, CHQueries: 65, ESQueries: 3, DurationMs: 2405, MaxDurationMs: 1500},
	}
	var st CostStats
	for i, tk := range ticks {
		st = st.Record(tk)
		if st.Last != tk {
			t.Errorf("tik %d: son %+v", i, st.Last)
		}
		if st.Total != want[i] {
			t.Errorf("tik %d: toplam %+v want %+v", i, st.Total, want[i])
		}
	}
}

func TestCostCounterAndLogLine(t *testing.T) {
	c := &Cost{}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.AddVM(1); c.AddCH(2); c.AddES(1) }()
	}
	wg.Wait()
	tk := c.Tick(42, 4, 1234)
	if tk != (TickCost{AtUnix: 42, Teams: 4, VMQueries: 10, CHQueries: 20, ESQueries: 10, DurationMs: 1234}) {
		t.Fatalf("tik %+v", tk)
	}
	if got, want := TickLogLine(tk), "[briefing] tick teams=4 vm=10 ch=20 es=10 dur=1234ms"; got != want {
		t.Errorf("log %q want %q", got, want)
	}
	var nilC *Cost
	nilC.AddVM(1)
	nilC.AddCH(1)
	nilC.AddES(1)
	if tk := nilC.Tick(1, 0, 0); tk.VMQueries != 0 || tk.CHQueries != 0 || tk.ESQueries != 0 {
		t.Errorf("nil sayaç %+v", tk)
	}
}
