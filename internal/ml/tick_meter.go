package ml

// tick_meter.go — v0.10.1812 (öğrenme döngüsü audit §10 dilim 1.1, §13 D3/D10):
// ölçek ölçümü. Prod'da ML ana anahtarı kapalı; operatör gölgede açınca her
// tik sinyal türü başına şunu söyler: kaç servis görüldü, kaç seri yazıldı,
// ES / VM / CH'ye kaç sorgu gitti, süre, ve ES log terms tavanı (MaxServices)
// dışında kaç servis kaldı. Tespit davranışı DEĞİŞMEZ.
//
// Kaynaklar: ES/VM sayaçları Deps.ObserveES/ObserveVM'in global sayaçlarının
// dedektör çevresindeki farkı (dedektörler sırayla koşar); CH sayısı ctx
// sayacı (chstore.WithQueryCounter, tracedConn kapısı); seri/servis
// Deps.insertSeries'ten; tavan dışı servis ES yanıtından (logstore
// MLDroppedServices). Seri yazmayan dedektör (change_point, pod aykırısı)
// yalnız aday servisleriyle sayılır.

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// tickMeter — bir dedektör çağrısının seri/servis/tavan ölçümü.
type tickMeter struct {
	mu       sync.Mutex
	rows     int
	services map[string]struct{}
	series   map[string]struct{}
	dropped  int
}

func newTickMeter() *tickMeter {
	return &tickMeter{services: map[string]struct{}{}, series: map[string]struct{}{}}
}

func (m *tickMeter) observeRows(rows []chstore.MLSeriesRow) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.rows += len(rows)
	for _, r := range rows {
		m.services[r.Service] = struct{}{}
		m.series[chstore.MLSeriesKey(r.Service, r.Part)] = struct{}{}
	}
	m.mu.Unlock()
}

// observeDropped — aynı tikte iki ES isteği (log RED + kırılım) aynı servis
// kümesini görür: toplam değil en büyük.
func (m *tickMeter) observeDropped(n int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if n > m.dropped {
		m.dropped = n
	}
	m.mu.Unlock()
}

// insertSeries — tik yolundaki tek ml_series yazımı: yazar ve ölçer.
// Backfill bunu KULLANMAZ (tik ölçümüne girmez).
func (d Deps) insertSeries(ctx context.Context, rows []chstore.MLSeriesRow) error {
	if err := d.Store.InsertMLSeries(ctx, rows); err != nil {
		return err
	}
	d.meter.observeRows(rows)
	return nil
}

// kindSample — bir dedektör çağrısının ham ölçümü (foldKindTicks girdisi).
type kindSample struct {
	Kind       string
	Services   map[string]struct{}
	Series     map[string]struct{}
	Rows       int
	ES, VM, CH int
	Dropped    int
	DurationMs int64
}

// sampleOf — meter + aday servisleri → örnek.
func sampleOf(kind string, m *tickMeter, evs []chstore.MLEvent, es, vm, ch int, ms int64) kindSample {
	s := kindSample{Kind: kind, Services: map[string]struct{}{}, Series: map[string]struct{}{}, ES: es, VM: vm, CH: ch, DurationMs: ms}
	if m != nil {
		m.mu.Lock()
		for k := range m.services {
			s.Services[k] = struct{}{}
		}
		for k := range m.series {
			s.Series[k] = struct{}{}
		}
		s.Rows, s.Dropped = m.rows, m.dropped
		m.mu.Unlock()
	}
	for _, e := range evs {
		s.Services[e.Service] = struct{}{}
	}
	return s
}

// foldKindTicks — SAF: dedektör örnekleri → sinyal türü başına ölçüm.
// Servis ve seri BİRLEŞİM (aynı servis iki dedektörde bir kez), sorgu ve
// süre TOPLAM, tavan dışı servis EN BÜYÜK.
func foldKindTicks(samples []kindSample) map[string]chstore.MLKindTick {
	svc := map[string]map[string]struct{}{}
	ser := map[string]map[string]struct{}{}
	out := map[string]chstore.MLKindTick{}
	for _, s := range samples {
		if s.Kind == "" {
			continue
		}
		if svc[s.Kind] == nil {
			svc[s.Kind], ser[s.Kind] = map[string]struct{}{}, map[string]struct{}{}
		}
		for k := range s.Services {
			svc[s.Kind][k] = struct{}{}
		}
		for k := range s.Series {
			ser[s.Kind][k] = struct{}{}
		}
		k := out[s.Kind]
		k.SeriesRows += s.Rows
		k.ES += s.ES
		k.VM += s.VM
		k.CH += s.CH
		k.DurationMs += s.DurationMs
		k.Dropped = max(k.Dropped, s.Dropped)
		out[s.Kind] = k
	}
	for kind, k := range out {
		k.Services, k.Series = len(svc[kind]), len(ser[kind])
		out[kind] = k
	}
	return out
}

// kindTickLines — SAF: tik başına sinyal türü log satırları (sabit sıra).
func kindTickLines(kinds map[string]chstore.MLKindTick) []string {
	names := make([]string, 0, len(kinds))
	for n := range kinds {
		names = append(names, n)
	}
	order := map[string]int{chstore.MLSignalLog: 0, chstore.MLSignalTrace: 1, chstore.MLSignalMetric: 2}
	sort.Slice(names, func(i, j int) bool {
		oi, iok := order[names[i]]
		oj, jok := order[names[j]]
		if iok != jok {
			return iok
		}
		if oi != oj {
			return oi < oj
		}
		return names[i] < names[j]
	})
	out := make([]string, 0, len(names))
	for _, n := range names {
		k := kinds[n]
		out = append(out, fmt.Sprintf("[ml] tick kind=%s services=%d series=%d rows=%d es=%d vm=%d ch=%d dropped=%d dur=%dms",
			n, k.Services, k.Series, k.SeriesRows, k.ES, k.VM, k.CH, k.Dropped, k.DurationMs))
	}
	return out
}
