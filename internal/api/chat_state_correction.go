package api

// chat_state_correction.go — v0.10.2334 (conversation-audit §6.1 G):
// kullanıcının varlık DÜZELTMESİ. Deterministik, model çağrısı yok.
//
// Düzeltme = kapalı ipucu listesi (correctionCue) + önceki turun çözülen
// varlığı (durumun servisi) + bu turun çözülen varlığı (sorudaki açık ad ya
// da döngünün okuduğu servis) ve ikisi FARKLI. O zaman:
//   - durum yeni varlığa geçer (A'nın değiştirme kuralı: operasyon/trace düşer);
//     ipucu olup yeni ad yoksa eski varlık bu turda DEVRALINMAZ ve döngü onu
//     yeniden seçemez (avoid);
//   - çift konuşmaya yazılır (ChatContext.Corrections, ≤5): aynı deyim bu
//     konuşmada doğrudan yeni varlığa çözülür;
//   - deyim (yanlış varlığa götüren, kullanıcının önceki turda YAZDIĞI söz
//     öbeği) bulunduysa öneri kartı çıkar (chat_alias_proposal.go); kalıcı
//     kayıt yalnız Kaydet tıkıyla (cosre_user_prefs.go).
//
// Deyim çıkarımı (aliasPhraseOf): önceki kullanıcı mesajının sözcükleri; soru,
// zaman, durum ve dolgu sözcükleri (aliasStopWords), sayılar ve ortam adları
// atlanır; kalan İLK ardışık öbek, en çok 3 sözcük ve 40 rune. Önceki mesaj
// katalogdaki bir servisi AÇIKÇA anıyorsa deyim yok — açık ad eş ad değildir.

import (
	"strings"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// chatCorrection — konuşmada kaydedilen düzeltme (servis adları; deyim
// boş olabilir — o zaman öneri yok, yalnız durum değişti).
type chatCorrection struct {
	Phrase   string `json:"phrase,omitempty"`
	Wrong    string `json:"wrong"`
	Right    string `json:"right"`
	Proposed bool   `json:"proposed,omitempty"` // öneri kartı bu konuşmada çıktı
}

const chatCorrectionsMax = 5

// correctionCuePhrases — kapalı liste (sözcük sınırında, küçük harf).
var correctionCuePhrases = []string{
	"o değil", "onu değil", "bu değil", "o degil", "onu degil", "bu degil",
	"onu sormadım", "onu demedim", "yanlış servis",
	"demek istedim", "demek istiyorum", "kastettim", "kastediyorum",
}

// correctionCue — SAF: mesaj bir varlık düzeltmesi dili taşıyor mu.
//   - liste deyimi ("o değil, payments", "payments demek istedim");
//   - "hayır" ile başlayıp içerik süren mesaj ("hayır, payments");
//   - eliptik yer belirteci: ≤3 sözcük ve son sözcük -daki/-deki/-taki/-teki
//     ("kart tarafındaki").
func correctionCue(q string) bool {
	w := mcptools.AliasWords(q)
	if len(w) == 0 {
		return false
	}
	joined := " " + strings.Join(w, " ") + " "
	for _, p := range correctionCuePhrases {
		if strings.Contains(joined, " "+p+" ") {
			return true
		}
	}
	if len(w) >= 2 && (w[0] == "hayır" || w[0] == "hayir") {
		return true
	}
	if len(w) <= 3 {
		last := w[len(w)-1]
		for _, suf := range []string{"daki", "deki", "taki", "teki"} {
			if strings.HasSuffix(last, suf) && utf8.RuneCountInString(last) > utf8.RuneCountInString(suf)+2 {
				return true
			}
		}
	}
	return false
}

// aliasStopWords — deyime girmeyen sözcükler (soru, durum, zaman, dolgu).
var aliasStopWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`nasıl nasil ne neden niye nedir niçin hangi hangisi durum durumu durumda
		var yok mı mi mu mü mısın misin musun müsün mısınız misiniz
		hata hatası hataları hatalar hatalı hatada yavaş yavaşlık gecikme sorun sorunu problem problemi
		bugün dün şimdi son saat saatte saattir dakika gün hafta sabah akşam gece öğleden öğlen sonra önce
		peki ya ve ile veya bir şey şeyler oldu olmuş oluyor olur gidiyor gitti iyi kötü normal
		bak bakar bakın göster getir lütfen acaba de da ki bu şu o için gibi çok daha en hiç
		selam merhaba servis servisi servisin servisinde servisler sistem
		ortam ortamı ortamda ortamında prod`) {
		aliasStopWords[w] = true
	}
}

// aliasPhraseOf — SAF: önceki mesajdan deyim ("" = öğrenilecek deyim yok).
func aliasPhraseOf(prevQ string, services, envs []string) string {
	words := mcptools.AliasWords(prevQ)
	for _, w := range words {
		if containsFold(services, w) {
			return ""
		}
	}
	var run []string
	for _, w := range words {
		skip := aliasStopWords[w] || utf8.RuneCountInString(w) < 2 || containsFold(envs, w) || isAllDigits(w)
		if skip {
			if len(run) > 0 {
				break
			}
			continue
		}
		run = append(run, w)
		if len(run) == 3 {
			break
		}
	}
	for len(run) > 0 && utf8.RuneCountInString(strings.Join(run, " ")) > cosreUserPhraseMaxRunes {
		run = run[:len(run)-1]
	}
	p := strings.Join(run, " ")
	if utf8.RuneCountInString(p) < cosreUserPhraseMinRunes {
		return ""
	}
	return p
}

// previousUserText — son kullanıcı mesajından önceki kullanıcı mesajı.
func previousUserText(msgs []copilot.ChatMessage) string {
	seen := false
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "user" {
			continue
		}
		if seen {
			return msgs[i].Text
		}
		seen = true
	}
	return ""
}

// conversationAliases — SAF: konuşmanın düzeltmeleri eş ad olarak (en yeni önce).
func conversationAliases(c ChatContext) []mcptools.EntityAlias {
	var out []mcptools.EntityAlias
	for i := len(c.Corrections) - 1; i >= 0; i-- {
		k := c.Corrections[i]
		if k.Phrase != "" && k.Right != "" {
			out = append(out, mcptools.EntityAlias{Phrase: k.Phrase, ID: "svc:" + k.Right, Not: "svc:" + k.Wrong})
		}
	}
	return out
}

// withCorrection — SAF: aynı deyimli (deyimsizse aynı yanlış varlıklı) eski
// kayıt düşer, yenisi sona; ≤5. Önceki önerilmiş işareti aynı çiftte korunur.
func withCorrection(list []chatCorrection, k chatCorrection) []chatCorrection {
	key := mcptools.AliasKey(k.Phrase)
	out := make([]chatCorrection, 0, len(list)+1)
	for _, x := range list {
		same := (key != "" && mcptools.AliasKey(x.Phrase) == key) || (key == "" && x.Phrase == "" && strings.EqualFold(x.Wrong, k.Wrong))
		if same {
			if strings.EqualFold(x.Right, k.Right) && x.Proposed {
				k.Proposed = true
			}
			continue
		}
		out = append(out, x)
	}
	out = append(out, k)
	if len(out) > chatCorrectionsMax {
		out = out[len(out)-chatCorrectionsMax:]
	}
	return out
}
