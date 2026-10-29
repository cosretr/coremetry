package rca

// v0.10.1577 — RenderBubbleUpTR tek gövde (CoSRE demeti + arka plan problem
// açıklaması). Bayt pinlerinin asıl tablosu api/copilot_bubbleup_test.go'da
// (sarmalayıcı üzerinden, değişmeden yeşil); burada render girdisinin iki
// yeni yolu: Window metni aynen geçer, Unreadable sınıfı / sonuç yok.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestRenderBubbleUpTRInputs(t *testing.T) {
	res := &chstore.BubbleUpResult{SelectionTotal: 40, BaselineTotal: 900, Attributes: []chstore.BubbleUpAttribute{
		{Key: "http.route", Values: []chstore.BubbleUpValue{{Value: "/v1/pay-now", SelectionPct: 0.8, BaselinePct: 0.11, Score: 0.69}}},
	}}
	cases := []struct {
		name string
		in   BubbleUpRender
		want string
	}{
		{"pencere metni aynen", BubbleUpRender{ErrorFamily: true, Window: "problemin açılışını izleyen 10 dk", Result: res},
			"Ayrışan boyutlar (BubbleUp) — hatalı span'ler aynı penceredeki TÜM span'lerle kıyaslandı; problemin açılışını izleyen 10 dk, hatalı 40 / tüm 900 span:\n" +
				"  - http.route=/v1/pay-now: hatalı kümede %80, tüm span'lerde %11\n" +
				"Bu satırlar HESAPLANMIŞ dağılım farkıdır: sorun bu değerlerde yoğunlaşıyor demektir, tek başına sebep kanıtı değildir.\n"},
		{"okunamadı sınıfı sonuçtan önce gelir", BubbleUpRender{Unreadable: "timeout", Result: res},
			"Ayrışan boyutlar (BubbleUp) OKUNAMADI (timeout) — sorunun belirli bir boyutta (rota, pod, sürüm…) yoğunlaşıp yoğunlaşmadığı BİLİNMİYOR; bu konuda sonuç çıkarma.\n"},
		{"sonuç yok", BubbleUpRender{},
			"Ayrışan boyutlar (BubbleUp) OKUNAMADI (sonuç yok) — sorunun belirli bir boyutta (rota, pod, sürüm…) yoğunlaşıp yoğunlaşmadığı BİLİNMİYOR; bu konuda sonuç çıkarma.\n"},
	}
	for _, c := range cases {
		if got := RenderBubbleUpTR(c.in); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	long := strings.Repeat("ş", 200)
	if got := truncateValue(long, BubbleUpRenderValueMax); len(got) > BubbleUpRenderValueMax+len("…") || strings.ContainsRune(got, '\uFFFD') || !strings.HasSuffix(got, "…") {
		t.Errorf("bayt bütçeli, rune-güvenli kırpma: %q", got)
	}
}
