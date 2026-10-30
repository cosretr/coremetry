package briefing

// items.go — kalem başına SAF bulgu üreticileri. Girdiler okuma katmanının
// satırları (yerel tipler; chstore'a bağımlılık yok). Eşik dışı = bulgu yok.

import (
	"fmt"
	"sort"
	"strings"
)

// ── Kalem 1: RED sapması (service_summary_5m cur vs PriorWindow) ────────

// REDRow — bir pencerenin servis RED'i.
type REDRow struct {
	Service string
	Spans   uint64
	Errors  uint64
	P99Ms   float64
}

func errPct(r REDRow) float64 {
	if r.Spans == 0 {
		return 0
	}
	return float64(r.Errors) * 100 / float64(r.Spans)
}

// newErrScore — önceki pencerede sıfır hata varken yükselen oranın sıralama puanı.
const newErrScore = 10

// REDFindings — SAF: üç bayrak (err↑, p99↑, trafik↓). Oran bayrakları İKİ
// pencerede de ≥ MinSpans ister (az trafikte 1 hata = %50 gürültüsü);
// hata oranı ayrıca mutlak tabanı (ErrRateMinPct) geçmeli. Trafik düşüşü
// yalnız önceki pencerenin hacmine bakar — cur'da hiç satır yoksa 0 sayılır
// (sessizleşen servis, tam kayıp).
func REDFindings(cur, prev []REDRow, th Thresholds, env string, windowS int) []Finding {
	pm := make(map[string]REDRow, len(prev))
	for _, r := range prev {
		pm[r.Service] = r
	}
	cm := make(map[string]REDRow, len(cur))
	names := make([]string, 0, len(cur)+len(prev))
	for _, r := range cur {
		cm[r.Service] = r
		names = append(names, r.Service)
	}
	for _, r := range prev {
		if _, ok := cm[r.Service]; !ok {
			names = append(names, r.Service)
		}
	}
	sort.Strings(names)
	var out []Finding
	for _, svc := range names {
		c, p := cm[svc], pm[svc]
		link := ServiceURL(svc, "", env, windowS)
		both := c.Spans >= th.MinSpans && p.Spans >= th.MinSpans
		if both {
			cp, pp := errPct(c), errPct(p)
			if cp >= th.ErrRateMinPct {
				switch r := ratio(cp, pp); {
				case pp == 0:
					out = append(out, Finding{Item: ItemRED, Kind: "err_up", Service: svc, Now: cp, Prev: pp, Unit: "%", Score: newErrScore,
						Text: fmt.Sprintf("%s hata oranı %s (önceki pencerede hata yoktu)", svc, pct(cp)), PageURL: link})
				case r >= th.ErrRateX:
					out = append(out, Finding{Item: ItemRED, Kind: "err_up", Service: svc, Now: cp, Prev: pp, Unit: "%", Score: r,
						Text: fmt.Sprintf("%s hata oranı %s (önceki %s, %s)", svc, pct(cp), pct(pp), xs(r)), PageURL: link})
				}
			}
			if r := ratio(c.P99Ms, p.P99Ms); r >= th.LatencyX {
				out = append(out, Finding{Item: ItemRED, Kind: "latency_up", Service: svc, Now: c.P99Ms, Prev: p.P99Ms, Unit: "ms", Score: r,
					Text: fmt.Sprintf("%s p99 %s (önceki %s, %s)", svc, ms(c.P99Ms), ms(p.P99Ms), xs(r)), PageURL: link})
			}
		}
		if p.Spans >= th.MinSpans {
			drop := (1 - float64(c.Spans)/float64(p.Spans)) * 100
			if drop >= th.TrafficDropPct {
				text := fmt.Sprintf("%s trafiği %%%.0f düştü (%d → %d span)", svc, drop, p.Spans, c.Spans)
				if c.Spans == 0 {
					text = fmt.Sprintf("%s bu pencerede hiç span üretmedi (önceki %d)", svc, p.Spans)
				}
				out = append(out, Finding{Item: ItemRED, Kind: "traffic_down", Service: svc, Now: float64(c.Spans), Prev: float64(p.Spans), Unit: "span",
					Score: drop / 50, Text: text, PageURL: link})
			}
		}
	}
	return out
}

// ── Kalem 6: açık Problem ───────────────────────────────────────────────

// ProblemRow — açık Problem (mcptools.OpenProblemRow'un yerel aynası).
type ProblemRow struct {
	ID, DisplayID, Priority, Service, Reason, PageURL string
	SinceUnix                                         int64
}

var prioScore = map[string]float64{"P1": 3, "P2": 2, "P3": 1}

// ProblemFindings — SAF: her açık Problem bir bulgu; P1 önce, eşitlikte yeni önce.
func ProblemFindings(rows []ProblemRow) []Finding {
	out := make([]Finding, 0, len(rows))
	for _, r := range rows {
		label := r.DisplayID
		if label == "" {
			label = r.ID
		}
		prio := r.Priority
		if prio == "" {
			prio = "P3"
		}
		text := fmt.Sprintf("%s · %s: %s", prio, r.Service, strings.TrimSpace(r.Reason))
		// Puan: öncelik tam kısım, açılış zamanı kesir (yeni > eski, sıra deterministik).
		score := prioScore[prio] + float64(r.SinceUnix%1_000_000_000)/1e10
		out = append(out, Finding{Item: ItemProblem, Kind: "open", Service: r.Service, Label: label, Now: float64(r.SinceUnix),
			Score: score, Text: text, PageURL: r.PageURL})
	}
	return out
}

// ── Kalem 7: son rollout + hata oranı farkı ─────────────────────────────

// RolloutRow — pencere içindeki bir sürüm geçişi + ComputeDeployImpact.
type RolloutRow struct {
	Service, FromVersion, ToVersion string
	AtUnix                          int64
	BeforeSpans, AfterSpans         uint64
	ErrBeforePct, ErrAfterPct       float64 // 0..100
}

// RolloutFindings — SAF: hata oranı deploy sonrası ≥ DeployErrDeltaPts puan
// arttıysa bulgu; iki taraf da ≥ MinSpans/4 span ister (etki penceresi 10 dk,
// 3 sa'lik pencerenin MinSpans tabanı burada fazla sıkı olurdu).
func RolloutFindings(rows []RolloutRow, th Thresholds, env string, windowS int) []Finding {
	floor := th.MinSpans / 4
	if floor == 0 {
		floor = 1
	}
	var out []Finding
	for _, r := range rows {
		if r.BeforeSpans < floor || r.AfterSpans < floor {
			continue
		}
		d := r.ErrAfterPct - r.ErrBeforePct
		if d < th.DeployErrDeltaPts {
			continue
		}
		from := r.FromVersion
		if from == "" {
			from = "?"
		}
		out = append(out, Finding{Item: ItemRollout, Kind: "err_after_deploy", Service: r.Service, Label: from + "→" + r.ToVersion,
			Now: r.ErrAfterPct, Prev: r.ErrBeforePct, Unit: "%", Score: d,
			Text:    fmt.Sprintf("%s %s→%s sonrası hata oranı %s (öncesi %s, +%.1f puan)", r.Service, from, r.ToVersion, pct(r.ErrAfterPct), pct(r.ErrBeforePct), d),
			PageURL: ServiceURL(r.Service, "", env, windowS)})
	}
	return out
}

// ── Kalem 3: yeni / geri dönen exception (Q5; kaynak Inbox, R1) ─────────

// ExceptionRow — Inbox exception/httperror satırı.
type ExceptionRow struct {
	ID, Kind, Service, Title, Status, Priority, PageURL string
	StartedAtUnix                                       int64
}

// ExceptionQualifies — SAF: pencerede İLK görülen (new) ya da geri dönen
// (regressed) exception ailesi satırı mı. "Artan tip" Faz 1'de yok (Q5, R2).
func ExceptionQualifies(r ExceptionRow, fromUnix int64) (kind string, ok bool) {
	if r.Kind != "exception" && r.Kind != "httperror" {
		return "", false
	}
	switch {
	case r.StartedAtUnix >= fromUnix:
		return "new", true
	case r.Status == "regressed":
		return "regressed", true
	}
	return "", false
}

// ExceptionFindings — SAF: niteleyen satırlar → bulgu (yeni > geri dönen, sonra öncelik).
func ExceptionFindings(rows []ExceptionRow, fromUnix int64) []Finding {
	var out []Finding
	for _, r := range rows {
		kind, ok := ExceptionQualifies(r, fromUnix)
		if !ok {
			continue
		}
		verb := "yeni"
		score := 2.0
		if kind == "regressed" {
			verb, score = "geri döndü", 1
		}
		score += prioScore[r.Priority] / 10
		out = append(out, Finding{Item: ItemException, Kind: kind, Service: r.Service, Label: r.Title, Now: float64(r.StartedAtUnix),
			Score: score, Text: fmt.Sprintf("%s: %s (%s)", r.Service, strings.TrimSpace(r.Title), verb), PageURL: r.PageURL})
	}
	return out
}

// ── Kalem 4: takım ERROR log sayısı (ES, 3×60 dk dilim — Q7) ────────────

// LogSlices — SAF: [from,to) penceresini ≤ 60 dk dilimlere böler (ES takım
// okuması 60 dk tavanlı); sıra eski → yeni.
func LogSlices(fromUnix, toUnix int64) [][2]int64 {
	const step = 3600
	var out [][2]int64
	for a := fromUnix; a < toUnix; a += step {
		b := a + step
		if b > toUnix {
			b = toUnix
		}
		out = append(out, [2]int64{a, b})
	}
	return out
}

// LogFindings — SAF: takımın ERROR sayısı cur vs prev. Taban LogMinCount;
// prev 0 iken cur ≥ taban → "yeni"; aksi kat ≥ LogLevelX. lowerBound = ES
// sayımı alt sınır (track_total_hits tavanı) — metin söyler.
func LogFindings(team string, cur, prev int64, lowerBound bool, th Thresholds, logsURL string) []Finding {
	if cur < int64(th.LogMinCount) {
		return nil
	}
	r := ratio(float64(cur), float64(prev))
	if prev > 0 && r < th.LogLevelX {
		return nil
	}
	n := fmt.Sprintf("%d", cur)
	if lowerBound {
		n = "≥" + n
	}
	text := fmt.Sprintf("takım logları: %s ERROR (önceki %d, %s; tüm ortamlar)", n, prev, xs(r))
	score := r
	if prev == 0 {
		score = newErrScore
	}
	return []Finding{{Item: ItemLog, Kind: "error_up", Label: team, Now: float64(cur), Prev: float64(prev), Unit: "log", Score: score, Text: text, PageURL: logsURL}}
}
