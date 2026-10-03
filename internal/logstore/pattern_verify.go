package logstore

// pattern_verify.go — v0.10.1080 (operatör, prod ES: "Oracle TNS error diyor
// ama loglarda öyle bir şey yok, hatalı desen buluyor.").
//
// ES dedektörü deseni token-OR query_string ile sayar ve regex'i hiç
// uygulamaz; standart çözümleyici tireyi atar, `message:"tns-"` çıplak `tns`
// terimine iner. O terimi taşıyan her doküman "Oracle TNS errors" sayılıyordu.
// CH sayımı regex'i zaten uygular (chPatternMatchSQL). Burada: ES'te tetiklemek
// üzere olan desenler için sınırlı bir örnek çekilir ve regex Go'da, CH'nin
// match() anlamıyla uygulanır (ES ikizi es_pattern_verify.go).

import (
	"context"
	"fmt"
	"log"
	"math"
	"regexp"
	"time"
)

// patternSampleSize — desen başına örneklenen gövde tavanı (shard başına
// terminate_after da bu). Oran tahmini için yeter, _source yalnız gövde.
const patternSampleSize = 50

// patternRegexCH — PatternSpec.Regex'i CH match() anlamıyla derler. CH
// match() re2 kullanır ve re2'nin varsayılanından farklı olarak `.` satır
// sonunu da eşler; Go regexp'i de re2 sözdizimi ama `.` varsayılanda satır
// sonunu eşlemez → (?s) öneki. Büyük/küçük harf duyarlılığı AYNEN: CH
// match() duyarlı, desen kendisi (?i) taşırsa (Auth failures) duyarsız.
func patternRegexCH(expr string) (*regexp.Regexp, error) {
	return regexp.Compile("(?s)" + expr)
}

// verifyPatternBodies — SAF: örnek gövdelere regex'i uygular. Derlenemeyen
// regex → Sampled 0 ("bilinmiyor"; dedektör sayımı değiştirmez — bozuk bir
// desen tanımı bütün olayları sessizce bastırmasın).
func verifyPatternBodies(expr string, bodies []string) PatternVerification {
	re, err := patternRegexCH(expr)
	if err != nil {
		log.Printf("[logstore] desen regex'i derlenemedi (%q): %v — doğrulama atlandı", expr, err)
		return PatternVerification{}
	}
	v := PatternVerification{Sampled: len(bodies)}
	for _, b := range bodies {
		if re.MatchString(b) {
			v.Matched++
			if v.Sample == "" {
				v.Sample = b
			}
		}
	}
	return v
}

// VerifiedRatioNote — SAF: olay açıklamasının doğrulama notu. Yalnız
// 0 < r < 1'de metin; r == 1 (tamamı doğrulandı) ve r ≤ 0 (örneklenmedi /
// CH) için "" — not yalnız sayının bir tahmin olduğu durumda görünür.
// Yüzde [1, 99]'a kıstırılır: 0.996 "%100", 0.004 "%0" okunmasın.
func VerifiedRatioNote(r float64) string {
	if r <= 0 || r >= 1 {
		return ""
	}
	pct := int(math.Round(r * 100))
	if pct < 1 {
		pct = 1
	}
	if pct > 99 {
		pct = 99
	}
	return fmt.Sprintf("örneklemde %%%d regex doğrulandı", pct)
}

// VerifyPatterns — CH: sayım (countOnePattern) regex'i zaten uyguluyor;
// doğrulanacak bir şey yok, sorgu YOK. nil = "backend regex'i uyguladı".
func (s *CHStore) VerifyPatterns(context.Context, []PatternSpec, time.Time, time.Time) ([]PatternVerification, error) {
	return nil, nil
}
