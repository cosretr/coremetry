package v2probe

// assess.go — v0.10.979 — V1–V14 yargıları (v2detect.go başlığındaki
// varsayımlar; spec §10) ve "bilgilendirilen kararlar" tablosu (§11). SAF:
// jetonlanmış sonuçlar üzerinde çalışır; kanıt dizeleri sorgu kimliği +
// jeton + sayı taşır. Kural: gereken bir sonuç Usable değilse yargı
// unknown'dır — kanıt yokluğu yanlışlama değildir (sourcestate ruhu).

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Verdict — confirmed | refuted | unknown.
type Verdict string

const (
	Confirmed Verdict = "confirmed"
	Refuted   Verdict = "refuted"
	Unknown   Verdict = "unknown"
)

// Assumption — bir V satırının yargısı.
type Assumption struct {
	ID       string   `json:"id"`
	Verdict  Verdict  `json:"verdict"`
	Evidence []string `json:"evidence,omitempty"`
	Note     string   `json:"note,omitempty"`
}

// Decision — bilgilendirilen karar satırı.
type Decision struct {
	ID      string   `json:"id"`
	Verdict string   `json:"verdict"`
	Queries []string `json:"queries,omitempty"`
}

type resultIndex struct {
	m     map[string]*Result
	units []Unit
}

func newIndex(units []Unit, results []Result) resultIndex {
	ix := resultIndex{m: make(map[string]*Result, len(results)), units: units}
	for i := range results {
		ix.m[results[i].Key()] = &results[i]
	}
	return ix
}

func (ix resultIndex) get(id, unit, variant string) *Result {
	return ix.m[id+"|"+unit+"|"+variant]
}

func (ix resultIndex) targets() []Unit { return ix.byRole("target") }
func (ix resultIndex) hubs() []Unit    { return ix.byRole("hub") }

func (ix resultIndex) byRole(role string) []Unit {
	var out []Unit
	for _, u := range ix.units {
		if u.Role == role {
			out = append(out, u)
		}
	}
	return out
}

// num — jeton-güvenli sayı biçimi.
func num(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', 4, 64)
}

func stateOf(r *Result) string {
	if r == nil {
		return "missing"
	}
	if r.Skipped {
		return "skipped"
	}
	return string(r.State)
}

// unitVerdict — birim başına ara sonuç.
type unitVerdict struct {
	v  Verdict
	ev string
}

// combine — birim yargılarını birleştirir: biri refuted → refuted; hepsi
// confirmed (≥1) → confirmed; aksi unknown.
func combine(id string, uvs []unitVerdict, note string) Assumption {
	a := Assumption{ID: id, Verdict: Unknown, Note: note}
	conf := 0
	for _, u := range uvs {
		if u.ev != "" {
			a.Evidence = append(a.Evidence, u.ev)
		}
		switch u.v {
		case Refuted:
			a.Verdict = Refuted
		case Confirmed:
			conf++
		}
	}
	if a.Verdict != Refuted && conf > 0 && conf == len(uvs) {
		a.Verdict = Confirmed
	}
	return a
}

// scalarOf — Usable skaler (Empty → 0 count biçimi).
func scalarOf(r *Result) (float64, bool) {
	if r == nil {
		return 0, false
	}
	return r.ScalarOrZero()
}

func namesPresent(r *Result, want ...string) (missing []string) {
	for _, w := range want {
		if !r.HasName(w) {
			missing = append(missing, w)
		}
	}
	return missing
}

// Assess — V1–V14.
func Assess(units []Unit, results []Result) []Assumption {
	ix := newIndex(units, results)
	return []Assumption{
		ix.v1(), ix.v2(), ix.v2b(), ix.v3(), ix.v4(), ix.v5(), ix.v6(), ix.v7(),
		ix.v8(), ix.v9(), ix.v10(), ix.v11(), ix.v12(), ix.v13(), ix.v14(),
	}
}

func (ix resultIndex) v1() Assumption {
	kinds := []struct{ id, prefix string }{{"K1.D", "kube_deployment"}, {"K1.S", "kube_statefulset"}, {"K1.DS", "kube_daemonset"}}
	var uvs []unitVerdict
	for _, t := range ix.targets() {
		for _, k := range kinds {
			r := ix.get(k.id, t.Token, "")
			if r == nil || !r.Usable() {
				uvs = append(uvs, unitVerdict{Unknown, k.id + "@" + t.Token + ": " + stateOf(r)})
				continue
			}
			miss := namesPresent(r, k.prefix+"_metadata_generation", k.prefix+"_status_observed_generation")
			if len(miss) > 0 {
				uvs = append(uvs, unitVerdict{Refuted, k.id + "@" + t.Token + ": missing " + strings.Join(miss, ", ")})
				continue
			}
			uvs = append(uvs, unitVerdict{Confirmed, fmt.Sprintf("%s@%s: %d names, generation+observed present", k.id, t.Token, len(r.Rows))})
		}
	}
	return combine("V1", uvs, "kube_<kind>_metadata_generation ve _status_observed_generation her hedefte her üç tür için")
}

func (ix resultIndex) v2() Assumption {
	var uvs []unitVerdict
	for _, t := range ix.targets() {
		checks := []struct {
			id   string
			want []string
		}{
			{"K2.1b", []string{"namespace", "replicaset", "owner_kind", "owner_name", "owner_is_controller"}},
			{"K2.1d", []string{"namespace", "statefulset", "revision"}},
		}
		for _, c := range checks {
			r := ix.get(c.id, t.Token, "")
			if r == nil || !r.Usable() {
				uvs = append(uvs, unitVerdict{Unknown, c.id + "@" + t.Token + ": " + stateOf(r)})
				continue
			}
			if miss := namesPresent(r, c.want...); len(miss) > 0 {
				uvs = append(uvs, unitVerdict{Refuted, c.id + "@" + t.Token + ": missing " + strings.Join(miss, ", ")})
			} else {
				uvs = append(uvs, unitVerdict{Confirmed, c.id + "@" + t.Token + ": all label names present"})
			}
		}
		r := ix.get("K2.3a", t.Token, "")
		switch {
		case r == nil || !r.Usable():
			uvs = append(uvs, unitVerdict{Unknown, "K2.3a@" + t.Token + ": " + stateOf(r)})
		case r.HasLabelKey("owner_is_controller"):
			uvs = append(uvs, unitVerdict{Confirmed, "K2.3a@" + t.Token + ": owner_is_controller rows"})
		default:
			uvs = append(uvs, unitVerdict{Refuted, "K2.3a@" + t.Token + ": no owner_is_controller label"})
		}
	}
	return combine("V2", uvs, "RS owner etiketleri (owner_kind, owner_name, owner_is_controller); STS revizyonu `revision` etiketinde")
}

func (ix resultIndex) v2b() Assumption {
	var uvs []unitVerdict
	for _, t := range ix.targets() {
		r := ix.get("K1.S", t.Token, "")
		if r == nil || !r.Usable() {
			uvs = append(uvs, unitVerdict{Unknown, "K1.S@" + t.Token + ": " + stateOf(r)})
			continue
		}
		miss := namesPresent(r, "kube_statefulset_status_current_revision", "kube_statefulset_status_update_revision")
		if len(miss) > 0 {
			uvs = append(uvs, unitVerdict{Refuted, "K1.S@" + t.Token + ": missing " + strings.Join(miss, ", ") + " → STS START generic, rollback invisible"})
		} else {
			uvs = append(uvs, unitVerdict{Confirmed, "K1.S@" + t.Token + ": current+update revision present"})
		}
	}
	return combine("V2b", uvs, "kube_statefulset_status_{current,update}_revision var (CMO denylist dışı)")
}

func (ix resultIndex) v3() Assumption {
	var uvs []unitVerdict
	var notes []string
	for _, t := range ix.targets() {
		r := ix.get("K1.R", t.Token, "")
		if r == nil || !r.Usable() {
			uvs = append(uvs, unitVerdict{Unknown, "K1.R@" + t.Token + ": " + stateOf(r)})
		} else if miss := namesPresent(r, "kube_replicaset_owner", "kube_replicaset_spec_replicas"); len(miss) > 0 {
			uvs = append(uvs, unitVerdict{Refuted, "K1.R@" + t.Token + ": missing " + strings.Join(miss, ", ")})
		} else {
			uvs = append(uvs, unitVerdict{Confirmed, "K1.R@" + t.Token + ": owner+spec_replicas present"})
		}
		for _, id := range []string{"K3.1", "K3.2"} {
			r := ix.get(id, t.Token, "")
			v, ok := scalarOf(r)
			switch {
			case !ok:
				uvs = append(uvs, unitVerdict{Unknown, id + "@" + t.Token + ": " + stateOf(r)})
			case v > 50000:
				uvs = append(uvs, unitVerdict{Refuted, id + "@" + t.Token + ": " + num(v) + " > 50000 (WorkerMaxSeries) — shard needed"})
			default:
				uvs = append(uvs, unitVerdict{Confirmed, id + "@" + t.Token + ": " + num(v)})
				if id == "K3.1" && v > 1000 {
					notes = append(notes, t.Token+": K3.1 > 1000 — v1 leg truncated today (client.go maxSeriesParsed) → dec 3")
				}
			}
		}
	}
	return combine("V3", uvs, strings.Join(notes, "; "))
}

func (ix resultIndex) v4() Assumption {
	var uvs []unitVerdict
	note := ""
	for _, t := range ix.targets() {
		present := []string{}
		unknown := false
		for _, id := range []string{"K1.D", "K1.R", "K1.S", "K1.DS"} {
			r := ix.get(id, t.Token, "")
			if r == nil || !r.Usable() {
				unknown = true
				continue
			}
			for _, row := range r.Rows {
				if n := row.Labels["__name__"]; strings.HasSuffix(n, "_created") {
					present = append(present, n)
				}
			}
		}
		k64 := ix.get("K6.4", t.Token, "")
		v, ok := scalarOf(k64)
		switch {
		case len(present) > 0 || (ok && v > 0):
			ev := "K1.*@" + t.Token + ": _created present (" + strings.Join(present, ", ") + ")"
			if ok {
				ev += ", K6.4=" + num(v)
			}
			uvs = append(uvs, unitVerdict{Refuted, ev})
			note = "favourable deviation — dec 9 revisit: incarnation could use `_created`"
		case unknown || !ok:
			uvs = append(uvs, unitVerdict{Unknown, "K1.*/K6.4@" + t.Token + ": " + stateOf(k64)})
		default:
			uvs = append(uvs, unitVerdict{Confirmed, "K1.*@" + t.Token + ": no _created, K6.4=0"})
		}
	}
	return combine("V4", uvs, note)
}

func (ix resultIndex) v5() Assumption {
	var uvs []unitVerdict
	for _, t := range ix.targets() {
		k05 := ix.get("K0.5", t.Token, "")
		v, ok := scalarOf(k05)
		a, b := ix.get("K0.6b", t.Token, ""), ix.get("K0.6b", t.Token, "second_sample")
		var sa, sb string
		samplesOK := a != nil && b != nil && a.Usable() && b.Usable() && a.Scalar != nil && b.Scalar != nil
		if samplesOK {
			sa, sb = *a.Scalar, *b.Scalar
		}
		switch {
		case !ok || !samplesOK:
			uvs = append(uvs, unitVerdict{Unknown, fmt.Sprintf("K0.5@%s: %s, K0.6b: %s/%s", t.Token, stateOf(k05), stateOf(a), stateOf(b))})
		case v >= 18 && v <= 22:
			uvs = append(uvs, unitVerdict{Refuted, "K0.5@" + t.Token + ": " + num(v) + " ⇒ 30 s scrape → dec 8 tick revisit"})
		case sa != sb:
			uvs = append(uvs, unitVerdict{Refuted, "K0.6b@" + t.Token + ": samples differ (" + sa + " ≠ " + sb + ")"})
		case v >= 8 && v <= 12:
			uvs = append(uvs, unitVerdict{Confirmed, "K0.5@" + t.Token + ": " + num(v) + " (60 s), K0.6b equal"})
		default:
			uvs = append(uvs, unitVerdict{Unknown, "K0.5@" + t.Token + ": " + num(v) + " (neither 60 s nor 30 s band)"})
		}
	}
	return combine("V5", uvs, "CMO scrape 60 s; iki K0.6b örneği aynı scrape zamanını döndürür")
}

func (ix resultIndex) v6() Assumption {
	var uvs []unitVerdict
	for _, t := range ix.targets() {
		r := ix.get("K0.4", t.Token, "")
		v, ok := scalarOf(r)
		switch {
		case !ok:
			uvs = append(uvs, unitVerdict{Unknown, "K0.4@" + t.Token + ": " + stateOf(r)})
		case math.Abs(v-1) < 1e-6:
			uvs = append(uvs, unitVerdict{Confirmed, "K0.4@" + t.Token + ": 1"})
		default:
			uvs = append(uvs, unitVerdict{Refuted, "K0.4@" + t.Token + ": " + num(v) + " with dedup on"})
		}
		if raw := ix.get("K0.4", t.Token, "dedup_off"); raw != nil {
			if rv, ok := scalarOf(raw); ok {
				uvs = append(uvs, unitVerdict{Confirmed, "K0.4b@" + t.Token + ": raw HA ratio " + num(rv) + " (informational)"})
			}
		}
	}
	for _, h := range ix.hubs() {
		r := ix.get("H0.2", h.Token, "")
		if r == nil || !r.Usable() {
			uvs = append(uvs, unitVerdict{Unknown, "H0.2@" + h.Token + ": " + stateOf(r)})
			continue
		}
		replica := false
		for _, row := range r.Rows {
			if row.Labels["prometheus_replica"] != "" {
				replica = true
			}
		}
		if replica {
			uvs = append(uvs, unitVerdict{Refuted, "H0.2@" + h.Token + ": prometheus_replica label present with dedup on"})
		} else {
			uvs = append(uvs, unitVerdict{Confirmed, fmt.Sprintf("H0.2@%s: %d rows, no prometheus_replica", h.Token, len(r.Rows))})
		}
	}
	return combine("V6", uvs, "dedup=true sonrası iş yükü başına tek seri")
}

func (ix resultIndex) v7() Assumption {
	var uvs []unitVerdict
	var notes []string
	for _, t := range ix.targets() {
		r := ix.get("K2.3c", t.Token, "")
		if r == nil || !r.Usable() {
			uvs = append(uvs, unitVerdict{Unknown, "K2.3c@" + t.Token + ": " + stateOf(r)})
			continue
		}
		reason := false
		for _, row := range r.Rows {
			if row.Labels["reason"] != "" {
				reason = true
			}
		}
		ver := ""
		if k03 := ix.get("K0.3", t.Token, ""); k03 != nil && k03.Usable() {
			for _, row := range k03.Rows {
				ver = row.Labels["version"]
			}
		}
		uvs = append(uvs, unitVerdict{Confirmed, fmt.Sprintf("K2.3c@%s: %d rows, reason=%v, K0.3=%s", t.Token, len(r.Rows), reason, ver)})
		if !reason {
			notes = append(notes, t.Token+": `reason` empty (KSM < v2.17) — not required")
		}
	}
	return combine("V7", uvs, strings.Join(notes, "; "))
}

func (ix resultIndex) v8() Assumption {
	var uvs []unitVerdict
	for _, t := range ix.targets() {
		r := ix.get("K2.4b", t.Token, "")
		v, ok := scalarOf(r)
		ds := ix.get("K2.3b", t.Token, "")
		switch {
		case !ok:
			uvs = append(uvs, unitVerdict{Unknown, "K2.4b@" + t.Token + ": " + stateOf(r)})
		case v == 0:
			uvs = append(uvs, unitVerdict{Refuted, "K2.4b@" + t.Token + ": 0 — DS rollback invisible"})
		case ds == nil || !ds.Usable():
			uvs = append(uvs, unitVerdict{Unknown, "K2.3b@" + t.Token + ": " + stateOf(ds)})
		case !ds.HasLabelValue("owner_kind", "DaemonSet"):
			uvs = append(uvs, unitVerdict{Unknown, "K2.4b@" + t.Token + ": " + num(v) + ", K2.3b has no DaemonSet row"})
		default:
			uvs = append(uvs, unitVerdict{Confirmed, "K2.4b@" + t.Token + ": " + num(v) + ", DaemonSet owner rows"})
		}
	}
	return combine("V8", uvs, "DS pod'larında label_controller_revision_hash")
}

func (ix resultIndex) v9() Assumption {
	var uvs []unitVerdict
	for _, t := range ix.targets() {
		r := ix.get("K0.2", t.Token, "")
		if r == nil || !r.Usable() {
			uvs = append(uvs, unitVerdict{Unknown, "K0.2@" + t.Token + ": " + stateOf(r)})
			continue
		}
		ksm, other := 0, []string{}
		for _, row := range r.Rows {
			switch j := row.Labels["job"]; j {
			case "kube-state-metrics":
				ksm++
			case "openshift-state-metrics":
			default:
				other = append(other, j)
			}
		}
		fresh := ix.get("K0.6", t.Token, "")
		age, ok := scalarOf(fresh)
		switch {
		case ksm != 1 || len(other) > 0:
			uvs = append(uvs, unitVerdict{Refuted, fmt.Sprintf("K0.2@%s: kube-state-metrics=%d, other jobs=%d", t.Token, ksm, len(other))})
		case !ok:
			uvs = append(uvs, unitVerdict{Unknown, "K0.6@" + t.Token + ": " + stateOf(fresh)})
		case age >= 90:
			uvs = append(uvs, unitVerdict{Refuted, "K0.6@" + t.Token + ": " + num(age) + " s ≥ 90"})
		default:
			uvs = append(uvs, unitVerdict{Confirmed, "K0.2@" + t.Token + ": single KSM, K0.6=" + num(age) + " s"})
		}
	}
	return combine("V9", uvs, "KSM taze (< 90 s) ve tek KSM")
}

func (ix resultIndex) v10() Assumption {
	var uvs []unitVerdict
	var notes []string
	for _, t := range ix.targets() {
		ids := []string{"K1.H", "K5.1", "K5.2", "K5.3", "K5.4", "K5.5", "K5.6"}
		bad := []string{}
		for _, id := range ids {
			if r := ix.get(id, t.Token, ""); r == nil || !r.Usable() {
				bad = append(bad, id)
			}
		}
		if len(bad) > 0 {
			uvs = append(uvs, unitVerdict{Unknown, t.Token + ": not usable " + strings.Join(bad, ", ")})
			continue
		}
		g, _ := scalarOf(ix.get("K5.1", t.Token, ""))
		rs, _ := scalarOf(ix.get("K5.3", t.Token, ""))
		sc, _ := scalarOf(ix.get("K5.2", t.Token, ""))
		uvs = append(uvs, unitVerdict{Confirmed, fmt.Sprintf("K5@%s: gen bumps %s : new RS %s : scaled %s", t.Token, num(g), num(rs), num(sc))})
		if rs > 0 {
			notes = append(notes, fmt.Sprintf("%s: K5.1/K5.3 = %s (write-volume sizing)", t.Token, num(g/rs)))
		}
	}
	return combine("V10", uvs, strings.Join(notes, "; "))
}

func (ix resultIndex) v11() Assumption {
	a := Assumption{ID: "V11", Verdict: Unknown, Note: "doğrudan probe yok: status_replicas semantiği KSM sözleşmesi"}
	for _, t := range ix.targets() {
		if r := ix.get("K1.D", t.Token, ""); r != nil && r.Usable() {
			a.Evidence = append(a.Evidence, fmt.Sprintf("K1.D@%s: status_replicas present=%v", t.Token, r.HasName("kube_deployment_status_replicas")))
		}
		if v, ok := scalarOf(ix.get("K5.2", t.Token, "")); ok {
			a.Evidence = append(a.Evidence, "K5.2@"+t.Token+": "+num(v))
		}
	}
	return a
}

// dcShare — D2 ÷ (D2 + D8); ok=false: kullanılamaz.
func (ix resultIndex) dcShare(unit string) (share float64, d2, d8 float64, ok bool) {
	d2, ok2 := scalarOf(ix.get("D2", unit, ""))
	d8, ok8 := scalarOf(ix.get("D8", unit, ""))
	if !ok2 || !ok8 || d2+d8 == 0 {
		return 0, d2, d8, false
	}
	return d2 / (d2 + d8), d2, d8, true
}

func (ix resultIndex) v12() Assumption {
	var uvs []unitVerdict
	var notes []string
	for _, t := range ix.targets() {
		bad := []string{}
		for _, id := range []string{"D1", "D2", "D3", "D4", "D5", "D6", "D7", "D8"} {
			if r := ix.get(id, t.Token, ""); r == nil || !r.Usable() {
				bad = append(bad, id)
			}
		}
		share, d2, d8, ok := ix.dcShare(t.Token)
		if len(bad) > 0 || !ok {
			uvs = append(uvs, unitVerdict{Unknown, t.Token + ": not usable " + strings.Join(bad, ", ")})
			continue
		}
		d7, _ := scalarOf(ix.get("D7", t.Token, ""))
		uvs = append(uvs, unitVerdict{Confirmed, fmt.Sprintf("D@%s: DC share %.1f%% (D2=%s, D8=%s), D7=%s", t.Token, share*100, num(d2), num(d8), num(d7))})
		if share >= 0.05 || d7 > 0 {
			notes = append(notes, t.Token+": DC usage material → dec 12")
		}
	}
	return combine("V12", uvs, strings.Join(notes, "; "))
}

func (ix resultIndex) v13() Assumption {
	var uvs []unitVerdict
	for _, t := range ix.targets() {
		if !t.NSFilter {
			uvs = append(uvs, unitVerdict{Confirmed, t.Token + ": no namespace filter (n/a)"})
			continue
		}
		bad := []string{}
		for _, id := range []string{"K3.1", "K3.2", "K3.3", "K3.4", "K3.5", "K3.6", "K3.7"} {
			if r := ix.get(id, t.Token, "ns"); r == nil || !r.Usable() {
				bad = append(bad, id)
			}
		}
		if len(bad) > 0 {
			uvs = append(uvs, unitVerdict{Unknown, t.Token + ": ns variant not usable " + strings.Join(bad, ", ")})
		} else {
			uvs = append(uvs, unitVerdict{Confirmed, t.Token + ": K3(ns) usable"})
		}
	}
	return combine("V13", uvs, "namespace filtresi sabit")
}

func (ix resultIndex) v14() Assumption {
	var uvs []unitVerdict
	var notes []string
	for _, t := range ix.targets() {
		p := ix.get("K1.P", t.Token, "")
		a := ix.get("K2.1a", t.Token, "")
		c := ix.get("K2.2a", t.Token, "")
		v, ok := scalarOf(c)
		switch {
		case p == nil || !p.Usable() || a == nil || !a.Usable() || !ok:
			uvs = append(uvs, unitVerdict{Unknown, fmt.Sprintf("K1.P/K2.1a/K2.2a@%s: %s/%s/%s", t.Token, stateOf(p), stateOf(a), stateOf(c))})
			continue
		}
		miss := namesPresent(p, "kube_pod_container_info", "kube_pod_owner")
		miss = append(miss, namesPresent(a, "image", "image_spec")...)
		switch {
		case len(miss) > 0:
			uvs = append(uvs, unitVerdict{Refuted, "K1.P/K2.1a@" + t.Token + ": missing " + strings.Join(miss, ", ")})
		case v == 0:
			uvs = append(uvs, unitVerdict{Refuted, "K2.2a@" + t.Token + ": 0"})
		default:
			uvs = append(uvs, unitVerdict{Confirmed, "K2.2a@" + t.Token + ": " + num(v)})
			var carry []string
			for _, id := range []string{"K2.2b", "K2.2c", "K2.2d", "K2.2e"} {
				if x, ok := scalarOf(ix.get(id, t.Token, "")); ok && x > 0 {
					carry = append(carry, id+"="+num(x))
				}
			}
			if len(carry) > 0 {
				notes = append(notes, t.Token+": digest/tag carriers "+strings.Join(carry, ", "))
			}
		}
	}
	return combine("V14", uvs, strings.Join(notes, "; "))
}

// ── Bilgilendirilen kararlar ────────────────────────────────────────────

func (ix resultIndex) rowsSummary(id, unit string) string {
	r := ix.get(id, unit, "")
	if r == nil || !r.Usable() {
		return stateOf(r)
	}
	if r.Scalar != nil {
		return *r.Scalar
	}
	return strconv.Itoa(len(r.Rows)) + " rows"
}

// h12Case — (A) exported_namespace == namespace; (B) yok; (C) farklı.
func h12Case(r *Result) string {
	if r == nil || !r.Usable() {
		return stateOf(r)
	}
	cases := map[string]int{}
	for _, row := range r.Rows {
		exp, ok := row.Labels["exported_namespace"]
		switch {
		case !ok || exp == "":
			cases["B"]++
		case exp == row.Labels["namespace"]:
			cases["A"]++
		default:
			cases["C"]++
		}
	}
	parts := []string{}
	for _, k := range []string{"A", "B", "C"} {
		if cases[k] > 0 {
			parts = append(parts, k+"="+strconv.Itoa(cases[k]))
		}
	}
	if len(parts) == 0 {
		return "no rows"
	}
	return strings.Join(parts, " ")
}

func labelNamesOf(r *Result) []string {
	seen := map[string]bool{}
	if r != nil {
		for _, row := range r.Rows {
			for k, v := range row.Labels {
				if v != "" {
					seen[k] = true
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Decisions — §11 tablosu.
func Decisions(units []Unit, results []Result) []Decision {
	ix := newIndex(units, results)
	var out []Decision
	add := func(id, verdict string, qs ...string) {
		out = append(out, Decision{ID: id, Verdict: verdict, Queries: qs})
	}

	// dec 2 — okuyucu tavanı.
	{
		parts := []string{}
		for _, h := range ix.hubs() {
			mx := 0.0
			for _, id := range []string{"H0.1", "H1.6a", "H1.6b"} {
				if v, ok := scalarOf(ix.get(id, h.Token, "")); ok && v > mx {
					mx = v
				}
			}
			fit := "fits 50k"
			if mx > 50000 {
				fit = "EXCEEDS 50k"
			}
			over, _ := scalarOf(ix.get("H1.6c", h.Token, ""))
			parts = append(parts, fmt.Sprintf("%s: max %s (%s), shards > 1000: %s", h.Token, num(mx), fit, num(over)))
		}
		add("dec 2", strings.Join(parts, "; "), "H0.1", "H1.6a", "H1.6b", "H1.6c")
	}
	// dec 3 — v1 kesimi.
	{
		parts := []string{}
		for _, t := range ix.targets() {
			a, _ := scalarOf(ix.get("K3.1", t.Token, ""))
			b, _ := scalarOf(ix.get("K6.1", t.Token, ""))
			tr := "not truncated"
			if a > 1000 || b > 1000 {
				tr = "v1 leg truncated today"
			}
			parts = append(parts, fmt.Sprintf("%s: K3.1=%s K6.1=%s → %s", t.Token, num(a), num(b), tr))
		}
		add("dec 3", strings.Join(parts, "; "), "K3.1", "K6.1")
	}
	// dec 5 — injectClusterLabel.
	{
		parts := []string{}
		for _, h := range ix.hubs() {
			with, wok := scalarOf(ix.get("H0.1", h.Token, ""))
			without, nok := scalarOf(ix.get("H0.1", h.Token, "nomatch"))
			eq := "no without-matcher pass"
			if wok && nok {
				if with == without {
					eq = "H0.1 with == without (" + num(with) + ")"
				} else {
					eq = fmt.Sprintf("H0.1 with %s ≠ without %s — injection hides Argo series", num(with), num(without))
				}
			}
			l3, l5 := labelNamesOf(ix.get("H0.3", h.Token, "")), labelNamesOf(ix.get("H0.5", h.Token, ""))
			parts = append(parts, fmt.Sprintf("%s: %s; H0.3 labels [%s] vs H0.5 [%s]", h.Token, eq, strings.Join(l3, ","), strings.Join(l5, ",")))
		}
		add("dec 5 / hubs[].injectClusterLabel", strings.Join(parts, "; "), "H0.1", "H0.3", "H0.5")
	}
	// dec 7 — apiServerUrls / pairGroup / suffix.
	{
		parts := []string{}
		for _, h := range ix.hubs() {
			ports := []string{}
			if r := ix.get("H2.5", h.Token, ""); r != nil && r.Usable() {
				for _, row := range r.Rows {
					ports = append(ports, row.Labels["port"]+"×"+row.Value)
				}
				sort.Strings(ports)
			}
			pairs := []string{}
			for _, r := range results {
				if r.ID == "H3.5" && r.Unit == h.Token && r.Usable() {
					v, _ := scalarOf(&r)
					pairs = append(pairs, r.Variant+"="+num(v))
				}
			}
			parts = append(parts, fmt.Sprintf("%s: ports [%s]; H3.4 %s; pairs [%s]; N4 %s; N6 %s",
				h.Token, strings.Join(ports, " "), ix.rowsSummary("H3.4", h.Token), strings.Join(pairs, " "), ix.rowsSummary("N4", h.Token), ix.rowsSummary("N6", h.Token)))
		}
		add("dec 7", strings.Join(parts, "; "), "H2.5", "H3.4", "H3.5", "N4", "N5a", "N5b", "N6")
	}
	// dec 8 — tik.
	{
		parts := []string{}
		for _, t := range ix.targets() {
			if v, ok := scalarOf(ix.get("K0.5", t.Token, "")); ok {
				parts = append(parts, t.Token+": K0.5="+num(v))
			} else {
				parts = append(parts, t.Token+": K0.5 "+stateOf(ix.get("K0.5", t.Token, "")))
			}
		}
		add("dec 8", strings.Join(parts, "; "), "K0.5")
	}
	add("dec 9", "see V4 ("+string(ix.v4().Verdict)+")", "K1.*", "K6.4")
	// dec 11 — Argo Rollouts.
	{
		any := false
		parts := []string{}
		for _, u := range ix.units {
			if u.Role != "target" && u.Role != "hub" {
				continue
			}
			n := 0
			for _, id := range []string{"R0", "R1", "R3"} {
				if r := ix.get(id, u.Token, ""); r != nil && r.Usable() {
					n += len(r.Rows) + len(r.Names)
				}
			}
			if v, ok := scalarOf(ix.get("R2", u.Token, "")); ok && v > 0 {
				n++
			}
			if n > 0 {
				any = true
			}
			parts = append(parts, fmt.Sprintf("%s: %d", u.Token, n))
		}
		v := "R0–R3 empty everywhere → do not build the Argo Rollouts slice"
		if any {
			v = "Argo Rollouts series present → slice needed"
		}
		add("dec 11", v+" ("+strings.Join(parts, ", ")+")", "R0", "R1", "R2", "R3")
	}
	// dec 12 — DC payı.
	{
		parts := []string{}
		for _, t := range ix.targets() {
			if share, _, _, ok := ix.dcShare(t.Token); ok {
				parts = append(parts, fmt.Sprintf("%s: %.1f%%", t.Token, share*100))
			} else {
				parts = append(parts, t.Token+": n/a")
			}
		}
		add("dec 12", "DC share "+strings.Join(parts, "; "), "D2", "D8", "D7")
	}
	// dec 15 — autosync.
	{
		parts := []string{}
		for _, h := range ix.hubs() {
			auto := []string{}
			if r := ix.get("H4.3", h.Token, ""); r != nil && r.Usable() {
				for _, row := range r.Rows {
					auto = append(auto, "autosync_enabled="+row.Labels["autosync_enabled"]+":"+row.Value)
				}
				sort.Strings(auto)
			}
			parts = append(parts, h.Token+": "+strings.Join(auto, " ")+"; H6.3 "+ix.rowsSummary("H6.3", h.Token))
		}
		add("dec 15", strings.Join(parts, "; "), "H4.3", "H4.4", "H6.3")
	}
	// dec 16 — envList tamlığı / env saflığı.
	{
		parts := []string{}
		for _, h := range ix.hubs() {
			parts = append(parts, fmt.Sprintf("%s: N3b (unknown env, known suffix) %s; N7 impure instances %s", h.Token, ix.rowsSummary("N3b", h.Token), ix.rowsSummary("N7", h.Token)))
		}
		add("dec 16", strings.Join(parts, "; "), "N3b", "N7")
	}
	// dec 26 — KSM profili/sürümü.
	{
		parts := []string{}
		for _, t := range ix.targets() {
			jobs := []string{}
			if r := ix.get("K0.2", t.Token, ""); r != nil && r.Usable() {
				for _, row := range r.Rows {
					jobs = append(jobs, row.Labels["job"]+"="+row.Value)
				}
				sort.Strings(jobs)
			}
			ver := []string{}
			if r := ix.get("K0.3", t.Token, ""); r != nil && r.Usable() {
				for _, row := range r.Rows {
					ver = append(ver, row.Labels["version"])
				}
				sort.Strings(ver)
			}
			parts = append(parts, fmt.Sprintf("%s: jobs [%s] version [%s]", t.Token, strings.Join(jobs, " "), strings.Join(ver, " ")))
		}
		add("dec 26", strings.Join(parts, "; "), "K0.2", "K0.3")
	}
	// dec 27 — querier sınırları.
	{
		parts := []string{}
		for _, u := range ix.units {
			kinds := map[string]int{}
			warns := 0
			for _, r := range results {
				if r.Unit != u.Token || r.Skipped {
					continue
				}
				if !r.Usable() {
					kinds[string(r.State)]++
				}
				warns += len(r.Warnings)
			}
			ks := []string{}
			for k, n := range kinds {
				ks = append(ks, k+"="+strconv.Itoa(n))
			}
			sort.Strings(ks)
			parts = append(parts, fmt.Sprintf("%s: errors [%s] warnings %d", u.Token, strings.Join(ks, " "), warns))
		}
		add("dec 27", strings.Join(parts, "; "), "all")
	}
	// dec 28 — H1.2 durumu, H2.3, sürümler.
	{
		parts := []string{}
		for _, h := range ix.hubs() {
			vers := []string{}
			if r := ix.get("H5.3e", h.Token, ""); r != nil && r.Usable() {
				for _, row := range r.Rows {
					vers = append(vers, row.Labels["version"])
				}
				sort.Strings(vers)
			}
			parts = append(parts, fmt.Sprintf("%s: H1.2 case %s; H2.3 empty dest_server %s; H5.3c dry_run %s; versions [%s]",
				h.Token, h12Case(ix.get("H1.2", h.Token, "")), ix.rowsSummary("H2.3", h.Token), ix.rowsSummary("H5.3c", h.Token), strings.Join(vers, " ")))
		}
		add("dec 28", strings.Join(parts, "; "), "H1.2", "H2.3", "H5.3c", "H5.3e")
	}
	// dec 29 — T1/T2.
	{
		parts := []string{}
		if r := ix.get("T1", "clickhouse", ""); r != nil && r.Usable() {
			for _, row := range r.Rows {
				cl := row.Labels["cluster"]
				if cl == "" {
					cl = "(empty)"
				}
				parts = append(parts, cl+": sampled="+row.Labels["sampled"]+" k8s_cluster="+row.Labels["k8s_cluster"]+" ocp_cluster="+row.Labels["ocp_cluster"]+" env_name="+row.Labels["env_name"])
			}
		} else {
			parts = append(parts, "T1 "+stateOf(ix.get("T1", "clickhouse", "")))
		}
		if r := ix.get("T1", "clickhouse", "fallback"); r != nil {
			parts = append(parts, "T0 fallback used: `cluster` column missing — 0011 not applied")
		}
		add("dec 29", strings.Join(parts, "; "), "T1", "T2", "T3")
	}
	add("dec 30", "skipped (metrics-only)", "A", "V")
	return out
}
