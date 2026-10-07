package api

// rootcause_bubble_gate.go — v0.10.1119 (inceleme düzeltmesi): kök-neden
// yollarındaki ServiceBubbleUp taramaları için SÜREÇ GENELİ yuva tavanı.
//
// Neden: kök-neden hesapları istek iptalinden koparıldı (rootCauseComputeCtx)
// — çekmeceyi kapatan istemci paylaşılan hesabı öldürmesin diye. Kopuk iş
// tavansız olursa, hızla açılıp kapanan çekmeceler (ya da çok sayıda farklı
// problem) arkada istemcisi olmayan ham spans taramalarını üst üste
// biriktirebilir. Tavan: aynı anda en çok rootCauseBubbleSlots tarama
// (/rootcause/bubbleup, tam /rootcause ve onların SWR tazelemeleri ORTAK).
//
// Sözleşme:
//   - Yuva, ÖZGÜN context (canlı istek ya da SWR tazelemesinin kendi
//     context'i) yaşarken beklenir; bekleyen iptal edilirse tarama HİÇ
//     başlamaz ve sonuç önbelleğe yazılmaz.
//   - Kopma (WithoutCancel + bütçe) ancak yuva alındıktan SONRA olur.
//   - Bekleme rootCauseBubbleWait'i aşarsa ret: bubbleup ucu hata döner
//     (önbelleğe yazılmaz); tam demet eski davranışla bubbleUp'sız döner ama
//     gövde uncacheable() — taze diye saklanmaz.
//   - Bekleme / ret / iptal sayaçları + log satırı.

import (
	"context"
	"errors"
	"log"
	"sync/atomic"
	"time"
)

// rootCauseBubbleSlots — eşzamanlı kök-neden bubbleUp taraması tavanı (pod
// başına). Her tarama kendi içinde ≤ 6 anahtar sorgusu + totals ∥ keşif
// koşar; 3 yuva ≈ en çok ~20 eşzamanlı ham spans sorgusu.
const rootCauseBubbleSlots = 3

// rootCauseBubbleWait — yuva için en uzun bekleme. bubbleup ucunun bütçesi
// (85 sn) + bu bekleme istemcinin 95 sn'lik zaman aşımının altında kalır.
var rootCauseBubbleWait = 8 * time.Second

var rootCauseBubbleSem = make(chan struct{}, rootCauseBubbleSlots)

// errRootCauseBubbleBusy — yuva bekleme süresi doldu.
var errRootCauseBubbleBusy = errors.New("rootcause bubbleup: tarama yuvaları dolu (bekleme süresi doldu)")

// rootCauseBubbleGateStats — süreç sayaçları (log + test).
var rootCauseBubbleGateStats struct {
	waited    atomic.Int64 // yuva hemen boş değildi, beklendi
	rejected  atomic.Int64 // bekleme süresi doldu
	cancelled atomic.Int64 // bekleyen iptal edildi
}

// acquireRootCauseBubbleSlot — waitCtx yaşarken yuva bekler. Dönen release
// tam bir kez çağrılmalı.
func acquireRootCauseBubbleSlot(waitCtx context.Context) (func(), error) {
	release := func() { <-rootCauseBubbleSem }
	select {
	case rootCauseBubbleSem <- struct{}{}:
		return release, nil
	default:
	}
	if err := waitCtx.Err(); err != nil {
		rootCauseBubbleGateStats.cancelled.Add(1)
		return nil, err
	}
	n := rootCauseBubbleGateStats.waited.Add(1)
	start := time.Now()
	timer := time.NewTimer(rootCauseBubbleWait)
	defer timer.Stop()
	select {
	case rootCauseBubbleSem <- struct{}{}:
		if d := time.Since(start); d > time.Second {
			log.Printf("[rootcause] bubbleUp yuvası %s beklendi (toplam bekleme=%d)", d.Round(time.Millisecond), n)
		}
		return release, nil
	case <-waitCtx.Done():
		c := rootCauseBubbleGateStats.cancelled.Add(1)
		log.Printf("[rootcause] bubbleUp yuvası beklerken iptal: %v (toplam iptal=%d)", waitCtx.Err(), c)
		return nil, waitCtx.Err()
	case <-timer.C:
		r := rootCauseBubbleGateStats.rejected.Add(1)
		log.Printf("[rootcause] bubbleUp reddedildi: %d yuva %s dolu kaldı (toplam ret=%d)", rootCauseBubbleSlots, rootCauseBubbleWait, r)
		return nil, errRootCauseBubbleBusy
	}
}

// logRootCauseBudget — hesap bütçesi aşıldıysa uyarı (alt-okumalar yumuşak
// düşer; bu satır olmadan kısmi demet sessiz kalırdı).
func logRootCauseBudget(ctx context.Context, part, anchor string, budget time.Duration) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		log.Printf("[rootcause] WARN %s bütçesi (%s) aşıldı: %s — süre dolan alt-okumalar boş döndü", part, budget, anchor)
	}
}
