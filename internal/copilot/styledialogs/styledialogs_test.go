package styledialogs

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/evalrubric"
)

func foldTR(s string) string { return strings.ToLower(strings.ReplaceAll(s, "İ", "i")) }

// turns — diyaloğun turları ("K"/"C", metin; devam satırları tura katılır).
func turns(text string) (who []string, body []string) {
	for _, ln := range strings.Split(text, "\n") {
		if strings.HasPrefix(ln, "K: ") || strings.HasPrefix(ln, "C: ") {
			who = append(who, ln[:1])
			body = append(body, ln[3:])
			continue
		}
		body[len(body)-1] += "\n" + ln
	}
	return who, body
}

func cosreTurns(d Dialog) []string {
	who, body := turns(d.Text)
	var out []string
	for i, w := range who {
		if w == "C" {
			out = append(out, body[i])
		}
	}
	return out
}

func TestSetShape(t *testing.T) {
	ds := All()
	if len(ds) < 8 || len(ds) > 10 {
		t.Fatalf("set %d diyalog; 8–10 olmalı", len(ds))
	}
	if SetVersion == "" {
		t.Fatal("set sürümü boş")
	}
	idRe := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	seen := map[string]bool{}
	classSeen := map[string]bool{}
	addrSeen := map[string]bool{}
	valid := map[string]bool{}
	for _, c := range Classes {
		valid[c] = true
	}
	for _, d := range ds {
		if !idRe.MatchString(d.ID) || seen[d.ID] {
			t.Errorf("kimlik %q geçersiz ya da tekrar", d.ID)
		}
		seen[d.ID] = true
		if n := len(d.Text); n > MaxDialogBytes || n == 0 {
			t.Errorf("%s: modele giden metin %d B (tavan %d)", d.ID, n, MaxDialogBytes)
		}
		if len(d.Classes) == 0 {
			t.Errorf("%s: sınıf yok", d.ID)
		}
		for _, c := range d.Classes {
			if !valid[c] {
				t.Errorf("%s: bilinmeyen sınıf %q", d.ID, c)
			}
			classSeen[c] = true
		}
		switch d.Address {
		case AddressSen, AddressSiz, AddressNeutral:
			addrSeen[d.Address] = true
		default:
			t.Errorf("%s: hitap %q", d.ID, d.Address)
		}
		if d.Shows == "" || d.Facts == "" {
			t.Errorf("%s: gösterir / olgular boş", d.ID)
		}
		who, _ := turns(d.Text)
		if len(who) < 4 || who[0] != "K" {
			t.Errorf("%s: çok turlu (≥2 K + ≥2 C) ve K ile başlamalı: %v", d.ID, who)
		}
		for i := 1; i < len(who); i++ {
			if who[i] == who[i-1] {
				t.Errorf("%s: turlar sırayla K / C olmalı: %v", d.ID, who)
			}
		}
	}
	for _, c := range Classes {
		if !classSeen[c] {
			t.Errorf("sınıf %s hiçbir diyalogda yok", c)
		}
	}
	if !addrSeen[AddressSen] || !addrSeen[AddressSiz] {
		t.Errorf("iki hitap da sette olmalı: %v", addrSeen)
	}
}

// TestNeutralNames — yalnız nötr adlar: kebap / alt çizgili / noktalı her
// tanımlayıcı izinli kalıba uyar; bilinen gerçek ad ve ortam sözcükleri yok.
func TestNeutralNames(t *testing.T) {
	allowed := regexp.MustCompile(`^((checkout|orders|payments|inventory)(-[0-9a-z]{4}-[0-9a-z]{3})?|consumer-\d+)$`)
	identRe := regexp.MustCompile(`[A-Za-z0-9]+(?:[-_.][A-Za-z0-9]+)+`)
	// Kurum / müşteri adları repo genelinde TestNoCustomerIdentifiersInRepo
	// (api) ile denetlenir; burada ortam sözcükleri.
	deny := []string{"openshift", "ocp", "prod-", "-prod", "coremetry", ".com", ".local", "namespace", "cluster"}
	for _, d := range All() {
		low := foldTR(d.Text)
		for _, w := range deny {
			if strings.Contains(low, w) {
				t.Errorf("%s: gerçek ad / ortam sözcüğü %q", d.ID, w)
			}
		}
		for _, id := range identRe.FindAllString(d.Text, -1) {
			if regexp.MustCompile(`^[0-9.,:]+$`).MatchString(id) {
				continue // sayı ya da saat
			}
			if !allowed.MatchString(id) {
				t.Errorf("%s: izinsiz tanımlayıcı %q", d.ID, id)
			}
		}
	}
}

// TestNoBoilerplate — paylaşılan kalıp listelerindeki hiçbir ifade sette yok;
// hiçbir CoSRE satırı selamla başlamaz, nezaketle bitmez.
func TestNoBoilerplate(t *testing.T) {
	greet := []string{"merhaba", "selam", "günaydın", "iyi günler", "tabii", "elbette", "rica ederim", "memnuniyetle"}
	courtesy := []string{"iyi çalışmalar", "kolay gelsin", "rica ederim", "umarım", "yardımcı olabildiysem", "her zaman"}
	for _, d := range All() {
		low := foldTR(d.Text)
		for _, p := range append(append([]string{}, evalrubric.BoilerplateOpenings...), evalrubric.BoilerplateClosings...) {
			if strings.Contains(low, p) {
				t.Errorf("%s: kalıp ifade %q", d.ID, p)
			}
		}
		for _, c := range cosreTurns(d) {
			lc := foldTR(strings.TrimSpace(c))
			for _, g := range greet {
				if strings.HasPrefix(lc, g) {
					t.Errorf("%s: CoSRE satırı selamla başlıyor: %q", d.ID, c)
				}
			}
			tail := lc[max(0, len(lc)-80):]
			for _, g := range courtesy {
				if strings.Contains(tail, g) {
					t.Errorf("%s: CoSRE satırı nezaketle bitiyor: %q", d.ID, c)
				}
			}
			if f := evalrubric.FluencyOf(c); f.BoilerplateOpening || f.BoilerplateClosing {
				t.Errorf("%s: akıcılık ölçütü kalıp sayıyor: %q", d.ID, c)
			}
		}
	}
}

// TestNumbersFromFacts — CoSRE satırındaki her rakam dizisi aynı diyaloğun
// olgu notunda geçer: örnek, doğrulayıcının yakalayacağı uydurma sayıyı öğretmez.
func TestNumbersFromFacts(t *testing.T) {
	numRe := regexp.MustCompile(`\d+(?:[.,:]\d+)*`)
	for _, d := range All() {
		for _, c := range cosreTurns(d) {
			for _, n := range numRe.FindAllString(c, -1) {
				if !strings.Contains(d.Facts, n) {
					t.Errorf("%s: %q olgularda yok", d.ID, n)
				}
			}
		}
	}
}

// TestStyleRules — madde işareti yalnız 3+ paralel öğede; her cevap soruyla
// bitmez; siz diyaloğu siz der, sen diyaloğu siz demez.
func TestStyleRules(t *testing.T) {
	bulletRe := regexp.MustCompile(`(?m)^\s*([-*•]|\d+[.)])\s`)
	sizRe := regexp.MustCompile(`(?i)(\bsiz\b|\bsizin\b|[ıiuü]n[ıiuü]z)`)
	bulleted, total, questions := 0, 0, 0
	for _, d := range All() {
		for _, c := range cosreTurns(d) {
			total++
			if n := len(bulletRe.FindAllString(c, -1)); n > 0 {
				bulleted++
				if n < 3 {
					t.Errorf("%s: %d maddelik liste (en az 3)", d.ID, n)
				}
			}
			if strings.HasSuffix(strings.TrimSpace(c), "?") {
				questions++
			}
			if d.Address == AddressSen && sizRe.MatchString(c) {
				t.Errorf("%s: sen diyaloğunda siz hitabı: %q", d.ID, c)
			}
		}
		if d.Address == AddressSiz && !sizRe.MatchString(strings.Join(cosreTurns(d), " ")) {
			t.Errorf("%s: siz diyaloğunda siz hitabı yok", d.ID)
		}
	}
	if bulleted == 0 || bulleted*3 > total {
		t.Errorf("madde listesi %d / %d CoSRE satırında; bir örnek gösterir, çoğunluk düzyazı", bulleted, total)
	}
	if questions == 0 || questions*3 > total {
		t.Errorf("soruyla biten %d / %d; teklif örneği olmalı ama her cevap soruyla bitmemeli", questions, total)
	}
}

// TestDocsIndexInSync — docs/cosre/style-dialogs/README.md ile gömülü set
// aynı kimlikler, aynı sırayla, aynı sınıf ve aynı metinle; her girişte
// "Onay:" satırı var.
func TestDocsIndexInSync(t *testing.T) {
	b, err := os.ReadFile("../../../docs/cosre/style-dialogs/README.md")
	if err != nil {
		t.Fatal(err)
	}
	secRe := regexp.MustCompile("(?s)\n## ([a-z0-9-]+)\n(.*?)```text\n(.*?)\n```")
	var ids []string
	docs := map[string][2]string{}
	for _, m := range secRe.FindAllStringSubmatch(string(b), -1) {
		ids = append(ids, m[1])
		docs[m[1]] = [2]string{m[2], m[3]}
	}
	var want []string
	for _, d := range All() {
		want = append(want, d.ID)
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("dizin kimlikleri %v, set %v", ids, want)
	}
	for _, d := range All() {
		meta, text := docs[d.ID][0], docs[d.ID][1]
		if text != d.Text {
			t.Errorf("%s: dizindeki metin setle aynı değil", d.ID)
		}
		if !strings.Contains(meta, "- Sınıf: "+strings.Join(d.Classes, ", ")+"\n") {
			t.Errorf("%s: dizindeki sınıf satırı setle aynı değil", d.ID)
		}
		if !regexp.MustCompile(`(?m)^- Onay: \S`).MatchString(meta) {
			t.Errorf("%s: Onay satırı yok", d.ID)
		}
	}
}

func TestSelect(t *testing.T) {
	for _, mode := range []Mode{ModeLoop, ModeGuided, ModeGeneral} {
		for _, c := range append(append([]string{}, Classes...), "", "bilinmeyen") {
			for _, a := range []string{"", AddressSen, AddressSiz} {
				ids := Select(c, a, mode)
				if !reflect.DeepEqual(ids, Select(c, a, mode)) {
					t.Fatalf("kararsız: %s %s %d", c, a, mode)
				}
				if len(ids) != mode.count() {
					t.Fatalf("%s/%s/%d: %d örnek, %d bekleniyordu", c, a, mode, len(ids), mode.count())
				}
				seen := map[string]bool{}
				hasSiz := false
				for _, id := range ids {
					d, ok := ByID(id)
					if !ok || seen[id] {
						t.Fatalf("bilinmeyen ya da tekrar kimlik %q", id)
					}
					seen[id] = true
					want := AddressSen
					if a == AddressSiz {
						want = AddressSiz
					}
					if d.Address != AddressNeutral && d.Address != want {
						t.Errorf("%s/%s: hitaba uymayan %s", c, a, id)
					}
					hasSiz = hasSiz || d.Address == AddressSiz
					if mode == ModeGeneral && !d.HasClass(ClassChat) && !d.HasClass(ClassKnowledge) {
						t.Errorf("genel cevapta sohbet / bilgi dışı örnek %s", id)
					}
				}
				if a == AddressSiz && mode == ModeLoop && !hasSiz {
					t.Errorf("%s/siz/%d: siz örneği yok %v", c, mode, ids)
				}
			}
		}
	}
	// Sınıfı taşıyan örnek varsa ilk sırada o gelir.
	for _, c := range Classes {
		if d, _ := ByID(Select(c, AddressSen, ModeGuided)[0]); !d.HasClass(c) && c != ClassChat {
			t.Errorf("%s: guided örneği sınıfı taşımıyor (%s)", c, d.ID)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{"serbest metin\n", "=== a\nbilinmez: x\nK: a\n", "=== a\nbaşlıksız satır\n"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("hata bekleniyordu: %q", s)
		}
	}
	ds, err := Parse("# yorum\n=== a\nsınıf: status\nhitap: -\nK: x\nC: y\n- bir\n")
	if err != nil || len(ds) != 1 || ds[0].Text != "K: x\nC: y\n- bir" || ds[0].Classes[0] != ClassStatus {
		t.Fatalf("ayrıştırma: %+v %v", ds, err)
	}
}
