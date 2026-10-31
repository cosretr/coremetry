package briefing

// askref.go — v0.10.1716 (Login Briefing dilim 7, "Tasarım önerisi" UI kartı):
// "Sor" tıklaması composer'ı satırla doldurur ve isteğe context.briefingId +
// briefingLine taşır. Sunucu kartı (cosre_briefings) yeniden okur ve satırın
// bağlamını SAYISIZ, sınırlı bir ek olarak döngüye koyar: kalem + özneler
// (servis / etiket adları). Sayılar ekte YOK — model onları araçlarla aynı
// pencerede yeniden okur; satırın sayısı kullanıcının mesajında zaten var.

import (
	"strings"
	"unicode"
)

// Sınırlar — ek tavanı (satır başı ≤ MaxRowsPerLine özne, özne ≤ MaxAskSubjectRunes).
const (
	MaxAskSubjectRunes = 120
	ulidLen            = 26
)

// AskRef — satırın döngüye giden bağlamı.
type AskRef struct {
	Item     string
	Subjects []string
	PageURL  string
}

// ValidID — SAF: Crockford base32 ULID (26 karakter, I L O U yok).
func ValidID(id string) bool {
	if len(id) != ulidLen {
		return false
	}
	for _, c := range id {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'A' && c <= 'Z' && c != 'I' && c != 'L' && c != 'O' && c != 'U':
		default:
			return false
		}
	}
	return true
}

// cleanSubject — kontrol karakterleri boşluğa, kırpılmış, tavanlı.
func cleanSubject(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return clipRunes(strings.Join(strings.Fields(s), " "), MaxAskSubjectRunes)
}

// LineAskRef — SAF: kart + istemcinin kimliği/satır sırası → ek bağlamı.
// Kimlik kartınkiyle eşleşmiyorsa (kart yenilendi) ya da satır yoksa ok=false:
// eski kartın satırı yeni kartın satırına bağlanmaz.
func LineAskRef(b Briefing, id string, line int) (AskRef, bool) {
	if !ValidID(id) || b.ID != id || line < 0 || line >= len(b.Lines) {
		return AskRef{}, false
	}
	l := b.Lines[line]
	ref := AskRef{Item: l.Item, PageURL: l.PageURL}
	seen := map[string]bool{}
	for _, f := range l.Rows {
		for _, s := range []string{f.Service, f.Label} {
			s = cleanSubject(s)
			if s == "" || seen[s] {
				continue
			}
			seen[s] = true
			ref.Subjects = append(ref.Subjects, s)
		}
		if len(ref.Subjects) >= MaxRowsPerLine {
			break
		}
	}
	if len(ref.Subjects) > MaxRowsPerLine {
		ref.Subjects = ref.Subjects[:MaxRowsPerLine]
	}
	return ref, true
}
