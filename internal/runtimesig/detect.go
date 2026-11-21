package runtimesig

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// detect.go — SAF türetilmiş göstergeler. Girdi: pod başına sinyal serileri
// (taban birim; sayaçlar sn başına), son RecentWindow ile önceki
// BaselineWindow kıyaslanır. Çıktı: kontrol başına seviye + ölçü + eşik +
// Türkçe kanıt cümlesi (CoSRE / Problem açıklaması; UI kendi metnini alan
// değerlerinden i18n ile kurar).

// Kontrol adları (Problem kural id soneki: runtime:dotnet-<check>).
const (
	CheckGCPressure     = "gc-pressure"
	CheckGen2Spike      = "gen2-spike"
	CheckPoolStarvation = "threadpool-starvation"
	CheckLockContention = "lock-contention"
	CheckExceptionSpike = "exception-spike"
	CheckMemoryLeak     = "memory-leak"
)

// CheckOrder — kararlı sıra (eşitlikte kart / bulgu sırası).
var CheckOrder = []string{CheckPoolStarvation, CheckGCPressure, CheckGen2Spike, CheckLockContention, CheckExceptionSpike, CheckMemoryLeak, CheckHTTPPoolSaturn, CheckKafkaBackpressure, CheckKafkaLag, CheckFDSaturation}

// Seviyeler. info = görünür ama Problem adayı değil (ör. GC payı yüksek
// ama artmıyor; kuyruk yüksek ama tamamlanan iş de orantılı artıyor).
const (
	LevelInfo     = "info"
	LevelWarning  = "warning"
	LevelCritical = "critical"
)

// Pencereler (ns): son 10 dk kıyas önceki 50 dk; sızıntı tüm 60 dk.
const (
	RecentWindowNs   = int64(10 * 60 * 1e9)
	AnalysisWindowNs = int64(60 * 60 * 1e9)
	minRecentPoints  = 3
	minLeakPoints    = 20
)

// Point — tek kova (unix ns, taban birim).
type Point struct {
	T int64
	V float64
}

// PodData — bir pod'un okunmuş serileri.
type PodData struct {
	Service, Pod string
	Series       map[SignalKey][]Point
	// Gen2 — gen2 koleksiyon/sn (nesil etiketi çözülebildiyse).
	Gen2 []Point
}

// Thresholds — dedektör eşikleri (runtime_signals ayarı + GC payı vidası).
type Thresholds struct {
	chstore.RuntimeSignalConfig
	GCShareWarnPct, GCShareCritPct float64
}

// ThresholdsFrom — ayar bloklarından eşikler.
func ThresholdsFrom(sig chstore.RuntimeSignalConfig, alerts chstore.RuntimeAlertConfig) Thresholds {
	return Thresholds{RuntimeSignalConfig: chstore.NormalizeRuntimeSignals(sig), GCShareWarnPct: alerts.GCShareWarnPct, GCShareCritPct: alerts.GCShareCritPct}
}

// PodEntityRef — sinyalin pod varlığı (kanıt id'si + pod sayfası bağlantısı).
type PodEntityRef struct {
	ID        string `json:"id"`
	ClusterID string `json:"clusterId,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Pod       string `json:"pod"`
}

// Signal — bir kontrolün hükmü (pod ya da servis düzeyi).
type Signal struct {
	Check     string  `json:"check"`
	Level     string  `json:"level"`
	Service   string  `json:"service"`
	Pod       string  `json:"pod,omitempty"` // servis düzeyinde en kötü pod
	Pods      int     `json:"pods"`          // sinyali taşıyan pod sayısı
	Value     float64 `json:"value"`         // son 10 dk ölçüsü
	Baseline  float64 `json:"baseline"`      // önceki 50 dk (sızıntıda pencere başı)
	Threshold float64 `json:"threshold"`
	Unit      string  `json:"unit"` // % | /s | /min | items | MB
	// Rising — ölçü tabana göre yükseliyor mu (GC payı / kuyruk).
	Rising bool `json:"rising"`
	// Extra — kontrole özgü yardımcı ölçüler (completedRatio, threadsRatio,
	// r2, growthPct …); UI ve kanıt metni okur.
	Extra   map[string]float64 `json:"extra,omitempty"`
	Summary string             `json:"summary"` // TR kanıt cümlesi
	// PodEntity — 2026-11-21: en kötü pod'un entity kaydı (API varlık katmanından
	// çözer: servisin entity_seen pod'ları + cluster eşlemesi; ad eşlemesi değil).
	PodEntity *PodEntityRef `json:"podEntity,omitempty"`
}

func levelRank(l string) int {
	switch l {
	case LevelCritical:
		return 3
	case LevelWarning:
		return 2
	case LevelInfo:
		return 1
	}
	return 0
}

// splitWindow — son pencere ve taban ortalamaları + son pencere nokta sayısı.
func splitWindow(pts []Point, now int64) (recent, base float64, nRecent, nBase int) {
	cut := now - RecentWindowNs
	from := now - AnalysisWindowNs
	var sr, sb float64
	for _, p := range pts {
		if math.IsNaN(p.V) || math.IsInf(p.V, 0) || p.T < from || p.T > now {
			continue
		}
		if p.T > cut {
			sr += p.V
			nRecent++
		} else {
			sb += p.V
			nBase++
		}
	}
	if nRecent > 0 {
		recent = sr / float64(nRecent)
	}
	if nBase > 0 {
		base = sb / float64(nBase)
	}
	return
}

// recentSlope — son pencerede doğrusal eğim (birim / dk); < 2 nokta → 0.
func recentSlope(pts []Point, now int64) float64 {
	var xs, ys []float64
	for _, p := range pts {
		if p.T > now-RecentWindowNs && p.T <= now && !math.IsNaN(p.V) {
			xs = append(xs, float64(p.T)/60e9)
			ys = append(ys, p.V)
		}
	}
	slope, _, _ := linreg(xs, ys)
	return slope
}

// linreg — en küçük kareler: eğim, kesişim, R².
func linreg(xs, ys []float64) (slope, icpt, r2 float64) {
	n := float64(len(xs))
	if n < 2 {
		return 0, 0, 0
	}
	var mx, my float64
	for i := range xs {
		mx += xs[i]
		my += ys[i]
	}
	mx /= n
	my /= n
	var sxx, sxy, syy float64
	for i := range xs {
		dx, dy := xs[i]-mx, ys[i]-my
		sxx += dx * dx
		sxy += dx * dy
		syy += dy * dy
	}
	if sxx == 0 {
		return 0, my, 0
	}
	slope = sxy / sxx
	icpt = my - slope*mx
	if syy > 0 {
		r2 = (sxy * sxy) / (sxx * syy)
	}
	return slope, icpt, r2
}

func r1(v float64) float64 { return math.Round(v*10) / 10 }

// EvaluatePod — tek pod'un kontrolleri (yalnız tetiklenenler). SAF.
func EvaluatePod(d PodData, now int64, th Thresholds) []Signal {
	var out []Signal
	mk := func(check, level string, value, base, thr float64, unit, summary string) Signal {
		return Signal{Check: check, Level: level, Service: d.Service, Pod: d.Pod, Pods: 1,
			Value: r1(value), Baseline: r1(base), Threshold: thr, Unit: unit, Summary: summary}
	}
	where := d.Pod
	if where == "" {
		where = d.Service
	}

	// 1) GC baskısı — GC zaman payı yüksek VE yükseliyor (JVM gc-share vidası).
	if pts := d.Series[SigGCTime]; len(pts) > 0 {
		r, b, nr, nb := splitWindow(pts, now)
		share, baseShare := r*100, b*100
		if nr >= minRecentPoints && th.GCShareWarnPct > 0 && share >= th.GCShareWarnPct {
			rising := nb == 0 || share >= baseShare*1.25
			level, thr := LevelInfo, th.GCShareWarnPct
			switch {
			case share >= th.GCShareCritPct:
				level, thr = LevelCritical, th.GCShareCritPct
			case rising:
				level = LevelWarning
			}
			trend := "artmıyor (sürekli yüksek)"
			if rising {
				trend = fmt.Sprintf("yükseliyor (önceki 50 dk %%%.1f)", baseShare)
			}
			s := mk(CheckGCPressure, level, share, baseShare, thr, "%",
				fmt.Sprintf("GC baskısı: %s zamanının %%%.1f'i GC'de (eşik %%%.0f), %s", where, share, thr, trend))
			s.Rising = rising
			out = append(out, s)
		}
	}

	// 2) Gen2 sıçraması — tam (gen2) koleksiyon/dk taban üstü.
	if len(d.Gen2) > 0 {
		r, b, nr, _ := splitWindow(d.Gen2, now)
		perMin, basePerMin := r*60, b*60
		floor := math.Max(basePerMin, 0.1)
		if nr >= minRecentPoints && perMin >= th.Gen2MinPerMin && perMin >= th.SpikeRatio*floor {
			s := mk(CheckGen2Spike, LevelWarning, perMin, basePerMin, th.Gen2MinPerMin, "/min",
				fmt.Sprintf("Gen2 sıçraması: %s dakikada %.1f tam GC (önceki 50 dk %.1f/dk, %.0f× eşik)", where, perMin, basePerMin, th.SpikeRatio))
			s.Rising = true
			out = append(out, s)
		}
	}

	// 3) Thread-pool açlığı — kuyruk büyüyor, tamamlanan iş/sn düz.
	if pts := d.Series[SigPoolQueue]; len(pts) > 0 {
		q, qb, nr, nb := splitWindow(pts, now)
		if nr >= minRecentPoints && q >= th.PoolQueueWarn {
			growing := nb == 0 || q >= qb*1.5 || recentSlope(pts, now) > 0
			extra := map[string]float64{}
			flat, known := true, false
			if cp := d.Series[SigPoolCompleted]; len(cp) > 0 {
				c, cb, ncr, ncb := splitWindow(cp, now)
				if ncr > 0 && ncb > 0 && cb > 0 {
					known = true
					ratio := c / cb
					extra["completedPerSec"], extra["completedBasePerSec"], extra["completedRatio"] = r1(c), r1(cb), math.Round(ratio*100)/100
					flat = ratio <= 1.1
				}
			}
			if tp := d.Series[SigThreads]; len(tp) > 0 {
				t, tb, ntr, ntb := splitWindow(tp, now)
				if ntr > 0 && ntb > 0 && tb > 0 {
					extra["threads"], extra["threadsBase"] = r1(t), r1(tb)
				}
			}
			level, thr := LevelInfo, th.PoolQueueWarn
			if growing && flat {
				level = LevelWarning
				if q >= th.PoolQueueCrit {
					level, thr = LevelCritical, th.PoolQueueCrit
				}
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Thread pool açlığı: %s kuyruk ortalaması %.0f iş öğesi (önceki 50 dk %.0f, eşik %.0f)", where, q, qb, thr)
			switch {
			case known && flat:
				fmt.Fprintf(&b, ", tamamlanan iş/sn düz (%.0f → %.0f)", extra["completedBasePerSec"], extra["completedPerSec"])
			case known:
				fmt.Fprintf(&b, ", tamamlanan iş/sn de artıyor (%.0f → %.0f) — yük artışı, açlık değil", extra["completedBasePerSec"], extra["completedPerSec"])
			default:
				b.WriteString(", tamamlanan iş verisi yok")
			}
			if extra["threadsBase"] > 0 && extra["threads"] > extra["threadsBase"]*1.2 {
				fmt.Fprintf(&b, "; thread sayısı %.0f → %.0f (enjeksiyon)", extra["threadsBase"], extra["threads"])
			}
			if !growing {
				b.WriteString("; kuyruk büyümüyor")
			}
			s := mk(CheckPoolStarvation, level, q, qb, thr, "items", b.String())
			s.Rising, s.Extra = growing, extra
			out = append(out, s)
		}
	}

	// 4) Kilit çekişmesi ve 5) exception sıçraması — taban × SpikeRatio.
	spike := func(key SignalKey, check string, min float64, label string) {
		pts := d.Series[key]
		if len(pts) == 0 {
			return
		}
		r, b, nr, _ := splitWindow(pts, now)
		floor := math.Max(b, min/10)
		if nr < minRecentPoints || r < min || r < th.SpikeRatio*floor {
			return
		}
		level := LevelWarning
		if r >= th.SpikeRatio*3*floor && r >= min*3 {
			level = LevelCritical
		}
		s := mk(check, level, r, b, min, "/s",
			fmt.Sprintf("%s: %s saniyede %.1f (önceki 50 dk %.1f/sn, ×%.1f)", label, where, r, b, r/math.Max(b, 1e-9)))
		s.Rising = true
		out = append(out, s)
	}
	spike(SigLockContention, CheckLockContention, th.LockMinPerSec, "Kilit çekişmesi")
	spike(SigExceptions, CheckExceptionSpike, th.ExcMinPerSec, "Exception sıçraması")

	// 6) Bellek sızıntısı eğilimi — GC sonrası heap (yoksa working set) tüm
	// pencerede doğrusal ve kalıcı büyüyor.
	leakKey := SigHeapUsed
	if len(d.Series[leakKey]) < minLeakPoints {
		leakKey = SigWorkingSet
	}
	if pts := d.Series[leakKey]; len(pts) >= minLeakPoints {
		var xs, ys []float64
		from := now - AnalysisWindowNs
		for _, p := range pts {
			if p.T >= from && p.T <= now && !math.IsNaN(p.V) {
				xs = append(xs, float64(p.T)/60e9)
				ys = append(ys, p.V)
			}
		}
		if len(xs) >= minLeakPoints {
			slope, icpt, r2 := linreg(xs, ys)
			start := slope*xs[0] + icpt
			end := slope*xs[len(xs)-1] + icpt
			growthMB := (end - start) / (1024 * 1024)
			growthPct := 0.0
			if start > 0 {
				growthPct = (end - start) / start * 100
			}
			if slope > 0 && r2 >= th.LeakMinR2 && growthPct >= th.LeakMinGrowthPct && growthMB >= th.LeakMinMB {
				what := "GC sonrası heap"
				if leakKey == SigWorkingSet {
					what = "working set"
				}
				s := mk(CheckMemoryLeak, LevelWarning, end/(1024*1024), start/(1024*1024), th.LeakMinGrowthPct, "MB",
					fmt.Sprintf("Bellek sızıntısı eğilimi: %s %s 60 dk'da %.0f MB → %.0f MB (+%%%.0f, R² %.2f)", where, what, start/(1024*1024), end/(1024*1024), growthPct, r2))
				s.Rising = true
				s.Extra = map[string]float64{"growthPct": r1(growthPct), "r2": math.Round(r2*100) / 100, "growthMB": r1(growthMB)}
				out = append(out, s)
			}
		}
	}
	// 7) Süreç düzeyi ortak kontroller (common.go) — her runtime.
	out = append(out, evaluateCommon(d, now, mk, where)...)
	return out
}

// Rollup — pod sinyallerini (servis, kontrol) başına tek satıra indirir: en
// kötü seviye, eşitlikte en yüksek ölçü; Pods = sinyali taşıyan pod sayısı.
// Sıra: seviye (kritik önce), CheckOrder, servis. SAF.
func Rollup(sigs []Signal) []Signal {
	type key struct{ svc, check string }
	by := map[key]*Signal{}
	var order []key
	for _, s := range sigs {
		k := key{s.Service, s.Check}
		cur, ok := by[k]
		if !ok {
			c := s
			by[k] = &c
			order = append(order, k)
			continue
		}
		pods := cur.Pods + s.Pods
		if levelRank(s.Level) > levelRank(cur.Level) || (s.Level == cur.Level && s.Value > cur.Value) {
			c := s
			*cur = c
		}
		cur.Pods = pods
	}
	out := make([]Signal, 0, len(order))
	for _, k := range order {
		s := *by[k]
		if s.Pods > 1 {
			s.Summary += fmt.Sprintf(" · %d pod", s.Pods)
		}
		out = append(out, s)
	}
	idx := map[string]int{}
	for i, c := range CheckOrder {
		idx[c] = i
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := levelRank(out[i].Level), levelRank(out[j].Level); a != b {
			return a > b
		}
		if idx[out[i].Check] != idx[out[j].Check] {
			return idx[out[i].Check] < idx[out[j].Check]
		}
		return out[i].Service < out[j].Service
	})
	return out
}

// Evaluate — tüm pod'lar → servis düzeyi sinyaller. SAF.
func Evaluate(pods []PodData, now int64, th Thresholds) []Signal {
	var all []Signal
	for _, p := range pods {
		all = append(all, EvaluatePod(p, now, th)...)
	}
	return Rollup(all)
}

// Actionable — Problem adayı mı (warning / critical).
func (s Signal) Actionable() bool { return levelRank(s.Level) >= 2 }
