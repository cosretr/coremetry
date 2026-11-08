package logstore

import (
	"encoding/json"
	"strings"
)

// trace_records.go — v0.10.1988 (operatör: "API gateway servislerinde mesaj alanı yerine traceRecords
// altında geliyor ve çok değerli bilgiler var; trace logu CoSRE'ye sorulduğunda bunu dikkate alsın").
//
// Bazı kayıtlar (gateway / BFF iz günlükleri) gövdeyi `message`'a değil, bir `traceRecords` DİZİSİNE
// yazar: her öğe {message, metadata.Type, severity, source, timestamp}. Mesaj alanı boşken bu dizi tek
// satırlık okunur bir gövdeye çevrilir ("Type: message", öğe başına satır). Böylece tablo, desenler,
// arama ve CoSRE'nin trace-log kanıtı aynı metni görür; ham alan ayrıca öznitelik olarak kalır.
// Servis adına bağlı DEĞİL: alan varsa her kayıtta çalışır.

// traceRecordsMaxItem / Total — tek öğe ve toplam gövde tavanı (yanıt gövdeleri çok büyük olabilir).
const (
	traceRecordsMaxItem  = 2000
	traceRecordsMaxTotal = 16000
)

// traceRecordsBody — SAF: kaynakta traceRecords (dizi ya da JSON dizgesi) varsa okunur gövde; yoksa "".
func traceRecordsBody(src map[string]any) string {
	raw, ok := src["traceRecords"]
	if !ok {
		return ""
	}
	var items []any
	switch v := raw.(type) {
	case []any:
		items = v
	case string:
		if err := json.Unmarshal([]byte(v), &items); err != nil {
			return ""
		}
	default:
		return ""
	}
	var b strings.Builder
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		msg, _ := m["message"].(string)
		msg = strings.TrimSpace(msg)
		if msg == "" {
			continue
		}
		if len(msg) > traceRecordsMaxItem {
			msg = msg[:traceRecordsMaxItem] + "…"
		}
		typ := ""
		if md, ok := m["metadata"].(map[string]any); ok {
			typ, _ = md["Type"].(string)
		}
		line := msg
		if typ != "" {
			line = typ + ": " + msg
		}
		if b.Len()+len(line)+1 > traceRecordsMaxTotal {
			b.WriteString("\n…")
			break
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	return b.String()
}
