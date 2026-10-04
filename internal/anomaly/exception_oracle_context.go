package anomaly

// exception_oracle_context.go — v0.10.1100 (operatör onaylı): Oracle hata
// grubunun (`ora:` parmak izi, v0.10.1092) AI açıklama girdisi.
//
// ── KUSUR ───────────────────────────────────────────────────────────────
//
// BuildExceptionExplainInput span'den beslenir: temsilî stack, örnek trace'in
// span'leri, o trace'in logları, pod dağılımı. Oracle hata tablosu satırı
// bunların hiçbirini taşımaz (örnekler stack'siz, trace çoğu zaman başka bir
// servisin) — açıklama boş ya da jenerik çıkıyordu.
//
// ── KARAR ───────────────────────────────────────────────────────────────
//
// `ora:` grubunda girdi Oracle verisinden kurulur: kaynak, kod + operasyon,
// son 1 sa / önceki 1 sa ve oran (GroupStatsCache — Exceptions satırıyla AYNI
// sayı), kanal kırılımı (ilk 3, %), etkilenen servisler (ilk 3), en yeni
// satırlardan host / instance dağılımı, çözülen trace'ler (≤5, mevcut trace →
// servis çözümüyle) ve en yeni 3 satırın eşlenen alanları. Okumalar sınırlı:
// OracleErrorsByOpCode tavanı oracleExplainRowLimit, trace çözümü ≤5 id; yeni
// tablo yok. Hepsi best-effort — okuma düşerse o blok "yok" basılır.
//
// anomaly → oracle importu YOK (oracle anomaly'yi import ediyor): blob
// gerçekleri main.go'nun enjekte ettiği OracleFactsFn'den gelir
// (oracle.GroupStatsCache.ExplainFacts).

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/promptfmt"
)

const (
	// oracleExplainRowLimit — explain başına okunan en yeni satır tavanı
	// (host/instance dağılımı, trace'ler ve örnekler bu kümeden).
	oracleExplainRowLimit = 50
	// oracleExplainLookback — satır penceresi: son görülmeden geriye.
	oracleExplainLookback = time.Hour
	oracleExplainTraceMax = 5
	oracleExplainSamples  = 3
	oracleExplainTopN     = 3
	oracleExplainAttrMax  = 8
	// oracleExplainBurst — patlama katı; api.oraclePriorityAt ile AYNI (3×).
	oracleExplainBurst = 3
)

// OracleNamedCount — sıralı kırılım satırı (oracle.BreakdownEntry ikizi).
type OracleNamedCount struct {
	Name  string `json:"name"`
	Count uint64 `json:"count"`
	// Pct — toplam içindeki pay (yalnız kanal / host / instance; 0 = yazılmaz).
	Pct int `json:"pct,omitempty"`
}

// OracleExplainFacts — grubun blob gerçekleri (oracle.GroupStatsCache).
type OracleExplainFacts struct {
	SourceID, SourceName string
	Lag                  time.Duration // kapanmış dakika gecikmesi (GroupLag)
	LastHour, PrevHour   uint64
	Channels, Services   []OracleNamedCount // azalan sıralı
	BlobOK               bool               // blob okundu (false: saatlik sayılar bilinmiyor)
}

// OracleFactsFn — main.go enjekte eder (oracle.GroupStatsCache.ExplainFacts).
// ok=false: Oracle grubu değil ya da kaynağı yok (silinmiş).
type OracleFactsFn func(g chstore.ExceptionGroup) (OracleExplainFacts, bool)

var oracleFactsFn atomic.Pointer[OracleFactsFn]

// SetOracleExplainFacts — boot'ta bir kez (her rolde: api explain + worker özeti).
func SetOracleExplainFacts(f OracleFactsFn) { oracleFactsFn.Store(&f) }

// OracleExplainFactsFor — enjekte edilmemişse / kaynak yoksa ok=false.
func OracleExplainFactsFor(g chstore.ExceptionGroup) (OracleExplainFacts, bool) {
	if p := oracleFactsFn.Load(); p != nil && *p != nil {
		return (*p)(g)
	}
	return OracleExplainFacts{}, false
}

// OracleContextReader — kurucunun okuma yüzü (*chstore.Store; testte sahte).
type OracleContextReader interface {
	OracleGroupSource(ctx context.Context, g *chstore.ExceptionGroup) (string, error)
	OracleErrorsByOpCode(ctx context.Context, sourceID, op, code string, from, to time.Time, limit int) ([]chstore.OracleErrorRow, error)
	TraceFactsByIDs(ctx context.Context, ids []string, from, to time.Time) (map[string]chstore.TraceFact, error)
}

// OracleTraceRef — çözülen trace: servis (hata veren en derin span) + exception tipi.
type OracleTraceRef struct {
	TraceID string `json:"traceId"`
	Service string `json:"service,omitempty"`
	ExType  string `json:"exType,omitempty"`
}

// OracleRowSample — en yeni satırın eşlenen alanları (prompt'a giden kısa örnek).
type OracleRowSample struct {
	Time         string            `json:"time"`
	Severity     string            `json:"severity,omitempty"`
	Message      string            `json:"message,omitempty"`
	ExternalCode string            `json:"externalCode,omitempty"`
	ErrorType    string            `json:"type,omitempty"`
	Task         string            `json:"task,omitempty"`
	Channel      string            `json:"channel,omitempty"`
	Host         string            `json:"host,omitempty"`
	Weight       int               `json:"adet,omitempty"` // ön-toplanmış satır (>1)
	Columns      map[string]string `json:"columns,omitempty"`
}

// OracleExplainContext — Oracle explain girdisinin YAPISAL hâli (prompt bu
// yapıdan basılır; tablo-testli).
type OracleExplainContext struct {
	SourceName, Code, Operation string
	SourceKnown                 bool // kaynak çözüldü
	HourKnown                   bool // saatlik sayılar blob'dan okundu
	LastHour, PrevHour          uint64
	Flow                        string // patlama / yeni akış / sürekli akış / sönüyor / sessiz / bilinmiyor
	LagMin                      int
	Channels                    []OracleNamedCount // ilk 3, % ile
	ChannelCount                int
	Services                    []OracleNamedCount // ilk 3
	ServiceCount                int
	Hosts, Instances            []OracleNamedCount // en yeni satırlardan, ilk 3, % ile
	Traces                      []OracleTraceRef   // ≤5
	Samples                     []OracleRowSample  // ≤3
	RowsRead                    int
	RowsCapped                  bool
}

// SummaryLine — "Oracle · <kaynak> · <kod> · <operasyon>" (panel başlığı ve log).
func (c OracleExplainContext) SummaryLine() string {
	parts := []string{"Oracle"}
	for _, p := range []string{c.SourceName, c.Code, c.Operation} {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " · ")
}

// oracleFlow — SAF: son 1 sa vs önceki 1 sa sınıfı (patlama katı öncelik
// kuralıyla aynı).
func oracleFlow(known bool, last, prev uint64) string {
	switch {
	case !known:
		return "bilinmiyor"
	case last == 0 && prev == 0:
		return "sessiz"
	case prev == 0:
		return "yeni akış"
	case last >= oracleExplainBurst*prev:
		return "patlama"
	case last*oracleExplainBurst <= prev:
		return "sönüyor"
	}
	return "sürekli akış"
}

// withPct — SAF: ilk n girdi, toplam içindeki % ile (toplam TÜM girdilerden).
func withPct(in []OracleNamedCount, n int) []OracleNamedCount {
	var total uint64
	for _, e := range in {
		total += e.Count
	}
	out := make([]OracleNamedCount, 0, n)
	for i, e := range in {
		if i >= n {
			break
		}
		if total > 0 {
			e.Pct = int((e.Count*100 + total/2) / total)
		}
		out = append(out, e)
	}
	return out
}

// countRows — SAF: satır alanının ağırlıklı dağılımı (azalan; eşitlikte ad).
func countRows(rows []chstore.OracleErrorRow, field func(chstore.OracleErrorRow) string) []OracleNamedCount {
	m := map[string]uint64{}
	for _, r := range rows {
		if v := strings.TrimSpace(field(r)); v != "" {
			m[v] += uint64(r.EffectiveWeight())
		}
	}
	out := make([]OracleNamedCount, 0, len(m))
	for k, v := range m {
		out = append(out, OracleNamedCount{Name: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// oracleTraceIDs — SAF: satırlardaki ayrık trace id'ler, en yeni önce, ≤ n.
func oracleTraceIDs(rows []chstore.OracleErrorRow, n int) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		id := strings.TrimSpace(r.TraceID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		if len(out) >= n {
			break
		}
	}
	return out
}

// oracleRowSample — SAF: satır → kısa örnek. Eşlenmeyen kolonlar (attr) verbatim,
// ilk oracleExplainAttrMax anahtar (sıralı), değer 160 rune; ağırlık attribute'u
// ayrı alan (Adet).
func oracleRowSample(r chstore.OracleErrorRow, loc *time.Location) OracleRowSample {
	s := OracleRowSample{
		Time: r.Time.In(loc).Format(time.RFC3339), Severity: strings.TrimSpace(r.SeverityText),
		Message: truncRunes(strings.TrimSpace(r.Body), 400), ExternalCode: strings.TrimSpace(r.ExternalCode),
		ErrorType: strings.TrimSpace(r.ErrorType), Task: strings.TrimSpace(r.TaskCode),
		Channel: strings.TrimSpace(r.ChannelCode), Host: strings.TrimSpace(r.HostName),
	}
	if w := r.EffectiveWeight(); w > 1 {
		s.Weight = w
	}
	keys := make([]int, 0, len(r.AttrKeys))
	for i, k := range r.AttrKeys {
		if k == chstore.OracleWeightAttr || i >= len(r.AttrValues) || strings.TrimSpace(r.AttrValues[i]) == "" {
			continue
		}
		keys = append(keys, i)
	}
	// Hata anlamı taşıyan kolonlar (SONUC, mesaj, hata/sonuç kodu) tavanın
	// İÇİNDE kalsın: önce onlar, sonra ada göre.
	sort.Slice(keys, func(a, b int) bool {
		ka, kb := r.AttrKeys[keys[a]], r.AttrKeys[keys[b]]
		if pa, pb := oracleMeaningCol(ka), oracleMeaningCol(kb); pa != pb {
			return pa
		}
		return ka < kb
	})
	for n, i := range keys {
		if n >= oracleExplainAttrMax {
			break
		}
		if s.Columns == nil {
			s.Columns = map[string]string{}
		}
		s.Columns[r.AttrKeys[i]] = truncRunes(strings.TrimSpace(r.AttrValues[i]), 160)
	}
	return s
}

// oracleMeaningHints — eşlenmeyen kolon adında hatanın anlamını taşıyan parçalar.
var oracleMeaningHints = []string{"SONUC", "RESULT", "MESAJ", "MESSAGE", "MSG", "ACIKLAMA", "DESC", "HATA", "ERR", "KOD", "CODE"}

// oracleMeaningCol — SAF: kolon adı (büyük/küçük harf duyarsız) bir ipucu içeriyor mu.
func oracleMeaningCol(key string) bool {
	k := strings.ToUpper(key)
	for _, h := range oracleMeaningHints {
		if strings.Contains(k, h) {
			return true
		}
	}
	return false
}

// assembleOracleExplainContext — SAF çekirdek: blob gerçekleri + en yeni
// satırlar + trace çözümü → yapısal bağlam. Eksik parça (blob yok, satır yok,
// trace çözümü düştü) yalnız kendi alanını boş bırakır.
func assembleOracleExplainContext(g *chstore.ExceptionGroup, facts OracleExplainFacts, factsOK bool,
	rows []chstore.OracleErrorRow, rowLimit int, traceFacts map[string]chstore.TraceFact, loc *time.Location) OracleExplainContext {
	if loc == nil {
		loc = time.UTC
	}
	c := OracleExplainContext{
		Code: strings.TrimSpace(g.Type), Operation: strings.TrimSpace(g.Message),
		SourceKnown: factsOK, RowsRead: len(rows), RowsCapped: rowLimit > 0 && len(rows) >= rowLimit,
	}
	c.SourceName = strings.TrimSpace(facts.SourceName)
	if c.SourceName == "" {
		c.SourceName = strings.TrimPrefix(g.Service, chstore.OracleGroupServicePrefix)
		if c.SourceName == g.Service { // gerçek servis adı — kaynak adı değil
			c.SourceName = ""
		}
	}
	if factsOK {
		c.HourKnown = facts.BlobOK
		c.LastHour, c.PrevHour = facts.LastHour, facts.PrevHour
		c.LagMin = int(facts.Lag / time.Minute)
		c.Channels, c.ChannelCount = withPct(facts.Channels, oracleExplainTopN), len(facts.Channels)
		c.ServiceCount = len(facts.Services)
		for i, e := range facts.Services {
			if i >= oracleExplainTopN {
				break
			}
			c.Services = append(c.Services, OracleNamedCount{Name: e.Name, Count: e.Count})
		}
	}
	c.Flow = oracleFlow(c.HourKnown, c.LastHour, c.PrevHour)
	c.Hosts = withPct(countRows(rows, func(r chstore.OracleErrorRow) string { return r.HostName }), oracleExplainTopN)
	c.Instances = withPct(countRows(rows, func(r chstore.OracleErrorRow) string { return r.InstanceID }), oracleExplainTopN)
	for _, id := range oracleTraceIDs(rows, oracleExplainTraceMax) {
		ref := OracleTraceRef{TraceID: id}
		if f, ok := traceFacts[id]; ok {
			ref.Service, ref.ExType = f.Service, f.ExType
		}
		c.Traces = append(c.Traces, ref)
	}
	for i, r := range rows {
		if i >= oracleExplainSamples {
			break
		}
		c.Samples = append(c.Samples, oracleRowSample(r, loc))
	}
	return c
}

// fmtNamed — SAF: "MOB %60 (5400) · WEB %30 (2700)"; pct 0 → yalnız sayı.
func fmtNamed(in []OracleNamedCount) string {
	parts := make([]string, 0, len(in))
	for _, e := range in {
		if e.Pct > 0 {
			parts = append(parts, fmt.Sprintf("%s %%%d (%d)", e.Name, e.Pct, e.Count))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s (%d)", e.Name, e.Count))
	}
	return strings.Join(parts, " · ")
}

// renderOracleExplainPrompt — SAF montaj: meta JSON + blok satırları + kapanış.
// Boş blok "yok" yazılır (model "ölçülmedi"yi "sıfır" sanmasın — bilinmeyen ile
// boş ayrı kelimeler).
func renderOracleExplainPrompt(g *chstore.ExceptionGroup, c OracleExplainContext, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	meta := map[string]any{
		"source": c.SourceName, "errorCode": c.Code, "operation": c.Operation,
		"state": g.State, "occurrences": g.Occurrences,
		"firstSeen": time.Unix(0, g.FirstSeen).In(loc).Format(time.RFC3339),
		"lastSeen":  time.Unix(0, g.LastSeen).In(loc).Format(time.RFC3339),
		"timezone":  loc.String(),
	}
	if !c.SourceKnown {
		meta["sourceResolved"] = false
	}
	mp, _ := json.Marshal(meta)
	var sb strings.Builder
	fmt.Fprintf(&sb, "Oracle HATA GRUBU (span exception DEĞİL — Oracle hata tablosu satırları; stacktrace yok):\n```json\n%s\n```",
		promptfmt.FenceSafe(string(mp)))

	sb.WriteString("\n\nSaatlik akış (yalnız KAPANMIŞ dakikalar")
	if c.LagMin > 0 {
		fmt.Fprintf(&sb, "; son ~%d dk henüz sayılmadı", c.LagMin)
	}
	sb.WriteString("): ")
	if c.HourKnown {
		fmt.Fprintf(&sb, "son 1 sa=%d · önceki 1 sa=%d", c.LastHour, c.PrevHour)
		if c.PrevHour > 0 {
			fmt.Fprintf(&sb, " · oran %.1f×", float64(c.LastHour)/float64(c.PrevHour))
		}
		fmt.Fprintf(&sb, " → %s", c.Flow)
	} else {
		sb.WriteString("bilinmiyor (kırılım blob'u okunamadı)")
	}

	sb.WriteString("\nKanal kırılımı: ")
	if len(c.Channels) > 0 {
		sb.WriteString(fmtNamed(c.Channels))
		if c.ChannelCount > len(c.Channels) {
			fmt.Fprintf(&sb, " · toplam %d kanal", c.ChannelCount)
		}
	} else {
		sb.WriteString("yok")
	}
	sb.WriteString("\nEtkilenen servisler (trace → servis çözümü, oy): ")
	if len(c.Services) > 0 {
		sb.WriteString(fmtNamed(c.Services))
		if c.ServiceCount > len(c.Services) {
			fmt.Fprintf(&sb, " · toplam %d servis", c.ServiceCount)
		}
	} else {
		sb.WriteString("çözülemedi")
	}
	if len(c.Hosts) > 0 || len(c.Instances) > 0 {
		fmt.Fprintf(&sb, "\nHost / instance (en yeni %d satır", c.RowsRead)
		if c.RowsCapped {
			sb.WriteString(", tavan — yalnız en yeniler")
		}
		sb.WriteString("): ")
		if len(c.Hosts) > 0 {
			sb.WriteString("host " + fmtNamed(c.Hosts))
		}
		if len(c.Instances) > 0 {
			if len(c.Hosts) > 0 {
				sb.WriteString(" ; ")
			}
			sb.WriteString("instance " + fmtNamed(c.Instances))
		}
	}
	if len(c.Traces) > 0 {
		if tp, err := json.Marshal(c.Traces); err == nil {
			fmt.Fprintf(&sb, "\n\nÇözülen trace'ler (en yeni satırlardan, ≤%d):\n```json\n%s\n```", oracleExplainTraceMax, promptfmt.FenceSafe(string(tp)))
		}
	}
	if len(c.Samples) > 0 {
		if sp, err := json.Marshal(c.Samples); err == nil {
			fmt.Fprintf(&sb, "\n\nEn yeni %d satır (eşlenen alanlar; columns = eşlenmeyen kolonlar verbatim):\n```json\n%s\n```", len(c.Samples), promptfmt.FenceSafe(string(sp)))
		}
	} else {
		sb.WriteString("\n\nÖrnek satır: yok (pencerede satır okunamadı)")
	}
	sb.WriteString("\n\nKodun anlamını, nerede yoğunlaştığını, patlama mı sürekli akış mı olduğunu ve önce DB tarafının mı çağıran tarafın mı kontrol edilmesi gerektiğini YALNIZ bu kanıta dayanarak söyle.")
	return sb.String()
}

// BuildOracleExceptionExplainInput — `ora:` grubunun explain girdisi. Okumalar
// sınırlı ve best-effort: kaynak (blob gerçeklerinden, yoksa parmak izinden
// geri çözüm), en yeni ≤oracleExplainRowLimit satır (son görülmeden 1 sa geri),
// ≤5 trace id'nin servisi. EvTraces = çözülen trace'ler (UI örnek satırlarını
// kutular). Stack / kod alanları BOŞ: "Kodu da incele" bu grupta koşmaz.
func BuildOracleExceptionExplainInput(ctx context.Context, rd OracleContextReader, g *chstore.ExceptionGroup, loc *time.Location) ExceptionExplainInput {
	if loc == nil {
		loc = time.UTC
	}
	facts, factsOK := OracleExplainFactsFor(*g)
	src := facts.SourceID
	if src == "" && rd != nil {
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		src, _ = rd.OracleGroupSource(sctx, g)
		cancel()
	}
	var rows []chstore.OracleErrorRow
	from, to := oracleExplainWindow(g.FirstSeen, g.LastSeen)
	if src != "" && rd != nil {
		rctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		rows, _ = rd.OracleErrorsByOpCode(rctx, src, g.Message, g.Type, from, to, oracleExplainRowLimit)
		cancel()
	}
	var traceFacts map[string]chstore.TraceFact
	if ids := oracleTraceIDs(rows, oracleExplainTraceMax); len(ids) > 0 && rd != nil {
		tctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		traceFacts, _ = rd.TraceFactsByIDs(tctx, ids, from.Add(-5*time.Minute), to.Add(5*time.Minute))
		cancel()
	}
	oc := assembleOracleExplainContext(g, facts, factsOK, rows, oracleExplainRowLimit, traceFacts, loc)
	ev := make([]string, 0, len(oc.Traces))
	for _, t := range oc.Traces {
		ev = append(ev, t.TraceID)
	}
	in := ExceptionExplainInput{
		User:      renderOracleExplainPrompt(g, oc, loc),
		EvTraces:  ev,
		ErrorText: truncRunes(g.Type+": "+g.Message, 2000),
		Oracle:    &oc,
	}
	if len(ev) > 0 {
		in.TraceID = ev[0]
	}
	return in
}

// oracleExplainWindow — SAF: [son görülme − 1 sa (ilk görülme daha yakınsa o),
// son görülme + 2 dk) — örnek ucuyla aynı ileri pay.
func oracleExplainWindow(firstNs, lastNs int64) (time.Time, time.Time) {
	last := time.Unix(0, lastNs).UTC()
	from := last.Add(-oracleExplainLookback)
	if first := time.Unix(0, firstNs).UTC(); first.After(from) {
		from = first
	}
	return from, last.Add(2 * time.Minute)
}
