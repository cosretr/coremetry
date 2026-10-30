package api

// explain_trace_critpath.go — v0.10.1627 (CoSRE kritik yol planı R2,
// operatör onaylı): trace açıklamasının kanıt paketine deterministik KRİTİK
// YOL bloğu.
//
// Semptom: yavaş ama SAĞLIKLI bir trace'te (HATA ZİNCİRİ boş) modele hiçbir
// deterministik gecikme çapası gitmiyordu; kompakt span satırı (traceLite)
// url.full / server.address / peer.service / db.name / messaging.* taşımaz
// (bir izin listesi değil, v0.9.462'den beri bilinçli izdüşüm). Model 12 s'lik
// dış gateway beklemesini kök SERVER süresiyle karıştırabiliyordu.
//
// Çözüm (v0.10.1371/1379 "span JSON'u değil, ayrı blok" kararının uzantısı):
// tracechain.CriticalPathOf TÜM span'ler üstünden (100'lük kesimden önce)
// hesaplanır; blok User'ın başına HATA ZİNCİRİ'nin ardından konur (çekmece
// kırpması baştan tutar). traceExplainUser'a dokunulmaz (golden pin). Tek
// adımlı yolda blok yazılmaz: anlatacak bir "en büyük pay" yok ve tek-span
// trace'lerin kanıtı bayt bayt eskisi gibi kalır.

import (
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/tracechain"
)

// traceCritMinSteps — blok için gereken en az kritik yol adımı.
const traceCritMinSteps = 2

// traceCriticalPathOf — SAF: span'ler → kritik yol (tracechain).
func traceCriticalPathOf(spans []chstore.SpanRow) tracechain.CriticalPath {
	return tracechain.CriticalPathOf(spans)
}

// traceCriticalHead — SAF: User başına giren blok ("\n\n" ile biter; yoksa
// "") ve en büyük paylı adım (blok yoksa nil).
func traceCriticalHead(cp tracechain.CriticalPath) (string, *tracechain.PathStep) {
	if !cp.OK || len(cp.Steps) < traceCritMinSteps {
		return "", nil
	}
	b := cp.Block()
	top, ok := cp.TopStep()
	if b == "" || !ok {
		return "", nil
	}
	return b + "\n\n", &top
}

// traceCriticalFollowUp — SAF: çekmece takibine giren blok ("\n\n" ile
// başlar; yoksa "") — ilk cevabın gördüğünün aynısı.
func traceCriticalFollowUp(spans []chstore.SpanRow) string {
	head, _ := traceCriticalHead(traceCriticalPathOf(spans))
	if head == "" {
		return ""
	}
	return "\n\n" + head[:len(head)-2]
}
