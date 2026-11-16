package sre

// timeline.go — v0.10.1288 (AI SRE Faz 2: zaman çizelgesi).
//
// SAF buildTimeline: kayıttaki gerçeklerden ≤30 olaylık, zaman sıralı çizelge
// — problem başlangıcı, deploy'lar, log artışı başlangıcı, ilk hata, alarmlar
// (ReadProblemWindowEvents [başlangıç − 60 dk, şimdi]; öznenin ve aşağı akış
// db:/ext: düğümlerinin problemleri) ve incident'a bağlanma. Yeni okuma yok.
//
// Tavan: zorunlu olaylar (başlangıç, ilk hata, artış, incident) her zaman
// kalır; kalan yer deploy'lara, sonra alarmlara başlangıca YAKINLIK sırasıyla
// dağıtılır; çıktı zamana göre artan (eşitlikte tür sırası).

import (
	"fmt"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// TimelineEvent — çizelgenin tek satırı.
type TimelineEvent struct {
	At    int64  `json:"at"` // unix ns
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Ref   string `json:"ref,omitempty"` // problem / incident kimliği ya da kanıt künyesi
	// LabelMsg — v0.10.2301 (S4): Label'ın metin anahtarı (ek alan; Label
	// aynen). Kural / servis adları ham argüman.
	LabelMsg *chstore.StdMsg `json:"labelMsg,omitempty"`
}

// Olay türleri (FE aynası hypothesisView.ts TIMELINE_KIND).
const (
	TLProblemStart  = "problem_start"
	TLDeploy        = "deploy"
	TLSpikeStart    = "spike_start"
	TLFirstError    = "first_error"
	TLAlert         = "alert"
	TLAlertResolved = "alert_resolved"
	TLIncident      = "incident_attach"
)

// TimelineMax — çizelgenin tavanı.
const TimelineMax = 30

var timelineKindRank = map[string]int{
	TLDeploy: 0, TLProblemStart: 1, TLFirstError: 2, TLSpikeStart: 3, TLAlert: 4, TLAlertResolved: 5, TLIncident: 6,
}

// buildTimeline — SAF. problemID: problemin kendi alarm satırı çizelgeye
// "alarm" olarak ikinci kez girmesin. now: pencere sonu.
func buildTimeline(ev Evidence, problemID string, now time.Time) []TimelineEvent {
	from := time.Unix(0, ev.StartNs).Add(-60 * time.Minute).UnixNano()
	to := now.UnixNano()
	in := func(ns int64) bool { return ns >= from && ns <= to }
	var must, deploys, alerts []TimelineEvent

	label := "Problem başladı"
	labelMsg := chstore.NewStdMsg("tl.start")
	if pf := ev.Facts.Problem; pf != nil && firstNonEmpty(pf.RuleName, pf.RuleID) != "" {
		label += ": " + truncRunes(firstNonEmpty(pf.RuleName, pf.RuleID), 80)
		labelMsg = chstore.NewStdMsg("tl.startRule", "name", truncRunes(firstNonEmpty(pf.RuleName, pf.RuleID), 80))
	}
	must = append(must, TimelineEvent{At: ev.StartNs, Kind: TLProblemStart, Label: label, Ref: problemID, LabelMsg: labelMsg})
	if e, src, srcK, ok := ev.firstErrorK(); ok && src != "problem başlangıcı" && in(e) {
		must = append(must, TimelineEvent{At: e, Kind: TLFirstError, Label: "İlk hata (" + src + ")", LabelMsg: chstore.NewStdMsg("tl.firstError", "srcK", srcK)})
	}
	if sp := ev.Facts.Spike; sp != nil && sp.FromMs > 0 && sp.Factor >= spikeFindingFactor {
		at := sp.FromMs * int64(time.Millisecond)
		if in(at) {
			lvl, k := "", "tl.spike"
			if sp.Level == "error" {
				lvl, k = ", ERROR", "tl.spikeErr"
			}
			must = append(must, TimelineEvent{At: at, Kind: TLSpikeStart, Label: fmt.Sprintf("Log artışı başladı (×%.1f%s)", sp.Factor, lvl), Ref: ev.ref(stepLogSpike, ""),
				LabelMsg: chstore.NewStdMsg(k, "f", fmt.Sprintf("%.1f", sp.Factor))})
		}
	}
	if ef := ev.Facts.Events; ef != nil && ef.IncidentID != "" && in(ef.IncidentAt) {
		must = append(must, TimelineEvent{At: ef.IncidentAt, Kind: TLIncident, Label: "Incident'a bağlandı", Ref: ef.IncidentID, LabelMsg: chstore.NewStdMsg("tl.incident")})
	}
	if df := ev.Facts.Deploys; df != nil {
		for _, r := range df.Rows {
			if !in(r.At) {
				continue
			}
			l := "Deploy: " + deployName(r)
			k, kv := "tl.deploy", []any{"name", deployName(r)}
			if r.Prev != "" && r.Prev != r.Version {
				l += " (←" + r.Prev + ")"
				k, kv = "tl.deployPrev", append(kv, "prev", r.Prev)
			}
			if r.Source == "rollout" && !imageChanged(r) {
				l = "Rollout (imaj değişmedi): " + r.Name
				k, kv = "tl.rollout", []any{"name", r.Name}
			}
			if deployFailed(r.Status) {
				l += " · " + r.Status
				k, kv = k+"St", append(kv, "st", r.Status)
			}
			deploys = append(deploys, TimelineEvent{At: r.At, Kind: TLDeploy, Label: truncRunes(l, 140), Ref: ev.ref(stepDeploys, ""), LabelMsg: chstore.NewStdMsg(k, kv...)})
		}
	}
	if ef := ev.Facts.Events; ef != nil {
		add := func(a AlertFact, related bool) {
			name := truncRunes(firstNonEmpty(a.RuleName, a.RuleID), 80)
			if related {
				name = a.Service + " — " + name
			}
			if a.StartedAt > 0 && in(a.StartedAt) {
				l := "Alarm açıldı: " + name
				m := chstore.NewStdMsg("tl.alert", "name", name)
				if a.Priority != "" {
					l += " (" + a.Priority + ")"
					m = chstore.NewStdMsg("tl.alertP", "name", name, "p", a.Priority)
				}
				alerts = append(alerts, TimelineEvent{At: a.StartedAt, Kind: TLAlert, Label: l, Ref: a.ID, LabelMsg: m})
			}
			if a.ResolvedAt > 0 && in(a.ResolvedAt) {
				alerts = append(alerts, TimelineEvent{At: a.ResolvedAt, Kind: TLAlertResolved, Label: "Alarm kapandı: " + name, Ref: a.ID,
					LabelMsg: chstore.NewStdMsg("tl.alertResolved", "name", name)})
			}
		}
		for _, a := range ef.Alerts {
			if a.ID != problemID {
				add(a, false)
			}
		}
		for _, a := range ef.Related {
			add(a, true)
		}
	}
	near := func(xs []TimelineEvent) {
		sort.SliceStable(xs, func(i, j int) bool {
			di, dj := absNs(xs[i].At-ev.StartNs), absNs(xs[j].At-ev.StartNs)
			if di != dj {
				return di < dj
			}
			return xs[i].At < xs[j].At
		})
	}
	near(deploys)
	near(alerts)
	out := append([]TimelineEvent{}, must...)
	for _, group := range [][]TimelineEvent{deploys, alerts} {
		for _, e := range group {
			if len(out) >= TimelineMax {
				break
			}
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].At != out[j].At {
			return out[i].At < out[j].At
		}
		return timelineKindRank[out[i].Kind] < timelineKindRank[out[j].Kind]
	})
	return out
}

func absNs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
