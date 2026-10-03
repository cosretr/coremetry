package api

// rca_extras.go — RCA kanıt kataloğu GENİŞLEMESİ (v0.9.1203, AI Faz
// 6.1, onaylı plan K4a).
//
// /rootcause fan-out'u BubbleUp/BlastRadius/Correlations'ı hesaplayıp
// panelde çiziyordu ama verdict hakemi HİÇBİRİNİ görmüyordu —
// "hesaplanıp hiç anlatılmayan" sınıfı. Üçü artık E-uzayına katalog
// satırı olarak girer; shields + attribution + feedback OLDUĞU GİBİ
// çalışır (halüsinasyon koruması bedava — K4a'nın seçilme gerekçesi).
//
// Nedensellik ayrımı BİLİNÇLİ:
//   - Correlations komşuları OLASI NEDENDİR → entity dolu, K3 beyaz
//     listesi (ve şemanın root_cause.entity enum'u) GENİŞLER. v0.10.1090:
//     YALNIZ causeEligible (özneyle kenarlı + konuma göre kötüleşen)
//     satırlar; iyileşen / bağlantısız olanlar adsız tek satıra iner.
//   - Blast çağıranları MAĞDURDUR, neden değil → entity boş; adları
//     yalnız gösterilen-jeton yoluyla meşrulaşır (model zincirde
//     anabilir ama kök neden İLAN EDEMEZ).
//   - BubbleUp değerleri boyuttur, servis değil → entity boş.
//
// Maliyet: blast + correlations MV/state okumaları (saniye-altı);
// BubbleUp ham spans taraması olduğundan kendi 8 sn tavanıyla koşar ve
// hepsi soft-fail — kanıt toplanamazsa katalog o aile OLMADAN kurulur
// (dürüst yokluk), verdict düşmez. Hepsi 10 dk'lık verdict cache'inin
// arkasında, ankor başına bir kez.

import (
	"context"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rca"
)

// gatherRCACatalogExtras — üç kaynağı paralel, soft-fail toplar.
// anchorStartNs=0 (ankor satırı çözülememiş) ⇒ pencere kurulamaz,
// dürüstçe boş döner — eski pencereye uydurma kanıt bağlamayız.
func (s *Server) gatherRCACatalogExtras(ctx context.Context, h *chstore.RootCauseHypothesis, anchorStartNs int64) rcaCatalogExtras {
	var out rcaCatalogExtras
	if h == nil || h.Service == "" || anchorStartNs <= 0 {
		return out
	}
	from := time.Unix(0, anchorStartNs)
	to := from.Add(rca.ExtrasWindow)

	winSec := int(rca.ExtrasWindow.Seconds())
	var cs []chstore.ChangedService
	var topoEdges []chstore.ServiceEdge
	topoKnown := false

	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		if br, err := s.store.GetServiceBlastRadius(ctx, h.Service, from, to); err == nil && br.TotalCallers > 0 {
			out.Blast = &br
		}
	}()
	// v0.10.1090 — /rootcause ile AYNI işaretleme: 50'lik havuz (aynı
	// sorgu) + öznenin topoloji komşuluğu (rootCauseTopo). Katalog yalnız
	// causeEligible satırı "kötüleşen komşu" (olası neden) yapar; 1063'e dek
	// burası skora göre ilk 3'ü yönsüz / kenarsız alıyordu.
	go func() {
		defer wg.Done()
		if got, err := s.store.GetCorrelatedChangesMVTop(ctx, from, winSec, 4*winSec, chstore.ChangedServicesMarkPool); err == nil {
			cs = got
		}
	}()
	go func() {
		defer wg.Done()
		topoEdges, topoKnown = s.rootCauseTopo(ctx, h.Service, from, to, 4*winSec)
	}()
	go func() {
		defer wg.Done()
		bctx, cancel := context.WithTimeout(ctx, rca.BubbleUpTimeout)
		defer cancel()
		// v0.10.992 — hata alt kümesi kıyası serviceBubbleUp'ta (ortak).
		if bu, err := s.serviceBubbleUp(bctx, h.Service, true, from, to); err == nil {
			out.BubbleUp = bu
		}
	}()
	wg.Wait()
	if cs != nil {
		out.Correlations = chstore.MarkCorrelationCauses(cs, h.Service, topoEdges, topoKnown)
	}
	out.TopologyKnown = topoKnown
	return out
}
