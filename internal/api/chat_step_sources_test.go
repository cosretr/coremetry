package api

import (
	"strings"
	"testing"
)

func TestStepSourceStatuses(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []string // "source:state"
	}{
		{"düz metin", "no data", nil},
		{"hata sözleşmesi", `{"error":"timeout","retryable":true,"hint":"x"}`, nil},
		{"tek kaynak", `{"source":{"source":"logs","backend":"elasticsearch","state":"empty","returned":0},"logs":[]}`, []string{"logs:empty"}},
		{"kaynak sonda, büyük veri önde", `{"series":[` + strings.Repeat(`{"v":1},`, 2000) + `{"v":2}],"source":{"source":"metrics","state":"partial","flags":["partial","truncated"]}}`, []string{"metrics:partial"}},
		{"çoklu kaynak", `{"sources":[{"source":"traces","state":"ok"},{"source":"traces","state":"delayed","notes":["a]b"]}]}`, []string{"traces:ok", "traces:delayed"}},
		{"durumsuz girdi atlanır", `{"sources":[{"source":"traces"},{"source":"logs","state":"unauthorized"}]}`, []string{"logs:unauthorized"}},
		{"bozuk json", `{"source":`, nil},
	}
	for _, c := range cases {
		got := stepSourceStatuses(c.content)
		var s []string
		for _, g := range got {
			s = append(s, g.Source+":"+g.State)
		}
		if strings.Join(s, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %v want %v", c.name, s, c.want)
		}
	}
	// v0.10.944 — nil ile boş dilim AYRI: birleştirilmiş metin karşılaştırması
	// ikisini ayıramaz. Çözülebilir JSON, durumsuz → boş (nil değil) dilim;
	// JSON değil / bozuk → nil (FE "denetlenemedi" okur).
	if got := stepSourceStatuses(`{"count":2,"services":[{"name":"checkout"}],"source":"mv"}`); got == nil || len(got) != 0 {
		t.Fatalf("durumsuz JSON boş ama nil olmayan dilim vermeli: %#v", got)
	}
	for _, c := range []string{"no data", `{"source":`} {
		if got := stepSourceStatuses(c); got != nil {
			t.Fatalf("%q: denetlenemeyen çıktı nil vermeli: %#v", c, got)
		}
	}
	// flags taşınır; detay kırpılır.
	got := stepSourceStatuses(`{"source":{"source":"metrics","state":"partial","flags":["partial","truncated"],"detail":"` + strings.Repeat("ğ", 300) + `"}}`)
	if len(got) != 1 || len(got[0].Flags) != 2 || len([]rune(got[0].Detail)) != 161 {
		t.Fatalf("got %+v", got)
	}
}

// TestToolResultDeepLinkLogs — v0.10.944: search_logs sonucundaki /logs
// bağlantısı çipe geçer; kök-göreli olmayan ya da başka sayfaya giden
// bağlantı reddedilir.
func TestToolResultDeepLinkLogs(t *testing.T) {
	if l, ok := toolResultDeepLink("search_logs", `{"source":{"state":"ok"},"deep_link":"/logs?q=x&range=custom%3A1-2"}`); !ok || l.Href != "/logs?q=x&range=custom%3A1-2" {
		t.Fatalf("got %+v %v", l, ok)
	}
	for _, bad := range []string{`{"deep_link":"//evil.example/logs?x"}`, `{"deep_link":"/traces?x"}`, `{"deep_link":"https://x/logs?"}`} {
		if _, ok := toolResultDeepLink("search_logs", bad); ok {
			t.Errorf("kabul edilmemeliydi: %s", bad)
		}
	}
}

// TestStepSourcesAllFailed — v0.10.944: künye (chatSourceNoteTR) yalnız
// kullanılabilir kaynağı olan çağrıyı sayar; tüm kaynakları hata sınıfındaki
// "başarılı" sonuç veri döndürmemiştir.
func TestStepSourcesAllFailed(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"tek kaynak ulaşılamadı", `{"source":{"state":"unreachable"}}`, true},
		{"biri kullanılabilir", `{"sources":[{"state":"timeout"},{"state":"empty"}]}`, false},
		{"boş sonuç okunmuştur", `{"source":{"state":"empty"}}`, false},
		{"kısmi okunmuştur", `{"source":{"state":"partial"}}`, false},
		{"hepsi hata", `{"sources":[{"state":"unauthorized"},{"state":"not_configured"},{"state":"error"}]}`, true},
		{"JSON değil", "no data", false},
		{"kaynaksız", `{"count":2}`, false},
	}
	for _, c := range cases {
		if got := stepSourcesAllFailed(stepSourceStatuses(c.content)); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// TestNextLoopOpen — v0.10.944: log araması önceden yakalanmış trace
// bağlantısını ezmez; boş yuvayı doldurur. Trace araçları son-yazan-kazanır.
func TestNextLoopOpen(t *testing.T) {
	cases := []struct{ cur, tool, href, want string }{
		{"/traces?a", "search_logs", "/logs?b", "/traces?a"},
		{"", "search_logs", "/logs?b", "/logs?b"},
		{"/traces?a", "search_traces", "/traces?c", "/traces?c"},
		{"/logs?b", "trace_stats", "/traces?d", "/traces?d"},
	}
	for _, c := range cases {
		if got := nextLoopOpen(c.cur, c.tool, c.href); got != c.want {
			t.Errorf("nextLoopOpen(%q,%q,%q)=%q want %q", c.cur, c.tool, c.href, got, c.want)
		}
	}
}

// TestChatWiresStepSourcesFooter — v0.10.944 kablolama pini: künye kaydı
// tüm-kaynaklar-hata sonucunu dışlar; çip `sources`'u nil değilse (boş dahil)
// ve hata değilse taşır.
func TestChatWiresStepSourcesFooter(t *testing.T) {
	src := readSourceFile(t, "copilot_chat.go")
	for _, must := range []string{
		"srcs := stepSourceStatuses(tr.Content)",
		"if !tr.IsError && !stepSourcesAllFailed(srcs) {",
		"if srcs != nil && !tr.IsError {",
		"loopOpen = nextLoopOpen(loopOpen, tc.Name, l.Href)",
	} {
		if !strings.Contains(src, must) {
			t.Errorf("copilot_chat.go kablolaması kayıp: %s", must)
		}
	}
}
