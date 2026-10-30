// Package briefing — CoSRE Login Briefing'in SAF çekirdeği (docs/cosre/
// briefing-audit.md "Tasarım önerisi"; operatör onayı 2026-10-30, Q1–Q12).
//
// Burada I/O YOK: ayar normalizasyonu, eşik kararları, kalem başına bulgu
// üreticileri, satır derleyici (sıra sabit 1→7), sessizlik satırı, önbellek
// anahtarı ve bulgu özeti (LLM girdisi + findings-hash). Okumalar
// (MV / VM / ES / problems / deploys) api katmanında, Service sekmelerinin
// KULLANDIĞI fonksiyonlardan yapılır; bu paket yalnız sayıları karşılaştırır.
//
// Sözleşme: bulgu UYDURULMAZ — her satır bir okuma satırından gelir; okuma
// başarısızsa kalem "okunamadı" (Partial) olarak işaretlenir, "yok" sayılmaz.
// Sayılar LLM'den geçmez: kart satırı bulgunun kendisini gösterir, LLM
// (varsa) yalnız başlık cümlesini kurar.
package briefing

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"net/url"
	"sort"
	"strings"
)

// Kalem kimlikleri — sıra = kart sırası (1→7).
const (
	ItemRED       = "red"
	ItemClient    = "client"
	ItemException = "exception"
	ItemLog       = "log"
	ItemOracle    = "oracle"
	ItemProblem   = "problem"
	ItemRollout   = "rollout"
)

// ItemOrder — kart sırası.
var ItemOrder = []string{ItemRED, ItemClient, ItemException, ItemLog, ItemOracle, ItemProblem, ItemRollout}

// UnresolvedTeamLine — takım çözülemediğinde sohbetin tek satırı (operatör metni).
const UnresolvedTeamLine = "Ekibin tanımlı değil — hangi takımdasın?"

// ── Ayar ───────────────────────────────────────────────────────────────

// Thresholds — kalem eşikleri (Q4: sabit kat, Faz 1).
type Thresholds struct {
	ErrRateX          float64 `json:"err_rate_x"`           // hata oranı kat (cur/prev)
	ErrRateMinPct     float64 `json:"err_rate_min_pct"`     // mutlak taban: bunun altı hiç satır değil
	LatencyX          float64 `json:"latency_x"`            // p99 kat
	TrafficDropPct    float64 `json:"traffic_drop_pct"`     // trafik düşüşü %
	MinSpans          uint64  `json:"min_spans"`            // oran için asgari span (iki pencere)
	ClientX           float64 `json:"client_x"`             // istemci metriği kat (kafka/http/grpc)
	LogLevelX         float64 `json:"log_level_x"`          // ERROR/WARN sayısı kat
	LogMinCount       uint64  `json:"log_min_count"`        // log satırı tabanı
	OracleCodeX       float64 `json:"oracle_code_x"`        // Oracle kod sayısı kat
	OracleMinCount    uint64  `json:"oracle_min_count"`     // Oracle satırı tabanı
	DeployErrDeltaPts float64 `json:"deploy_err_delta_pts"` // deploy sonrası hata oranı farkı (puan)
}

// Config — system_settings["cosre.briefing"] blobu.
type Config struct {
	Enabled          bool       `json:"enabled"`
	WindowS          int        `json:"window_s"`
	RefreshIntervalS int        `json:"refresh_interval_s"`
	TTLS             int        `json:"ttl_s"`
	Envs             []string   `json:"envs"`
	LLMPhrasing      bool       `json:"llm_phrasing"`
	Thresholds       Thresholds `json:"thresholds"`
}

// Sınırlar — ayar yalnız bu aralıkta oynar (sabitler tavan/taban).
const (
	DefaultWindowS  = 3 * 3600
	MinWindowS      = 3600
	MaxWindowS      = 3 * 3600 // ES 3×60 dk dilimi (Q7) ve worker maliyeti (R9)
	DefaultRefreshS = 300
	MinRefreshS     = 120
	MaxRefreshS     = 3600
	DefaultTTLS     = 600
	MaxTTLS         = 3600 // tablo TTL'i (1 sa) bunun üstünü zaten siler
	MaxEnvs         = 4
	MaxLines        = 7
	// MaxRowsPerLine — satırın taşıdığı bulgu tavanı (kart + LLM girdisi).
	MaxRowsPerLine = 3
)

// DefaultConfig — varsayılan: KAPALI (operatör açar); Q2 3 sa / 5 dk; Q3 env "" + prod; Q8 LLM açık.
func DefaultConfig() Config {
	return Config{
		Enabled: false, WindowS: DefaultWindowS, RefreshIntervalS: DefaultRefreshS, TTLS: DefaultTTLS,
		Envs: []string{"", "prod"}, LLMPhrasing: true,
		Thresholds: DefaultThresholds(),
	}
}

// DefaultThresholds — Q4 sabit katlar.
func DefaultThresholds() Thresholds {
	return Thresholds{
		ErrRateX: 2, ErrRateMinPct: 1, LatencyX: 1.5, TrafficDropPct: 50, MinSpans: 200,
		ClientX: 2, LogLevelX: 2, LogMinCount: 20, OracleCodeX: 2, OracleMinCount: 5,
		DeployErrDeltaPts: 1,
	}
}

func clampF(v, def, lo, hi float64) float64 {
	if v == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return def
	}
	return math.Max(lo, math.Min(hi, v))
}

func clampI(v, def, lo, hi int) int {
	if v == 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampU(v, def, lo, hi uint64) uint64 {
	if v == 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Normalize — SAF: sıfır alan varsayılana, her alan aralığına; envs kırpılır,
// tekilleştirilir, tavanlanır (boş liste → [""]). TTL refresh'ten kısa olamaz
// (aksi halde iki üretim arasında kart kaybolur).
func (c Config) Normalize() Config {
	d := DefaultThresholds()
	t := c.Thresholds
	out := Config{
		Enabled:          c.Enabled,
		WindowS:          clampI(c.WindowS, DefaultWindowS, MinWindowS, MaxWindowS),
		RefreshIntervalS: clampI(c.RefreshIntervalS, DefaultRefreshS, MinRefreshS, MaxRefreshS),
		TTLS:             clampI(c.TTLS, DefaultTTLS, MinRefreshS, MaxTTLS),
		LLMPhrasing:      c.LLMPhrasing,
		Thresholds: Thresholds{
			ErrRateX:          clampF(t.ErrRateX, d.ErrRateX, 1.1, 100),
			ErrRateMinPct:     clampF(t.ErrRateMinPct, d.ErrRateMinPct, 0.01, 100),
			LatencyX:          clampF(t.LatencyX, d.LatencyX, 1.1, 100),
			TrafficDropPct:    clampF(t.TrafficDropPct, d.TrafficDropPct, 10, 99),
			MinSpans:          clampU(t.MinSpans, d.MinSpans, 10, 1_000_000),
			ClientX:           clampF(t.ClientX, d.ClientX, 1.1, 100),
			LogLevelX:         clampF(t.LogLevelX, d.LogLevelX, 1.1, 100),
			LogMinCount:       clampU(t.LogMinCount, d.LogMinCount, 1, 1_000_000),
			OracleCodeX:       clampF(t.OracleCodeX, d.OracleCodeX, 1.1, 100),
			OracleMinCount:    clampU(t.OracleMinCount, d.OracleMinCount, 1, 1_000_000),
			DeployErrDeltaPts: clampF(t.DeployErrDeltaPts, d.DeployErrDeltaPts, 0.1, 100),
		},
	}
	if out.TTLS < out.RefreshIntervalS {
		out.TTLS = out.RefreshIntervalS
	}
	seen := map[string]bool{}
	for _, e := range c.Envs {
		e = strings.TrimSpace(e)
		if seen[e] || len(out.Envs) >= MaxEnvs {
			continue
		}
		seen[e] = true
		out.Envs = append(out.Envs, e)
	}
	if len(out.Envs) == 0 {
		out.Envs = []string{""}
	}
	return out
}

// ParseConfig — SAF: blob yok / bozuk → varsayılan; eksik alan varsayılan.
// llm_phrasing ve envs yalnız blobda AÇIKÇA varsa varsayılanı ezer.
func ParseConfig(raw []byte) Config {
	c := DefaultConfig()
	if len(raw) == 0 {
		return c
	}
	var in struct {
		Config
		LLMPhrasing *bool    `json:"llm_phrasing"`
		Envs        []string `json:"envs"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return c
	}
	out := in.Config
	out.LLMPhrasing = c.LLMPhrasing
	if in.LLMPhrasing != nil {
		out.LLMPhrasing = *in.LLMPhrasing
	}
	out.Envs = c.Envs
	if in.Envs != nil {
		out.Envs = in.Envs
	}
	return out.Normalize()
}

// ── Bulgu + satır ──────────────────────────────────────────────────────

// Finding — tek kanıt satırı (araç satırı şekli; sayılar okumadan aynen).
type Finding struct {
	Item    string  `json:"item"`
	Kind    string  `json:"kind"`
	Service string  `json:"service,omitempty"`
	Label   string  `json:"label,omitempty"`
	Now     float64 `json:"now"`
	Prev    float64 `json:"prev"`
	Unit    string  `json:"unit,omitempty"`
	// Score — satır içi sıralama (büyük önce); kat ya da mutlak fark.
	Score   float64 `json:"score"`
	Text    string  `json:"text"`
	PageURL string  `json:"page_url"`
}

// Line — kartın bir kalem satırı.
type Line struct {
	Item    string    `json:"item"`
	Text    string    `json:"text"`
	PageURL string    `json:"page_url"`
	Total   int       `json:"total"` // kalemdeki bulgu sayısı (gösterilen ≤ MaxRowsPerLine)
	Rows    []Finding `json:"rows"`
}

// Lines — SAF: bulgular → satırlar. Kalem başına bir satır, sıra ItemOrder;
// satır içi Score azalan, eşitlikte servis+etiket (deterministik).
func Lines(fs []Finding) []Line {
	by := map[string][]Finding{}
	for _, f := range fs {
		by[f.Item] = append(by[f.Item], f)
	}
	var out []Line
	for _, item := range ItemOrder {
		rows := by[item]
		if len(rows) == 0 {
			continue
		}
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].Score != rows[j].Score {
				return rows[i].Score > rows[j].Score
			}
			if rows[i].Service != rows[j].Service {
				return rows[i].Service < rows[j].Service
			}
			return rows[i].Label < rows[j].Label
		})
		total := len(rows)
		if len(rows) > MaxRowsPerLine {
			rows = rows[:MaxRowsPerLine]
		}
		text := rows[0].Text
		if total > 1 {
			text += fmt.Sprintf(" (+%d daha)", total-1)
		}
		out = append(out, Line{Item: item, Text: text, PageURL: rows[0].PageURL, Total: total, Rows: append([]Finding(nil), rows...)})
		if len(out) >= MaxLines {
			break
		}
	}
	return out
}

// ── Pivot URL'leri (serviceHref şekli, frontend/src/lib/serviceHref.ts) ──

// RangeParam — SAF: pencere → URL range preset ("3h", "90m").
func RangeParam(windowS int) string {
	if windowS%3600 == 0 {
		return fmt.Sprintf("%dh", windowS/3600)
	}
	return fmt.Sprintf("%dm", windowS/60)
}

// ServiceURL — SAF: /service?name=&tab=&range=&env= (tab "" = overview).
func ServiceURL(service, tab, env string, windowS int) string {
	v := url.Values{}
	v.Set("name", service)
	if tab != "" {
		v.Set("tab", tab)
	}
	v.Set("range", RangeParam(windowS))
	if env != "" {
		v.Set("env", env)
	}
	return "/service?" + v.Encode()
}

// ── Anahtarlar ─────────────────────────────────────────────────────────

func fnvHex(parts ...string) string {
	h := fnv.New64a()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return fmt.Sprintf("%016x", h.Sum64())
}

// SortedServices — SAF: kırp, boşları at, tekilleştir, sırala (girdi değişmez).
func SortedServices(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// ScopeHash — SAF: servis kümesinin özeti (sıra bağımsız, uzunluk değil — v0.5.187).
func ScopeHash(services []string) string { return fnvHex(SortedServices(services)...) }

// CacheKey — SAF: briefing:v1:<fnv(team_key)>:<fnv(küme)>:<fnv(env)>:<window>:<to/300>
// (briefing-audit §2). TÜM girdiler; env de hash'li (boş env ≠ ":"-çakışması).
func CacheKey(teamKey string, services []string, env string, windowS int, toUnix int64) string {
	return fmt.Sprintf("briefing:v1:%s:%s:%s:%d:%d", fnvHex(teamKey), ScopeHash(services), fnvHex(env), windowS, toUnix/300)
}

// ── Sessizlik + başlık ─────────────────────────────────────────────────

// SilenceText — SAF: bulgu yokken tek satır (sessizlik ≠ sağlıklı değil: okunanı söyler).
func SilenceText(services, windowS int) string {
	return fmt.Sprintf("Son %s içinde takımının %d servisinde eşiği aşan bir değişiklik yok.", humanWindow(windowS), services)
}

func humanWindow(windowS int) string {
	if windowS%3600 == 0 {
		return fmt.Sprintf("%d sa", windowS/3600)
	}
	return fmt.Sprintf("%d dk", windowS/60)
}

// TemplateSummary — SAF: deterministik başlık (LLM yok / ≤2 bulgu / model hatası).
func TemplateSummary(lines []Line, services, windowS int) string {
	if len(lines) == 0 {
		return SilenceText(services, windowS)
	}
	n := 0
	for _, l := range lines {
		n += l.Total
	}
	parts := make([]string, 0, len(lines))
	for _, l := range lines {
		parts = append(parts, itemLabelTR[l.Item])
	}
	return fmt.Sprintf("Son %s: %d bulgu — %s.", humanWindow(windowS), n, strings.Join(parts, ", "))
}

var itemLabelTR = map[string]string{
	ItemRED: "RED sapması", ItemClient: "istemci metrikleri", ItemException: "yeni exception",
	ItemLog: "log hataları", ItemOracle: "Oracle hataları", ItemProblem: "açık Problem", ItemRollout: "rollout etkisi",
}

// FindingCount — SAF: satırlardaki toplam bulgu.
func FindingCount(lines []Line) int {
	n := 0
	for _, l := range lines {
		n += l.Total
	}
	return n
}

// ── Sayı biçimi ────────────────────────────────────────────────────────

func pct(v float64) string { return fmt.Sprintf("%%%.1f", v) }

func ms(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.1f s", v/1000)
	}
	return fmt.Sprintf("%.0f ms", v)
}

func ratio(now, prev float64) float64 {
	if prev <= 0 {
		return 0
	}
	return now / prev
}

func xs(r float64) string {
	if r <= 0 {
		return "yeni"
	}
	return fmt.Sprintf("%.1f×", r)
}
