// Package styledialogs — v0.10.2336 (docs/cosre/conversation-audit.md §6.1 C,
// §7 karar 8): CoSRE v2 anlatımı için ÖRNEK DİYALOG seti. Küçük model üslubu
// kuraldan değil örnekten alır (§5 madde 2); bu paket örnekleri ve onları
// cevap sınıfına göre seçen deterministik işlevi taşır. Bulgu sabit, cümle
// serbest: örnekler biçim değil konuşma kaydı öğretir.
//
// Tek kopya burada (`go:embed` paket dizini dışını okuyamaz); operatörün
// inceleme dizini docs/cosre/style-dialogs/README.md, eşitliği test pinler.
// Dosya .txt: imaj bağlamı *.md'yi dışlar (.dockerignore).
//
// Bu paket copilot'u içe aktarmaz (döngü yok); modele giden başlık ve
// birleştirme copilot/prompts.go'dadır.
package styledialogs

import (
	_ "embed"
	"fmt"
	"strings"
)

// SetVersion — setin sürümü; bir diyalog eklenince, çıkınca ya da metni
// değişince artar (değerlendirme raporu ve istem sürümü bunu okur).
const SetVersion = "2026-11-18.1"

// MaxDialogBytes — tek diyaloğun modele giden metninin tavanı (audit C satırı).
const MaxDialogBytes = 450

// Cevap sınıfları — api/chat_answer_class.go'daki değerlerle birebir
// (api tarafı test eşitliği pinler; report ve "" burada yok: rapor türleri
// örnek almaz).
const (
	ClassStatus    = "status"
	ClassErrorHunt = "error_hunt"
	ClassExplain   = "explanation"
	ClassCompare   = "comparison"
	ClassKnowledge = "knowledge"
	ClassChat      = "chat"
)

// Classes — geçerli sınıf etiketleri.
var Classes = []string{ClassStatus, ClassErrorHunt, ClassExplain, ClassCompare, ClassKnowledge, ClassChat}

// Hitap etiketleri: "-" = hitap eki taşımayan diyalog (iki hitapta da seçilir).
const (
	AddressSen     = "sen"
	AddressSiz     = "siz"
	AddressNeutral = "-"
)

// Dialog — bir örnek. Text modele giden tek alandır; diğerleri inceleme için.
type Dialog struct {
	ID      string
	Classes []string
	Address string // sen | siz | -
	Shows   string // ne gösterdiği (inceleme)
	Facts   string // CoSRE satırlarının dayandığı araç olguları (inceleme)
	Text    string // "K: …\nC: …" satırları
}

// HasClass — diyalog bu sınıfla etiketli mi.
func (d Dialog) HasClass(c string) bool {
	for _, x := range d.Classes {
		if x == c {
			return true
		}
	}
	return false
}

//go:embed dialogs.txt
var raw string

var set = mustParse(raw)

// All — set, dosya sırasıyla (kopya).
func All() []Dialog { return append([]Dialog(nil), set...) }

// ByID — kimliğe göre diyalog.
func ByID(id string) (Dialog, bool) {
	for _, d := range set {
		if d.ID == id {
			return d, true
		}
	}
	return Dialog{}, false
}

// Texts — kimliklerin modele giden metinleri (bilinmeyen kimlik atlanır).
func Texts(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if d, ok := ByID(id); ok {
			out = append(out, d.Text)
		}
	}
	return out
}

func mustParse(s string) []Dialog {
	ds, err := Parse(s)
	if err != nil {
		panic("styledialogs: " + err.Error())
	}
	return ds
}

// Parse — SAF: dialogs.txt biçimi. "#" ile başlayan satır yorum; "=== id"
// blok açar; "anahtar: değer" başlıkları ilk "K: " satırına kadar; sonra
// "K: " / "C: " tur satırları ve (madde gibi) devam satırları.
func Parse(s string) ([]Dialog, error) {
	var out []Dialog
	var cur *Dialog
	var body []string
	flush := func() {
		if cur != nil {
			cur.Text = strings.TrimRight(strings.Join(body, "\n"), "\n")
			out = append(out, *cur)
		}
		cur, body = nil, nil
	}
	for i, ln := range strings.Split(s, "\n") {
		switch {
		case strings.HasPrefix(ln, "#") && cur == nil:
			continue
		case strings.HasPrefix(ln, "=== "):
			flush()
			cur = &Dialog{ID: strings.TrimSpace(ln[4:])}
		case cur == nil:
			if strings.TrimSpace(ln) != "" {
				return nil, fmt.Errorf("satır %d: blok dışında metin", i+1)
			}
		case strings.HasPrefix(ln, "K: ") || strings.HasPrefix(ln, "C: ") || len(body) > 0:
			if strings.TrimSpace(ln) == "" {
				continue
			}
			body = append(body, ln)
		default:
			k, v, ok := strings.Cut(ln, ": ")
			if !ok {
				if strings.TrimSpace(ln) == "" {
					continue
				}
				return nil, fmt.Errorf("satır %d: başlık bekleniyordu", i+1)
			}
			switch k {
			case "sınıf":
				for _, c := range strings.Split(v, ",") {
					cur.Classes = append(cur.Classes, strings.TrimSpace(c))
				}
			case "hitap":
				cur.Address = strings.TrimSpace(v)
			case "gösterir":
				cur.Shows = strings.TrimSpace(v)
			case "olgular":
				cur.Facts = strings.TrimSpace(v)
			default:
				return nil, fmt.Errorf("satır %d: bilinmeyen başlık %q", i+1, k)
			}
		}
	}
	flush()
	return out, nil
}

// Mode — örneklerin gideceği anlatım kademesi.
type Mode int

const (
	ModeLoop    Mode = iota // serbest döngü: 3 örnek
	ModeGuided              // guided anlatım: 1 örnek
	ModeGeneral             // araçsız genel cevap: 1 sohbet / bilgi örneği
)

// count — kademe başına örnek sayısı. Döngü 3: bayt pini (yeni istem ≤
// bugünkü v2 döngü çekirdeği) en uzun üç diyalogla da tutar. Guided 1:
// en sık koşan tek çağrılı anlatımın girişi en az büyüsün (audit C: "aşarsa
// guided tek örneğe iner").
func (m Mode) count() int {
	if m == ModeLoop {
		return 3
	}
	return 1
}

// Select — SAF ve kararlı: sınıf + hitap + kademe → diyalog kimlikleri.
//
//  1. Hitaba uyanlar: hitap "-" ya da istenen hitap ("" = sen, karar 13).
//  2. Sınıfla etiketliler, set sırasıyla.
//  3. Döngüde (birden çok örnek) hitap "siz" ise seçimde en az bir "siz"
//     diyaloğu olur (yoksa sonuncunun yerine havuzdaki ilk "siz" diyaloğu
//     girer) — model hitabı örnekten görür. Tek örnekli kademede sınıf önce gelir.
//  4. Eksik yer havuzdan set sırasıyla dolar. Havuz: döngü ve guided'da bütün
//     set; genel cevapta yalnız sohbet / bilgi diyalogları.
//
// Bilinmeyen sınıf yalnız 4. adımla dolar; aynı girdi hep aynı sonucu verir.
func Select(class, address string, mode Mode) []string {
	if address != AddressSiz {
		address = AddressSen
	}
	n := mode.count()
	pool := func(d Dialog) bool {
		if d.Address != AddressNeutral && d.Address != address {
			return false
		}
		return mode != ModeGeneral || d.HasClass(ClassChat) || d.HasClass(ClassKnowledge)
	}
	var out []string
	taken := map[string]bool{}
	add := func(d Dialog) {
		out = append(out, d.ID)
		taken[d.ID] = true
	}
	for _, d := range set {
		if len(out) < n && pool(d) && d.HasClass(class) {
			add(d)
		}
	}
	if address == AddressSiz && n > 1 && !anySiz(out) {
		for _, d := range set {
			if d.Address == AddressSiz && pool(d) && !taken[d.ID] {
				if len(out) == n {
					delete(taken, out[n-1])
					out = out[:n-1]
				}
				add(d)
				break
			}
		}
	}
	for _, d := range set {
		if len(out) < n && pool(d) && !taken[d.ID] {
			add(d)
		}
	}
	return out
}

func anySiz(ids []string) bool {
	for _, id := range ids {
		if d, _ := ByID(id); d.Address == AddressSiz {
			return true
		}
	}
	return false
}
