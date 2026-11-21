package sre

// coverage.go — CoSRE Faz B3: kanıt kapsama kapısı ("yanlış yerine
// sonuçsuz"). SAF, deterministik kural:
//
//	top = skor sırasında İLK supported hipotez
//	top yok                              → insufficient (no_supported)
//	top'un bağımsız kaynağı < 2          → insufficient (single_source)
//	aksi                                 → sufficient
//
// Bağımsız kaynak = top'un doğrulayan kontrollerinin künyelerindeki
// farklı adımlar (refSource). Eksik listesine, oyun kitabında top'u (ya da
// top yoksa herhangi bir hipotezi) besleyip okunamayan kaynaklar eklenir —
// "ne eksik" sorusunun cevabı (≤3).
//
// Kip (sre_agent.evidenceGate): "" / "shadow" = VARSAYILAN — karar kayda
// yazılır, arayüz / anlatım göstermez (ölçülene dek); "show" = arayüz
// "Kanıt yetersiz" bandı çizer; "off" = hesaplanmaz. Değerlendirme tekrarı
// kipten bağımsız hesaplar ve tekrar sonucuna (replayConclusion) uygular —
// ölçülen şey gösterilecek olandır.

const (
	GateShadow = "shadow"
	GateShow   = "show"
	GateOff    = "off"

	GateSufficient   = "sufficient"
	GateInsufficient = "insufficient"

	GapNoSupported  = "no_supported"
	GapSingleSource = "single_source"
	GapBlindSource  = "blind_source"

	gateMinSources = 2
	gateBlindMax   = 3
)

// GateGap — eksik kanıtın tek kalemi (FE gate.gap.<code> ile çevirir).
type GateGap struct {
	Code   string `json:"code"`
	Source string `json:"source,omitempty"` // adım adı (single_source / blind_source)
	Status string `json:"status,omitempty"` // blind_source: erişim durumu
}

// CoverageGate — kapının kararı.
type CoverageGate struct {
	Mode    string    `json:"mode"`
	Verdict string    `json:"verdict"`
	Top     string    `json:"top,omitempty"` // hipotez türü
	Sources []string  `json:"sources,omitempty"`
	Missing []GateGap `json:"missing,omitempty"`
}

// NormalizeGateMode — SAF: bilinmeyen / boş → shadow.
func NormalizeGateMode(m string) string {
	switch m {
	case GateShow, GateOff:
		return m
	}
	return GateShadow
}

// evaluateCoverage — SAF: durumlu hipotezler + oyun kitabı + erişim → karar.
func evaluateCoverage(hs []Hypothesis, pb *Playbook, reach []ReachEntry, mode string) CoverageGate {
	g := CoverageGate{Mode: NormalizeGateMode(mode), Verdict: GateInsufficient}
	var top *Hypothesis
	for i := range hs {
		if hs[i].State == HypStateSupported {
			top = &hs[i]
			break
		}
	}
	if top == nil {
		g.Missing = append(g.Missing, GateGap{Code: GapNoSupported})
	} else {
		g.Top = top.Kind
		seen := map[string]bool{}
		for _, r := range top.StateRefs {
			if s := refSource(r); s != "" && !seen[s] {
				seen[s] = true
				g.Sources = append(g.Sources, s)
			}
		}
		if len(g.Sources) >= gateMinSources {
			g.Verdict = GateSufficient
			return g
		}
		g.Missing = append(g.Missing, GateGap{Code: GapSingleSource, Source: firstNonEmpty(append(g.Sources, "")...)})
	}
	g.Missing = append(g.Missing, blindGaps(top, pb, reach, g.Sources)...)
	return g
}

// blindGaps — SAF: oyun kitabında top'u besleyen (top yoksa herhangi) ve
// okunamayan kaynaklar (≤3, kontrol sırası).
func blindGaps(top *Hypothesis, pb *Playbook, reach []ReachEntry, have []string) []GateGap {
	if pb == nil {
		return nil
	}
	skip := map[string]bool{}
	for _, s := range have {
		skip[s] = true
	}
	var out []GateGap
	for _, c := range pb.Checks {
		if top != nil && !containsStr(c.Hypotheses, top.Kind) {
			continue
		}
		for _, src := range c.Sources {
			st := reachOf(reach, src)
			if skip[src] || st == ReachChecked || st == ReachPartial {
				continue
			}
			skip[src] = true
			if len(out) < gateBlindMax {
				out = append(out, GateGap{Code: GapBlindSource, Source: src, Status: st})
			}
		}
	}
	return out
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// coverageTR — SAF: kararın düz metni (tekrar sonucu).
func coverageTR(g CoverageGate) string {
	if g.Verdict == GateSufficient {
		return ""
	}
	s := "KANIT YETERSİZ — kesin kök neden belirtilmedi. Eksik:"
	for i, m := range g.Missing {
		if i > 0 {
			s += ";"
		}
		switch m.Code {
		case GapNoSupported:
			s += " desteklenen hipotez yok"
		case GapSingleSource:
			s += " en olası hipotez tek kaynağa dayanıyor (" + firstNonEmpty(m.Source, "künyesiz") + ")"
		case GapBlindSource:
			s += " " + m.Source + " bakılamadı (" + m.Status + ")"
		}
	}
	return s
}
