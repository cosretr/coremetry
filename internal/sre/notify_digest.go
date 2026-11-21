package sre

// notify_digest.go — CoSRE Faz C1: saklı araştırma kaydından Problem
// bildiriminin özet bloğu. SAF; yeni okuma / LLM çağrısı yok — yalnız kayıt.
// sre notify'ı import ETMEZ (TestSREPackageNeverImportsNotify): türler
// burada, notify türlerine çeviri internal/srenotify'da.
//
// Kanıt kapısı (coverage.go) arayüzde gölgede olsa da BİLDİRİMDE kararına
// uyulur: kayıtta karar yoksa (kip off / eski kayıt) burada aynı kuralla
// hesaplanır. "insufficient" → neden yok, eksikler var. Metin kod
// adlarından çizilir (notify/sre_summary.go): kontrol gerekçeleri (ham
// değer taşıyabilir: log deseni, pod adı…) bloğa GİRMEZ.

import (
	"context"
	"sort"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// NotifyDigest — bildirime giren araştırma özeti (notify.InvestigationDigest aynası).
type NotifyDigest struct {
	InvestigationID, ProblemID, State string
	Finished, Sufficient              bool
	TeamConfirmed                     bool // top'u ekip doğruladı ("ekip doğruladı")
	TopKind, TopLabel                 string
	Evidence                          []NotifyEvidence
	Missing                           []GateGap
	Blind                             []ReachEntry
}

// NotifyEvidence — en olası hipotezi doğrulayan kontrol (ad + kaynak adım).
type NotifyEvidence struct{ Check, Source string }

// NotifyInfo — kayıt okumasının sonucu.
type NotifyInfo struct {
	Enabled        bool // sre_agent açık VE notifySummary açık
	AutoPriorities []string
	Digest         *NotifyDigest
}

const (
	digestEvidenceMax = 3
	digestBlindMax    = 4
)

// Digest — SAF: kayıt → bildirim özeti.
func Digest(inv Investigation) NotifyDigest {
	d := NotifyDigest{
		InvestigationID: chstore.SREInvestigationID(inv.ProblemID), ProblemID: inv.ProblemID, State: inv.State,
		Finished: inv.State == StateDone || inv.State == StatePartial,
	}
	if !d.Finished {
		return d
	}
	hs := inv.Hypotheses
	if len(hs) > 0 && hs[0].State == "" {
		hs = assignHypothesisStates(hs) // eski kayıt: durum aynı kuralla
	}
	reach := inv.Reach
	if len(reach) == 0 {
		reach = buildReach(inv.Playbook, inv.Steps)
	}
	var g CoverageGate
	switch {
	case hasRulings(hs):
		// Ekip kararı kayıttaki kapı kararından sonra gelmiş olabilir: aynı
		// kural kararlarla yeniden (doğrulanan söylenir, reddedilen asla).
		g = evaluateCoverage(hs, inv.Playbook, reach, GateShadow)
	case inv.Coverage != nil && inv.Coverage.Verdict != "":
		g = *inv.Coverage
	default:
		g = evaluateCoverage(hs, inv.Playbook, reach, GateShadow)
	}
	d.Sufficient = g.Verdict == GateSufficient
	d.TeamConfirmed = d.Sufficient && g.Ruling == MarkConfirmed
	if d.Sufficient {
		for _, h := range hs {
			if h.Kind != g.Top {
				continue
			}
			d.TopKind, d.TopLabel = h.Kind, h.Label
			d.Evidence = digestEvidence(h)
			break
		}
		if d.TopKind == "" { // karar var, kart yok (kırpılmış kayıt) — iddia etme
			d.Sufficient = false
			d.Missing = []GateGap{{Code: GapNoSupported}}
		}
	} else {
		d.Missing = append(d.Missing, g.Missing...)
	}
	for _, r := range reach {
		if r.Status == ReachChecked || r.Status == ReachPartial || r.Status == ReachNotRun {
			continue
		}
		if len(d.Blind) >= digestBlindMax {
			break
		}
		d.Blind = append(d.Blind, ReachEntry{Source: r.Source, Status: r.Status})
	}
	return d
}

// digestEvidence — SAF: doğrulayan kontroller, ağırlığa göre azalan (≤3).
func digestEvidence(h Hypothesis) []NotifyEvidence {
	pos := make([]HypothesisCheck, 0, len(h.Checks))
	for _, c := range h.Checks {
		if c.Outcome > 0 {
			pos = append(pos, c)
		}
	}
	sort.SliceStable(pos, func(i, j int) bool { return pos[i].Weight > pos[j].Weight })
	if len(pos) > digestEvidenceMax {
		pos = pos[:digestEvidenceMax]
	}
	out := make([]NotifyEvidence, 0, len(pos))
	for _, c := range pos {
		out = append(out, NotifyEvidence{Check: c.Name, Source: refSource(c.EvidenceRef)})
	}
	return out
}

// NotifyLookup — bildirim özeti okuyucusu (srenotify.Lookup sarar).
// Paylaşılan kayıt (StateShared) bir adımda kaynak araştırmaya gider.
func NotifyLookup(st QueueStore) func(ctx context.Context, problemID string) (NotifyInfo, error) {
	return func(ctx context.Context, problemID string) (NotifyInfo, error) {
		cfg := CurrentSettings()
		info := NotifyInfo{Enabled: cfg.IsEnabled() && cfg.NotifySummaryOn(), AutoPriorities: cfg.Priorities()}
		if !info.Enabled {
			return info, nil
		}
		inv, ok, err := Load(ctx, st, problemID)
		if err != nil || !ok {
			return info, err
		}
		if inv.State == StateShared && inv.SharedFrom != "" {
			src, ok2, err2 := Load(ctx, st, inv.SharedFrom)
			if err2 != nil {
				return info, err2
			}
			if ok2 {
				inv = src
			}
		}
		d := Digest(inv)
		info.Digest = &d
		return info, nil
	}
}
