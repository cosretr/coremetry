package sre

// hypotheses_runtime.go — 2026-11-21 (.NET runtime paritesi + operatör:
// ".NET servis JVM metrikleri göstermemeli"). Doygunluk hipotezinin runtime
// kontrolleri runtime tespitine göre seçilir:
//
//   - jvm_post_gc      — yalnız JVM ya da runtime'ı bilinmeyen serviste.
//   - runtime_signals  — yalnız .NET serviste: runtimesig sinyalleri
//     (thread pool açlığı, GC baskısı, kilit çekişmesi, exception sıçraması,
//     gen2, sızıntı eğilimi). "Neden yavaş" sorusu "thread pool açlığı:
//     kuyruk 150 (önceki 50 dk 2), tamamlanan iş/sn düz" kanıtıyla cevaplanır.
//
// Uymayan kontrol kartta HİÇ listelenmez (Score atlar) — 0 katkılı bir
// "JVM post-GC verisi yok" satırı .NET kartında JVM yazmasın. Ağırlık
// toplamı yalnız uygulanan kontrollerden; JVM servisinin skoru değişmez.

import (
	"fmt"
	"strings"

	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/runtimesig"
)

func podRuntime(ev Evidence) string {
	if ev.Facts.Pods == nil {
		return ""
	}
	return ev.Facts.Pods.Runtime
}

// checkApplies — kontrol adı → bu kanıtta uygulanır mı (nil = her zaman).
var checkApplies = map[string]func(Evidence) bool{
	"jvm_post_gc": func(ev Evidence) bool {
		rt := podRuntime(ev)
		return rt == "" || rt == string(runtimesig.RuntimeJVM)
	},
	// .NET'te her zaman; diğer runtime'larda yalnız ortak süreç sinyali (dosya
	// tanıtıcısı, HttpClient havuzu, Kafka) VARSA — JVM kartı aksi hâlde aynen.
	"runtime_signals": func(ev Evidence) bool {
		return podRuntime(ev) == string(runtimesig.RuntimeDotnet) || (ev.Facts.Pods != nil && len(ev.Facts.Pods.RuntimeSignals) > 0)
	},
}

// chkRuntimeSignals — .NET sinyalleri. Gecikme belirtisinde thread-pool
// açlığı ve GC baskısı önce gelir (sinyaller seviyeye göre sıralı gelir;
// eşit seviyede gecikmeyi en iyi açıklayan seçilir).
func chkRuntimeSignals(ev Evidence) (int, string, string) {
	pf := ev.Facts.Pods
	ref := ev.ref(stepSweep, "runtime")
	if pf == nil {
		return 0, ".NET runtime sinyali okunmadı", ref
	}
	switch state := pf.RuntimeState; {
	case pf.Runtime != string(runtimesig.RuntimeDotnet), state == "ok", state == "partial":
	case state == "":
		return 0, ".NET runtime sinyali okunmadı", ref
	case state == "empty":
		return 0, ".NET runtime metriği yok (process.runtime.dotnet.* / dotnet.*)", ref
	default:
		return 0, ".NET runtime sinyalleri okunamadı / kapalı (" + pf.RuntimeState + ")", ref
	}
	pref := map[string]int{runtimesig.CheckPoolStarvation: 0, runtimesig.CheckGCPressure: 1, runtimesig.CheckLockContention: 2,
		runtimesig.CheckGen2Spike: 3, runtimesig.CheckMemoryLeak: 4, runtimesig.CheckExceptionSpike: 5}
	var best *runtimesig.Signal
	var also []string
	for i := range pf.RuntimeSignals {
		s := &pf.RuntimeSignals[i]
		if !s.Actionable() {
			continue
		}
		if best == nil || (s.Level == best.Level && pref[s.Check] < pref[best.Check]) || (s.Level == runtimesig.LevelCritical && best.Level != runtimesig.LevelCritical) {
			if best != nil {
				also = append(also, mcptools.RuntimeCheckTR(best.Check))
			}
			best = s
			continue
		}
		also = append(also, mcptools.RuntimeCheckTR(s.Check))
	}
	if best == nil {
		if pf.Runtime != string(runtimesig.RuntimeDotnet) {
			return -1, "süreç sinyalleri eşik altı (dosya tanıtıcısı / HTTP havuzu / Kafka, son 60 dk)", ref
		}
		return -1, ".NET runtime sağlıklı: thread pool / GC / kilit / exception sinyali yok (son 60 dk)", ref
	}
	why := best.Summary
	if len(also) > 0 {
		why += fmt.Sprintf(" (ayrıca: %s)", strings.Join(also, ", "))
	}
	return 1, why, ref
}
