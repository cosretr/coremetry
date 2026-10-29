package api

// chat_clarify_test.go — v0.10.1571 (operatör, 2026-10-29: "çipler çok
// kullanılmıyor, soru cevap şeklinde frontier model gibi olsun"): düz metin
// netleştirme sorusunun SAF çekirdeği — hangi soru kaynak sorusunu tetikler,
// serbest metin cevap nasıl sınıflanır, bekleyen sorunun TTL'i.

import (
	"testing"
	"time"
)

func TestSourceAmbiguous(t *testing.T) {
	for _, c := range []struct {
		q    string
		want bool
	}{
		// Telemetri şekilli, wiki işareti yok → iki kaynak da makul.
		{"operasyon kataloğundaki servisler", true},
		{"servis sahipliği kimde", true},
		{"log saklama süresi ne kadar", true},
		{"metrik isimlendirme kuralı", true},
		// Açık wiki işareti → wiki kademesi zaten aradı.
		{"namespace nasıl oluşturulur", false},
		{"deploy runbook'u nedir", false},
		{"wikide servis sağlığı nasıl izlenir", false},
		{"how to rotate kafka certs", false},
		// Telemetri adı yok.
		{"türkiyenin başkenti neresi", false},
		{"merhaba nasılsın", false},
		{"bu neden böyle", false},
		// Tek sözcük / boş.
		{"servisler", false},
		{"", false},
		// Trace kimliği → guided trace_by_id.
		{"4bf92f3577b34da6a3ce929d0e0e4736 trace hatası", false},
	} {
		if got := sourceAmbiguous(c.q); got != c.want {
			t.Errorf("sourceAmbiguous(%q) = %v, want %v", c.q, got, c.want)
		}
	}
	// Ön koşul: belirsiz satırlar guided'a oturmuyor (oturan soru kapıya gelmez).
	for _, q := range []string{"operasyon kataloğundaki servisler", "servis sahipliği kimde", "log saklama süresi ne kadar", "metrik isimlendirme kuralı"} {
		if r := routeGuidedIntentExt(q, []string{"orders-api", "billing-api"}, nil, nil, "", false, nil); r.Intent != guidedNone {
			t.Errorf("%q guided'a oturuyor (%s) — kapı ölçülemez", q, r.Intent)
		}
	}
}

func TestClassifySourceReply(t *testing.T) {
	for _, c := range []struct {
		in, want string
	}{
		{"wiki", srcWiki},
		{"Wiki", srcWiki},
		{"wikide ara", srcWiki},
		{"dokümanda", srcWiki},
		{"wiki'de bak", srcWiki},
		{"önce wiki lütfen", srcWiki},
		{"runbook", srcWiki},
		{"coremetry", srcTelemetry},
		{"Coremetry'de", srcTelemetry},
		{"servislerde", srcTelemetry},
		{"veride", srcTelemetry},
		{"telemetri", srcTelemetry},
		{"canlı veride ara", srcTelemetry},
		{"Coremetry verisinde", srcTelemetry},
		// Cevap değil.
		{"namespace nasıl oluşturulur", ""},
		{"orders-api sağlığı nasıl?", ""},
		{"hayır başka bir şey", ""},
		{"wiki ve coremetry", ""}, // iki liste
		{"evet", ""},              // dolgu yalnız
		{"bunu wiki ve coremetry ikisinde de bir ara bakalım", ""},
		{"", ""},
	} {
		if got := classifySourceReply(c.in); got != c.want {
			t.Errorf("classifySourceReply(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPendingAskValid(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	src := func(age time.Duration) *chatPendingAsk {
		return &chatPendingAsk{Kind: pendingKindSource, Question: "q", AskedAt: now.Add(-age).UnixNano()}
	}
	for _, c := range []struct {
		name string
		p    *chatPendingAsk
		want bool
	}{
		{"taze", src(time.Second), true},
		{"tam TTL", src(chatPendingTTL), true},
		{"TTL+1ns", src(chatPendingTTL + time.Nanosecond), false},
		{"11 dk", src(11 * time.Minute), false},
		{"saat kayması 30 sn ileri", src(-30 * time.Second), true},
		{"saat kayması 2 dk ileri", src(-2 * time.Minute), false},
		{"nil", nil, false},
		{"soru boş", &chatPendingAsk{Kind: pendingKindSource, AskedAt: now.UnixNano()}, false},
		{"bilinmeyen tür", &chatPendingAsk{Kind: "x", Question: "q", AskedAt: now.UnixNano()}, false},
		{"zaman yok", &chatPendingAsk{Kind: pendingKindSource, Question: "q"}, false},
		{"sentez trace'siz", &chatPendingAsk{Kind: pendingKindTraceSynth, Fingerprint: "ora:1", AskedAt: now.UnixNano()}, false},
		{"sentez tam", &chatPendingAsk{Kind: pendingKindTraceSynth, TraceID: "t", Fingerprint: "ora:1", AskedAt: now.UnixNano()}, true},
	} {
		if got := pendingAskValid(c.p, now); got != c.want {
			t.Errorf("%s: pendingAskValid = %v, want %v", c.name, got, c.want)
		}
	}
	// takePending: süresi dolmuş soru da SİLİNİR.
	st := &chatContextState{ctx: ChatContext{Pending: src(11 * time.Minute)}}
	if _, ok := st.takePending(now); ok || st.ctx.Pending != nil || !st.dirty {
		t.Fatalf("süresi dolmuş soru silinmeli: ok=%v pending=%v dirty=%v", ok, st.ctx.Pending, st.dirty)
	}
	var nilSt *chatContextState
	if _, ok := nilSt.takePending(now); ok {
		t.Fatal("nil durum")
	}
	if !(ChatContext{Pending: src(0)}).Empty() {
		t.Fatal("bekleyen soru çalışma kümesi değil — Empty görmemeli")
	}
}
