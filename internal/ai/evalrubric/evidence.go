package evalrubric

// evidence.go — v0.10.2360 (docs/cosre/investigation-design.md S3;
// sre-evidence-audit §5.4): kanıt bloğu ekleme kuralının KAPALI kalıp
// listeleri ve konuşma kipinin üç hakemsiz ölçütü. Sunucunun ekleme kararı
// (api/chat_evidence_policy.go) aynı listeleri kullanır: ölçüm ile karar aynı
// tanımla çalışır.
//
//  1. Canlı veri iddiasının kanıtı var: araç okunmuş turda sayı / kimlik
//     taşıyan cevapta en az bir blok; vaka tür isteyebilir.
//  2. Gereksiz kanıt yığını yok: blok sayısı vaka tavanını ve kuralın
//     tavanını (4) aşmaz, tür başına 1.
//  3. Tahmin veri gibi sunulmadı: sahtede olmayan sayı yok; araçsız turda
//     blok yok ve neden öne süren cümle ihtiyat sözcüğü taşır.

import (
	"regexp"
	"strings"
)

// showMeCues — kullanıcı turunda "göster / nereden biliyorsun / emin misin".
var showMeCues = []string{"göster", "gösterir misin", "nereden biliyorsun", "neye dayan", "emin misin", "kanıt", "show me"}

// actionCues — cevap metninde eyleme yönlendirme.
var actionCues = []string{"rollback", "geri al", "restart", "yeniden başlat", "eskalasyon", "eskale", "ölçekle", "scale"}

// hedgeWords — ihtiyat sözcükleri.
var hedgeWords = []string{"muhtemelen", "büyük ihtimalle", "olabilir", "genellikle", "sanırım", "görünüyor", "olası"}

// causeCues — neden öne süren kalıplar.
var causeCues = []string{"çünkü", "nedeni", "sebebi", "kaynaklanıyor", "yüzünden", "kaynaklı"}

func hasAny(text string, list []string) bool {
	t := foldTR(text)
	for _, c := range list {
		if strings.Contains(t, c) {
			return true
		}
	}
	return false
}

// ShowMeCue — kullanıcı turu kanıt istiyor mu.
func ShowMeCue(question string) bool { return hasAny(question, showMeCues) }

// ActionCue — cevap eyleme yönlendiriyor mu.
func ActionCue(answer string) bool { return hasAny(answer, actionCues) }

// Hedged — cevapta ihtiyat sözcüğü var mı.
func Hedged(answer string) bool { return hasAny(answer, hedgeWords) }

// CauseClaim — cevap bir neden öne sürüyor mu.
func CauseClaim(answer string) bool { return hasAny(answer, causeCues) }

// liveClaimRe — sayı, saat ya da uzun onaltılık kimlik.
var liveClaimRe = regexp.MustCompile(`\d|[0-9a-f]{16,}`)

// LiveDataClaim — gövde canlı veri iddiası (sayı / saat / kimlik) taşıyor mu.
func LiveDataClaim(answer string) bool { return liveClaimRe.MatchString(StripSourceLine(answer)) }

// EvidenceCapShowMe — "göster" turundaki tavan (kuralın üst sınırı).
const EvidenceCapShowMe = 4

// EvidenceInput — bir turun ölçüt girdisi.
type EvidenceInput struct {
	Answer     string
	Calls      int      // turun veri okuması
	Attached   []string // eklenen blok tipleri (sırayla)
	Ungrounded int      // sahtede olmayan sayı
	Require    []string // vaka: cevapta olması gereken tipler
	Max        *int     // vaka: en çok blok
}

// EvidenceScore — üç ölçüt; nil = bu turda ölçülmez.
type EvidenceScore struct {
	ClaimsHaveEvidence *bool
	NoPile             bool
	NoGuessAsData      bool
	Notes              []string
}

// EvidenceOf — SAF: üç ölçüt.
func EvidenceOf(in EvidenceInput) EvidenceScore {
	out := EvidenceScore{NoPile: true, NoGuessAsData: true}
	has := map[string]int{}
	for _, t := range in.Attached {
		has[t]++
	}
	if (in.Calls > 0 && LiveDataClaim(in.Answer)) || len(in.Require) > 0 {
		ok := len(in.Attached) > 0
		for _, r := range in.Require {
			if has[r] == 0 {
				ok = false
				out.Notes = append(out.Notes, "istenen kanıt yok: "+r)
			}
		}
		if len(in.Attached) == 0 {
			out.Notes = append(out.Notes, "canlı veri iddiası kanıtsız")
		}
		out.ClaimsHaveEvidence = &ok
	}
	if (in.Max != nil && len(in.Attached) > *in.Max) || len(in.Attached) > EvidenceCapShowMe {
		out.NoPile = false
		out.Notes = append(out.Notes, "kanıt yığını: "+itoa(len(in.Attached))+" blok")
	}
	for t, n := range has {
		if n > 1 {
			out.NoPile = false
			out.Notes = append(out.Notes, "aynı türden birden çok blok: "+t)
		}
	}
	if in.Ungrounded > 0 {
		out.NoGuessAsData = false
		out.Notes = append(out.Notes, "sahtede olmayan sayı")
	}
	if in.Calls == 0 {
		if len(in.Attached) > 0 {
			out.NoGuessAsData = false
			out.Notes = append(out.Notes, "araçsız turda kanıt bloğu")
		}
		if CauseClaim(in.Answer) && !Hedged(in.Answer) {
			out.NoGuessAsData = false
			out.Notes = append(out.Notes, "araçsız neden ihtiyatsız sunuldu")
		}
	}
	return out
}
