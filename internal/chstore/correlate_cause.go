package chstore

import "sort"

// correlate_cause.go — v0.10.1063. "Ne başka değişti" satırlarının hangisi
// kök-neden manşetine ("olası yukarı/aşağı akış yayılımı") ÇIKABİLİR.
//
// Operatör bildirimi: "<svc-B> ile ilgili olduğunu düşünüyor ama alakasız."
// Bileşik skor yönsüz büyüklük (hata %76.8 → %0 düşüşü, skorun ¾'ü) ve
// topolojiden habersiz; panel en üst satırı "propagation" diye manşete
// taşıyordu. Kural İKİ koşul, ikisi de zorunlu:
//
//  1. KENAR — özneyle analiz penceresinde (taban + cari) topology_edges_5m
//     kenarı var. Kenarsız "yayılım" iddiası uydurmadır. YALNIZ DOĞRUDAN
//     kenar sayılır: kuyruk üzerinden bağlı üretici→tüketici (svc → queue →
//     svc) ya da iki atlamalı komşu "bağlı" DEĞİL — bilinçli dar tutuldu,
//     yanlış iddia yerine iddia yok.
//  2. YÖN, konuma göre:
//     - Aşağı akış (özne adayı çağırıyor) ya da iki yönlü: aday kötüleşmiş
//       (ChangeWorse) ya da trafiği kesilmiş (ChangeLost). Kötüleşen /
//       sönen bağımlılık çağıranın gecikmesini/hatasını açıklayabilir.
//     - Yukarı akış (aday özneyi çağırıyor): YALNIZ trafik sıçraması (hacim
//       kapılı > +%25) — özneye yük bindirmesi. Çağıranın p99/hata artışı ya
//       da trafik kaybı öznenin SONUCUDUR (etki alanı; rca/extras.go
//       "çağıranlar kök neden adayı değildir", sentezleyicide upstream ×0.6).
//     İyileşen (better), sakinleşen (quieter) ve yönü okunamayan (unknown)
//     satır hiçbir konumda uygun değil.
//
// Satır listeden DÜŞMEZ ve skoru DEĞİŞMEZ (büyüklük dürüst kalır); yalnız
// sıra: uygun adaylar önce, sonra kalanlar, iyileşen/sakinleşenler en sonda.
//
// topoKnown=false (topoloji okunamadı) → hiçbir satır uygun DEĞİL. Yön
// bilinçli: kenarı doğrulanmamış yayılım iddiası tam bu hatanın kendisi;
// panel "bağlantı doğrulanamadı" der (RootCause.TopologyKnown), hipotez
// işçisinin kendi adayları (ayrı yol) etkilenmez.

// Özneye göre konum.
const (
	RelationUpstream   = "upstream"   // aday özneyi çağırıyor
	RelationDownstream = "downstream" // özne adayı çağırıyor
	RelationBoth       = "both"
)

// MarkCorrelationCauses — Relation + CauseEligible basar ve yeniden sıralar.
// SAF; girdi dilimine dokunmaz (yeni dilim döner), kesmez (tavan çağıranda,
// işaretlemeden SONRA). edges: özne odaklı topoloji okuması
// (GetServiceGraphTopN(subject, …)) — öznenin geçmediği kenarlar yok sayılır.
func MarkCorrelationCauses(cs []ChangedService, subject string, edges []ServiceEdge, topoKnown bool) []ChangedService {
	out := make([]ChangedService, len(cs))
	copy(out, cs)
	rel := map[string]string{}
	add := func(svc, r string) {
		if svc == "" || svc == subject {
			return
		}
		if prev, ok := rel[svc]; ok && prev != r {
			rel[svc] = RelationBoth
			return
		}
		rel[svc] = r
	}
	if topoKnown && subject != "" {
		for _, e := range edges {
			switch subject {
			case e.Source:
				add(e.Target, RelationDownstream)
			case e.Target:
				add(e.Source, RelationUpstream)
			}
		}
	}
	for i := range out {
		out[i].Relation = rel[out[i].Service]
		out[i].CauseEligible = out[i].Service != subject && causeEligible(out[i])
	}
	group := func(c ChangedService) int {
		switch {
		case c.CauseEligible:
			return 0
		case c.Direction == ChangeBetter || c.Direction == ChangeQuieter:
			return 2
		default: // worse / lost / unknown / boş
			return 1
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return group(out[i]) < group(out[j]) })
	return out
}

// causeEligible — konum × yön kuralı (yukarıdaki şerh).
func causeEligible(c ChangedService) bool {
	degraded := c.Direction == ChangeWorse || c.Direction == ChangeLost
	switch c.Relation {
	case RelationDownstream, RelationBoth:
		return degraded
	case RelationUpstream:
		return c.surge
	default:
		return false
	}
}
