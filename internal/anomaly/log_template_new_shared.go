package anomaly

// log_template_new_shared.go — v0.10.2069 (eski log_template_new → ML
// new_template taşıması). "Bu kalıp yeni mi" kararının ML'e açılan girişi.
// ÇATAL YOK: eski kayıtçının adımlarıyla AYNI iç işlevler, aynı sırayla —
//   1. maskerQuietGate (v0.10.1829 / MaskerVersion 3: sürüm geçişinin 24 sa
//      sessiz penceresi; blob okunamazsa fail-closed),
//   2. detectNewLogTemplates (v0.10.1030 aile kararı + v0.10.2040 kv kimlik
//      maskesi, ön ek imzası ≥ 6 belirteç / ilk 48 karakter / ≥ 24 harf,
//      Jaccard, JSON `message`),
//   3. filterNewTemplateNoise (v0.10.2028 ≥ 20 satır + batch servis hariç,
//      v0.10.2029 batch'te çoğunluk ERROR+ kalıp).
// Fark yalnız açma anahtarı: eski yol anomaly_sensitivity.logTemplateNew'e,
// ML yolu kendi dedektör anahtarına bakar (çağıran kapıyı uygular).
//
// Okuma: CH log_templates defteri (templater puller'ın yazdığı), tik başına
// en çok 2 sınırlı okuma (aday + aday varsa bilinen). Elasticsearch'e
// DOKUNMAZ.

import (
	"context"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/templater"
)

// NewLogTemplateFamilies — kapısız ortak karar (yukarıdaki üç adım). cut =
// gürültü tabanının kestiği aday sayısı.
func NewLogTemplateFamilies(ctx context.Context, store *chstore.Store, window time.Duration) ([]LogTemplateAnomaly, int, error) {
	if quiet, err := maskerQuietGate(store.LogMaskerStateCached(ctx)); quiet {
		return nil, 0, err
	}
	found, err := detectNewLogTemplates(ctx, store, window, time.Now())
	if err != nil {
		return nil, 0, err
	}
	kept, cut := filterNewTemplateNoise(found, store.AnomalySensitivityForDetectors().IsBatchService, templater.TemplateLevel)
	return kept, cut, nil
}

// LegacyEventID — SAF: aynı kalıbın eski log_template_new olay satırının
// kimliği (newTemplateEvent ile aynı ifade). ML olayı bunu kanıtında taşır;
// /anomalies birleşik listesi eski satırı bununla bastırır (ml.BuildUnified).
func (a LogTemplateAnomaly) LegacyEventID() string {
	return chstore.FingerprintAnomaly("log_template_new", a.TemplateID, a.Service)
}
