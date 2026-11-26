package api

// answer_seam_fallback.go — v0.10.2439 (operatör kararı 2026-11-21, "CoSRE
// kanıt kapısı açık"): zorla kipi artık varsayılan. Doğrulayıcı cevabı
// engellediğinde (yeniden üretimden sonra da uyuşmayan değer kaldığında)
// cevap SESSİZCE kaybolmaz ve yerine değer UYDURULMAZ:
//
//   - değerler yerinde işaretli kalır (seamAnnotate; v0.10.2480'dan beri kısa
//     "†" işareti — karar 6: cümle silinmez);
//   - cevabın sonuna tek satırlık dipnot (verifyFootnoteTR: hangi değerler) eklenir;
//   - takip çipi (kind=verify): uyuşmayan değerleri (≤3) veriden yeniden
//     kontrol ettiren kullanıcı cümlesi. Tık yeni bir tur açar; değerler
//     o turda araçla yeniden okunur ya da kaynak belirsizse CoSRE sorar.
//
// Model ayrımı yok: not ve çip sunucu metnidir, hangi model (Qwen / gemma)
// cevaplarsa cevaplasın aynı SAF karar uygulanır. Ek model çağrısı yok.

import (
	"strconv"
	"strings"
)

// Dipnot (v0.10.2480, F): işaretli cevabın sonundaki TEK satır — hangi
// değerlerin bu turun verisinde bulunamadığı (öneri dili yok: detectOffer'ı
// tetiklemez; stripEnforceNote ayrıca keser).
const (
	verifyFootnotePrefix   = "_† Bu turun verisinde bulunamadı: "
	verifyFootnoteTail     = " — yerlerine tahmin yazılmadı; veriden yeniden sorgulanabilir._"
	verifyFootnoteMaxToks  = 6
	verifyFootnoteTokRunes = 48
)

// verifyFootnoteTR — SAF: uyuşmayan değerlerin (tekil, ≤6, uzun olan kısaltılmış) dipnot satırı.
func verifyFootnoteTR(mm []verifyClaim) string {
	var toks []string
	seen := map[string]bool{}
	extra := 0
	for _, c := range mm {
		t := strings.TrimSpace(c.Text)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		if len(toks) == verifyFootnoteMaxToks {
			extra++
			continue
		}
		toks = append(toks, "«"+clipRunes(t, verifyFootnoteTokRunes)+"»")
	}
	line := strings.Join(toks, ", ")
	if extra > 0 {
		line += " +" + strconv.Itoa(extra)
	}
	return verifyFootnotePrefix + line + verifyFootnoteTail
}

// seamWithEnforceNote — SAF: metnin sonuna dipnot paragrafı (zaten varsa aynen).
func seamWithEnforceNote(text string, mm []verifyClaim) string {
	t := strings.TrimRight(text, " \t\n")
	if hasEnforceNote(t) {
		return text
	}
	note := verifyFootnoteTR(mm)
	if t == "" {
		return note
	}
	return t + "\n\n" + note
}

// hasEnforceNote — son paragraf dipnot mu.
func hasEnforceNote(t string) bool {
	i := strings.LastIndex(t, "\n\n")
	last := t
	if i >= 0 {
		last = t[i+2:]
	}
	return strings.HasPrefix(last, verifyFootnotePrefix) && strings.HasSuffix(last, verifyFootnoteTail)
}

// stripEnforceNote — SAF: sondaki dipnot paragrafını keser (durumun öneri
// okuması modelin kendi son paragrafını görsün).
func stripEnforceNote(text string) string {
	t := strings.TrimRight(text, " \t\n")
	if !hasEnforceNote(t) {
		return text
	}
	if i := strings.LastIndex(t, "\n\n"); i >= 0 {
		return strings.TrimRight(t[:i], " \t\n")
	}
	return ""
}

// verifyRecheckKind / verifyRecheckLabel — takip çipi.
const (
	verifyRecheckKind      = "verify"
	verifyRecheckLabel     = "Değerleri veriden yeniden kontrol et"
	verifyRecheckMaxTokens = 3
)

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
