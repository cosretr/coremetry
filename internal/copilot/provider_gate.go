package copilot

// provider_gate.go — v0.10.1564 (dış skill denetimi L1, operatör onayı
// 2026-10-29): model sunucusuna giden çağrılara EŞZAMANLILIK KAPISI.
//
// KUSUR: tek GPU'lu yerel model (gemma) sohbeti, ✨ Explain'i ve arka plan
// açıklayıcılarını (problem / exception otomatik açıklaması, AI SRE
// araştırma anlatımı, evalset koşusu) aynı anda alıyordu; copilot.Service'te
// hiçbir sınır yoktu. Arka plan turu GPU'yu doldurduğunda operatörün sohbeti
// sıraya giriyor ya da sunucu 503 dönüyordu.
//
// KARAR — sağlayıcı sunucusu (host) başına ağırlıklı kapı:
//
//   etkileşimli  aynı anda en çok gateInteractiveMax
//   arka plan    aynı anda en çok gateBackgroundMax; etkileşimli bir çağrı
//                UÇUŞTA ya da BEKLİYORKEN yeni arka plan çağrısı başlamaz
//
// Uçuştaki arka plan çağrısı kesilmez (yarım cevap israftır); yalnız yenisi
// bekler. Sınıf ctx'teki CallMeta.Surface'ten: Ayarlar'ın "arka plan" profil
// grubu (surfaceGroups[SurfaceGroupBackground]) + evalset koşuları; geri
// kalan her şey etkileşimli. Host başına: bulut profili (Anthropic/OpenAI)
// yerel GPU'yu beklemez.
//
// Kapı taşıyıcıda (tracedTransport.RoundTrip): sağlayıcı paketindeki DOKUZ
// HTTP çağrısının hepsi (tek atış, akış, araçlı döngü) cfg.HTTPClient.Do'dan
// geçer ve istemcinin tek kurulum yeri buildCopilotHTTPClient — kapı tek
// yerde, atlanamaz. Akışlı cevapta slot GÖVDE KAPANANA dek tutulur (üretim
// orada sürer). Bekleme isteğin ctx'ine bağlı: istemci zaman aşımı ya da
// iptal bekleyeni de bırakır. Uzun bekleme loga düşer (overload sebebi).

import (
	"context"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	gateInteractiveMax = 2
	gateBackgroundMax  = 1
	// gateWaitLog — bu süreden uzun bekleyen çağrı loglanır.
	gateWaitLog = 2 * time.Second
)

// gateClass — çağrının kapı sınıfı.
type gateClass int

const (
	gateInteractive gateClass = iota
	gateBackground
)

func (c gateClass) String() string {
	if c == gateBackground {
		return "background"
	}
	return "interactive"
}

// gateClassOf — SAF: yüzeyden sınıf. Arka plan = Ayarlar'ın arka plan profil
// grubu + evalset koşuları; bilinmeyen / boş yüzey etkileşimli (yanlış yönde
// hata operatörü bekletmez, arka planı bekletir).
func gateClassOf(surface string) gateClass {
	if strings.HasPrefix(surface, chstore.AICallEvalsetSurfacePrefix) {
		return gateBackground
	}
	for _, s := range surfaceGroups[SurfaceGroupBackground] {
		if surface == s {
			return gateBackground
		}
	}
	return gateInteractive
}

// hostGate — tek sağlayıcı sunucusunun sayaçları.
type hostGate struct {
	inter, back int // uçuşta
	interWait   int // bekleyen etkileşimli
}

// canStart — SAF: bu sınıf şimdi başlayabilir mi.
func (g *hostGate) canStart(c gateClass) bool {
	if c == gateBackground {
		return g.back < gateBackgroundMax && g.inter == 0 && g.interWait == 0
	}
	return g.inter < gateInteractiveMax
}

// providerGate — host → sayaç; durum değişince bekleyenler uyandırılır
// (changed kanalı kapatılıp yenilenir).
type providerGate struct {
	mu      sync.Mutex
	hosts   map[string]*hostGate
	changed chan struct{}
}

func newProviderGate() *providerGate {
	return &providerGate{hosts: map[string]*hostGate{}, changed: make(chan struct{})}
}

// gate — süreç-genelinde tek kapı (tüm profiller, host başına sayaç).
var gate = newProviderGate()

func (p *providerGate) host(h string) *hostGate {
	g := p.hosts[h]
	if g == nil {
		g = &hostGate{}
		p.hosts[h] = g
	}
	return g
}

// broadcastLocked — bekleyenleri uyandırır. Çağıran mu'yu tutar.
func (p *providerGate) broadcastLocked() {
	close(p.changed)
	p.changed = make(chan struct{})
}

// acquire — slot alır (ctx bitene dek bekler); dönen release TEK kez etkili.
func (p *providerGate) acquire(ctx context.Context, host string, c gateClass) (func(), time.Duration, error) {
	start := time.Now()
	p.mu.Lock()
	g := p.host(host)
	waiting := false
	for !g.canStart(c) {
		if c == gateInteractive && !waiting {
			waiting = true
			g.interWait++
		}
		ch := p.changed
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			p.mu.Lock()
			if waiting {
				g.interWait--
				p.broadcastLocked() // arka plan bekleyeni artık başlayabilir
			}
			p.mu.Unlock()
			return nil, time.Since(start), ctx.Err()
		case <-ch:
		}
		p.mu.Lock()
	}
	if waiting {
		g.interWait--
	}
	if c == gateBackground {
		g.back++
	} else {
		g.inter++
	}
	p.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			p.mu.Lock()
			if c == gateBackground {
				g.back--
			} else {
				g.inter--
			}
			p.broadcastLocked()
			p.mu.Unlock()
		})
	}
	return release, time.Since(start), nil
}

// gatedBody — gövde kapanınca (ya da EOF'ta) slotu bırakır.
type gatedBody struct {
	io.ReadCloser
	release func()
}

func (b *gatedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.release()
	}
	return n, err
}

func (b *gatedBody) Close() error {
	err := b.ReadCloser.Close()
	b.release()
	return err
}

// gatedRoundTrip — kapı + taşıyıcı. Hata yolunda slot hemen bırakılır.
func gatedRoundTrip(rt http.RoundTripper, r *http.Request) (*http.Response, error) {
	surface := MetaFromContext(r.Context()).Surface
	c := gateClassOf(surface)
	release, waited, err := gate.acquire(r.Context(), r.URL.Host, c)
	if err != nil {
		log.Printf("[copilot] provider gate: %s call (surface=%q) gave up after %s waiting for a slot on %s: %v",
			c, surface, waited.Round(time.Millisecond), r.URL.Host, err)
		return nil, err
	}
	if waited >= gateWaitLog {
		log.Printf("[copilot] provider gate: %s call (surface=%q) waited %s for a slot on %s (overload)",
			c, surface, waited.Round(time.Millisecond), r.URL.Host)
	}
	resp, err := rt.RoundTrip(r)
	if err != nil {
		release()
		return nil, err
	}
	if resp.Body == nil || resp.Body == http.NoBody {
		release()
		return resp, nil
	}
	resp.Body = &gatedBody{ReadCloser: resp.Body, release: release}
	return resp, nil
}
