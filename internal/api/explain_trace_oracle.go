package api

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/promptfmt"
)

// explain_trace_oracle.go — v0.10.921 (Kademe A, operatör onayı 2026-09-25):
// "Explain trace Oracle data source'una gidip detaylara erişse". Canlı Oracle
// sorgusu DEĞİL: poller'ın ClickHouse'a zaten kopyaladığı hata satırları
// (oracle_error_log) trace kimliğiyle okunur ve Explain'e kanıt olarak girer.
// Bu satırlar Trace › Logs sekmesinde her role zaten açık — yeni erişim yok.
// Request/response ise operatör kararıyla şimdilik kapsam dışı (Kademe B).

const (
	// oracleExplainMaxRows — modele giden satır tavanı (loglar 15; Oracle
	// satırları daha az ve daha yoğun).
	oracleExplainMaxRows = 10
	// oracleExplainAttrMax — satır başına en çok bu kadar ek kolon.
	oracleExplainAttrMax = 6
	// oracleExplainValueRunes — gövde ve ek kolon değeri başına rün tavanı.
	oracleExplainValueRunes = 200
	// oracleExplainFetchTimeout — ClickHouse okuması; loglarla PARALEL.
	oracleExplainFetchTimeout = 4 * time.Second
)

// oracleLite — modele giden kompakt satır. Boş alanlar düşer (omitempty):
// küçük modelin bağlamı kolon adlarıyla dolmasın.
type oracleLite struct {
	Time      string            `json:"time"`
	Source    string            `json:"source,omitempty"`
	Operation string            `json:"operation,omitempty"`
	ErrorCode string            `json:"errorCode,omitempty"`
	External  string            `json:"externalCode,omitempty"`
	ErrorType string            `json:"errorType,omitempty"`
	Channel   string            `json:"channel,omitempty"`
	Task      string            `json:"task,omitempty"`
	Host      string            `json:"host,omitempty"`
	Instance  string            `json:"instance,omitempty"`
	RequestID string            `json:"requestId,omitempty"`
	Customer  string            `json:"customerId,omitempty"`
	Teller    string            `json:"tellerId,omitempty"`
	Message   string            `json:"message,omitempty"`
	Count     int               `json:"count,omitempty"` // >1: ön-toplanmış grup satırı
	Extra     map[string]string `json:"extra,omitempty"`
}

// oracleExplainBlock — SAF: trace'in Oracle hata satırlarından prompt bloğu.
// Satır yoksa "" döner ve prompt bayt-bayt eskisi gibi kalır. Sıralama
// zamana göre (deterministik: aynı girdi aynı önbellek anahtarı).
func oracleExplainBlock(rows []chstore.OracleErrorRow, sourceNames map[string]string) (string, int) {
	if len(rows) == 0 {
		return "", 0
	}
	sorted := append([]chstore.OracleErrorRow(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].Time.Equal(sorted[j].Time) {
			return sorted[i].Time.Before(sorted[j].Time)
		}
		return sorted[i].RowID < sorted[j].RowID
	})
	total := len(sorted)
	if len(sorted) > oracleExplainMaxRows {
		sorted = sorted[:oracleExplainMaxRows]
	}
	grouped := false
	lite := make([]oracleLite, 0, len(sorted))
	for _, r := range sorted {
		e := oracleLite{
			Time:      r.Time.UTC().Format("2006-01-02 15:04:05Z"),
			Source:    sourceNames[r.SourceID],
			Operation: r.OperationCode,
			ErrorCode: r.ErrorCode,
			External:  r.ExternalCode,
			ErrorType: r.ErrorType,
			Channel:   r.ChannelCode,
			Task:      r.TaskCode,
			Host:      r.HostName,
			Instance:  r.InstanceID,
			RequestID: r.RequestID,
			Customer:  r.CustomerID,
			Teller:    r.TellerID,
			Message:   truncRunesN(strings.TrimSpace(r.Body), oracleExplainValueRunes),
		}
		if w := r.EffectiveWeight(); w > 1 {
			e.Count = w
			grouped = true
		}
		e.Extra = oracleExtraAttrs(r)
		lite = append(lite, e)
	}
	payload, err := json.Marshal(lite)
	if err != nil {
		return "", 0
	}
	var b strings.Builder
	b.WriteString("\n\nBu trace'in ORACLE hata satırları (kurumun işlem/hata tablosu; Coremetry'nin kopyası), zamana göre:\n```json\n")
	b.WriteString(promptfmt.FenceSafe(string(payload)))
	b.WriteString("\n```")
	if total > len(lite) {
		fmt.Fprintf(&b, "\n(Toplam %d satırın ilk %d tanesi gösterildi.)", total, len(lite))
	}
	if grouped {
		b.WriteString("\n`count` alanı olan satır TOPLU bir satırdır: aynı dakika/operasyon/hata kodu için N adet; tek bir olayın detayı değildir, sayıyı tek olay gibi anlatma.")
	}
	b.WriteString("\nOracle satırındaki hata kodu ve operasyonu span'larla ve loglarla eşleştir; kodu aynen aktar.")
	return b.String(), len(lite)
}

// oracleExtraAttrs — eşlemede tüketilmeyen kolonlar (attr_keys/values), ad
// sırasıyla, tavanlı. Ağırlık attribute'u zaten `count` olarak gidiyor.
func oracleExtraAttrs(r chstore.OracleErrorRow) map[string]string {
	if len(r.AttrKeys) == 0 {
		return nil
	}
	type kv struct{ k, v string }
	var kvs []kv
	for i, k := range r.AttrKeys {
		if i >= len(r.AttrValues) || k == chstore.OracleWeightAttr {
			continue
		}
		v := strings.TrimSpace(r.AttrValues[i])
		if v == "" {
			continue
		}
		kvs = append(kvs, kv{k, truncRunesN(v, oracleExplainValueRunes)})
	}
	if len(kvs) == 0 {
		return nil
	}
	sort.Slice(kvs, func(i, j int) bool { return kvs[i].k < kvs[j].k })
	if len(kvs) > oracleExplainAttrMax {
		kvs = kvs[:oracleExplainAttrMax]
	}
	out := make(map[string]string, len(kvs))
	for _, e := range kvs {
		out[e.k] = e.v
	}
	return out
}

// traceExplainExtra — Explain trace cevabına eşlik eden meta (kanıt span'leri,
// kod kaynağı, Oracle satır sayısı). api.go'dan taşındı (api.go büyümesin).
func traceExplainExtra(in traceExplainInput, cc devops.CodeContext, includeCode bool) map[string]any {
	return map[string]any{
		"evidenceSpanIds": in.Evidence,
		"code":            codePayload(cc, includeCode),
		"oracleRows":      in.OracleRows,
	}
}
