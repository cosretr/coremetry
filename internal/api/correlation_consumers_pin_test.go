package api

// v0.10.1090 — KAYNAK ÇİVİSİ: "ne başka değişti" korelasyonlarını aday /
// kötüleşen diye sunan hiçbir tüketici satırı causeEligible ya da direction'a
// bakmadan okumaz. v0.10.1063 yalnız manşeti düzeltmişti; aynı yönsüz skor
// üç yerde daha "olası neden" / "kötüleşen" olarak sızıyordu (hakem kataloğu,
// /shift, şerit). Davranış testleri ayrı (rca_extras_cause_test.go,
// shift_change_groups_test.go, RootCauseRibbon.candidates.test.tsx); bu çivi
// yeni bir okuma yolunun süzgeci atlamasını yakalar.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func pinReadRepo(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("%s okunamadı: %v", rel, err)
	}
	return string(b)
}

// pinStripLineComments — `//` ile başlayan satırları atar (yorumdaki örnek
// kod çiviyi kandırmasın).
func pinStripLineComments(src string) string {
	var b strings.Builder
	for _, ln := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "//") {
			continue
		}
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	return b.String()
}

// pinBody — `anchor`'ın ilk geçtiği yerden sonraki ilk dengeli { … } gövdesi.
func pinBody(t *testing.T, src, anchor, file string) string {
	t.Helper()
	i := strings.Index(src, anchor)
	if i < 0 {
		t.Fatalf("%s: %q bulunamadı — çivi güncellenmeli", file, anchor)
	}
	open := strings.Index(src[i:], "{")
	if open < 0 {
		t.Fatalf("%s: %q gövdesi yok", file, anchor)
	}
	depth := 0
	for j := i + open; j < len(src); j++ {
		switch src[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[i+open : j+1]
			}
		}
	}
	t.Fatalf("%s: %q gövdesi kapanmıyor", file, anchor)
	return ""
}

func TestCorrelationConsumersCheckCause(t *testing.T) {
	// 1. Hakem kataloğu: Correlations döngüsü entity'li satırı YALNIZ
	//    CauseEligible'dan sonra basar; toplayıcı işaretlemeden geçirir.
	extras := pinStripLineComments(pinReadRepo(t, "internal/rca/extras.go"))
	if n := strings.Count(extras, "range extras.Correlations"); n != 1 {
		t.Fatalf("rca/extras.go: Correlations %d kez okunuyor (1 beklenir) — yeni okuma süzgeçten geçmeli", n)
	}
	loop := pinBody(t, extras, "range extras.Correlations", "rca/extras.go")
	gate := strings.Index(loop, "!cs.CauseEligible")
	add := strings.Index(loop, "addPos(cs.Service")
	if gate < 0 || add < 0 || gate > add {
		t.Errorf("rca/extras.go: korelasyon satırı CauseEligible kapısından ÖNCE beyaz listeye giriyor:\n%s", loop)
	}
	gather := pinStripLineComments(pinReadRepo(t, "internal/api/rca_extras.go"))
	if !strings.Contains(gather, "chstore.MarkCorrelationCauses(") {
		t.Error("rca_extras.go: gatherRCACatalogExtras korelasyonları MarkCorrelationCauses'tan geçirmiyor")
	}

	// 2. /shift: Worsened YALNIZ shiftChangeGroups'tan atanır; o da yöne bakar.
	shift := pinStripLineComments(pinReadRepo(t, "internal/api/shift_page.go"))
	assigns := regexp.MustCompile(`\.Worsened\s*(,|=)`).FindAllStringIndex(shift, -1)
	if len(assigns) != 1 || !strings.Contains(shift, "out.Worsened, out.Lost, out.Improved = shiftChangeGroups(") {
		t.Errorf("shift_page.go: Worsened shiftChangeGroups dışında atanıyor (%d atama)", len(assigns))
	}
	groups := pinBody(t, shift, "func shiftChangeGroups(", "shift_page.go")
	for _, want := range []string{"switch c.Direction", "chstore.ChangeWorse", "chstore.ChangeLost", "chstore.ChangeBetter"} {
		if !strings.Contains(groups, want) {
			t.Errorf("shift_page.go: shiftChangeGroups %q içermeli", want)
		}
	}

	// 3. Şerit: correlations okuyan her ADAY fonksiyonu causeEligible'a bakar.
	//    localizedNote / ribbonNoCandidateNote aday üretmez, yalnız "başka
	//    servis kıpırdadı mı" sayar — bilinçli muaf.
	ts := pinStripLineComments(pinReadRepo(t, "frontend/src/lib/rootCauseCandidates.ts"))
	exempt := map[string]bool{"localizedNote": true, "ribbonNoCandidateNote": true}
	fnRe := regexp.MustCompile(`export function (\w+)`)
	locs := fnRe.FindAllStringSubmatchIndex(ts, -1)
	checked := 0
	for k, loc := range locs {
		name := ts[loc[2]:loc[3]]
		end := len(ts)
		if k+1 < len(locs) {
			end = locs[k+1][0]
		}
		body := ts[loc[0]:end]
		if exempt[name] || !strings.Contains(body, ".correlations") {
			continue
		}
		checked++
		if !strings.Contains(body, "causeEligible === true") {
			t.Errorf("rootCauseCandidates.ts: %s correlations'ı causeEligible'a bakmadan okuyor", name)
		}
	}
	if checked < 2 { // ribbonCandidates + coMovingCause
		t.Errorf("rootCauseCandidates.ts: yalnız %d aday fonksiyonu denetlendi — çivi kör kaldı", checked)
	}
}
