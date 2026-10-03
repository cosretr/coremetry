package chstore

// problem_priority_inbox.go — v0.10.1072: inbox görünüm kuralının DAR
// istisna listesi (problem_priority blobu, `inboxKeepSourcePriority`).
//
// Operatör (prod, 2026-10-02 akşamı): "Dün akşam CRM database'inde sorun
// oldu ama problemlerde P1 gelmedi." Kritik bir hata oranı anomalisi
// kaynağında P1'di (computePriority: critical + 14.9× ≥ 2×) ama inbox
// v0.9.487'nin "exception dışı türler HEP P3" görünüm kuralıyla P3
// gösteriyordu. Kural genel olarak DOĞRU (trace_op / log / davranış /
// SLO gürültüsü P1 görünümüne karışmasın) — yalnız birkaç kaynak sınıfı
// "kendi P1'i gerçek P1" kanıtlı. Bu dosya o sınıfları tanımlar.
//
// KALIP SÖZDİZİMİ: kaynak kimliğinin TAMAMINA uyan glob; `*` herhangi bir
// karakter dizisi (boş, ':' ve '/' dahil), başka meta karakter yok, büyük-
// küçük duyarlı (kural kimlikleri öyle). Önek için sona `*` koyulur
// ("builtin-*"). Kimlik: Problem satırında RuleID; incident satırında
// "incident:<severity>" (incident'in kural kimliği yok, sınıfı önem).

import (
	"fmt"
	"strings"
)

const (
	// inboxKeepMaxCount — liste tavanı; elle düzenlenmiş bir satır satır
	// başına eşleştirmeyi şişiremesin (her satır × her kalıp).
	inboxKeepMaxCount = 20
	// inboxKeepMaxLen — tek kalıbın tavanı (kural kimlikleri kısa).
	inboxKeepMaxLen = 128
)

// Varsayılan kalıplar — adlı sabitler, çünkü api tarafı gerekçe metnini
// hangi kalıbın eşleştiğine göre seçiyor (inboxKeepLabel).
const (
	InboxKeepErrorRateAnomaly = "anomaly:*:error_rate" // kritik hata oranı anomalisi
	InboxKeepBuiltin          = "builtin-*"            // yerleşik kurallar (v0.10.1069'dan beri varsayılan kapalı)
	InboxKeepDBHealth         = "db-health:*"          // DB sağlık kuralları (ayrı dilimde ekleniyor)
	InboxKeepCriticalIncident = "incident:critical"    // kritik incident
	// v0.10.1083 (operatör: "Oracle hataları da problemse hâlâ düşmüyor") —
	// dış kaynak (Oracle hata tablosu) hata SAYISI serisi ve onun kümesi.
	InboxKeepExtErrorCount = "anomaly:ext:*:ext:error_count" // dış kaynak hata serisi
	InboxKeepExtCluster    = "anomaly-cluster:ext:*"         // dış kaynak hata kümesi
	// InboxKeepExtCap — tavan ÖZET satırı (critical, Value = tavana takılan seri
	// / Threshold = tavan): üyeleri P1 görünürken özet P3'e çivilenmesin.
	InboxKeepExtCap = "anomaly:ext-cap:*"
)

// DefaultInboxKeepSourcePriority — alan YOKKEN geçerli liste. BİLİNÇLİ
// olarak DAR: trace_op / trace_op_latency / log_* / behavior_change /
// `anomaly-auto:*` (terfi etmiş anomali) / SLO burn (`slo:*`) / yavaş ifade /
// self-health burada YOK — v0.9.487'nin susturduğu gürültü tam o sınıflar.
//
// v0.10.1083 — dış kaynak hata serisi + kümesi + tavan özeti eklendi
// (operatör onaylı; kaynak sağlığı `anomaly:ext-down:` bilinçli DIŞARIDA).
// Kayıtlı ESKİ varsayılan liste tek seferlik göçle yeniye taşınır
// (problem_priority_inbox_migrate.go); özelleştirilmiş liste dokunulmaz.
func DefaultInboxKeepSourcePriority() []string {
	return []string{InboxKeepErrorRateAnomaly, InboxKeepBuiltin, InboxKeepDBHealth, InboxKeepCriticalIncident,
		InboxKeepExtErrorCount, InboxKeepExtCluster, InboxKeepExtCap}
}

// legacyInboxKeepSourcePriority — v0.10.1072'nin varsayılanı (göçün "eski
// varsayılan" tanımı; DEĞİŞTİRME — kayıtlı listeyi bununla kıyaslıyoruz).
func legacyInboxKeepSourcePriority() []string {
	return []string{InboxKeepErrorRateAnomaly, InboxKeepBuiltin, InboxKeepDBHealth, InboxKeepCriticalIncident}
}

func inboxKeepPtr(l []string) *[]string { return &l }

// inboxKeepMinLiteral — kalıpta `*` dışında en az bu kadar karakter.
const inboxKeepMinLiteral = 3

// inboxKeepPatternErr — TEK kalıp kuralı (kırpılmış, boş olmayan kalıp
// için); PUT doğrulaması ve normalize AYNI yüklemi kullanır. Geniş kalıp
// ("*", "*:*", "*-*") bütün kimliklere uyar = v0.9.487'yi sessizce tamamen
// geri almak; o karar bir liste girdisiyle değil sürümle verilir. Bu yüzden:
// ilk `*`'tan ÖNCE literal önek ŞART ve en az 3 literal karakter.
func inboxKeepPatternErr(p string) error {
	if len(p) > inboxKeepMaxLen {
		return fmt.Errorf("kalıp en çok %d karakter (%d)", inboxKeepMaxLen, len(p))
	}
	if strings.HasPrefix(p, "*") {
		return fmt.Errorf("%q `*` ile başlıyor — ilk `*`'tan önce literal önek gerekir (ör. \"anomaly:*\")", p)
	}
	if len(p)-strings.Count(p, "*") < inboxKeepMinLiteral {
		return fmt.Errorf("%q çok geniş — `*` dışında en az %d karakter gerekir", p, inboxKeepMinLiteral)
	}
	return nil
}

// ValidateInboxKeepSourcePriority — PUT doğrulaması: boş girdi ve tekrar
// sessizce düşer (yazım kolaylığı); kurala uymayan kalıp ve normalize
// edilmiş (tekilleştirilmiş) listede 20'yi aşan sayı HATA — operatör
// yazdığının kaydedilmediğini görsün, normalize sessizce düşürmesin.
func ValidateInboxKeepSourcePriority(in []string) error {
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		p := strings.TrimSpace(raw)
		if p == "" || seen[p] {
			continue
		}
		if err := inboxKeepPatternErr(p); err != nil {
			return fmt.Errorf("inboxKeepSourcePriority: %w", err)
		}
		seen[p] = true
	}
	if len(seen) > inboxKeepMaxCount {
		return fmt.Errorf("inboxKeepSourcePriority: en çok %d kalıp (%d farklı kalıp geldi)", inboxKeepMaxCount, len(seen))
	}
	return nil
}

// NormalizeInboxKeepSourcePriority — kırp, boşu at, kurala uymayanı at
// (inboxKeepPatternErr: literal öneksiz ya da 3 literalden az — "*", "*:*" —
// ya da aşırı uzun; elle düzenlenmiş blob da güvenli şekle düşsün), tekrarı
// at (ilk görülen kalır), 20'de kes.
//
// ASLA nil DÖNMEZ: boş sonuç `[]string{}` — "boş = istisna yok" sözleşmesinin
// taşıyıcısı (nil işaretçi JSON'da null → geri okumada varsayılan olurdu;
// BatchServicePatterns emsali).
func NormalizeInboxKeepSourcePriority(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		p := strings.TrimSpace(raw)
		if p == "" || seen[p] || inboxKeepPatternErr(p) != nil {
			continue
		}
		seen[p] = true
		out = append(out, p)
		if len(out) == inboxKeepMaxCount {
			break
		}
	}
	return out
}

// InboxKeepSourcePriorityList — ETKİN liste: nil işaretçi → varsayılan;
// nil olmayan → normalize içerik (BOŞ olabilir = istisna kapalı).
func (c ProblemPriorityConfig) InboxKeepSourcePriorityList() []string {
	if c.InboxKeepSourcePriority == nil {
		return DefaultInboxKeepSourcePriority()
	}
	return NormalizeInboxKeepSourcePriority(*c.InboxKeepSourcePriority)
}

// MatchInboxKeepSourcePriority — kimliğe uyan İLK kalıbı döner (gerekçe
// metni onu söyler). Boş kimlik hiçbir kalıba uymaz. SAF + tablo-testli.
func MatchInboxKeepSourcePriority(patterns []string, id string) (string, bool) {
	if id == "" {
		return "", false
	}
	for _, p := range patterns {
		if globMatch(p, id) {
			return p, true
		}
	}
	return "", false
}

// globMatch — yalnız `*` tanıyan, iki uçtan çapalı eşleştirici. path.Match
// DEĞİL, bilinçli: onun `*`'ı '/' geçemez ve dış kaynak öznesi
// ("anomaly:ext:<kaynak>/<v1>/…:<metrik>") tam olarak '/' taşıyor.
// Açgözlü olmayan geri izleme: son `*`'tan devam, O(len(p)·len(s)) en kötü.
func globMatch(p, s string) bool {
	pi, si := 0, 0
	star, mark := -1, 0
	for si < len(s) {
		switch {
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, si
			pi++
		case pi < len(p) && p[pi] == s[si]:
			pi++
			si++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}
