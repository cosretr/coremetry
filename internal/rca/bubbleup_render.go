package rca

// bubbleup_render.go — v0.10.1577 (dış skill denetimi V1 dilim 2): BubbleUp
// sonucunun Türkçe prompt metni, TEK gövde. İki okuyucu: CoSRE kök-neden
// demetinin BubbleUp adımı (api/copilot_bubbleup.go, v0.10.992 — metin orada
// doğdu, bayt pinleri copilot_bubbleup_test.go'da) ve arka plan problem
// açıklamasının derin kanıt bloğu (anomaly/investigation.go — sentezleyicinin
// kalıcı yazdığı DeepEvidence.BubbleUp). İkinci bir kopya iki yüzeyin modele
// aynı kanıtı farklı sözcüklerle söylemesi demekti.
//
// Sözleşme: en çok BubbleUpRenderCap boyut, yalnız ≥ BubbleUpRenderMinScore
// (5 puan) ayrışan; "ayrışma yok", "kıyas kurulamadı" ve "okunamadı" AYRI
// söylenir (yokluk ≠ okunamadı). Birim dönüşümü TopBubbleUp'ta (v0.10.991).

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	// BubbleUpRenderCap — metne giren en çok boyut (katalogla aynı: 3).
	BubbleUpRenderCap = 3
	// BubbleUpRenderMinScore — en küçük ayrışma (pay farkı, oran): 5 puan.
	// Katalog > 0 kullanır (hakem her satırı tartar); küçük modele giden
	// metinde %0,4'lük fark "ayrışma" diye sunulursa uydurma sebep olur.
	BubbleUpRenderMinScore = 0.05
	// BubbleUpRenderValueMax — değer kırpma, BAYT bütçesi (URL / SQL uzun olur).
	BubbleUpRenderValueMax = 80
)

// BubbleUpRender — RenderBubbleUpTR girdisi.
type BubbleUpRender struct {
	// ErrorFamily — true: hatalı alt küme tüm span'lere karşı; false: pencere
	// ÖNCEKİ eş-boy pencereye karşı.
	ErrorFamily bool
	// Window — pencerenin insan metni ("son 10 dk", "problemin açılışını
	// izleyen 10 dk").
	Window string
	// Unreadable — boş değilse okuma düştü; değer sınıfıdır (sourcestate:
	// timeout, unreachable…). Result yokken boşsa "sonuç yok" sayılır.
	Unreadable string
	Result     *chstore.BubbleUpResult
}

// RenderBubbleUpTR — SAF. Satır sonuyla biter; başlık hep "Ayrışan boyutlar
// (BubbleUp)".
func RenderBubbleUpTR(r BubbleUpRender) string {
	const title = "Ayrışan boyutlar (BubbleUp)"
	bu := r.Result
	if r.Unreadable != "" || bu == nil {
		why := r.Unreadable
		if why == "" {
			why = "sonuç yok"
		}
		return fmt.Sprintf("%s OKUNAMADI (%s) — sorunun belirli bir boyutta (rota, pod, sürüm…) yoğunlaşıp yoğunlaşmadığı BİLİNMİYOR; bu konuda sonuç çıkarma.\n", title, why)
	}
	win := r.Window
	var head, selWord, baseWord string
	if r.ErrorFamily {
		head = fmt.Sprintf("hatalı span'ler aynı penceredeki TÜM span'lerle kıyaslandı; %s, hatalı %d / tüm %d span", win, bu.SelectionTotal, bu.BaselineTotal)
		selWord, baseWord = "hatalı kümede", "tüm span'lerde"
	} else {
		head = fmt.Sprintf("bu pencere ÖNCEKİ eş-boy pencereyle kıyaslandı; %s, bu pencere %d / önceki %d span", win, bu.SelectionTotal, bu.BaselineTotal)
		selWord, baseWord = "bu pencerede", "önceki pencerede"
	}
	switch {
	case bu.SelectionTotal == 0 && r.ErrorFamily:
		return fmt.Sprintf("%s: bu pencerede (%s) hatalı span yok — kıyas kurulamadı.\n", title, win)
	case bu.SelectionTotal == 0:
		return fmt.Sprintf("%s: bu pencerede (%s) span yok — kıyas kurulamadı.\n", title, win)
	case bu.BaselineTotal == 0:
		return fmt.Sprintf("%s: önceki pencerede span yok — kıyas kurulamadı (servis yeni başlamış ya da trafik yeni gelmiş olabilir).\n", title)
	}
	tops := TopBubbleUp(bu, BubbleUpRenderCap, BubbleUpRenderMinScore)
	if len(tops) == 0 {
		return fmt.Sprintf("%s — %s: belirgin ayrışma YOK (hiçbir attribute değeri en az 5 puan fazla temsil edilmiyor) — sorun tek bir rota / pod / sürüme özgü görünmüyor.\n", title, head)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s:\n", title, head)
	for _, t := range tops {
		fmt.Fprintf(&b, "  - %s=%s: %s %%%.0f, %s %%%.0f\n", t.Key, truncateValue(t.Value, BubbleUpRenderValueMax), selWord, t.SelPct, baseWord, t.BasePct)
	}
	b.WriteString("Bu satırlar HESAPLANMIŞ dağılım farkıdır: sorun bu değerlerde yoğunlaşıyor demektir, tek başına sebep kanıtı değildir.\n")
	return b.String()
}

// truncateValue — BAYT bütçeli, rune sınırında kesen kırpma (api.truncate'in
// v0.9.842 sözleşmesi: yarım rune → U+FFFD olmaz).
func truncateValue(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
