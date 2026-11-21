package evaluator

// runtime_dotnet.go — 2026-11-21 .NET runtime sinyallerinin Problem hattı
// (operatör: "Coremetry runtime dotnet o metrikleri de kullanabilir, biraz
// daha akıllı olabilir. Aynı Elastic APM olduğu gibi.").
//
// Dedektör SAF çekirdeği internal/runtimesig (detect.go); burada yalnız
// yaşam döngüsü. /aiops dört soru:
//
//  1. Ölçüm seyrek (5 dk'da bir filo okuması, ≤ 2 kuşak × 9 VM sorgusu),
//     yaşam döngüsü TİK BAŞINA: son ölçüm bellekten (dotnetSigMemo) sunulur,
//     bayat süpürme açık satırı "kaynak sustu" diye kapatmaz. Ölçüm düşerse
//     son iyi ölçüm 15 dk'ya kadar kullanılır; daha eskiyse tik atlanır
//     (yumuşak-hata yönü: okunamadı ≠ temiz, hiçbir şey kapanmaz).
//  2. Yaş eskalasyonu reconcileRuntime'ın effectiveSeverity clamp'ı (JVM GC
//     kuralıyla aynı); öncelik computePriority'den, özel P1 yolu yok.
//  3. Incident'a bağlanmaz.
//  4. Tam satır: reconcileRuntime açık satırı TAŞIR (StartedAt korunur).
//
// Kimlik: servis başına kontrol başına TEK Problem
// (runtime:dotnet-<check>:<servis>; pod en kötü pod, açıklamada pod sayısı) —
// JVM GC'nin pod başına satırından bilinçli farklı: aynı deploy'un 30 pod'u
// 30 Problem açmasın. Önek `runtime:` → bildirim türü problem, birleşme AYRI
// (ProblemMergeClass), BubbleUp yok — sınıflandırıcı değişmedi.
//
// Kip (anomaly_sensitivity.runtime.signals.mode):
//   off    — okuma yok; açık runtime:dotnet-* satırları "kural kapalı" ile kapanır.
//   shadow — VARSAYILAN. Ölçüm koşar, "açardı" logu yazılır, Problem YOK;
//            önceki "on" döneminden kalan açıklar "gölge kip" notuyla kapanır.
//            Sinyaller servis sayfasında ve CoSRE'de canlı (API, ayrı yol).
//   on     — warning/critical sinyal Problem açar; info açık satırı tutar
//            (histerezis), sinyal yoksa kapanır.
// Kaynak YALNIZ VM (JVM GC kuralıyla aynı operatör kararı: CH-tabanlı alarm yok).

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/runtimesig"
)

const (
	dotnetRulePrefix   = "runtime:dotnet-"
	dotnetMeasureEvery = 5 * time.Minute
	dotnetMemoMaxAge   = 15 * time.Minute
	dotnetMaxNewPerTik = 20
)

// dotnetSigMemo — son filo ölçümü (sıfır değer kullanılabilir).
type dotnetSigMemo struct {
	mu   sync.Mutex
	at   time.Time // son BAŞARILI ölçüm
	try  time.Time // son deneme
	sigs []runtimesig.Signal
}

// dotnetProblemID — servis başına kontrol başına tek satır.
func dotnetProblemID(check, service string) string {
	return dotnetRulePrefix + check + ":" + service
}

// dotnetPlan — SAF yaşam döngüsü kararı: açılacak / tazelenecek / kapanacak.
// open: Actionable sinyal (warning/critical) ya da açık satırın info sinyali
// (histerezis). close: açık runtime:dotnet-* satırı, sinyali yok.
type dotnetAction struct {
	sig     runtimesig.Signal
	hasOpen bool
}

func dotnetPlan(sigs []runtimesig.Signal, openIDs map[string]bool, maxNew int) (act []dotnetAction, closeIDs []string) {
	seen := map[string]bool{}
	newN := 0
	for _, s := range sigs {
		id := dotnetProblemID(s.Check, s.Service)
		has := openIDs[id]
		if !s.Actionable() && !has {
			continue
		}
		if !has {
			if newN >= maxNew {
				continue
			}
			newN++
		}
		if s.Level == runtimesig.LevelInfo {
			s.Level = runtimesig.LevelWarning // açık satır info'da warning kalır
		}
		seen[id] = true
		act = append(act, dotnetAction{sig: s, hasOpen: has})
	}
	for id := range openIDs {
		if !seen[id] {
			closeIDs = append(closeIDs, id)
		}
	}
	return act, closeIDs
}

// evaluateRuntimeSignals — evaluateRuntimePods'tan, tik başına.
func (e *Evaluator) evaluateRuntimeSignals(ctx context.Context, snap *chstore.OpenProblems, vmActive bool) {
	mode := e.store.AnomalySensitivityForDetectors().RuntimeSignals().Mode
	openIDs := map[string]bool{}
	for _, p := range snap.All() {
		if p != nil && p.Status == "open" && strings.HasPrefix(p.RuleID, dotnetRulePrefix) {
			openIDs[p.ID] = true
		}
	}
	switch {
	case mode == chstore.RuntimeSignalsOff:
		e.closeDotnetProblems(ctx, snap, openIDs, "runtime sinyalleri kapalı (rule disabled)")
		return
	case !vmActive:
		e.closeDotnetProblems(ctx, snap, openIDs, "VM yapılandırılmamış — .NET runtime kuralı yalnız VM'den okur")
		return
	}
	sigs, ok := e.dotnetSignals(ctx)
	if !ok {
		return
	}
	if mode == chstore.RuntimeSignalsShadow {
		e.closeDotnetProblems(ctx, snap, openIDs, "gölge kip — Problem açılmaz")
		return
	}
	acts, closeIDs := dotnetPlan(sigs, openIDs, dotnetMaxNewPerTik)
	for _, a := range acts {
		s := a.sig
		id := dotnetProblemID(s.Check, s.Service)
		existing := snap.ByID(id)
		e.reconcileRuntime(ctx, runtimeReconcile{
			ruleID: dotnetRulePrefix + s.Check, ruleName: "Runtime · .NET " + dotnetCheckName(s.Check),
			metric:  "runtime.dotnet_" + strings.ReplaceAll(s.Check, "-", "_"),
			service: s.Service, pod: s.Pod, problemID: id,
			open: true, hasOpen: a.hasOpen && existing != nil, existing: existing,
			severity: s.Level, value: s.Value, threshold: s.Threshold, reason: s.Summary,
		})
	}
	for _, id := range closeIDs {
		if p := snap.ByID(id); p != nil {
			e.reconcileRuntime(ctx, runtimeReconcile{
				ruleID: p.RuleID, metric: p.Metric, service: p.Service, pod: p.Pod, problemID: id,
				open: false, hasOpen: true, existing: p, reason: "sinyal geçti",
			})
		}
	}
}

// dotnetSignals — memo'lu filo ölçümü. ok=false → bu tik karar yok.
func (e *Evaluator) dotnetSignals(ctx context.Context) ([]runtimesig.Signal, bool) {
	m := &e.dotnetSig
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if now.Sub(m.try) >= dotnetMeasureEvery {
		m.try = now
		pods, notes, err := runtimesig.ReadFleetDotnet(ctx, e.vmetrics, now)
		if err != nil {
			log.Printf("[evaluator] runtime .NET: VM okunamadı (son iyi ölçüm korunur): %v", err)
		} else {
			if len(notes) > 0 {
				log.Printf("[evaluator] runtime .NET: kısmi okuma: %s", strings.Join(notes, "; "))
			}
			cfg := e.store.AnomalySensitivityForDetectors().RuntimeSignals()
			th := runtimesig.ThresholdsFrom(cfg, e.store.GetRuntimeAlerts(ctx))
			m.sigs, m.at = runtimesig.Evaluate(pods, now.UnixNano(), th), now
			if cfg.Mode == chstore.RuntimeSignalsShadow {
				for _, s := range m.sigs {
					if s.Actionable() {
						log.Printf("[evaluator] runtime .NET SHADOW (açardı %s %s): %s", dotnetProblemID(s.Check, s.Service), s.Level, s.Summary)
					}
				}
			}
		}
	}
	if m.at.IsZero() || now.Sub(m.at) > dotnetMemoMaxAge {
		return nil, false
	}
	return m.sigs, true
}

func (e *Evaluator) closeDotnetProblems(ctx context.Context, snap *chstore.OpenProblems, openIDs map[string]bool, why string) {
	for id := range openIDs {
		p := snap.ByID(id)
		if p == nil {
			continue
		}
		resolved := *p
		chstore.MarkResolved(&resolved, time.Now().UnixNano())
		resolved.Description = p.Description + " · (" + why + ")"
		if err := e.store.UpsertProblem(ctx, resolved); err != nil {
			log.Printf("[evaluator] runtime .NET kapatılamadı %s: %v", id, err)
			continue
		}
		e.countResolved()
	}
}

func dotnetCheckName(check string) string {
	switch check {
	case runtimesig.CheckGCPressure:
		return "GC baskısı"
	case runtimesig.CheckGen2Spike:
		return "Gen2 sıçraması"
	case runtimesig.CheckPoolStarvation:
		return "thread pool açlığı"
	case runtimesig.CheckLockContention:
		return "kilit çekişmesi"
	case runtimesig.CheckExceptionSpike:
		return "exception sıçraması"
	case runtimesig.CheckMemoryLeak:
		return "bellek sızıntısı eğilimi"
	}
	return fmt.Sprintf("runtime %s", check)
}
