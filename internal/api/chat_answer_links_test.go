package api

// v0.10.1297 — operatör 2026-10-17: BSA operasyon adı linki yanlış
// (coremetry.io + search=). Prod CoSRE (gemma4 31b) cevabına
//
//	// build_link result
//	https://coremetry.io/traces?order=desc&range=custom:…&search=<ad>&sort=duration
//
// yazdı ve "Coremetry'de aç" /traces?search=<operasyon adı> açtı (ad span'lerde
// yok → sıfır trace; doğrusu fonksiyon kodu GCWCD01 süzgeci). Pinler:
//   - cevap süzgeci yabancı/uydurma host'lu Coremetry linkini sunucunun
//     doğruladığı kök-göreli linkle değiştirir ya da söker, araç yankı
//     satırını siler; wiki/dış doküman linkine dokunmaz;
//   - ön-getirme operasyonu kodla çözdüyse döngünün search=<ad> linkleri
//     (build_link çipi, deep_link → answer.open) kod süzgecine çevrilir.

import (
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const bsaOp = "DIGITAL_PAYMENTSYSTEMCAMPAIGN_JUZDAN_LIST_OF_GAINED_CAMPAIGNS"

const bsaValidated = `/traces?filters=%5B%7B%22k%22%3A%22FUNCTION_CODE%22%2C%22op%22%3A%22%3D%22%2C%22v%22%3A%5B%22GCWCD01%22%5D%7D%5D&range=custom%3A1-2`

func TestSanitizeAnswerLinks(t *testing.T) {
	tools := map[string]bool{"build_link": true, "search_traces": true}
	validated := []guidedAnswerLink{{Label: "Coremetry'de aç", Href: bsaValidated}}
	foreign := "https://coremetry.io/traces?order=desc&range=custom:1-2&search=" + bsaOp + "&sort=duration"
	cases := []struct {
		name, in, pub string
		links         []guidedAnswerLink
		want          string
	}{
		{"operatör vakası: yankı + uydurma host → doğrulanmış link",
			"Özet.\n```\n// build_link result\n" + foreign + "\n```\nSon.", "", validated,
			"Özet.\n```\n" + bsaValidated + "\n```\nSon."},
		{"yankı satırı tek başına silinir, çit boş kalırsa çit de",
			"Özet.\n```\n// build_link result\n```\nSon.", "", nil, "Özet.\nSon."},
		{"markdown link → doğrulanmış hedef, etiket kalır",
			"[Traces'te aç](" + foreign + ")", "", validated, "[Traces'te aç](" + bsaValidated + ")"},
		{"uydurma coremetry host'u, doğrulanmış eş yok → sökülür (md etiketi kalır)",
			"Bak: [aç](https://coremetry.io/logs?q=x) ve https://app.coremetry.io/traces?search=a.", "", nil,
			"Bak: aç ve ."},
		{"kendi PUBLIC_URL'imiz + doğrulanmış eş → kök-göreli doğrulanmış",
			"Link: https://apm.bank.local/traces?search=" + bsaOp, "https://apm.bank.local", validated,
			"Link: " + bsaValidated},
		{"kendi PUBLIC_URL'imiz, eş yok → aynen",
			"Link: https://apm.bank.local/problems?problem=p1", "https://apm.bank.local/", nil,
			"Link: https://apm.bank.local/problems?problem=p1"},
		{"wiki / dış doküman linki aynen",
			"Runbook: https://wiki.bank.local/display/OPS/Payment+Runbook ve [Jira](https://jira.bank.local/browse/OPS-1)", "", validated,
			"Runbook: https://wiki.bank.local/display/OPS/Payment+Runbook ve [Jira](https://jira.bank.local/browse/OPS-1)"},
		{"yabancı host + ürün yolu + doğrulanmış eş → değiştirilir",
			"https://apm.example.com/traces?search=x", "", validated, bsaValidated},
		{"katalog dışı ad yankı sayılmaz",
			"// not result\nmetin", "", nil, "// not result\nmetin"},
		{"kök-göreli metin linkine dokunulmaz",
			"Bkz. /traces?service=a", "", validated, "Bkz. /traces?service=a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sanitizeAnswerLinks(c.in, c.links, c.pub, tools)
			if got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
			if strings.Contains(got, "coremetry.io") {
				t.Errorf("uydurma host kaldı: %q", got)
			}
		})
	}
}

func hrefQuery(t *testing.T, href string) (url.Values, []chstore.FilterExpr) {
	t.Helper()
	u, err := url.Parse(href)
	if err != nil {
		t.Fatal(err)
	}
	var fs []chstore.FilterExpr
	if raw := u.Query().Get("filters"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &fs); err != nil {
			t.Fatal(err)
		}
	}
	return u.Query(), fs
}

func TestOpSearchHrefToCodeFilter(t *testing.T) {
	base := "/traces?order=desc&range=custom%3A1-2&search=" + bsaOp + "&sort=duration"
	cases := []struct {
		name, href string
		codes      []string
		wantSearch string
		wantFilter string // "" = süzgeç yok
		wantOp     string
	}{
		{"operatör vakası", base, []string{"GCWCD01"}, "", "GCWCD01", "="},
		{"harf duyarsız ad", strings.Replace(base, bsaOp, strings.ToLower(bsaOp), 1), []string{"GCWCD01"}, "", "GCWCD01", "="},
		{"çok kod IN", base, []string{"A1", "B2"}, "", "A1", "IN"},
		{"başka search aynen", "/traces?search=checkout", []string{"GCWCD01"}, "checkout", "", ""},
		{"traces değil aynen", "/logs?q=" + bsaOp, []string{"GCWCD01"}, "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := opSearchHrefToCodeFilter(c.href, bsaOp, "FUNCTION_CODE", c.codes)
			if !strings.HasPrefix(c.href, "/traces?") {
				if got != c.href {
					t.Errorf("değişmemeli: %s", got)
				}
				return
			}
			q, fs := hrefQuery(t, got)
			if q.Get("search") != c.wantSearch {
				t.Errorf("search = %q (%s)", q.Get("search"), got)
			}
			if c.wantFilter == "" {
				if len(fs) != 0 {
					t.Errorf("süzgeç eklenmemeli: %s", got)
				}
				return
			}
			if len(fs) != 1 || fs[0].Key != "FUNCTION_CODE" || fs[0].Op != c.wantOp || fs[0].Values[0] != c.wantFilter {
				t.Errorf("kod süzgeci: %+v (%s)", fs, got)
			}
			if q.Get("range") != "custom:1-2" || q.Get("sort") != "duration" {
				t.Errorf("diğer paramlar korunmalı: %s", got)
			}
		})
	}
	// Mevcut süzgeç korunur, aynı anahtar ikinci kez eklenmez.
	withF := "/traces?search=" + bsaOp + "&filters=" + url.QueryEscape(`[{"k":"FUNCTION_CODE","op":"=","v":["GCWCD01"]},{"k":"http.route","op":"=","v":["/x"]}]`)
	if _, fs := hrefQuery(t, opSearchHrefToCodeFilter(withF, bsaOp, "FUNCTION_CODE", []string{"GCWCD01"})); len(fs) != 2 {
		t.Errorf("süzgeç tekrarlanmamalı: %+v", fs)
	}
	// Bozuk filters → href aynen (yanlış link kurmaktansa eskisi).
	bad := "/traces?search=" + bsaOp + "&filters=%5Bnope"
	if got := opSearchHrefToCodeFilter(bad, bsaOp, "FUNCTION_CODE", []string{"GCWCD01"}); got != bad {
		t.Errorf("bozuk filters aynen kalmalı: %s", got)
	}
}

func TestOpPrefetchRewriteSearchHref(t *testing.T) {
	href := "/traces?search=" + bsaOp
	var nilP *opPrefetch
	if nilP.rewriteSearchHref(href) != href {
		t.Error("ön-getirme yokken href aynen")
	}
	span := &opPrefetch{Operation: bsaOp, Codes: []string{"GCWCD01"}} // codeKey boş = span operasyonu
	if span.rewriteSearchHref(href) != href {
		t.Error("span operasyonunda yeniden yazım olmamalı")
	}
	code := &opPrefetch{Operation: bsaOp, Codes: []string{"GCWCD01"}, codeKey: "FUNCTION_CODE"}
	if _, fs := hrefQuery(t, code.rewriteSearchHref(href)); len(fs) != 1 || fs[0].Values[0] != "GCWCD01" {
		t.Errorf("kodla çözülen operasyon: %+v", fs)
	}
	// Kart linki tüm kodları taşır (eskiden yalnız ilki).
	tr := opTracesHref(opTarget{Name: bsaOp, Codes: []string{"A1", "B2"}, HasOra: true}, "FUNCTION_CODE", time.Unix(1000, 0), time.Unix(2000, 0))
	if _, fs := hrefQuery(t, tr); len(fs) != 1 || fs[0].Op != "IN" || len(fs[0].Values) != 2 {
		t.Errorf("kart linki tüm kodlar: %s", tr)
	}
}

// Kaynak pini: iki cevap yayını da süzgeçten geçer; iki çip yolu da yeniden
// yazılır (saf yardımcı çağrılmıyorsa tablo testi hiçbir şeyi pinlemez).
func TestAnswerLinkGuardsReachable(t *testing.T) {
	b, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if n := strings.Count(src, "appendCharts(sanitizeAnswerLinks("); n != 2 {
		t.Errorf("iki cevap yayını da sanitizeAnswerLinks'ten geçmeli (got %d)", n)
	}
	if n := strings.Count(src, "opPre.rewriteSearchHref(l.Href)"); n != 2 {
		t.Errorf("deep_link + build_link çipleri yeniden yazılmalı (got %d)", n)
	}
}

// v0.10.2212 — Operator-reported (prod endpoint "Ask CoSRE", gemma4 31b, v2):
// başlık cümlesinin altına araç iç notu sızdı. Yalnız bilinen şablon silinir.
func TestStripServerToolNoticeExactProdString(t *testing.T) {
	const leak = "__':build_link' calls were already provided by the server."
	in := "POST /api/x son 30 dakikada 13 çağrı, %53,85 hata.\n" + leak + "\n\nEn yavaş trace'ler aşağıda."
	got := sanitizeAnswerLinks(in, nil, "", map[string]bool{"build_link": true})
	if strings.Contains(got, "already provided by the server") {
		t.Fatalf("iç not sızdı: %q", got)
	}
	if got != "POST /api/x son 30 dakikada 13 çağrı, %53,85 hata.\n\nEn yavaş trace'ler aşağıda." {
		t.Fatalf("çevre metin değişti: %q", got)
	}
	for _, v := range []string{
		"`build_link` call was already provided by the server",
		"**operation_prefetch calls were already provided by the server.**",
	} {
		if out := stripServerToolNotices("a\n" + v + "\nb"); out != "a\nb" {
			t.Errorf("%q silinmedi: %q", v, out)
		}
	}
	// Şablon dışı cümleler aynen kalır (geniş regex yok).
	for _, keep := range []string{
		"Linkler sunucu tarafından eklendi.",
		"The data was already provided by the server team yesterday, see build_link.",
		"build_link calls were already provided by the server, ve hata oranı %5.",
	} {
		if out := stripServerToolNotices(keep); out != keep {
			t.Errorf("dokunulmamalı: %q → %q", keep, out)
		}
	}
}
