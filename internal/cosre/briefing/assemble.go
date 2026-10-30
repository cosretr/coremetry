package briefing

import (
	"encoding/json"
	"sort"
)

// Briefing — bir takım × env için üretilmiş kart (cosre_briefings.payload).
type Briefing struct {
	ID            string `json:"id"` // ULID (Q10 geri alındı 2026-10-31: satır geri bildirimi yok)
	Team          string `json:"team"`
	TeamKey       string `json:"team_key"`
	Env           string `json:"env"`
	WindowS       int    `json:"window_s"`
	GeneratedAtMs int64  `json:"generated_at_ms"`
	ScopeHash     string `json:"scope_hash"`
	Services      int    `json:"services"`
	// Trimmed — MaxTeamServices tavanına takılıp OKUNMAYAN servis (Q12: "N servis dışarıda").
	Trimmed int    `json:"trimmed"`
	Lines   []Line `json:"lines"`
	Silence bool   `json:"silence"`
	Summary string `json:"summary"`
	// Phrasing — "template" | "llm" (başlığı kim kurdu).
	Phrasing string `json:"phrasing"`
	// Partial — okunamayan kalemler (hata / kaynak yok): "yok" DEĞİL, "okunamadı".
	Partial []string `json:"partial,omitempty"`
	// Skipped — bu kurulumda kaynağı olmayan kalemler (ör. CH logstore → log; Oracle kapalı).
	Skipped []string `json:"skipped,omitempty"`
}

// AssembleInput — derleyicinin girdileri (okumalar çağıranda).
type AssembleInput struct {
	ID, Team, TeamKey, Env string
	WindowS                int
	GeneratedAtMs          int64
	Services               []string
	Trimmed                int
	Findings               []Finding
	Partial, Skipped       []string
}

// Assemble — SAF: bulgular → kart. Sessizlik yalnız bulgu YOKSA (okunamayan
// kalem varsa bile — Partial ayrıca söyler). Başlık deterministik; LLM
// cümleleştirmesi ayrı adım (Summary/Phrasing'i ezer).
func Assemble(in AssembleInput) Briefing {
	lines := Lines(in.Findings)
	if lines == nil {
		lines = []Line{}
	}
	b := Briefing{
		ID: in.ID, Team: in.Team, TeamKey: in.TeamKey, Env: in.Env, WindowS: in.WindowS,
		GeneratedAtMs: in.GeneratedAtMs, ScopeHash: ScopeHash(in.Services),
		Services: len(SortedServices(in.Services)), Trimmed: in.Trimmed,
		Lines: lines, Silence: len(lines) == 0, Phrasing: "template",
		Partial: sortedUniq(in.Partial), Skipped: sortedUniq(in.Skipped),
	}
	b.Summary = TemplateSummary(lines, b.Services+b.Trimmed, in.WindowS)
	return b
}

func sortedUniq(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// FindingsHash — SAF: kartın İÇERİK özeti (kimlik/zaman hariç) — LLM önbelleği
// anahtarı: aynı bulgular yeniden cümleleştirilmez.
func FindingsHash(b Briefing) string {
	type row struct {
		Item, Kind, Service, Label string
		Now, Prev                  float64
	}
	var rows []row
	for _, l := range b.Lines {
		for _, f := range l.Rows {
			rows = append(rows, row{f.Item, f.Kind, f.Service, f.Label, f.Now, f.Prev})
		}
	}
	raw, _ := json.Marshal(struct {
		Rows    []row
		Totals  []int
		Env     string
		Window  int
		N, Trim int
		Partial []string
	}{rows, lineTotals(b.Lines), b.Env, b.WindowS, b.Services, b.Trimmed, b.Partial})
	return fnvHex(string(raw))
}

func lineTotals(ls []Line) []int {
	out := make([]int, len(ls))
	for i, l := range ls {
		out[i] = l.Total
	}
	return out
}
