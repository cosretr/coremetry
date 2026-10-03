package rca

// extras.go — katalog genişlemesinin SAF yarısı (v0.9.1203 Faz 6.1;
// v0.9.1208'de internal/rca'ya indi — Faz 6.3b: problem-auto-explain
// aynı katalog makinesini kullanır, api↔anomaly import yönü gereği saf
// çekirdek paylaşılan pakette yaşar). Toplama (IO) api tarafında kaldı
// (gatherRCACatalogExtras).

import (
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	// ExtrasWindow — kanıt penceresi: ankorun AÇILIŞINI izleyen dilim
	// (correlate.go'nun 600 sn varsayılanıyla aynı; ≥300 sn olduğu için
	// correlations her zaman MV yolunda).
	ExtrasWindow = 10 * time.Minute
	// extrasCorrCap / extrasBubbleCap / extrasBlastWorst —
	// plandaki "ilk 3" kapları. Sabitler ID determinizminin parçası.
	extrasCorrCap    = 3
	extrasBubbleCap  = 3
	extrasBlastWorst = 3
	// BubbleUpTimeout — ham-spans kıyası verdict kurulumunu
	// süresiz bekletemez; süre dolarsa aile atlanır.
	BubbleUpTimeout = 8 * time.Second
)

// CatalogExtras — katalog genişlemesinin girdileri. Sıfır değeri
// "hiçbiri toplanamadı" demek ve katalog bugünkü hâliyle kurulur.
type CatalogExtras struct {
	Blast *chstore.BlastRadius
	// Correlations — MarkCorrelationCauses'tan GEÇMİŞ satırlar (v0.10.1090):
	// katalog yalnız CauseEligible olanı aday yapar; işaretsiz satır aday
	// değildir (güvenli yön).
	Correlations []chstore.ChangedService
	// TopologyKnown — işaretleme için topoloji okundu mu; adsız "aday
	// değil" satırının dürüst gerekçesi ("bağlı değil" / "doğrulanamadı").
	TopologyKnown bool
	BubbleUp      *chstore.BubbleUpResult
}

// BuildEvidenceCatalogExt — taban kataloğu kurar, üç yeni aileyi
// SONUNA ekler. Taban builder'a dokunulmaz: mevcut E/N kimlikleri
// bayt-bayt aynı kalır (10 dk cache'li verdict'in kimlik-kararlılık
// sözleşmesi, rca_evidence.go:89-91) — yeni aileler sayaçları taban
// kataloğun kaldığı yerden devralır.
func BuildEvidenceCatalogExt(h *chstore.RootCauseHypothesis, extras CatalogExtras) EvidenceCatalog {
	cat := BuildEvidenceCatalog(h)
	posN := len(cat.PositiveIDs())
	addPos := func(entity, text string) {
		posN++
		ref := EvidenceRef{ID: fmt.Sprintf("E%d", posN), Kind: Positive, Entity: entity, Text: text}
		cat.Refs = append(cat.Refs, ref)
		cat.byID[ref.ID] = ref
		if entity != "" {
			cat.Entities[entity] = true
		}
	}
	anchor := ""
	if h != nil {
		anchor = h.Service
	}

	// ── 5. Etki alanı (BlastRadius) — tek satır, MAĞDURLAR ────────────
	if b := extras.Blast; b != nil && b.TotalCallers > 0 {
		worst := make([]string, 0, extrasBlastWorst)
		for i, c := range b.Callers {
			if i >= extrasBlastWorst {
				break
			}
			t := fmt.Sprintf("%s (hata %%%.1f", c.Service, c.ErrorRate)
			if c.HasOpenProblem {
				t += ", açık problemi var"
			}
			t += ")"
			worst = append(worst, t)
		}
		txt := fmt.Sprintf("etki alanı: %d çağıran servis", b.TotalCallers)
		if b.CascadingCallers > 0 {
			txt += fmt.Sprintf(" (%d'sinde kaskad problem)", b.CascadingCallers)
		}
		if len(worst) > 0 {
			txt += " — öne çıkanlar: " + strings.Join(worst, ", ")
		}
		txt += ". Bunlar ETKİLENENLERDİR; etki alanı genişliği ciddiyeti gösterir, çağıranlar kök neden adayı değildir."
		addPos("", txt)
	}

	// ── 6. Aynı pencerede kötüleşen BAĞLI komşular (Correlations) ─────
	// Olası nedenler: entity dolu → beyaz liste (ve şema enum'u) genişler.
	// v0.10.1090 — YALNIZ CauseEligible satır (özneyle topoloji kenarı +
	// konuma göre kötüleşen / trafiği kesilen; MarkCorrelationCauses).
	// v0.10.1063 manşeti düzeltti ama burası skora göre ilk 3'ü yönsüz ve
	// kenarsız alıyordu: iyileşen, bağlantısız servis hakeme "kötüleşen
	// komşu" diye sunulup beyaz listeye giriyordu. Kalanlar ADSIZ tek
	// satırda: ne entity ne gösterilen jeton olurlar, model yalnız "başka
	// servis de kıpırdadı ama neden değil" bilgisini alır.
	added, other := 0, 0
	for _, cs := range extras.Correlations {
		if cs.Service == "" || cs.Service == anchor {
			continue
		}
		if !cs.CauseEligible {
			other++
			continue
		}
		if added >= extrasCorrCap {
			continue
		}
		added++
		what := "kötüleşen"
		if cs.Direction == chstore.ChangeLost {
			what = "trafiği kesilen"
		}
		addPos(cs.Service, fmt.Sprintf(
			"aynı pencerede %s komşu: %s (p99 Δ%+.0f%%, hata Δ%+.1f%%, istek Δ%+.0f%%)",
			what, cs.Service, cs.P99DeltaPct, cs.ErrDeltaPct, cs.RateDeltaPct))
	}
	if other > 0 {
		why := "özneyle topoloji kenarı yok ya da kötüleşmedi (iyileşen / trafiği azalan)"
		if !extras.TopologyKnown {
			why = "özneyle bağlantıları doğrulanamadı (topoloji okunamadı)"
		}
		addPos("", fmt.Sprintf(
			"aynı pencerede değişen ama bağlantısız / iyileşen %d servis daha var — %s. Bunlar kök neden adayı DEĞİLDİR.",
			other, why))
	}

	// ── 7. Hatalarda ayrışan boyutlar (BubbleUp) ──────────────────────
	// Boyut değerleri servis değildir → entity boş; tireli değerler
	// gösterilen-jeton yoluyla K3'te meşrulaşır (checkRCAEntities).
	// v0.10.991 — seçim ve birim TopBubbleUp'ta (bubbleup.go): chstore payları
	// ORAN (0–1); eski satır onları doğrudan %-biçimiyle basıp "%1 … %0"
	// yazıyordu. Puanı ≤ 0 olan (hatalarda AYRIŞMAYAN) boyut kanıt değildir.
	if bu := extras.BubbleUp; bu != nil && bu.SelectionTotal > 0 {
		for _, t := range TopBubbleUp(bu, extrasBubbleCap, 0) {
			addPos("", fmt.Sprintf(
				"hatalarda ayrışan boyut: %s=%s (hatalı kümede %%%.0f, tabanda %%%.0f)",
				t.Key, t.Value, t.SelPct, t.BasePct))
		}
	}

	return cat
}
