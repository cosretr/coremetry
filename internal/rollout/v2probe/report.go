package v2probe

// report.go — v0.10.979 — Finalize (ham koşu → jetonlu rapor; tek geçiş) ve
// Render (deterministik markdown; golden testli). SAF.
//
// Finalize'ın sözleşmesi: girdi ham değerler taşır ve çağıranın goroutine'ini
// terk etmez; çıktıdaki HER dize jetonlayıcıdan geçmiştir (Unit, Variant,
// Rows, Detail, Warnings, Infos, Skipped). Rapor yalnız katalog ŞABLONUNU
// basar (Expr), etkin ifadeyi asla. Satırlar önce (değer azalan, ham etiket
// demeti artan) sıralanır, MaxRows'a kesilir, SONRA jetonlanır: numaralama
// upstream sırasından bağımsız.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/sourcestate"
)

// Version — rapor başlığındaki sürüm.
const Version = "v0.10.979"

// DetailMax — v0.10.979 — upstream hata metni (thanos 64 KiB gövde tavanı,
// CH sürücü hatası) rapora sınırsız girmesin: Detail ve EarlyStop bu bayta
// kırpılır. Kırpma ScrubText'ten SONRA: önce kırpmak bir takma adı ortadan
// bölüp jetonlanamayan parça ("realcluster-pr…") sızdırırdı. Sonek kırpma
// IsBadData (HasPrefix) ve firstWord tüketicilerini korur.
const DetailMax = 512

// clipText — bayt tavanı, rune sınırında; kırpılınca "…" eklenir.
func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// ClickHouseUnit — T paketinin sözde birimi (jetonlanmaz).
const ClickHouseUnit = "clickhouse"

// Unit — raporda bir birim.
type Unit struct {
	Token          string `json:"token"`
	Role           string `json:"role"` // target | hub | clickhouse
	WithoutMatcher bool   `json:"withoutMatcher,omitempty"`
	NSFilter       bool   `json:"nsFilter,omitempty"`
	Calls          int    `json:"calls"`
	DurationMs     int64  `json:"durationMs"`
	State          string `json:"state"`
	EarlyStop      string `json:"earlyStop,omitempty"`
}

// RawUnit — api katmanının verdiği birim (ID ham; Token tohumla aynı).
type RawUnit struct {
	ID             string
	Token          string
	Role           string
	WithoutMatcher bool
	NSFilter       bool
	Calls          int
	DurationMs     int64
	State          string
	EarlyStop      string
}

// RawRun — Finalize girdisi.
type RawRun struct {
	RunID, Pod, Status    string
	StartedAt             time.Time
	FinishedAt            time.Time
	BudgetS               int
	Calls, Planned        int
	Packs                 []Pack
	Units                 []RawUnit
	Results               []Result // Unit = RawUnit.ID ya da ClickHouseUnit
	Warnings              []string
	Skipped               []string
	EnvListN, SuffixListN int
}

// Report — jetonlu, kalıcı çıktı (api katmanı 1 saat bellekte tutar).
type Report struct {
	Status       string       `json:"status"`
	RunID        string       `json:"runId"`
	Pod          string       `json:"pod"`
	StartedAt    int64        `json:"startedAt"`
	FinishedAt   int64        `json:"finishedAt"`
	Calls        int          `json:"calls"`
	Planned      int          `json:"planned"`
	DurationMs   int64        `json:"durationMs"`
	BudgetS      int          `json:"budgetS"`
	Tokens       int          `json:"tokens"`
	Packs        []Pack       `json:"packs"`
	Units        []Unit       `json:"units"`
	Warnings     []string     `json:"warnings"`
	Assumptions  []Assumption `json:"assumptions"`
	Decisions    []Decision   `json:"decisions"`
	Results      []Result     `json:"results"`
	Skipped      []string     `json:"skipped"`
	DeniedLabels []string     `json:"deniedLabels"`
	Markdown     string       `json:"markdown"`
	started      time.Time
	finished     time.Time
}

// rowTuple — ham etiket demeti (sıralı anahtar=değer), sıralama anahtarı.
func rowTuple(r Row) string {
	keys := make([]string, 0, len(r.Labels))
	for k := range r.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(r.Labels[k])
		b.WriteByte(0)
	}
	return b.String()
}

func sortRows(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		vi, oki := parseNum(rows[i].Value)
		vj, okj := parseNum(rows[j].Value)
		if oki && okj && vi != vj {
			return vi > vj
		}
		return rowTuple(rows[i]) < rowTuple(rows[j])
	})
}

// Finalize — ham koşu → rapor. Sıra: birimler → sonuçlar (verilen sıra =
// yürütme sırası) → satırlar sıralı → jeton → yargı → render.
func Finalize(raw RawRun, seeds Seeds) Report {
	tok := NewTokenizer(seeds)
	rep := Report{
		Status: raw.Status, RunID: raw.RunID, Pod: raw.Pod,
		StartedAt: raw.StartedAt.UnixMilli(), FinishedAt: raw.FinishedAt.UnixMilli(),
		Calls: raw.Calls, Planned: raw.Planned, BudgetS: raw.BudgetS, Packs: raw.Packs,
		DurationMs: raw.FinishedAt.Sub(raw.StartedAt).Milliseconds(),
		started:    raw.StartedAt, finished: raw.FinishedAt,
		Units: make([]Unit, 0, len(raw.Units)), Results: make([]Result, 0, len(raw.Results)),
		Warnings: []string{}, Skipped: []string{},
	}
	if rep.Packs == nil {
		rep.Packs = []Pack{}
	}
	for _, u := range raw.Units {
		token := u.Token
		if u.Role == ClickHouseUnit || u.ID == ClickHouseUnit {
			token = ClickHouseUnit
		} else if token == "" {
			token = tok.ClusterToken(u.ID)
		} else {
			tok.ClusterToken(u.ID) // tohum eşlemesi (id → token) öğrenilsin
		}
		rep.Units = append(rep.Units, Unit{Token: token, Role: u.Role, WithoutMatcher: u.WithoutMatcher,
			NSFilter: u.NSFilter, Calls: u.Calls, DurationMs: u.DurationMs, State: u.State, EarlyStop: clipText(tok.ScrubText(u.EarlyStop), DetailMax)})
	}
	for _, r := range raw.Results {
		q, _ := Find(r.ID)
		out := r
		if r.Unit == ClickHouseUnit {
			out.Unit = ClickHouseUnit
		} else {
			out.Unit = tok.ClusterToken(r.Unit)
		}
		if r.Variant == "inst" {
			out.Variant = "inst:" + tok.Value("namespace", r.VariantRaw)
		}
		out.VariantRaw = ""
		out.Detail = clipText(tok.ScrubText(r.Detail), DetailMax)
		out.Warnings = scrubList(tok, r.Warnings)
		out.Infos = scrubList(tok, r.Infos)
		if len(r.Rows) > 0 {
			rows := append([]Row(nil), r.Rows...)
			sortRows(rows)
			if out.Total < len(rows) {
				out.Total = len(rows)
			}
			if q.MaxRows > 0 && len(rows) > q.MaxRows {
				rows = rows[:q.MaxRows]
				out.Truncated = true
			}
			out.Rows = make([]Row, 0, len(rows))
			for _, row := range rows {
				out.Rows = append(out.Rows, Row{Labels: tok.Row(row.Labels, q.LiteralFor), Value: row.Value})
			}
		} else if r.Rows != nil {
			out.Rows = []Row{}
		}
		if r.Names != nil {
			out.Names = append([]string{}, r.Names...)
			sort.Strings(out.Names)
		}
		rep.Results = append(rep.Results, out)
	}
	rep.Warnings = dedupSorted(scrubList(tok, raw.Warnings))
	for _, s := range raw.Skipped {
		rep.Skipped = append(rep.Skipped, tok.ScrubText(s))
	}
	rep.Assumptions = Assess(rep.Units, rep.Results)
	rep.Decisions = Decisions(rep.Units, rep.Results)
	rep.DeniedLabels = tok.Denied()
	if rep.DeniedLabels == nil {
		rep.DeniedLabels = []string{}
	}
	rep.Tokens = tok.Count()
	rep.Markdown = Render(rep)
	return rep
}

func scrubList(tok *Tokenizer, in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, tok.ScrubText(s))
	}
	return out
}

func dedupSorted(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// ── Render ──────────────────────────────────────────────────────────────

type pasteRow struct {
	label string
	ids   []string
	roles []string
}

var pasteRows = []pasteRow{
	{"K0.2 KSM jobs / K0.3 version / K0.4 dup ratio / K0.5 scrape", []string{"K0.2", "K0.3", "K0.4", "K0.5"}, []string{"target"}},
	{"K1 presence table (name → count)", []string{"K1.D", "K1.R", "K1.P", "K1.S", "K1.DS", "K1.H", "K1.X"}, []string{"target"}},
	{"K2.2 image label forms; K2.3 `reason` present?; K2.4 pod labels", []string{"K2.2a", "K2.2b", "K2.2c", "K2.2d", "K2.2e", "K2.3c", "K2.4a", "K2.4b", "K2.4c", "K2.4d", "K2.4e", "K2.4f", "K2.4g"}, []string{"target"}},
	{"K3 counts (any > 1000?)", []string{"K3.1", "K3.2", "K3.3", "K3.4", "K3.5", "K3.6", "K3.7"}, []string{"target"}},
	{"K4 stuck / lag / paused / STS mid-rollout", []string{"K4.1", "K4.2", "K4.3", "K4.4"}, []string{"target"}},
	{"K5 gen bumps : new RS : reactivated RS : scaled deployments", []string{"K5.1", "K5.3", "K5.4", "K5.2", "K5.5", "K5.6"}, []string{"target"}},
	{"K6 v1 leg counts (`_created` present?)", []string{"K6.1", "K6.2", "K6.3", "K6.4"}, []string{"target"}},
	{"D DC counts and share", []string{"D1", "D2", "D3", "D4", "D5", "D6", "D7", "D8"}, []string{"target"}},
	{"R Argo Rollouts names/counts", []string{"R0", "R1", "R2", "R3"}, []string{"target", "hub"}},
	{"H0.1 / H0.4 / dedup / label names (H0.3 vs H0.5)", []string{"H0.1", "H0.4", "H0.2", "H0.3", "H0.5"}, []string{"hub"}},
	{"H1.2 case (A/B/C); instances; max apps per instance and shard", []string{"H1.2", "H1.1", "H1.5", "H1.6a", "H1.6b", "H1.6c"}, []string{"hub"}},
	{"H2 `dest_server` count, `\"\"`, in-cluster, ports, apps per target ns", []string{"H2.2", "H2.3", "H2.4", "H2.5", "H2.6", "H2.7", "H2.8"}, []string{"hub"}},
	{"H3 duplicates / pairs / pair mismatches", []string{"H3.1", "H3.2", "H3.3", "H3.4", "H3.5"}, []string{"hub"}},
	{"H4 status table; autosync share", []string{"H4.1", "H4.2", "H4.3"}, []string{"hub"}},
	{"H5 label names; versions per instance", []string{"H5.1a", "H5.1b", "H5.1c", "H5.1d", "H5.3a", "H5.3b", "H5.3c", "H5.3d", "H5.3e"}, []string{"hub"}},
	{"H6 syncs/h and /24h by phase and autosync; transitions/h and /2m; non-steady size", []string{"H6.1", "H6.2a", "H6.2b", "H6.3", "H6.4", "H6.5", "H6.6", "H6.7"}, []string{"hub"}},
	{"N1–N7 ratios, near misses, suffix share and uniqueness, hyphenated teams?", []string{"N1", "N3a", "N3b", "N3c", "N4", "N5b", "N6", "N7", "L1"}, []string{"hub"}},
}

// summary — bir sonucun kompakt hücre özeti.
func summary(r *Result) string {
	if r == nil {
		return "—"
	}
	if r.Skipped {
		return "skipped"
	}
	if !r.Usable() {
		return string(r.State)
	}
	if r.State == sourcestate.Empty {
		if r.Scalar == nil && len(r.Rows) == 0 && len(r.Names) == 0 {
			return "0 (empty)"
		}
	}
	if r.Scalar != nil {
		return *r.Scalar
	}
	if len(r.Names) > 0 || (r.Rows == nil && r.Names != nil) {
		return strconv.Itoa(len(r.Names)) + " names"
	}
	parts := []string{}
	for i, row := range r.Rows {
		if i == 3 {
			parts = append(parts, "… +"+strconv.Itoa(len(r.Rows)-3))
			break
		}
		parts = append(parts, rowLabel(row)+"="+row.Value)
	}
	if r.Truncated && r.Total > len(r.Rows) {
		parts = append(parts, "(total "+strconv.Itoa(r.Total)+")")
	}
	if len(parts) == 0 {
		return "0 rows"
	}
	return strings.Join(parts, ", ")
}

func rowLabel(row Row) string {
	keys := make([]string, 0, len(row.Labels))
	for k := range row.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	vals := make([]string, 0, len(keys))
	for _, k := range keys {
		vals = append(vals, row.Labels[k])
	}
	if len(vals) == 1 {
		return vals[0]
	}
	return "{" + strings.Join(vals, "/") + "}"
}

func esc(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

func has(roles []string, r string) bool {
	for _, x := range roles {
		if x == r {
			return true
		}
	}
	return false
}

// Render — deterministik markdown.
func Render(rep Report) string {
	ix := newIndex(rep.Units, rep.Results)
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...); b.WriteByte('\n') }

	// 1. Başlık.
	w("# Rollouts v2 §11 sorgu paketi — %s", Version)
	w("")
	// v0.10.979 — pod adı yalnız JSON'da; markdown yapıştırılır, hostname jetonlanmaz.
	w("- run: `%s` · status: **%s**", rep.RunID, rep.Status)
	w("- started %s · finished %s · duration %d ms · budget %d s", rep.started.UTC().Format(time.RFC3339), rep.finished.UTC().Format(time.RFC3339), rep.DurationMs, rep.BudgetS)
	w("- calls %d / planned %d · packs %s · tokens %d", rep.Calls, rep.Planned, packList(rep.Packs), rep.Tokens)
	for _, u := range rep.Units {
		line := fmt.Sprintf("- unit `%s` (%s): %s, %d calls, %d ms", u.Token, u.Role, u.State, u.Calls, u.DurationMs)
		if u.WithoutMatcher {
			line += ", without-matcher pass"
		}
		if u.NSFilter {
			line += ", namespace filter"
		}
		if u.EarlyStop != "" {
			line += ", early stop: " + u.EarlyStop
		}
		w("%s", line)
	}
	w("")
	w("Eşleme özel: değerler jetonlu (§11.0); etiket adları, sayımlar, enum, sürüm, port ve uyarı metni harfi harfine. Rapor şablon ifadeleri basar, etkin ifadeyi değil.")
	w("")

	// 2. §11.9 yapıştırma tablosu.
	cols := []Unit{}
	for _, u := range rep.Units {
		if u.Role == "target" || u.Role == "hub" {
			cols = append(cols, u)
		}
	}
	w("## §11.9 paste-back")
	w("")
	hdr := "| ID |"
	sep := "|---|"
	for _, c := range cols {
		hdr += " " + c.Token + " |"
		sep += "---|"
	}
	w("%s", hdr)
	w("%s", sep)
	for _, pr := range pasteRows {
		line := "| " + esc(pr.label) + " |"
		for _, c := range cols {
			if !has(pr.roles, c.Role) {
				line += " — |"
				continue
			}
			cells := []string{}
			for _, id := range pr.ids {
				r := ix.get(id, c.Token, "")
				if r == nil {
					// pair/inst varyantları: id ile başlayan her sonuç.
					for i := range rep.Results {
						x := &rep.Results[i]
						if x.ID == id && x.Unit == c.Token {
							cells = append(cells, id+"["+x.Variant+"]: "+summary(x))
						}
					}
					continue
				}
				cell := id + ": " + summary(r)
				if d := ix.get(id, c.Token, "dedup_off"); d != nil {
					cell += " (dedup off " + summary(d) + ")"
				}
				if n := ix.get(id, c.Token, "ns"); n != nil {
					cell += " (ns " + summary(n) + ")"
				}
				cells = append(cells, cell)
			}
			if pr.label == "D DC counts and share" {
				if share, _, _, ok := ix.dcShare(c.Token); ok {
					cells = append(cells, fmt.Sprintf("share %.1f%%", share*100))
				}
			}
			// v0.10.979 — §11.9 satırı etiket ADLARINI ve H1.2 harfini sorar; hücre
			// yalnız değer basıyordu. Karar tablosuyla (dec 5 / dec 28) aynı yardımcılar.
			if pr.label == "H0.1 / H0.4 / dedup / label names (H0.3 vs H0.5)" {
				l3 := labelNamesOf(ix.get("H0.3", c.Token, ""))
				l5 := labelNamesOf(ix.get("H0.5", c.Token, ""))
				cells = append(cells, "labels ["+strings.Join(l3, ",")+"] vs ["+strings.Join(l5, ",")+"]")
			}
			if strings.HasPrefix(pr.label, "H1.2 case (A/B/C)") {
				cells = append(cells, "case "+h12Case(ix.get("H1.2", c.Token, "")))
			}
			if len(cells) == 0 {
				line += " — |"
			} else {
				line += " " + esc(strings.Join(cells, " · ")) + " |"
			}
		}
		w("%s", line)
	}
	for _, lbl := range []string{"A (optional) version; field presence; username kind", "V HTTP codes, counts, repo kind, flavour, proxy"} {
		line := "| " + lbl + " |"
		for range cols {
			line += " skipped (metrics-only) |"
		}
		w("%s", line)
	}
	{
		line := "| T1/T2 coverage and cluster form (per span cluster value) |"
		t1, t2 := ix.get("T1", ClickHouseUnit, ""), ix.get("T2", ClickHouseUnit, "")
		for _, c := range cols {
			cells := []string{}
			if t1 != nil && t1.Usable() {
				for _, row := range t1.Rows {
					if row.Labels["cluster"] == c.Token {
						cells = append(cells, "T1: sampled="+row.Labels["sampled"]+" depl="+row.Labels["depl"]+" rs="+row.Labels["rs"]+" env_name="+row.Labels["env_name"]+" k8s_cluster="+row.Labels["k8s_cluster"]+" ocp_cluster="+row.Labels["ocp_cluster"])
					}
				}
			} else if t1 != nil {
				cells = append(cells, "T1: "+summary(t1))
			}
			if t2 != nil && t2.Usable() {
				for _, row := range t2.Rows {
					if row.Labels["cluster"] == c.Token {
						cells = append(cells, "T2: deploy_env="+row.Labels["deploy_env"]+" n="+row.Value)
					}
				}
			}
			if len(cells) == 0 {
				line += " — |"
			} else {
				line += " " + esc(strings.Join(cells, " · ")) + " |"
			}
		}
		w("%s", line)
	}
	{
		line := "| Querier limits (timeout, max samples); warnings seen |"
		for _, c := range cols {
			kinds := map[string]int{}
			warns := []string{}
			for i := range rep.Results {
				r := &rep.Results[i]
				if r.Unit != c.Token || r.Skipped {
					continue
				}
				if !r.Usable() {
					kinds[string(r.State)+":"+firstWord(r.Detail)]++
				}
				warns = append(warns, r.Warnings...)
			}
			ks := []string{}
			for k, n := range kinds {
				ks = append(ks, k+"×"+strconv.Itoa(n))
			}
			sort.Strings(ks)
			cell := "errors [" + strings.Join(ks, " ") + "]"
			if ws := dedupSorted(warns); len(ws) > 0 {
				cell += " · warnings: " + strings.Join(ws, "; ")
			}
			line += " " + esc(cell) + " |"
		}
		w("%s", line)
	}
	w("")

	// 3. Paket → birim → sorgu.
	for _, p := range AllPacks {
		if !hasPack(rep.Packs, p) {
			continue
		}
		w("## Pack %s", p)
		w("")
		unitsFor := []Unit{}
		for _, u := range rep.Units {
			if p == PackT {
				if u.Role == ClickHouseUnit {
					unitsFor = append(unitsFor, u)
				}
				continue
			}
			for _, q := range ByPack(p) {
				if q.Runs(u.Role) {
					unitsFor = append(unitsFor, u)
					break
				}
			}
		}
		for _, u := range unitsFor {
			w("### %s @ `%s`", p, u.Token)
			w("")
			for _, q := range ByPack(p) {
				if p != PackT && !q.Runs(u.Role) {
					continue
				}
				var rs []*Result
				for i := range rep.Results {
					if rep.Results[i].ID == q.ID && rep.Results[i].Unit == u.Token {
						rs = append(rs, &rep.Results[i])
					}
				}
				if len(rs) == 0 {
					continue
				}
				w("#### %s — %s", q.ID, q.Expect)
				w("")
				w("```promql")
				w("%s", q.Expr)
				w("```")
				renderPair(&b, q, rs, rep.started)
				for _, r := range rs {
					renderResult(&b, q, r, rep.started)
				}
			}
		}
	}

	// 4. Varsayımlar.
	w("## Varsayımlar V1–V14")
	w("")
	w("| V | verdict | evidence | note |")
	w("|---|---|---|---|")
	for _, a := range rep.Assumptions {
		w("| %s | %s | %s | %s |", a.ID, a.Verdict, esc(strings.Join(a.Evidence, "<br>")), esc(a.Note))
	}
	w("")

	// 5. Kararlar.
	w("## Bilgilendirilen kararlar")
	w("")
	w("| decision | verdict | queries |")
	w("|---|---|---|")
	for _, d := range rep.Decisions {
		w("| %s | %s | %s |", esc(d.ID), esc(d.Verdict), strings.Join(d.Queries, ", "))
	}
	w("")

	// 6. Atlananlar.
	w("## Atlananlar")
	w("")
	w("- A (§11.6 Argo CD API): skipped (metrics-only)")
	w("- V (§11.7 Azure DevOps): skipped (metrics-only)")
	w("- L3 (§11.5 name listing): operator-only, never run by the probe")
	for _, s := range rep.Skipped {
		w("- %s", s)
	}
	for _, u := range rep.Units {
		if u.EarlyStop != "" {
			w("- unit `%s`: early stop — %s", u.Token, u.EarlyStop)
		}
	}
	if rep.Status == "budget_exhausted" {
		w("- run budget exhausted: remaining queries skipped")
	}
	w("")

	// 7. Altbilgi.
	w("## Footer")
	w("")
	if len(rep.DeniedLabels) > 0 {
		w("- default-denied label names (values never shown): %s", strings.Join(rep.DeniedLabels, ", "))
	} else {
		w("- default-denied label names: none")
	}
	totals := map[Pack]int{}
	for _, r := range rep.Results {
		if q, ok := Find(r.ID); ok {
			totals[q.Pack]++
		}
	}
	parts := []string{}
	for _, p := range AllPacks {
		if n := totals[p]; n > 0 {
			parts = append(parts, string(p)+"="+strconv.Itoa(n))
		}
	}
	w("- results per pack: %s", strings.Join(parts, " "))
	if len(rep.Warnings) > 0 {
		w("- run warnings (scrubbed): %s", esc(strings.Join(rep.Warnings, "; ")))
	}
	return b.String()
}

func firstWord(s string) string {
	if i := strings.IndexAny(s, ": "); i > 0 {
		return s[:i]
	}
	return s
}

func packList(ps []Pack) string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, string(p))
	}
	return strings.Join(out, ",")
}

func hasPack(ps []Pack, p Pack) bool {
	for _, x := range ps {
		if x == p {
			return true
		}
	}
	return false
}

func offsetOf(t, start time.Time) string {
	if t.IsZero() || start.IsZero() {
		return "?"
	}
	return "t+" + strconv.FormatInt(int64(t.Sub(start).Seconds()), 10) + " s"
}

// renderPair — nomatch/with çifti ve K0.6b çift örneği: yan yana + Δ.
func renderPair(b *strings.Builder, q Query, rs []*Result, start time.Time) {
	var with, without, s1, s2 *Result
	for _, r := range rs {
		switch r.Variant {
		case "":
			with, s1 = r, r
		case "nomatch":
			without = r
		case "second_sample":
			s2 = r
		}
	}
	if with != nil && without != nil {
		delta := "n/a"
		if a, ok := with.ScalarOrZero(); ok {
			if c, ok := without.ScalarOrZero(); ok {
				delta = num(c - a)
			}
		} else if with.Usable() && without.Usable() {
			delta = strconv.Itoa(len(without.Rows)-len(with.Rows)) + " rows"
		}
		fmt.Fprintf(b, "| variant | result | Δ (without − with) |\n|---|---|---|\n| with matcher | %s | |\n| without matcher | %s | %s |\n\n",
			esc(summary(with)), esc(summary(without)), esc(delta))
	}
	if q.Repeat&RepeatSecondSample != 0 && s1 != nil && s2 != nil {
		eq := "no"
		if s1.Scalar != nil && s2.Scalar != nil && *s1.Scalar == *s2.Scalar {
			eq = "yes"
		}
		fmt.Fprintf(b, "- sample 1 (%s): %s · sample 2 (%s): %s · equal: %s\n\n",
			offsetOf(s1.SampleAt, start), summary(s1), offsetOf(s2.SampleAt, start), summary(s2), eq)
	}
}

func renderResult(b *strings.Builder, q Query, r *Result, start time.Time) {
	head := "- "
	if r.Variant != "" {
		head += "`" + r.Variant + "` "
	}
	head += "state: **" + string(r.State) + "**"
	if r.Skipped {
		head += " (skipped)"
	}
	if r.Detail != "" {
		head += " — " + esc(r.Detail)
	}
	if r.Truncated {
		head += fmt.Sprintf(" · truncated (total %d)", r.Total)
	}
	head += fmt.Sprintf(" · %d ms", r.DurationMs)
	fmt.Fprintln(b, head)
	switch {
	case r.Scalar != nil:
		fmt.Fprintf(b, "  - scalar: `%s`\n", *r.Scalar)
	case len(r.Rows) > 0:
		keys := map[string]bool{}
		for _, row := range r.Rows {
			for k := range row.Labels {
				keys[k] = true
			}
		}
		cols := make([]string, 0, len(keys))
		for k := range keys {
			cols = append(cols, k)
		}
		sort.Strings(cols)
		fmt.Fprintln(b, "")
		fmt.Fprintf(b, "  | %s | value |\n", strings.Join(cols, " | "))
		fmt.Fprintf(b, "  |%s---|\n", strings.Repeat("---|", len(cols)))
		for _, row := range r.Rows {
			vals := make([]string, 0, len(cols))
			for _, c := range cols {
				vals = append(vals, esc(row.Labels[c]))
			}
			fmt.Fprintf(b, "  | %s | %s |\n", strings.Join(vals, " | "), esc(row.Value))
		}
		if r.Truncated && r.Total > len(r.Rows) {
			fmt.Fprintf(b, "  | … | +%d rows |\n", r.Total-len(r.Rows))
		}
		fmt.Fprintln(b, "")
	case r.Names != nil && q.Shape == ShapeNames:
		if len(r.Names) == 0 {
			fmt.Fprintln(b, "  - names: (none)")
		} else {
			fmt.Fprintf(b, "  - names (%d): %s\n", len(r.Names), strings.Join(r.Names, ", "))
		}
	}
	for _, wv := range r.Warnings {
		fmt.Fprintf(b, "  - warning: %s\n", esc(wv))
	}
	for _, in := range r.Infos {
		fmt.Fprintf(b, "  - info: %s\n", esc(in))
	}
	fmt.Fprintln(b, "")
}
