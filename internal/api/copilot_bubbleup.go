package api

// copilot_bubbleup.go — v0.10.992 — dış skill denetimi 2026-09-19 V1, dilim 1
// (docs/audit/external-skills-audit-2026-09-19.md; Honeycomb: "BubbleUp en
// değerli adım, sebep bariz görünse de atlanmaz").
//
// BubbleUp motoru (chstore/bubbleup.go) /rootcause panelinde ve verdict
// hakeminin kataloğunda zaten vardı; CoSRE'nin "neden X bozuldu" demeti
// (guidedRootCauseBundle) onu hiç görmüyordu — model "hangi rota / pod /
// sürümde yoğunlaşıyor" sorusuna kanıtsız kalıyordu. Bu dosya:
//
//   - serviceBubbleUp → chstore.ServiceBubbleUp: kıyasın TEK kurulduğu yer.
//     Hata ailesi → aynı pencerede hatalı span'ler tüm span'lere karşı;
//     diğerleri → pencere, ÖNCEKİ eş-boy pencereye karşı (v0.9.1063
//     zaman-kaydırmalı kıyas). /rootcause'un iki fan-out'u ve katalog
//     toplayıcı da artık bunu çağırır (davranış aynı).
//   - planGuidedBubbleUp (SAF): demetin kıyas şekli + penceresi. Pencere HEP
//     rca.ExtrasWindow (10 dk — verdict kataloğuyla aynı): açık problem varsa
//     onun AÇILIŞINI izleyen 10 dk ("açılışta ne değişti"), yoksa sorunun
//     SON 10 dakikası.
//   - renderBubbleUpTR (SAF): küçük model için prefetch metni — en çok 3
//     boyut, yalnız ≥5 puan ayrışan; "ayrışma yok" ve "okunamadı" AYRI söylenir
//     (yokluk ≠ okunamadı).
//
// Maliyet: ham spans taraması — bu yüzden soru başına BİR kez, 10 dakikalık
// pencere (/rootcause paneli 1 saate kadar tarar ve ~40 sn sürebiliyor; sohbet
// cevabı onu bekleyemez) ve rca.BubbleUpTimeout (8 sn) tavanıyla; süre
// dolarsa adım "okunamadı" der, demet düşmez. Arka plan işçisine (sentezleyici) TAŞINMADI:
// o dilim açık problem × tik başına ham tarama demek, ayrı karar ister.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rca"
	"github.com/cilcenk/coremetry/internal/sourcestate"
)

const (
	// guidedBubbleUpCap — demet metnine giren en çok boyut (katalogla aynı: 3).
	guidedBubbleUpCap = 3
	// guidedBubbleUpMinScore — en küçük ayrışma (pay farkı, oran): 5 puan.
	// Katalog > 0 kullanır (hakem her satırı tartar); küçük modele giden
	// prefetch'te %0,4'lük fark "ayrışma" diye sunulursa uydurma sebep olur.
	guidedBubbleUpMinScore = 0.05
	// guidedBubbleUpValueMax — değer kırpma (URL / SQL attribute'ları uzun olur).
	guidedBubbleUpValueMax = 80
)

// serviceBubbleUp — servis kapsamlı BubbleUp kıyası (dosya başı). Kıyasın
// kendisi chstore.ServiceBubbleUp'ta: MCP `bubble_up` aracı da aynısını çağırır.
func (s *Server) serviceBubbleUp(ctx context.Context, service string, errorFamily bool, started, end time.Time) (*chstore.BubbleUpResult, error) {
	return s.store.ServiceBubbleUp(ctx, service, errorFamily, started, end)
}

// guidedBubbleUpPlan — demetin BubbleUp kıyası.
type guidedBubbleUpPlan struct {
	ErrorFamily bool      // true: hatalı alt küme; false: önceki pencereye karşı
	Started     time.Time // seçim penceresi
	End         time.Time
	Anchored    bool // pencere açık bir problemin açılışına çıpalı
}

// planGuidedBubbleUp — SAF (tablo testli). Açık problem varsa İLKİ (liste
// öncelik sıralı) çıpadır: aile onun metriğinden, pencere açılışını izleyen
// 10 dk. Problem yoksa pencere sorunun son 10 dakikası; aile, pencerede hata
// varsa hata alt kümesi (en çok sorulan "neden hata veriyor"), yoksa zaman
// kıyası.
func planGuidedBubbleUp(probs []chstore.Problem, curErrors uint64, to time.Time) guidedBubbleUpPlan {
	if len(probs) > 0 {
		p := probs[0]
		started := time.Unix(0, p.StartedAt)
		return guidedBubbleUpPlan{
			ErrorFamily: exemplarKindForMetric(p.Metric) == chstore.ExemplarError,
			Started:     started, End: started.Add(rca.ExtrasWindow), Anchored: true,
		}
	}
	return guidedBubbleUpPlan{ErrorFamily: curErrors > 0, Started: to.Add(-rca.ExtrasWindow), End: to}
}

// compare — adım çipinin argüman yankısı.
func (p guidedBubbleUpPlan) compare() string {
	if p.ErrorFamily {
		return "errors_vs_all"
	}
	return "window_vs_previous"
}

// renderBubbleUpTR — SAF: demet metni (dosya başı). err ≠ nil → "OKUNAMADI"
// (sınıfıyla); seçim ya da taban boş → kıyas kurulamadı; ayrışma yok → açıkça
// "YOK"; aksi hâlde en çok guidedBubbleUpCap satır.
func renderBubbleUpTR(bu *chstore.BubbleUpResult, plan guidedBubbleUpPlan, err error) string {
	const title = "Ayrışan boyutlar (BubbleUp)"
	if err != nil || bu == nil {
		why := "sonuç yok"
		if err != nil {
			why = string(sourcestate.Classify(err))
		}
		return fmt.Sprintf("%s OKUNAMADI (%s) — sorunun belirli bir boyutta (rota, pod, sürüm…) yoğunlaşıp yoğunlaşmadığı BİLİNMİYOR; bu konuda sonuç çıkarma.\n", title, why)
	}
	mins := int(plan.End.Sub(plan.Started).Minutes())
	win := fmt.Sprintf("son %d dk", mins)
	if plan.Anchored {
		win = fmt.Sprintf("problemin açılışını izleyen %d dk", mins)
	}
	var head, selWord, baseWord string
	if plan.ErrorFamily {
		head = fmt.Sprintf("hatalı span'ler aynı penceredeki TÜM span'lerle kıyaslandı; %s, hatalı %d / tüm %d span", win, bu.SelectionTotal, bu.BaselineTotal)
		selWord, baseWord = "hatalı kümede", "tüm span'lerde"
	} else {
		head = fmt.Sprintf("bu pencere ÖNCEKİ eş-boy pencereyle kıyaslandı; %s, bu pencere %d / önceki %d span", win, bu.SelectionTotal, bu.BaselineTotal)
		selWord, baseWord = "bu pencerede", "önceki pencerede"
	}
	switch {
	case bu.SelectionTotal == 0 && plan.ErrorFamily:
		return fmt.Sprintf("%s: bu pencerede (%s) hatalı span yok — kıyas kurulamadı.\n", title, win)
	case bu.SelectionTotal == 0:
		return fmt.Sprintf("%s: bu pencerede (%s) span yok — kıyas kurulamadı.\n", title, win)
	case bu.BaselineTotal == 0:
		return fmt.Sprintf("%s: önceki pencerede span yok — kıyas kurulamadı (servis yeni başlamış ya da trafik yeni gelmiş olabilir).\n", title)
	}
	tops := rca.TopBubbleUp(bu, guidedBubbleUpCap, guidedBubbleUpMinScore)
	if len(tops) == 0 {
		return fmt.Sprintf("%s — %s: belirgin ayrışma YOK (hiçbir attribute değeri en az 5 puan fazla temsil edilmiyor) — sorun tek bir rota / pod / sürüme özgü görünmüyor.\n", title, head)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s:\n", title, head)
	for _, t := range tops {
		fmt.Fprintf(&b, "  - %s=%s: %s %%%.0f, %s %%%.0f\n", t.Key, truncate(t.Value, guidedBubbleUpValueMax), selWord, t.SelPct, baseWord, t.BasePct)
	}
	b.WriteString("Bu satırlar HESAPLANMIŞ dağılım farkıdır: sorun bu değerlerde yoğunlaşıyor demektir, tek başına sebep kanıtı değildir.\n")
	return b.String()
}

// guidedBubbleUpStep — kök-neden demetinin BubbleUp adımı: çip + kanıt +
// prompt metni. Soft-fail: okuma düşerse metin "OKUNAMADI" der, demet sürer.
// Pencerede hiç span yokken (ve çıpa olacak problem yokken) adım atlanır —
// taranacak bir şey yok, demet bunu zaten söylüyor.
func (s *Server) guidedBubbleUpStep(ctx context.Context, emit func(string, any), b *strings.Builder, service string, probs []chstore.Problem, cx *aiServiceContext, to time.Time) {
	var curErrors uint64
	if cx != nil && cx.curErr == nil {
		if cx.Current.Spans == 0 && len(probs) == 0 {
			return
		}
		curErrors = cx.Current.ErrorCount
	}
	plan := planGuidedBubbleUp(probs, curErrors, to)
	n := emitGuidedStep(emit, "bubble_up", `{"service":"`+service+`","compare":"`+plan.compare()+`"}`)
	bctx, cancel := context.WithTimeout(ctx, rca.BubbleUpTimeout)
	defer cancel()
	bu, err := s.serviceBubbleUp(bctx, service, plan.ErrorFamily, plan.Started, plan.End)
	text := renderBubbleUpTR(bu, plan, err)
	emitGuidedStepResult(emit, n, "bubble_up", text, err)
	b.WriteString("\n")
	b.WriteString(text)
}
