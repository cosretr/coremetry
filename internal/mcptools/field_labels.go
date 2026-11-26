package mcptools

// field_labels.go — v0.10.2480 (G, operatör-bildirimi): CoSRE cevabı ham
// alan adlarını düzyazıya taşıyordu ("peakRatio: 9.11", "fingerprint: …").
// Oran / kimlik alanı taşıyan araç çıktıları küçük bir "labels" sözlüğü
// taşır: model değeri insan diliyle anar. EKLEMELİ (yeni anahtar); mevcut
// alanlar ve MCP sözleşmesi aynen.

// fieldLabelsTR — alan adı → cevapta kullanılacak Türkçe etiket.
var fieldLabelsTR = map[string]string{
	"peakRatio":    "tepe oranı — tabanın kaç katı (ör. \"tabanın 9 katı\")",
	"currentRatio": "şu anki oran — tabanın kaç katı",
	"fingerprint":  "hata grubu kimliği (cevapta kimlik yerine hata tipini / mesajını an)",
}

// withFieldLabels — out'a yalnız verilen alanların etiketleri.
func withFieldLabels(out map[string]any, fields ...string) map[string]any {
	l := make(map[string]string, len(fields))
	for _, f := range fields {
		if v, ok := fieldLabelsTR[f]; ok {
			l[f] = v
		}
	}
	if len(l) > 0 {
		out["labels"] = l
	}
	return out
}
