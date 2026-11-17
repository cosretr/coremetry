package mcptools

// entity_alias.go — v0.10.2334 (conversation-audit §6.1 G, karar 15):
// kullanıcının KENDİ eş adları ("kart tarafı" → svc:payments, "kart tarafı"
// ↛ svc:orders). Katalog sıralamasından ÖNCE uygulanır; iki yol aynı
// fonksiyonu çağırır: konuşma durumunun çözücüsü (api/chat_state_resolve.go)
// ve resolve_entity aracı (ResolveEntityText).
//
// Kurallar:
//   - Eşleme deyimin KÖK dizisiyle yapılır (Türkçe hâl ekleri kapalı listeyle
//     atılır: "kart tarafı" = "kart tarafındaki" = "kart tarafında"); deyim
//     sorunun içinde ardışık geçmeli.
//   - Hedef kimlikle (entity id; servis için svc:<ad>) CANLI katalogda
//     aranır; katalogda olmayan hedef = bayat → eşleme bütünüyle yok sayılır
//     (aday listesi aynen kalır). Çıplak adla eşleme yok.
//   - Kişisel: eş adlar isteğin bağlamına yalnız o kullanıcının turunda konur
//     (WithEntityAliases); bağlamsız çağrı (başka kullanıcı, değerlendirme,
//     gece işleri) hiçbir eş ad görmez.

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/entity"
)

// EntityAlias — bir eş ad. Phrase kullanıcının yazdığı biçim; ID hedef
// entity kimliği; Not yanlış çıkmış aday (o deyim için bir daha seçilmez).
type EntityAlias struct {
	Phrase string `json:"phrase"`
	ID     string `json:"id"`
	Not    string `json:"not,omitempty"`
}

// aliasSuffixes — kök için atılan hâl ekleri (uzundan kısaya; kök ≥3 rune kalır).
var aliasSuffixes = []string{
	"ındaki", "indeki", "undaki", "ündeki", "ndaki", "ndeki",
	"ında", "inde", "unda", "ünde", "daki", "deki", "taki", "teki",
	"nın", "nin", "nun", "nün", "dan", "den", "tan", "ten",
	"da", "de", "ta", "te", "ı", "i", "u", "ü",
}

func aliasStem(t string) string {
	for _, s := range aliasSuffixes {
		if strings.HasSuffix(t, s) && utf8.RuneCountInString(t)-utf8.RuneCountInString(s) >= 3 {
			return strings.TrimSuffix(t, s)
		}
	}
	return t
}

// AliasWords — SAF: metnin küçük harfli sözcükleri (Türkçe büyük/küçük;
// kesme işaretinden sonrası ek sayılır ve atılır: "payments'ı" → payments).
func AliasWords(text string) []string {
	text = strings.ToLowerSpecial(unicode.TurkishCase, text)
	var out []string
	for _, f := range strings.FieldsFunc(text, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' || r == '’' || r == '-' || r == '_')
	}) {
		if i := strings.IndexAny(f, "'’"); i >= 0 {
			f = f[:i]
		}
		if f = strings.Trim(f, "-_"); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// AliasStems — SAF: AliasWords'ün kökleri.
func AliasStems(text string) []string {
	w := AliasWords(text)
	for i := range w {
		w[i] = aliasStem(w[i])
	}
	return w
}

// AliasKey — SAF: deyimin karşılaştırma anahtarı (kökler boşlukla).
func AliasKey(phrase string) string { return strings.Join(AliasStems(phrase), " ") }

// AliasPhraseIn — SAF: deyimin kökleri metnin köklerinde ardışık geçiyor mu.
func AliasPhraseIn(text, phrase string) bool {
	p := AliasStems(phrase)
	if len(p) == 0 {
		return false
	}
	t := AliasStems(text)
	for i := 0; i+len(p) <= len(t); i++ {
		ok := true
		for j := range p {
			if t[i+j] != p[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// CandidateByID — SAF: kimliğin canlı katalogdaki adayı (yoksa false).
func CandidateByID(idx EntityCatalogIndex, id string) (EntityCandidate, bool) {
	if id == "" {
		return EntityCandidate{}, false
	}
	if name, ok := strings.CutPrefix(id, "svc:"); ok {
		for _, s := range idx.Services {
			if s == name {
				return EntityCandidate{Kind: entity.TypeService, Name: s, ID: entity.ServiceID(s)}, true
			}
		}
		return EntityCandidate{}, false
	}
	for _, list := range [][]EntityCandidate{idx.Namespaces, idx.Workloads, idx.Pods, idx.Dependencies} {
		for _, c := range list {
			if c.ID == id {
				return c, true
			}
		}
	}
	return EntityCandidate{}, false
}

// ApplyEntityAliases — SAF, iki çözücü yolunun ORTAK girişi: metinde geçen
// ilk eş adın (sıra çağıranın önceliğidir) hedefi canlı katalogdaysa tek
// aday olarak döner (Score 100, Match "alias") ve kullanılan eş ad; yoksa
// adaylar aynen ve nil. Bayat hedefli eş ad atlanır.
func ApplyEntityAliases(text string, idx EntityCatalogIndex, aliases []EntityAlias, cands []EntityCandidate) ([]EntityCandidate, *EntityAlias) {
	if len(aliases) == 0 {
		return cands, nil
	}
	for i := range aliases {
		a := aliases[i]
		if !AliasPhraseIn(text, a.Phrase) {
			continue
		}
		c, ok := CandidateByID(idx, a.ID)
		if !ok {
			continue
		}
		c.Score, c.Match = 100, "alias"
		return []EntityCandidate{c}, &a
	}
	return cands, nil
}

type entityAliasesKey struct{}

// WithEntityAliases — turun kullanıcısının eş adları bağlama (boşsa ctx aynen).
func WithEntityAliases(ctx context.Context, a []EntityAlias) context.Context {
	if len(a) == 0 {
		return ctx
	}
	return context.WithValue(ctx, entityAliasesKey{}, append([]EntityAlias(nil), a...))
}

// EntityAliasesFrom — bağlamdaki eş adlar (yoksa nil).
func EntityAliasesFrom(ctx context.Context) []EntityAlias {
	a, _ := ctx.Value(entityAliasesKey{}).([]EntityAlias)
	return a
}
