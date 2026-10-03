package api

// v0.10.1090 regresyon testi — v0.10.1063'ün açık bıraktığı boşluk: hakem
// kataloğu (rca/extras.go "kötüleşen komşu") skora göre ilk 3 korelasyonu
// yönsüz + kenarsız alıyordu; iyileşen (svc-b: hata %76.8 → %0, trafik
// −%99.5) ya da bağlantısız servis "kötüleşen komşu" diye sunulup
// root_cause.entity enum'una giriyordu. Artık YALNIZ causeEligible satır
// aday; kalanlar ADSIZ tek satır (ne entity ne gösterilen jeton).

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestCatalogExtCorrelationsOnlyCauseEligible(t *testing.T) {
	ex := extTestExtras()
	ex.Correlations = []chstore.ChangedService{
		// Skor sırası: iyileşen + bağlantısız satırlar EN ÜSTTE.
		{Service: "svc-b", Score: 404, ErrDeltaPct: -100, RateDeltaPct: -99.5, P99DeltaPct: -93.7, Direction: chstore.ChangeBetter, Relation: chstore.RelationDownstream},
		{Service: "svc-u", Score: 220, P99DeltaPct: 310, Direction: chstore.ChangeWorse}, // kenarsız
		{Service: "svc-q", Score: 90, RateDeltaPct: -60, Direction: chstore.ChangeQuieter, Relation: chstore.RelationDownstream},
		{Service: "svc-x", Score: 62, P99DeltaPct: 140, Direction: chstore.ChangeWorse, Relation: chstore.RelationDownstream, CauseEligible: true},
		{Service: "svc-l", Score: 50, RateDeltaPct: -98, Direction: chstore.ChangeLost, Relation: chstore.RelationDownstream, CauseEligible: true},
	}
	ex.TopologyKnown = true
	cat := buildRCAEvidenceCatalogExt(extTestHypothesis(), ex)
	all := renderRCAEvidenceCatalog(cat)

	for _, bad := range []string{"svc-b", "svc-u", "svc-q"} {
		if cat.Entities[bad] {
			t.Errorf("%q beyaz listeye GİRMEMELİ (iyileşen / bağlantısız): %+v", bad, cat.Entities)
		}
		if strings.Contains(all, bad) {
			t.Errorf("%q katalog metninde ADIYLA geçmemeli (gösterilen jeton olurdu):\n%s", bad, all)
		}
	}
	for _, want := range []string{
		"aynı pencerede kötüleşen komşu: svc-x (p99 Δ+140%",
		"aynı pencerede trafiği kesilen komşu: svc-l",
		"aynı pencerede değişen ama bağlantısız / iyileşen 3 servis daha var — özneyle topoloji kenarı yok ya da kötüleşmedi",
		"Bunlar kök neden adayı DEĞİLDİR.",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("katalog %q içermeli:\n%s", want, all)
		}
	}
	if !cat.Entities["svc-x"] || !cat.Entities["svc-l"] {
		t.Errorf("bağlı + kötüleşen / trafiği kesilen beyaz listede olmalı: %+v", cat.Entities)
	}
	// Şema enum'u aynı kaynaktan: iyileşen servis kök neden İLAN EDİLEMEZ.
	for _, e := range rcaAllowedEntities(cat) {
		if e == "svc-b" || e == "svc-u" || e == "svc-q" {
			t.Errorf("root_cause.entity enum'unda %q var", e)
		}
	}
	// Modelin adsız satırdaki servisi adıyla anması K3'e takılır.
	var sh rcaShieldReport
	checkRCAEntities(cat, []string{"kök neden svc-b servisinde"}, &sh)
	if len(sh.UnknownEntities) == 0 {
		t.Error("katalogda adı geçmeyen svc-b K3'ten geçmemeli")
	}
}

// Topoloji okunamadı: kimse uygun değil, adsız satır "doğrulanamadı" der
// ("bağlı değil" demek doğrulanmamış bir iddia olurdu).
func TestCatalogExtCorrelationsTopologyUnknown(t *testing.T) {
	ex := extTestExtras()
	ex.Correlations = []chstore.ChangedService{
		{Service: "svc-u", Score: 220, P99DeltaPct: 310, Direction: chstore.ChangeWorse},
	}
	ex.TopologyKnown = false
	cat := buildRCAEvidenceCatalogExt(extTestHypothesis(), ex)
	all := renderRCAEvidenceCatalog(cat)
	if cat.Entities["svc-u"] || strings.Contains(all, "svc-u") {
		t.Errorf("topoloji okunamadıysa svc-u aday olamaz:\n%s", all)
	}
	if !strings.Contains(all, "1 servis daha var — özneyle bağlantıları doğrulanamadı (topoloji okunamadı)") {
		t.Errorf("dürüst 'doğrulanamadı' satırı yok:\n%s", all)
	}
	// İşaretlenmemiş (eski) satır da aday değildir — güvenli yön.
	ex.Correlations = []chstore.ChangedService{{Service: "svc-z", Score: 99, Direction: chstore.ChangeWorse}}
	if c := buildRCAEvidenceCatalogExt(extTestHypothesis(), ex); c.Entities["svc-z"] {
		t.Error("CauseEligible taşımayan satır beyaz listeye girdi")
	}
	// Değişen başka servis yoksa adsız satır da yok.
	ex.Correlations = nil
	if out := renderRCAEvidenceCatalog(buildRCAEvidenceCatalogExt(extTestHypothesis(), ex)); strings.Contains(out, "servis daha var") {
		t.Errorf("korelasyon yokken adsız satır basıldı:\n%s", out)
	}
}

// Uçtan uca saf hat: gatherRCACatalogExtras'ın yaptığı gibi
// MarkCorrelationCauses → katalog. Kenarlı ama iyileşen ve kenarsız
// kötüleşen satır aday olmaz; kenarlı kötüleşen olur.
func TestCatalogExtMarkThenBuild(t *testing.T) {
	raw := []chstore.ChangedService{
		{Service: "svc-b", Score: 404, Direction: chstore.ChangeBetter},
		{Service: "svc-u", Score: 220, Direction: chstore.ChangeWorse},
		{Service: "svc-x", Score: 62, Direction: chstore.ChangeWorse},
	}
	edges := []chstore.ServiceEdge{
		{Source: "checkout", Target: "svc-x"},
		{Source: "checkout", Target: "svc-b"},
	}
	ex := rcaCatalogExtras{Correlations: chstore.MarkCorrelationCauses(raw, "checkout", edges, true), TopologyKnown: true}
	cat := buildRCAEvidenceCatalogExt(extTestHypothesis(), ex)
	if !cat.Entities["svc-x"] || cat.Entities["svc-b"] || cat.Entities["svc-u"] {
		t.Fatalf("işaretleme sonrası beyaz liste yanlış: %+v", cat.Entities)
	}
}
