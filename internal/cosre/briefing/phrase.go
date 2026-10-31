package briefing

// phrase.go — LLM başlık cümlesinin SAF yarısı (Q8): ne zaman çağrılır,
// modele ne gider (sıkıştırılmış bulgular, ≤1500 tok hedefi), dönen metin
// ne zaman kabul edilir. Sayılar modelden geçmez: rakam içeren başlık
// reddedilir, şablon kalır.

import (
	"encoding/json"
	"strings"
	"unicode"
)

const (
	// LLMMinFindings — bundan az bulguda LLM çağrılmaz (şablon yeter).
	LLMMinFindings = 3
	// LLMMaxTokens — başlık için completion tavanı.
	LLMMaxTokens = 300
	// LLMMaxInputBytes — sıkıştırılmış girdi tavanı (~1500 tok ≈ 6 KB, güvenli pay).
	LLMMaxInputBytes = 5000
	// HeadlineMaxRunes — kabul edilen başlık uzunluğu.
	HeadlineMaxRunes = 240
)

// NeedsLLM — SAF: ayar açık + model hazır + bulgu ≥ LLMMinFindings.
func NeedsLLM(b Briefing, llmPhrasing, modelReady bool) bool {
	return llmPhrasing && modelReady && FindingCount(b.Lines) >= LLMMinFindings
}

type compactRow struct {
	Service string  `json:"s,omitempty"`
	Kind    string  `json:"k"`
	Label   string  `json:"l,omitempty"`
	Now     float64 `json:"now"`
	Prev    float64 `json:"prev"`
	Unit    string  `json:"u,omitempty"`
}

type compactLine struct {
	Item  string       `json:"item"`
	Total int          `json:"total"`
	Rows  []compactRow `json:"rows"`
}

// CompactInput — SAF: modelin girdisi. Metin alanları (Text, PageURL) YOK;
// etiketler 60 rune'a kırpılır; tavanı aşarsa satırlar sondan düşer.
func CompactInput(b Briefing) []byte {
	in := struct {
		Team     string        `json:"team"`
		Env      string        `json:"env,omitempty"`
		WindowS  int           `json:"window_s"`
		Services int           `json:"services"`
		Lines    []compactLine `json:"lines"`
		Partial  []string      `json:"partial,omitempty"`
	}{Team: b.Team, Env: b.Env, WindowS: b.WindowS, Services: b.Services + b.Trimmed, Partial: b.Partial}
	for _, l := range b.Lines {
		cl := compactLine{Item: l.Item, Total: l.Total}
		for _, f := range l.Rows {
			cl.Rows = append(cl.Rows, compactRow{Service: f.Service, Kind: f.Kind, Label: clipRunes(f.Label, 60), Now: round2(f.Now), Prev: round2(f.Prev), Unit: f.Unit})
		}
		in.Lines = append(in.Lines, cl)
	}
	for {
		raw, _ := json.Marshal(in)
		if len(raw) <= LLMMaxInputBytes || len(in.Lines) == 0 {
			return raw
		}
		last := &in.Lines[len(in.Lines)-1]
		if len(last.Rows) > 1 {
			last.Rows = last.Rows[:len(last.Rows)-1]
		} else {
			in.Lines = in.Lines[:len(in.Lines)-1]
		}
	}
}

// AcceptHeadline — SAF: model cevabı başlık olarak kabul edilir mi. Boş,
// rakam içeren (sayılar kartta kalır), çok uzun ya da markdown/madde içeren
// cevap reddedilir. Kabul edilen metin tek satıra katlanır.
func AcceptHeadline(s string) (string, bool) {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" || len([]rune(s)) > HeadlineMaxRunes || strings.ContainsAny(s, "#*`|") || strings.HasPrefix(s, "-") {
		return "", false
	}
	for _, r := range s {
		if unicode.IsDigit(r) {
			return "", false
		}
	}
	return s, true
}

func clipRunes(s string, n int) string {
	if rs := []rune(s); len(rs) > n {
		return string(rs[:n])
	}
	return s
}

func round2(v float64) float64 {
	if v < 0 {
		return -round2(-v)
	}
	return float64(int64(v*100+0.5)) / 100
}
