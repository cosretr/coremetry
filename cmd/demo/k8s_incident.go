package main

// k8s_incident.go — v0.10.2486: yerel K8s metrik simülatörünün SENARYOLU
// olayı (saf durum makinesi; k8s_metrics.go okur, span gecikmesi de buradan).
//
// Bir çok-pod'lu iş yükünün TEK pod'u (api-gateway/channels, gw-prod-2-*)
// her `period`'da bir:
//
//	warmup  normal
//	ramp1   bellek limite tırmanır; GC baskısı → CPU + CFS throttle artar,
//	        o pod'un GET /api/v1/accounts/{id}/balance span'leri ~3×'e yavaşlar
//	kill1   OOMKilled: restart +1, last_terminated_reason=OOMKilled (yapışkan),
//	        notReady boyunca ready=0 (trafik kardeşlere gider)
//	gap     toparlanma
//	ramp2   aynı tırmanış (daha kısa)
//	kill2   OOMKilled #2: crash/2 boyunca waiting=CrashLoopBackOff, sonra
//	        ready=0 başlangıç, sonra normal
//
// Saat = simülatör başlangıcından geçen süre; her şey bunun SAF fonksiyonu
// (aynı an → aynı durum; restart sayacı döngü sayısıyla monoton).

import (
	"math"
	"time"
)

type k8sSchedule struct {
	period, warmup, ramp1, notReady, gap, ramp2, crash time.Duration
}

// k8sMinPeriod — bir döngünün tüm evreleri + pay.
const k8sMinPeriod = 25 * time.Minute

func defaultK8sSchedule(period time.Duration) k8sSchedule {
	if period < k8sMinPeriod {
		period = k8sMinPeriod
	}
	return k8sSchedule{period: period, warmup: 4 * time.Minute, ramp1: 8 * time.Minute,
		notReady: 45 * time.Second, gap: 135 * time.Second, ramp2: 6 * time.Minute, crash: time.Minute}
}

// k8sIncState — olay pod'unun t anındaki durumu. memFrac > 0 belleği
// limitin oranı olarak EZER; throttle > 0 CFS throttle oranını ezer.
type k8sIncState struct {
	memFrac  float64
	throttle float64
	cpuMult  float64
	slow     float64 // span gecikme çarpanı (1 = normal)
	ready    bool
	waiting  string
	restarts int
	lastKill time.Duration // son kill anı (başlangıçtan); -1 = hiç
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// rampState — ilerleme p∈[0,1): bellek %40 → limit; yavaşlama bellek
// %55'ten itibaren, %80'de tam (~3.3×); throttle %50'den itibaren.
func rampState(st k8sIncState, p float64) k8sIncState {
	st.memFrac = math.Min(0.995, 0.40+0.60*p)
	st.slow = 1 + 2.3*clamp01((st.memFrac-0.55)/0.25)
	st.throttle = 0.03 + 0.42*clamp01((st.memFrac-0.5)/0.4)
	st.cpuMult = 1 + 1.2*clamp01((st.memFrac-0.55)/0.4)
	return st
}

// incidentAt — SAF: başlangıçtan `el` sonra olay pod'unun durumu.
func incidentAt(el time.Duration, s k8sSchedule) k8sIncState {
	if el < 0 {
		el = 0
	}
	cycle := int(el / s.period)
	o := el % s.period
	base := time.Duration(cycle) * s.period
	kill1 := s.warmup + s.ramp1
	rec1 := kill1 + s.notReady
	r2 := rec1 + s.gap
	kill2 := r2 + s.ramp2
	rec2 := kill2 + s.crash

	st := k8sIncState{cpuMult: 1, slow: 1, ready: true, restarts: 2 * cycle, lastKill: -1}
	if cycle > 0 {
		st.lastKill = base - s.period + kill2
	}
	switch {
	case o < s.warmup:
	case o < kill1:
		st = rampState(st, float64(o-s.warmup)/float64(s.ramp1))
	case o < rec1:
		st.restarts++
		st.lastKill = base + kill1
		st.ready = false
		st.memFrac, st.cpuMult = 0.18, 1.8 // yeniden başlama: JIT/ısınma
	case o < r2:
		st.restarts++
		st.lastKill = base + kill1
	case o < kill2:
		st.restarts++
		st.lastKill = base + kill1
		st = rampState(st, float64(o-r2)/float64(s.ramp2))
	case o < rec2:
		st.restarts += 2
		st.lastKill = base + kill2
		st.ready = false
		if o-kill2 < s.crash/2 {
			st.waiting = "CrashLoopBackOff"
			st.memFrac, st.cpuMult = 0.01, 0
		} else {
			st.memFrac, st.cpuMult = 0.18, 1.8
		}
	default:
		st.restarts += 2
		st.lastKill = base + kill2
	}
	return st
}
