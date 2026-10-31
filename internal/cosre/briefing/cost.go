package briefing

// cost.go — v0.10.1728: üreticinin tik maliyeti (briefing-audit R9; CLAUDE.md
// "expensive endpoint = register a counter"). Sayım ÇAĞRI NOKTALARINDA
// (api katmanı Cost'u okuma yollarından geçirir), tahmin değil:
//   - vm — VM'e giden istek (range / instant / etiket keşfi; önbellek
//     isabeti sayılmaz) + Kafka sorusu (kaynak VM ise)
//   - ch — chstore çağrısı (okuma + kart yazımı; bir çağrı içeride birden
//     çok SQL koşabilir — sayılan çağrı)
//   - es — ES log okuması (önbellek isabeti sayılmaz); CH log kaynağında ch
// Süreç-içi; restart sıfırlar (Behavior / CodeFetch ile aynı kablo).

import (
	"fmt"
	"sync/atomic"
)

// Cost — TEK tikin sayacı. nil-güvenli (talep üzerine üretim nil geçer).
type Cost struct{ vm, ch, es atomic.Int64 }

func (c *Cost) AddVM(n int) {
	if c != nil {
		c.vm.Add(int64(n))
	}
}

func (c *Cost) AddCH(n int) {
	if c != nil {
		c.ch.Add(int64(n))
	}
}

func (c *Cost) AddES(n int) {
	if c != nil {
		c.es.Add(int64(n))
	}
}

// Tick — sayacın tik özeti (teams + süre çağırandan).
func (c *Cost) Tick(atUnix int64, teams int, durationMs int64) TickCost {
	t := TickCost{AtUnix: atUnix, Teams: int64(teams), DurationMs: durationMs}
	if c != nil {
		t.VMQueries, t.CHQueries, t.ESQueries = c.vm.Load(), c.ch.Load(), c.es.Load()
	}
	return t
}

// TickCost — bir tikin ölçümü.
type TickCost struct {
	AtUnix     int64 `json:"at_unix"`
	Teams      int64 `json:"teams"`
	VMQueries  int64 `json:"vm_queries"`
	CHQueries  int64 `json:"ch_queries"`
	ESQueries  int64 `json:"es_queries"`
	DurationMs int64 `json:"duration_ms"`
}

// CostTotals — süreç başından beri toplamlar.
type CostTotals struct {
	Ticks         int64 `json:"ticks"`
	Teams         int64 `json:"teams"`
	VMQueries     int64 `json:"vm_queries"`
	CHQueries     int64 `json:"ch_queries"`
	ESQueries     int64 `json:"es_queries"`
	DurationMs    int64 `json:"duration_ms"`
	MaxDurationMs int64 `json:"max_duration_ms"`
}

// CostStats — /admin/stats `briefing` bloğu.
type CostStats struct {
	Last  TickCost   `json:"last"`
	Total CostTotals `json:"total"`
}

// Record — SAF: tik → son + toplamlar.
func (s CostStats) Record(t TickCost) CostStats {
	s.Last = t
	s.Total.Ticks++
	s.Total.Teams += t.Teams
	s.Total.VMQueries += t.VMQueries
	s.Total.CHQueries += t.CHQueries
	s.Total.ESQueries += t.ESQueries
	s.Total.DurationMs += t.DurationMs
	if t.DurationMs > s.Total.MaxDurationMs {
		s.Total.MaxDurationMs = t.DurationMs
	}
	return s
}

// TickLogLine — SAF: tik başına info satırı.
func TickLogLine(t TickCost) string {
	return fmt.Sprintf("[briefing] tick teams=%d vm=%d ch=%d es=%d dur=%dms", t.Teams, t.VMQueries, t.CHQueries, t.ESQueries, t.DurationMs)
}
