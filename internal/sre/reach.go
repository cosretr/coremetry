package sre

// reach.go — CoSRE Faz B4: erişim beyanı. Sonuç hangi kaynaklara
// bakıldığını ve hangilerine bakılamadığını listeler (kör noktalar).
//
// SAF: adımların kayıttaki durumundan türetilir (okuma yok). Sıra: oyun
// kitabının kaynak sırası, ardından plandaki diğer adımlar (koşu sırası).
// Anlatım (summary) kaynak değildir.
//
//	checked        — okundu (ok)
//	partial        — kısmen okundu
//	no_data        — okuyucu baktı, pencerede veri yok
//	not_configured — okuyucu bağlı değil / istek kurulamadı (atlandı)
//	timed_out      — süre bütçesi / zaman aşımı
//	error          — okunamadı
//	not_run        — oyun kitabında var, bu koşuda planlanmadı

import "strings"

const (
	ReachChecked       = "checked"
	ReachPartial       = "partial"
	ReachNoData        = "no_data"
	ReachNotConfigured = "not_configured"
	ReachTimedOut      = "timed_out"
	ReachError         = "error"
	ReachNotRun        = "not_run"
)

// ReachEntry — tek kaynağın erişim durumu.
type ReachEntry struct {
	Source string `json:"source"` // adım adı
	Status string `json:"status"`
	Note   string `json:"note,omitempty"` // adımın özeti (≤160 rune; yalnız checked dışı)
}

// reachStatus — SAF: adım → erişim durumu.
func reachStatus(s Step) string {
	switch {
	case s.NoData:
		return ReachNoData
	case s.State == StepOK:
		return ReachChecked
	case s.State == StepPartial:
		return ReachPartial
	case s.State == StepSkipped:
		return ReachNotConfigured
	case s.State == StepError && isTimeout(s.Summary):
		return ReachTimedOut
	}
	return ReachError
}

// isTimeout — SAF: hata özeti süre aşımı mı.
func isTimeout(s string) bool {
	l := strings.ToLower(s)
	for _, k := range []string{"süre bütçesi", "deadline exceeded", "zaman aşımı", "timeout", "timed out"} {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

// buildReach — SAF: oyun kitabı (nil olabilir) + adımlar → erişim beyanı.
func buildReach(pb *Playbook, steps []Step) []ReachEntry {
	by := map[string]Step{}
	for _, s := range steps {
		by[s.Name] = s
	}
	seen := map[string]bool{stepSummary: true}
	var out []ReachEntry
	add := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		s, ok := by[name]
		if !ok {
			out = append(out, ReachEntry{Source: name, Status: ReachNotRun})
			return
		}
		e := ReachEntry{Source: name, Status: reachStatus(s)}
		if e.Status != ReachChecked {
			e.Note = truncRunes(oneLine(s.Summary), 160)
		}
		out = append(out, e)
	}
	if pb != nil {
		for _, src := range playbookSources(*pb) {
			add(src)
		}
	}
	for _, s := range steps {
		if knownSteps[s.Name] { // v0.10.2477 — platformun türetilmiş bilgisi, telemetri kaynağı değil
			continue
		}
		add(s.Name)
	}
	return out
}

// reachOf — SAF: kaynağın durumu (yoksa not_run).
func reachOf(reach []ReachEntry, source string) string {
	for _, r := range reach {
		if r.Source == source {
			return r.Status
		}
	}
	return ReachNotRun
}

// reachTR — SAF: erişim beyanının düz metni (tekrar sonucu / sohbet).
func reachTR(reach []ReachEntry) string {
	var ok, blind []string
	for _, r := range reach {
		if r.Status == ReachChecked || r.Status == ReachPartial {
			ok = append(ok, r.Source)
		} else {
			blind = append(blind, r.Source+" ("+r.Status+")")
		}
	}
	s := "Bakılan: " + joinOr(ok, "yok")
	if len(blind) > 0 {
		s += " · Bakılamayan: " + strings.Join(blind, ", ")
	}
	return s
}

func joinOr(xs []string, empty string) string {
	if len(xs) == 0 {
		return empty
	}
	return strings.Join(xs, ", ")
}
