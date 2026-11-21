// Package srenotify — CoSRE Faz C1: AI SRE araştırma kaydını Problem
// bildirimine bağlayan ince köprü. sre paketi notify'ı import edemez
// (TestSREPackageNeverImportsNotify); notify de sre'yi edemez (sre →
// mcptools → … → notify döngüsü). main.go bu paketi bağlar.
package srenotify

import (
	"context"

	"github.com/cilcenk/coremetry/internal/notify"
	"github.com/cilcenk/coremetry/internal/sre"
)

// Lookup — notify.Notifier.SetInvestigationLookup için okuyucu.
func Lookup(st sre.QueueStore) notify.InvestigationLookup {
	read := sre.NotifyLookup(st)
	return func(ctx context.Context, problemID string) (notify.InvestigationInfo, error) {
		info, err := read(ctx, problemID)
		out := notify.InvestigationInfo{Enabled: info.Enabled, AutoPriorities: info.AutoPriorities}
		if info.Digest != nil {
			d := Convert(*info.Digest)
			out.Digest = &d
		}
		return out, err
	}
}

// Convert — SAF: sre özeti → notify özeti.
func Convert(d sre.NotifyDigest) notify.InvestigationDigest {
	out := notify.InvestigationDigest{
		InvestigationID: d.InvestigationID, ProblemID: d.ProblemID, State: d.State,
		Finished: d.Finished, Sufficient: d.Sufficient, TopKind: d.TopKind, TopLabel: d.TopLabel,
	}
	for _, e := range d.Evidence {
		out.Evidence = append(out.Evidence, notify.DigestEvidence{Check: e.Check, Source: e.Source})
	}
	for _, g := range d.Missing {
		out.Missing = append(out.Missing, notify.DigestGap{Code: g.Code, Source: g.Source, Status: g.Status})
	}
	for _, r := range d.Blind {
		out.Blind = append(out.Blind, notify.DigestReach{Source: r.Source, Status: r.Status})
	}
	return out
}
