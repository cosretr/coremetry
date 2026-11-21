package sre

// hypothesis_state.go — CoSRE Faz B2: açık hipotez durumu (Datadog Bits
// "hipotez ağacı + elenen dallar" emsali).
//
// SAF, deterministik: Score'un kontrol sonuçlarından türetilir; skoru,
// bandı, sırayı DEĞİŞTİRMEZ. LLM / oy / operatör işareti (HumanState)
// duruma GİRMEZ.
//
//	supported — en az bir doğrulayan kontrol, doğrulayan ağırlık > çürüten
//	            ağırlık VE skor ≥ orta bant (0.40)
//	ruled_out — en az bir çürüten kontrol, çürüten ağırlık ≥ doğrulayan
//	            ağırlık VE skor < 0.40
//	tested    — kanıt okundu (sonuç ≠ 0 ya da künyeli kontrol) ama ikisi de değil
//	untested  — hiçbir kontrolün kaynağı okunmadı
//
// StateRefs = durumu belirleyen kontrollerin kanıt künyeleri
// ("<adım>" / "<adım>.evidences[i]"; tekrarsız, kontrol sırası).

const (
	HypStateSupported = "supported"
	HypStateRuledOut  = "ruled_out"
	HypStateTested    = "tested"
	HypStateUntested  = "untested"
)

// hypothesisState — SAF: tek kartın durumu + künyeler + gerekçe.
func hypothesisState(h Hypothesis) (state string, refs []string, reason string) {
	var posW, negW float64
	var pos, neg []HypothesisCheck
	touched := false
	for _, c := range h.Checks {
		switch {
		case c.Outcome > 0:
			posW += c.Weight
			pos = append(pos, c)
		case c.Outcome < 0:
			negW += c.Weight
			neg = append(neg, c)
		case c.EvidenceRef != "":
			touched = true
		}
	}
	switch {
	case len(pos) > 0 && posW > negW && h.Score >= bandMediumMin:
		return HypStateSupported, checkRefs(pos), checkNames(pos)
	case len(neg) > 0 && negW >= posW && h.Score < bandMediumMin:
		return HypStateRuledOut, checkRefs(neg), truncRunes(oneLine(neg[0].Reason), 200)
	case len(pos) > 0 || len(neg) > 0 || touched:
		return HypStateTested, checkRefs(append(append([]HypothesisCheck(nil), pos...), neg...)), "kanıt karışık ya da zayıf"
	}
	return HypStateUntested, nil, "kaynak okunmadı"
}

func checkRefs(cs []HypothesisCheck) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range cs {
		if c.EvidenceRef != "" && !seen[c.EvidenceRef] {
			seen[c.EvidenceRef] = true
			out = append(out, c.EvidenceRef)
		}
	}
	return out
}

func checkNames(cs []HypothesisCheck) string {
	s := ""
	for i, c := range cs {
		if i > 0 {
			s += ", "
		}
		s += c.Name
	}
	return s
}

// assignHypothesisStates — SAF: kartlara durum yazar (yeni dilim; girdi değişmez).
func assignHypothesisStates(hs []Hypothesis) []Hypothesis {
	out := append([]Hypothesis(nil), hs...)
	for i := range out {
		out[i].State, out[i].StateRefs, out[i].StateReason = hypothesisState(out[i])
	}
	return out
}

// refSource — SAF: künye → kaynak (adım adı): "deps.evidences[1]" → "deps".
func refSource(ref string) string {
	for i := 0; i < len(ref); i++ {
		if ref[i] == '.' {
			return ref[:i]
		}
	}
	return ref
}
