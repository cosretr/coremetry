package chstore

// anomaly_sensitivity_batch.go — v0.10.1039: batch servislerde yük artışı
// anomali değil.
//
// Operatör (prod): "Bazı batch işlerde ani yük artışı olabilir, onları
// anomali gibi düşünme — özellikle `-batch` geçen servis isimlerinde."
//
// NE DEĞİŞİR: adında bu kalıplardan biri geçen servislerde YÜKÜN KENDİSİ
// (istek hızı, span hacmi) anomali sayılmaz; yükün yan etkisi olan hata
// SAYISI artışı ancak hata ORANI da artmışsa olay sayılır (SAYIM VE PAY —
// yalnız susturur, yeni olay açmaz). Hata oranı anomalileri bu servislerde
// de AYNEN açılır — batch işi patlarsa sinyal susmamalı. Gecikme: v0.10.1046
// ile hacim işin olağan çalışma hacminin ≥ 2 katıyken gelen gecikme artışı
// YENİ olay açmaz; olağan yükte gelen artış ve zaten aktif olaylar AYNEN
// sürer (internal/anomaly/batch_latency.go). Kapsam listesi
// docs/DECISIONS.md'de (v0.10.1039, v0.10.1046).
//
// TEK YÜKLEM: her karar noktası (davranış motoru, metrik dedektörü,
// trace_op, self-volume-spike) IsBatchService'i çağırır; SQL tarafı
// (trace_op HAVING'i) koşulunu BatchServiceSQL ile AYNI normalize listeden
// üretir. İkinci bir eşleştirici yazmak yasak — Go ile SQL'in farklı servis
// kümesi demesi, bir tarafın susturduğunu diğerinin açması demek.

import (
	"strings"
	"unicode/utf8"
)

const (
	// batchPatternMinLen — bundan kısa kalıp DÜŞER. Boş ya da 1-2 karakterlik
	// bir kalıp ("-", "ba") filonun neredeyse her servis adında geçer ve
	// kuralı sessizce "hiçbir serviste yük anomalisi yok"a çevirirdi.
	batchPatternMinLen = 3
	// batchPatternMaxCount — liste tavanı. SQL koşulu kalıp başına bir terim
	// taşıyor; elle düzenlenmiş bir satır HAVING'i şişiremesin.
	batchPatternMaxCount = 10
)

// DefaultBatchServicePatterns — alan YOKKEN geçerli liste: operatörün
// adını verdiği kalıp. Bilinçli bir VARSAYILAN DAVRANIŞ değişikliği
// (v0.10.1039): yükseltmeden sonra `-batch` servisleri kendiliğinden
// kurala girer; kapatmanın yolu listeyi boş kaydetmek.
func DefaultBatchServicePatterns() []string { return []string{"-batch"} }

// asciiLower — yalnız A-Z → a-z. strings.ToLower DEĞİL, bilinçli: SQL
// ikizi positionCaseInsensitive yalnız ASCII harfleri katlıyor, ASCII
// dışı baytları birebir karşılaştırıyor. Go tarafı Unicode katlasaydı
// "ÖDEME-BATCH" gibi bir adda iki taraf farklı cevap verirdi (Türkçe
// İ/ı tam da bu sınıf). Kural: ASCII harfler büyük-küçük duyarsız, geri
// kalan her bayt birebir — iki tarafta da.
func asciiLower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// NormalizeBatchServicePatterns — kırp, ASCII küçült, 3 karakterden kısa
// olanı at, tekrarı at (ilk görülen kalır, sıra korunur), 10'da kes.
//
// ASLA nil DÖNMEZ: boş sonuç `[]string{}`. Bu, "boş = kural kapalı"
// sözleşmesinin taşıyıcısı — nil bir dilime işaretçi JSON'da `null`
// yazılır, `null` geri okununca işaretçi nil olur ve nil "alan yok =
// varsayılan" demektir: operatörün kapattığı kural bir kayıt-okuma
// turunda sessizce geri açılırdı (TestBatchPatternsJSONRoundTrip).
func NormalizeBatchServicePatterns(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		p := asciiLower(strings.TrimSpace(raw))
		if utf8.RuneCountInString(p) < batchPatternMinLen || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
		if len(out) == batchPatternMaxCount {
			break
		}
	}
	return out
}

// BatchServicePatternList — ETKİN liste. nil işaretçi (alan yazılmamış:
// bu sürümden eski her settings satırı) → varsayılan; nil olmayan işaretçi
// → normalize edilmiş içerik, BOŞ olabilir (= kural kapalı). Yayınlanan
// ayar zaten normalize; yeniden normalize etmek ≤10 kısa dizgide ucuz ve
// elle kurulmuş (testteki) bir struct'ı da güvenli kılıyor.
func (c AnomalySensitivityConfig) BatchServicePatternList() []string {
	if c.BatchServicePatterns == nil {
		return DefaultBatchServicePatterns()
	}
	return NormalizeBatchServicePatterns(*c.BatchServicePatterns)
}

// IsBatchService — TEK YÜKLEM (v0.10.1039). Servis adında etkin
// kalıplardan biri ALT DİZGİ olarak geçiyor mu (ASCII büyük-küçük
// duyarsız; konum serbest: baş, orta, son). Boş liste → daima false.
func (c AnomalySensitivityConfig) IsBatchService(name string) bool {
	return matchBatchPatterns(c.BatchServicePatternList(), name)
}

func matchBatchPatterns(patterns []string, name string) bool {
	if len(patterns) == 0 || name == "" {
		return false
	}
	n := asciiLower(name)
	for _, p := range patterns {
		if strings.Contains(n, p) {
			return true
		}
	}
	return false
}

// BatchServiceSQL — IsBatchService'in SQL ikizi, AYNI etkin listeden
// üretilir: kalıp başına `positionCaseInsensitive(col, ?) > 0`, OR ile
// bağlı, parantezli. Argümanlar normalize kalıpların kendisi (bind — SQL
// metnine kalıp gömülmez).
//
// Eşdeğerlik: positionCaseInsensitive ASCII harfleri katlayıp bayt
// düzeyinde alt dizgi arar = asciiLower + strings.Contains. Boş iğne
// CH'de 1 döner (her şeyle eşleşir) — normalize 3 karakterden kısayı
// attığı için boş iğne buraya ULAŞAMAZ. Eşdeğerlik fikstür tablosuyla
// testli (anomaly_sensitivity_batch_test.go; `clickhouse` ikilisi varsa
// canlı CH ile).
//
// Boş liste → ("", nil): kural kapalı. Çağıran boş koşulu "batch dalı
// YOK" diye okumalı (trace_op o durumda bugünkü SQL'i birebir üretir).
// col bir SABİT kolon adı olmalı (kullanıcı girdisi değil).
func (c AnomalySensitivityConfig) BatchServiceSQL(col string) (string, []any) {
	pats := c.BatchServicePatternList()
	if len(pats) == 0 {
		return "", nil
	}
	terms := make([]string, len(pats))
	args := make([]any, len(pats))
	for i, p := range pats {
		terms[i] = "positionCaseInsensitive(" + col + ", ?) > 0"
		args[i] = p
	}
	return "(" + strings.Join(terms, " OR ") + ")", args
}

// batchPatternsPtr — Normalize'ın somutlaştırma yardımcısı: daima nil
// olmayan işaretçi, daima nil olmayan dilim (yukarıdaki null tuzağı).
func batchPatternsPtr(p []string) *[]string {
	if p == nil {
		p = []string{}
	}
	return &p
}
