package api

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// v0.10.1553 — kaynak keşfi: SAF karar, çipler, komut sorusu, anlatım biçimi.

func TestPickChatSource(t *testing.T) {
	ex := srcCand{Kind: srcExcel, Score: 0.9, Label: "16 satır"}
	wk := srcCand{Kind: srcWiki, Score: 0.75, Label: "OpenTelemetry"}
	tlm := srcCand{Kind: srcTelemetry, Score: 0.9, Label: "orders servisi"}
	tlmWeak := srcCand{Kind: srcTelemetry, Score: 0.5, Label: "canlı veri"}
	exc := srcCand{Kind: srcExceptions, Score: 0.85, Label: "BSAException · 3 grup"}
	cases := []struct {
		name   string
		in     []srcCand
		winner string
		alts   []string
		tie    []string
	}{
		{"boş", nil, "", nil, nil},
		{"tek kaynak", []srcCand{wk}, srcWiki, nil, nil},
		{"CRMHouse: Excel kazanır, wiki çip", []srcCand{wk, ex}, srcExcel, []string{srcWiki}, nil},
		{"zayıf telemetri başa baş sayılmaz", []srcCand{wk, tlmWeak}, srcWiki, []string{srcTelemetry}, nil},
		{"Excel + servis adı eşit güçlü → sor", []srcCand{tlm, ex}, "", nil, []string{srcExcel, srcTelemetry}},
		{"exceptions 0.85 ile excel 0.9 başa baş → sor", []srcCand{exc, ex}, "", nil, []string{srcExcel, srcExceptions}},
		{"fark ≥ 0.1 → kazanan", []srcCand{srcCand{Kind: srcExcel, Score: 0.95}, wk}, srcExcel, []string{srcWiki}, nil},
	}
	kinds := func(cs []srcCand) []string {
		var out []string
		for _, c := range cs {
			out = append(out, c.Kind)
		}
		return out
	}
	for _, c := range cases {
		got := pickChatSource(c.in)
		w := ""
		if got.Winner != nil {
			w = got.Winner.Kind
		}
		if w != c.winner || strings.Join(kinds(got.Alts), ",") != strings.Join(c.alts, ",") || strings.Join(kinds(got.Tie), ",") != strings.Join(c.tie, ",") {
			t.Errorf("%s: winner=%q alts=%v tie=%v", c.name, w, kinds(got.Alts), kinds(got.Tie))
		}
	}
}

func TestSourceChipsAndStep(t *testing.T) {
	cs := []srcCand{{Kind: srcWiki, Label: "OpenTelemetry"}, {Kind: srcTelemetry}}
	if got := sourceChips(cs); strings.Join(got, "|") != "/wiki · OpenTelemetry|/telemetri" {
		t.Fatalf("çipler: %v", got)
	}
	if got := discoverStepLabel([]srcCand{{Kind: srcExcel, Label: "16 satır"}, {Kind: srcWiki, Label: "X"}}); got != "kaynak keşfi: Yüklenen tablolar 16 satır · Wiki X" {
		t.Fatalf("adım: %q", got)
	}
}

func TestExceptionTermOf(t *testing.T) {
	for q, want := range map[string]string{
		"BSAException neden artıyor":                              "BSAException",
		"org.apache.kafka.common.errors.TimeoutException kaç kez": "TimeoutException",
		"ORA-00001 hatası hangi serviste":                         "ORA-00001",
		"BKMQR-39 kodu":                                           "BKMQR-39",
		"opentelemetry java instrumentation":                      "",
		"CRMHouse ait vserver adreslerini getir":                  "",
		"NullPointerException ve IllegalStateException farkı":     "NullPointerException",
	} {
		if got := exceptionTermOf(q); got != want {
			t.Errorf("exceptionTermOf(%q) = %q, want %q", q, got, want)
		}
	}
}

func TestSourceCommandQuestion(t *testing.T) {
	msgs := []copilot.ChatMessage{
		{Role: "user", Text: "CRMHouse ait vserver adreslerini getir"},
		{Role: "assistant", Text: "…"},
		{Role: "user", Text: "/wiki · OpenTelemetry"},
	}
	if got := sourceCommandQuestion("· OpenTelemetry", msgs); got != "CRMHouse ait vserver adreslerini getir" {
		t.Fatalf("çip etiketi → önceki soru: %q", got)
	}
	if got := sourceCommandQuestion("", msgs); got != "CRMHouse ait vserver adreslerini getir" {
		t.Fatalf("argümansız → önceki soru: %q", got)
	}
	if got := sourceCommandQuestion("PROD sunucuları", msgs); got != "PROD sunucuları" {
		t.Fatalf("açık argüman: %q", got)
	}
	// Önceki mesaj da bir çipse onu atlar, gerçek soruya iner.
	msgs2 := append(append([]copilot.ChatMessage(nil), msgs...),
		copilot.ChatMessage{Role: "assistant", Text: "…"}, copilot.ChatMessage{Role: "user", Text: "/excel · 16 satır"})
	if got := previousUserQuestion(msgs2); got != "CRMHouse ait vserver adreslerini getir" {
		t.Fatalf("çip zinciri: %q", got)
	}
	if got := previousUserQuestion(msgs[:1]); got != "" {
		t.Fatalf("önceki soru yok: %q", got)
	}
	for _, k := range []string{srcExcel, srcTelemetry, srcExceptions, srcGeneral} {
		if sourceCommandKind(k) != k {
			t.Errorf("%s kaynak komutu olmalı", k)
		}
		if _, known := normalizeChatCommand("/" + k); !known {
			t.Errorf("/%s komut listesinde yok", k)
		}
	}
	if sourceCommandKind("wiki") != "" || sourceCommandKind("rca") != "" {
		t.Error("wiki / rca kaynak komutu dalına girmemeli")
	}
}

func TestEmitWithAltSources(t *testing.T) {
	var got map[string]any
	emit := emitWithAltSources(func(ev string, d any) {
		if ev == "answer" {
			got = d.(map[string]any)
		}
	}, []string{"/wiki · X", "/excel · 2 satır"})
	emit("answer", map[string]any{"text": "t", "suggestions": []string{"Açık problemler", "/wiki · X"}})
	if s := got["suggestions"].([]string); strings.Join(s, "|") != "Açık problemler|/wiki · X|/excel · 2 satır" {
		t.Fatalf("öneriler: %v", s)
	}
}

func TestRecordsNarrativeShape(t *testing.T) {
	if withRecordsNarrative("", "TABLO") != "TABLO" {
		t.Fatal("anlatım yoksa yalnız tablo")
	}
	if got := withRecordsNarrative("CRMHouse'a ait 16 kayıt.", "TABLO"); got != "CRMHouse'a ait 16 kayıt.\n\n---\n\nTABLO" {
		t.Fatalf("biçim: %q", got)
	}
	p := recordsUserPrompt("soru", "yüklenen tablo satırları", strings.Repeat("x", recordsMaxRunes+10))
	if !strings.Contains(p, "KESİLDİ") || !strings.HasPrefix(p, "SORU: soru") {
		t.Fatalf("kesilme notu: %q", p[:80])
	}
	if strings.Contains(recordsUserPrompt("s", "k", "kısa"), "KESİLDİ") {
		t.Fatal("kısa veri kesilmiş sayılmamalı")
	}
}

func TestExceptionsSourceMarkdown(t *testing.T) {
	ev := exceptionGroupsEvidence{LookbackS: 7 * 24 * 3600, Groups: []exGroupLine{
		{Type: "BSAException", Service: "bsa-cards", State: "new", Priority: "P2", Occurrences: 1234, LastSeen: 1_700_000_000_000_000_000, Href: "/problems?exc=a"},
	}}
	md := exceptionsSourceMarkdown("BSAException", ev)
	if !strings.Contains(md, "| BSAException | bsa-cards | new | P2 | 1,234 |") {
		t.Fatalf("tablo:\n%s", md)
	}
	if got := exceptionsSourceMarkdown("X", exceptionGroupsEvidence{Err: "boom"}); !strings.Contains(got, "okunamadı") {
		t.Fatalf("hata: %q", got)
	}
	if l := exceptionsSourceLinks(ev); len(l) != 1 || l[0].Href != "/problems?exc=a" || l[0].Label != "BSAException · bsa-cards" {
		t.Fatalf("bağlantılar: %+v", l)
	}
}

// Anlatım gerçek copilot yolundan geçer: kayıt bloğu modele gider, dönen metin
// anlatımdır.
func TestNarrateRecordsCallsModel(t *testing.T) {
	llm := newLoopLLM(t, func(int, map[string]any) map[string]any {
		return map[string]any{"content": "CRMHouse'a ait 2 vserver var: 10.240.84.231, 10.240.84.233."}
	})
	s, _ := newLoopTestServer(t, llm, "u-rec")
	var deltas strings.Builder
	got := s.narrateRecords(t.Context(), func(ev string, d any) {
		if ev == "delta" {
			deltas.WriteString(d.(map[string]string)["text"])
		}
	}, "CRMHouse ait vserver adreslerini getir", "yüklenen tablo satırları", "| 1 | lb-a | 10.240.84.231 | CRMHouse |")
	// Delta yalnız akış destekleyen sağlayıcıda gelir (sahte sağlayıcı tek parça döner).
	if !strings.Contains(got, "10.240.84.233") {
		t.Fatalf("anlatım %q, delta %q", got, deltas.String())
	}
	calls := llm.calls()
	if len(calls) != 1 {
		t.Fatalf("LLM çağrı sayısı %d", len(calls))
	}
	if body := mustJSON(t, calls[0]); !strings.Contains(body, "KAYITLAR") || !strings.Contains(body, "10.240.84.231") {
		t.Fatalf("istem kayıtları taşımalı: %s", body)
	}
}
