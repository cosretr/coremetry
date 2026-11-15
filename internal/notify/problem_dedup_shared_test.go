package notify

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/cache/cachetest"
)

// v0.10.2231 (Redis R5) — filo geneli tekrar tabanı.

// İki pod (lider devri / worker + sre) aynı Redis'te: ikinci gönderen
// bastırılır; gerçek eskalasyon geçer; salınım geçmez; çözülme sonrası
// yeniden açılış geçer.
func TestSharedDedupAcrossInstances(t *testing.T) {
	mem := cachetest.NewMem()
	a, b, c := &Notifier{}, &Notifier{}, &Notifier{}
	for _, n := range []*Notifier{a, b, c} {
		n.SetDedupCache(mem)
	}
	const p, ch = "prob-1", "chan-1"
	if ok, _ := a.allowChannelSend(p, ch, "open", "warning", 100); !ok {
		t.Fatal("ilk gönderim geçmeli")
	}
	if ok, _ := b.allowChannelSend(p, ch, "open", "warning", 100); ok {
		t.Fatal("ikinci pod aynı bildirimi göndermemeli")
	}
	if ok, _ := b.allowChannelSend(p, "chan-2", "open", "warning", 100); !ok {
		t.Fatal("başka kanal etkilenmemeli")
	}
	if ok, _ := b.allowChannelSend(p, ch, "open", "critical", 100); !ok {
		t.Fatal("warning→critical eskalasyonu geçmeli")
	}
	if ok, _ := c.allowChannelSend(p, ch, "open", "critical", 100); ok {
		t.Fatal("eskalasyon da filo genelinde bir kez")
	}
	if ok, _ := c.allowChannelSend(p, ch, "open", "warning", 100); ok {
		t.Fatal("critical sonrası warning (salınım) başka pod'da da geçmemeli")
	}
	// Çözülme (a işledi): problemin filo damgaları da geçersiz; aynı
	// kimlik/started_at ile yeniden açılış meşru haber. (b/c'nin yerel
	// defteri bugünkü gibi kendi pod'unda — kural değişmedi.)
	a.forgetProblem(p)
	if ok, _ := a.allowChannelSend(p, ch, "open", "warning", 100); !ok {
		t.Fatal("çözülme sonrası yeniden açılış geçmeli")
	}
	// Nesil sayacı TTL'le düşse de eski değere dönmez.
	mem.Expire(notifySharedGenPfx + notifyHash(p))
	time.Sleep(2 * time.Millisecond) // üretimde düşüş ≥ 2 sa sonra; aynı ms olamaz
	a.forgetProblem(p)
	if ok, _ := a.allowChannelSend(p, ch, "open", "info", 100); !ok {
		t.Fatal("yeniden kurulan nesil eski damgalara takılmamalı")
	}
}

// Redis kesik / bağlı değil: bugünkü süreç içi davranış (hata = gönder).
func TestSharedDedupFallsBackOpen(t *testing.T) {
	mem := cachetest.NewMem()
	mem.Down.Store(true)
	a, b := &Notifier{}, &Notifier{}
	a.SetDedupCache(mem)
	for _, n := range []*Notifier{a, b} {
		if ok, _ := n.allowChannelSend("prob-2", "chan-1", "open", "critical", 1); !ok {
			t.Fatal("Redis yokken her pod gönderir (alarm kaybolmaz)")
		}
		if ok, _ := n.allowChannelSend("prob-2", "chan-1", "open", "critical", 1); ok {
			t.Fatal("süreç içi taban aynen çalışmalı")
		}
	}
}
