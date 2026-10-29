package api

// rootcause_bubble_mode.go — v0.10.1495 (operatör, prod Problem detayı):
// `runtime:jvm-gc:<svc>:<pod>` problemi "WHERE ERRORS CONCENTRATE — top
// attribute over the error spans" gösterdi. Ama GC problemi hatayla ilgili
// değil ve sunucu o problemde HATALI span alt kümesini değil, zaman-kaydırmalı
// kıyası koşuyordu — başlık yanlış, bölüm anlamsız.
//
// Bölüm problem TÜRÜNDEN seçilir, TEK yerde (bu SAF işlev) ve cevapla
// birlikte istemciye gider (`bubbleMode`); panel kendi tahminini yapmaz:
//
//   - errors  — hata problemi: aynı pencerede hatalı span'ler tüm span'lere
//     karşı ("hatalar nerede yoğunlaşıyor"). Eski davranış.
//   - window  — gecikme / trafik / diğer servis metrikleri: pencere, ÖNCEKİ
//     eş-boy pencereye karşı ("bu pencerede ne öne çıktı"). Hata dili YOK.
//   - none    — span attribute'larının açıklayamadığı türler: runtime (JVM GC,
//     heap, thread — tek pod'un süreci), veritabanı kapasite/sağlık, monitor,
//     self-health, servis olmayan özne. Ham spans taraması HİÇ koşmaz.

import "github.com/cilcenk/coremetry/internal/chstore"

// v0.10.1577 — karar chstore.ProblemBubbleMode'a taşındı (sentezleyici de
// okuyor); burada yalnız api'nin string sabitleri + ince sarmalayıcı kalır.
const (
	bubbleModeErrors = string(chstore.BubbleModeErrors)
	bubbleModeWindow = string(chstore.BubbleModeWindow)
	bubbleModeNone   = string(chstore.BubbleModeNone)
)

// rootCauseBubbleMode — chstore.ProblemBubbleMode sarmalayıcısı.
func rootCauseBubbleMode(p *chstore.Problem) string {
	return string(chstore.ProblemBubbleMode(p))
}

// anomalyBubbleMode — SAF: anomali çıpası. trace_op hata oranı anomalisidir.
func anomalyBubbleMode(kind string) string {
	if exemplarKindForAnomaly(kind) == chstore.ExemplarError {
		return bubbleModeErrors
	}
	return bubbleModeWindow
}
