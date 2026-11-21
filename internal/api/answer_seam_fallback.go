package api

// answer_seam_fallback.go — v0.10.2439 (operatör kararı 2026-11-21, "CoSRE
// kanıt kapısı açık"): zorla kipi artık varsayılan. Doğrulayıcı cevabı
// engellediğinde (yeniden üretimden sonra da uyuşmayan değer kaldığında)
// cevap SESSİZCE kaybolmaz ve yerine değer UYDURULMAZ:
//
//   - değerler yerinde işaretli kalır (seamAnnotate, mevcut "(doğrulanmamış
//     değer)" deseni — karar 6: cümle silinmez);
//   - cevabın sonuna tek satırlık sunucu notu (verifyEnforceNoteTR) eklenir;
//   - takip çipi (kind=verify): uyuşmayan değerleri (≤3) veriden yeniden
//     kontrol ettiren kullanıcı cümlesi. Tık yeni bir tur açar; değerler
//     o turda araçla yeniden okunur ya da kaynak belirsizse CoSRE sorar.
//
// Model ayrımı yok: not ve çip sunucu metnidir, hangi model (Qwen / gemma)
// cevaplarsa cevaplasın aynı SAF karar uygulanır. Ek model çağrısı yok.

import (
	"strings"
)

// verifyEnforceNoteTR — işaretli cevabın sonundaki not (öneri dili yok:
// detectOffer'ı tetiklemez; stripEnforceNote ayrıca keser).
const verifyEnforceNoteTR = "_Not: kanıtla doğrulanamayan değerler işaretlendi, yerlerine tahmin yazılmadı; bu değerler veriden yeniden sorgulanabilir._"

// verifyRecheckKind / verifyRecheckLabel — takip çipi.
const (
	verifyRecheckKind      = "verify"
	verifyRecheckLabel     = "Değerleri veriden yeniden kontrol et"
	verifyRecheckMaxTokens = 3
)

// seamWithEnforceNote — SAF: metnin sonuna not paragrafı (zaten varsa aynen).
func seamWithEnforceNote(text string) string {
	t := strings.TrimRight(text, " \t\n")
	if strings.HasSuffix(t, verifyEnforceNoteTR) {
		return text
	}
	if t == "" {
		return verifyEnforceNoteTR
	}
	return t + "\n\n" + verifyEnforceNoteTR
}

// stripEnforceNote — SAF: sondaki not paragrafını keser (durumun öneri
// okuması modelin kendi son paragrafını görsün).
func stripEnforceNote(text string) string {
	t := strings.TrimRight(text, " \t\n")
	if !strings.HasSuffix(t, verifyEnforceNoteTR) {
		return text
	}
	return strings.TrimRight(strings.TrimSuffix(t, verifyEnforceNoteTR), " \t\n")
}

// seamRecheckChip — SAF: uyuşmayan değerlerden (tekil, ≤3) takip çipi.
func seamRecheckChip(mm []verifyClaim) chatFollowup {
	var toks []string
	seen := map[string]bool{}
	for _, c := range mm {
		if len(toks) < verifyRecheckMaxTokens && c.Text != "" && !seen[c.Text] {
			seen[c.Text] = true
			toks = append(toks, "«"+c.Text+"»")
		}
	}
	text := "Doğrulanamayan değerleri veriden yeniden kontrol et"
	if len(toks) > 0 {
		text = "Şu değerleri veriden yeniden kontrol et: " + strings.Join(toks, ", ")
	}
	return chatFollowup{Text: text, Label: verifyRecheckLabel, Kind: verifyRecheckKind}
}

// seamWithRecheckFollowup — `answer` yüküne (sığ kopya üzerinde) takip
// çipini ekler: mevcut çipler korunur, aynı türden çip varsa eklenmez.
// Eski istemci için düz metin `suggestions`'a da yazılır (withAnswerFollowups emsali).
func seamWithRecheckFollowup(m map[string]any, mm []verifyClaim) map[string]any {
	chip := seamRecheckChip(mm)
	fu, _ := m["followups"].([]chatFollowup)
	for _, c := range fu {
		if c.Kind == verifyRecheckKind {
			return m
		}
	}
	m["followups"] = append(append([]chatFollowup(nil), fu...), chip)
	sug, _ := m["suggestions"].([]string)
	m["suggestions"] = append(append([]string(nil), sug...), chip.Text)
	return m
}
