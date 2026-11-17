package api

// chat_alias_proposal.go — v0.10.2335 (conversation-audit §3.7, §6.1 G):
// düzeltmeden sonra eş ad ÖNERİSİ. Hafıza kademesinin operatör kuralı
// kartıyla (chat_memory.go cosreRuleAction) AYNI mekanizma: cevap bir
// `action` bloğu taşır (kind cosre_alias), istemci kartı çizer ve yalnız
// "Kaydet" tıkı POST /api/cosre/user-prefs/aliases çağırır. Sunucu HİÇBİR
// ŞEY kaydetmez; "Hayır" yalnız kartı kapatır (istemci).
//
// Kurallar: düzeltme başına tek öneri; konuşmada bir kez (ChatContext
// corrections[].proposed) — kapatılan öneri aynı konuşmada yeniden çıkmaz.
// Deyimsiz düzeltmede (önceki mesaj açık ad taşıyordu) ve aynı eşleme
// kullanıcının kaydında zaten varsa öneri yok. Değerlendirme koşusunda ve
// kullanıcısız istekte yok (uid boş). Blok "done"dan önce, durum
// kesinleştikten sonra; withBlockSeq onu `block`a katlar.

import (
	"strings"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

// cosreAliasActionEvent — withBlockSeq'in action bloğuna katladığı iç tür.
const cosreAliasActionEvent = "alias_action"

// cosreAliasAction — SAF: öneri kartının aksiyon yükü.
func cosreAliasAction(k chatCorrection) map[string]any {
	return map[string]any{
		"kind": "cosre_alias", "phrase": k.Phrase,
		"entityId": "svc:" + k.Right, "name": k.Right,
		"notId": "svc:" + k.Wrong, "notName": k.Wrong,
		"text":  "«" + k.Phrase + "» dediğinde " + k.Right + " servisini kastettiğini hatırlayayım mı?",
		"label": "Kaydet", "dismissLabel": "Hayır",
	}
}

// aliasSaved — SAF: kullanıcının kaydında aynı deyim aynı hedefe zaten bağlı.
func aliasSaved(p cosreUserPrefs, k chatCorrection) bool {
	key := mcptools.AliasKey(k.Phrase)
	for _, m := range p.M {
		if mcptools.AliasKey(m.P) == key && m.ID == "svc:"+k.Right {
			return true
		}
	}
	return false
}

// aliasProposal — turun önerisi (nil = yok); çıkarsa konuşmada işaretlenir.
func (t *chatStateTurn) aliasProposal() map[string]any {
	if t == nil || t.learned == nil || t.learned.Phrase == "" || t.learned.Proposed || t.uid == "" || aliasSaved(t.prefs, *t.learned) {
		return nil
	}
	c := t.st.ctx
	for i := len(c.Corrections) - 1; i >= 0; i-- {
		if strings.EqualFold(c.Corrections[i].Phrase, t.learned.Phrase) {
			list := append([]chatCorrection(nil), c.Corrections...)
			list[i].Proposed = true
			c.Corrections = list
			t.st.ctx, t.st.dirty = c, true
			break
		}
	}
	return cosreAliasAction(*t.learned)
}
