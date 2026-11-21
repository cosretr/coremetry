package mcptools

// runtime_signals.go — 2026-11-21 (.NET runtime paritesi + operatör: ".NET
// servis JVM metrikleri göstermemeli"). Servis taramasının "Metrik" halkası
// runtime tespitiyle seçilir:
//
//   - .NET → runtimesig sinyalleri (GC baskısı, thread-pool açlığı, kilit
//     çekişmesi, exception sıçraması, gen2, sızıntı eğilimi) bulgu + tablo;
//     JVM okunmaz, çıktıda JVM yazmaz.
//   - Go / Node / Python → halka atlanır ("JVM okunmadı"), JVM metni yok.
//   - JVM ya da bilinmeyen → eski JVM halkası (sweepRunJVM) aynen.
//
// Okuyucu Deps.RuntimeSignals (api: runtimeSignalReport — /runtime-signals
// ucuyla TEK gövde). nil → eski davranış.

import (
	"context"
	"fmt"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/runtimesig"
)

// RuntimeSignalsFunc — servisin runtime raporu (profil + sinyaller).
type RuntimeSignalsFunc func(ctx context.Context, service string) (runtimesig.Report, error)

const (
	sweepHopRuntime  = "runtime"
	sweepKindRuntime = "runtime"
)

var runtimeTitleTR = map[runtimesig.Runtime]string{
	runtimesig.RuntimeDotnet: ".NET", runtimesig.RuntimeGo: "Go", runtimesig.RuntimeNode: "Node.js", runtimesig.RuntimePython: "Python",
}

// RuntimeCheckTR — kontrolün Türkçe adı (bulgu + hipotez metni).
func RuntimeCheckTR(check string) string {
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
	case runtimesig.CheckFDSaturation:
		return "dosya tanıtıcısı doygunluğu"
	case runtimesig.CheckHTTPPoolSaturn:
		return "giden HTTP havuzu doygunluğu"
	case runtimesig.CheckKafkaBackpressure:
		return "Kafka producer geri basıncı"
	case runtimesig.CheckKafkaLag:
		return "Kafka consumer gecikmesi"
	}
	return check
}

// sweepRunRuntime — Metrik halkası: runtime'a göre .NET sinyalleri, atlama
// ya da JVM.
func sweepRunRuntime(ctx context.Context, r sweepReaders, req SweepRequest, alerts chstore.RuntimeAlertConfig) sweepOutcome {
	if r.runtimeSignals == nil {
		return sweepRunJVM(ctx, r, req, alerts)
	}
	elapsed := stopwatch()
	rep, err := r.runtimeSignals(ctx, req.Service)
	if err != nil {
		if rep.Profile.Runtime == runtimesig.RuntimeDotnet {
			o := sweepOutcome{section: SweepSection{Name: sweepHopRuntime}, failed: true}
			o.section.State, o.section.Summary = sweepFailState(err), cosreErrMsg(".NET runtime metriği okunamadı", err)
			return o
		}
		return sweepRunJVM(ctx, r, req, alerts)
	}
	switch rep.Profile.Runtime {
	case runtimesig.RuntimeDotnet:
		o := sweepDotnetOutcome(req, rep, elapsed())
		o.runtime = string(rep.Profile.Runtime)
		return o
	case runtimesig.RuntimeGo, runtimesig.RuntimeNode, runtimesig.RuntimePython:
		o := sweepOutcome{section: SweepSection{Name: sweepHopRuntime}}
		o.section.State = hopSkipped
		o.section.Summary = fmt.Sprintf("runtime %s — JVM metriği bu servis için okunmadı", runtimeTitleTR[rep.Profile.Runtime])
		o.runtime = string(rep.Profile.Runtime)
		appendSignalCands(&o, req, rep)
		return o
	}
	o := sweepRunJVM(ctx, r, req, alerts)
	o.runtime = string(rep.Profile.Runtime)
	appendSignalCands(&o, req, rep)
	return o
}

// appendSignalCands — JVM / Go / Node / Python: ortak süreç sinyalleri (dosya
// tanıtıcısı, HttpClient havuzu, Kafka istemcisi, GC payı) bölümü bozmadan
// bulgu adayı + veri olarak eklenir.
func appendSignalCands(o *sweepOutcome, req SweepRequest, rep runtimesig.Report) {
	o.runtimeSignals = rep.Signals
	for _, s := range rep.Signals {
		if c, ok := signalCand(req, s); ok {
			o.cands = append(o.cands, c)
		}
	}
}

// signalCand — SAF: actionable sinyal → bulgu adayı (ad = en kötü pod).
func signalCand(req SweepRequest, s runtimesig.Signal) (sweepCand, bool) {
	lvl, impact := "", 0.5
	switch s.Level {
	case runtimesig.LevelCritical:
		lvl, impact = levelCrit, 1
	case runtimesig.LevelWarning:
		lvl = levelWarn
	default:
		return sweepCand{}, false
	}
	name := s.Pod
	if name == "" {
		name = req.Service
	}
	c := sweepCand{Kind: sweepKindRuntime, Name: name, Level: lvl, Hop: sweepHopRuntime, Reasons: []string{s.Summary}, Impact: impact}
	if pe := s.PodEntity; pe != nil {
		// Varlık kanıtı: pod entity'si (cluster id + namespace → pod sayfası).
		c.ClusterID, c.Namespace = pe.ClusterID, pe.Namespace
		c.Reasons = append(c.Reasons, "pod varlığı "+pe.ID)
	}
	return c, true
}

// sweepDotnetOutcome — SAF: rapor → bölüm + bulgu adayları + kanıt.
func sweepDotnetOutcome(req SweepRequest, rep runtimesig.Report, durMs int64) sweepOutcome {
	o := sweepOutcome{section: SweepSection{Name: sweepHopRuntime}}
	names := make([]string, 0, len(rep.Profile.Signals))
	for _, s := range rep.Profile.Signals {
		names = append(names, s.Metric)
	}
	ev := cosreEvidence{Source: evSourceVM, QueryKind: evKindSummary,
		Query:      "runtime-signals " + req.Service + " (60 dk, 60 s adım): " + strings.Join(names, ", "),
		DurationMs: durMs, Rows: intPtr(len(rep.Signals))}
	o.evidences = append(o.evidences, cosreHopEvidence{Hop: sweepHopRuntime, As: "runtime_signals",
		Request: map[string]any{"service": req.Service, "runtime": string(rep.Profile.Runtime), "gen": rep.Profile.DotnetGen}, cosreEvidence: ev})
	o.runtimeSignals = rep.Signals
	if mode := rep.Mode; mode == chstore.RuntimeSignalsOff {
		o.section.State, o.section.Summary = hopSkipped, ".NET runtime sinyalleri kapalı (anomaly_sensitivity.runtime.signals.mode=off)"
		return o
	}
	if len(rep.Profile.Signals) == 0 || rep.Pods == 0 {
		o.section.State = hopEmpty
		o.section.Summary = "veri yok (.NET runtime) — process.runtime.dotnet.* / dotnet.* metriği son 60 dk yok"
		return o
	}
	o.anyDataFound = true
	o.section.State = hopOK
	if len(rep.Notes) > 0 {
		o.section.State, o.section.Note = hopPartial, strings.Join(rep.Notes, "; ")
	}
	crit, warn := 0, 0
	for _, s := range rep.Signals {
		lvl := ""
		switch s.Level {
		case runtimesig.LevelCritical:
			lvl, crit = levelCrit, crit+1
		case runtimesig.LevelWarning:
			lvl, warn = levelWarn, warn+1
		default:
			continue
		}
		name := s.Pod
		if name == "" {
			name = req.Service
		}
		impact := 0.5
		if lvl == levelCrit {
			impact = 1
		}
		o.cands = append(o.cands, sweepCand{Kind: sweepKindRuntime, Name: name, Level: lvl, Hop: sweepHopRuntime,
			Reasons: []string{s.Summary}, Impact: impact})
	}
	o.section.Summary = fmt.Sprintf("%d pod .NET (son 60 dk): %d kritik, %d uyarı sinyal", rep.Pods, crit, warn)
	return o
}
