package api

// chat_context_subject_test.go — v0.10.2138 (CoSRE router boşlukları D5,
// docs/cosre/router-gaps-audit.md C + H). Prod: devam soruları önceki turun
// trace / operasyon / servis öznesini bulamıyordu ("bu tracete …", "hangi iç
// işlemde", "neden gecikmiş olabilir?"). Yalnız v2; v1 bağlam JSON'u aynı.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPronounSubject(t *testing.T) {
	cases := map[string]string{
		"bu tracete client correlation id nedir":               "trace",
		"hayır. bu trace altında giden db çağrılarını listele": "trace",
		"bu operasyon neden yavaş":                             "operation",
		"bu endpointin p99'u ne":                               "operation",
		"peki bu servis öncesinde nereden çağrılıyor?":         "service",
		"bu servisin hataları":                                 "service",
		"bunun loglarına bak":                                  "",
		"bugün trace sayısı":                                   "", // "bu" sınırı: "bugün" değil
		"svc-a trace detail":                                   "",
	}
	for q, want := range cases {
		if got := pronounSubject(normalizeGuidedMsg(q)); got != want {
			t.Errorf("pronounSubject(%q) = %q, want %q", q, got, want)
		}
	}
}

func TestV2SubjectCue(t *testing.T) {
	cases := []struct {
		q    string
		want bool
	}{
		{"neden gecikmiş olabilir?", true},
		{"peki bu servis öncesinde nereden çağrılıyor?", true},
		{"hangi iç işlemde zaman harcanıyor", true},
		{"aradaki boş zamanlar ne", true},
		{"bu tracete client correlation id nedir", true},
		{"peki bu ne demek?", true}, // eski isFollowUpCue; devralma yine katkı ister
		{"merhaba", false},
		{"kubernetes nedir", false},
		{strings.Repeat("uzun bir soru ", 8) + "neden", false}, // >80 rune
	}
	for _, c := range cases {
		if got := v2SubjectCue(normalizeGuidedMsg(c.q)); got != c.want {
			t.Errorf("v2SubjectCue(%q) = %v, want %v", c.q, got, c.want)
		}
	}
}

func TestV2InheritService(t *testing.T) {
	q := normalizeGuidedMsg("neden gecikmiş olabilir?")
	cases := []struct {
		name                     string
		v2, prior                bool
		screen, ctxSvc, explicit string
		q                        string
		want                     string
	}{
		{"v2 devam → bağlam servisi", true, true, "", "svc-a", "", q, "svc-a"},
		{"v1 → yok", false, true, "", "svc-a", "", q, ""},
		{"ilk tur → yok", true, false, "", "svc-a", "", q, ""},
		{"ekran servisi kazanır", true, true, "svc-b", "svc-a", "", q, ""},
		{"açık servis kazanır", true, true, "", "svc-a", "svc-b", q, ""},
		{"bağlam boş", true, true, "", "", "", q, ""},
		{"filo sözcüğü", true, true, "", "svc-a", "", normalizeGuidedMsg("tüm servislerde neden gecikme var?"), ""},
		{"ipucu yok", true, true, "", "svc-a", "", normalizeGuidedMsg("kubernetes nedir"), ""},
	}
	for _, c := range cases {
		if got := v2InheritService(c.v2, c.prior, c.screen, c.ctxSvc, c.explicit, c.q); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestV2InheritTrace(t *testing.T) {
	const tid = "4bf92f3577b34da6a3ce929d0e0e4736"
	dem := normalizeGuidedMsg("bu tracete client correlation id nedir")
	inner := normalizeGuidedMsg("hangi iç işlemde zaman harcanıyor")
	cases := []struct {
		name          string
		v2, prior     bool
		screen        bool
		explain, ctxT string
		last, q       string
		ok            bool
	}{
		{"v2 zamir → bağlam trace'i", true, true, false, "", tid, "", dem, true},
		{"v1 → yok", false, true, false, "", tid, "", dem, false},
		{"ilk tur → yok", true, false, false, "", tid, "", dem, false},
		{"ekran trace'i kazanır", true, true, true, "", tid, "", dem, false},
		{"çekmece açıklaması varsa yok", true, true, false, "KONU: …", tid, "", dem, false},
		{"geçersiz id yazılmaz/okunmaz", true, true, false, "", "abc", "", dem, false},
		{"iç işlem + son rota trace", true, true, false, "", tid, string(guidedTraceByID), inner, true},
		{"iç işlem ama son rota servis", true, true, false, "", tid, string(guidedServiceHealth), inner, false},
		{"zamirsiz trace sözcüğü (svc-a trace detail) devralmaz", true, true, false, "", tid, "", normalizeGuidedMsg("svc-a trace detail"), false},
	}
	for _, c := range cases {
		got, ok := v2InheritTrace(c.v2, c.prior, c.screen, c.explain, c.ctxT, c.last, c.q)
		if ok != c.ok || (ok && got != tid) {
			t.Errorf("%s: got (%q,%v) want ok=%v", c.name, got, ok, c.ok)
		}
	}
}

func TestV2InheritOperation(t *testing.T) {
	q := normalizeGuidedMsg("bu operasyon neden yavaş")
	if got := v2InheritOperation(true, true, "", "OP_NAME_REST", q); got != "OP_NAME_REST" {
		t.Errorf("v2 zamir: %q", got)
	}
	if got := v2InheritOperation(false, true, "", "OP_NAME_REST", q); got != "" {
		t.Errorf("v1: %q", got)
	}
	if got := v2InheritOperation(true, true, "SCREEN_OP", "OP_NAME_REST", q); got != "" {
		t.Errorf("ekran operasyonu kazanır: %q", got)
	}
	if got := v2InheritOperation(true, true, "", "OP_NAME_REST", normalizeGuidedMsg("bu servis neden yavaş")); got != "" {
		t.Errorf("servis zamiri operasyon devralmaz: %q", got)
	}
}

func TestHasCallerAsk(t *testing.T) {
	cases := map[string]bool{
		"peki bu servis öncesinde nereden çağrılıyor?": true,
		"svc-a kimden çağrıldı":                        true,
		"svc-a kim tarafından çağrılıyor":              true,
		"svc-a hangi servislerden çağrılmış":           true,
		"/app/x/y kaç kez çağrıldı":                    false,
		"svc-a çağrı sayısı":                           false,
		"svc-a nereden geliyor":                        false,
	}
	for q, want := range cases {
		if got := hasCallerAsk(guidedTokens(normalizeGuidedMsg(q))); got != want {
			t.Errorf("hasCallerAsk(%q) = %v, want %v", q, got, want)
		}
	}
	if got := v2RouterQuestion("svc-a kimden çağrıldı", false); got != "svc-a kimden çağrıldı" {
		t.Errorf("v1 router girdisi değişmemeli: %q", got)
	}
}

// Bağlam yaması: yalnız geçerli trace yazılır, operasyon tavanlı; v1'in
// yazdığı bağlam (alanlar boş) JSON'da yeni anahtar taşımaz.
func TestContextSubjectPatches(t *testing.T) {
	const tid = "4BF92F3577B34DA6A3CE929D0E0E4736"
	c := v2SubjectPatch(ChatContext{}, guidedRoute{Intent: guidedTraceByID, TraceID: tid})
	if c.Trace != strings.ToLower(tid) {
		t.Errorf("trace yaması: %q", c.Trace)
	}
	if c := v2SubjectPatch(ChatContext{}, guidedRoute{Intent: guidedServiceHealth, Service: "svc-a"}); c.Trace != "" {
		t.Errorf("trace dışı rota trace yazmamalı: %q", c.Trace)
	}
	if c := withContextTrace(ChatContext{}, "not-a-trace"); c.Trace != "" {
		t.Errorf("geçersiz id: %q", c.Trace)
	}
	c = withContextOperation(ChatContext{}, strings.Repeat("X", 500), []string{"svc-a"})
	if len([]rune(c.Operation)) > chatContextOperationMax+1 || c.Service != "svc-a" {
		t.Errorf("operasyon tavanı/servis: %d %q", len([]rune(c.Operation)), c.Service)
	}
	if c := withContextOperation(ChatContext{Service: "svc-z"}, "OP", []string{"svc-a", "svc-b"}); c.Service != "svc-z" {
		t.Errorf("çok servisli hedef bağlam servisini ezmemeli: %q", c.Service)
	}
	b, _ := json.Marshal(ChatContext{Service: "svc-a", RangeS: 3600})
	if strings.Contains(string(b), "trace") || strings.Contains(string(b), "operation") {
		t.Errorf("boş özne alanları JSON'a girmemeli: %s", b)
	}
	if p := chatContextPreambleTR(ChatContext{Trace: strings.ToLower(tid)}); !strings.Contains(p, "- trace: ") {
		t.Errorf("önsöz trace satırı: %q", p)
	}
	if p := chatContextPreambleTR(ChatContext{}); p != "" {
		t.Errorf("boş bağlam önsözü boş kalmalı: %q", p)
	}
}

// Audit H "doğrulanmadı" maddesi: "Hayır. …" ile başlayan uzun cümle bekleyen
// netleştirmenin CEVABI olarak tüketilmez (her tür için düşer) — cümle yeni soru
// olarak akar. Kısa ret ("hayır gerek yok") trace sentez sorusunun cevabıdır.
func TestClarifyLongNegativeSentenceIsNotConsumed(t *testing.T) {
	long := "Hayır. bu trace altında giden db çağrılarını listele"
	for _, kind := range []string{pendingKindTraceSynth, pendingKindSource, pendingKindDocOrEntity} {
		p := &chatPendingAsk{Kind: kind, Question: "svc-a hataları", AskedAt: time.Now().UnixNano()}
		if got := clarifyReplyClass(p, long); got != clarifyReplyDropped {
			t.Errorf("%s: uzun 'Hayır. …' cümlesi cevap sayıldı (%q)", kind, got)
		}
	}
	if got := clarifyReplyClass(&chatPendingAsk{Kind: pendingKindTraceSynth}, "hayır gerek yok"); got != synthReplyNo {
		t.Errorf("kısa ret sentez cevabı olmalı: %q", got)
	}
}
