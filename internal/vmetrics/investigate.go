package vmetrics

// v0.10.944 (CoSRE araştırma asistanı) — query_metric / list_metric_labels
// araçlarının VictoriaMetrics kabiliyetleri.
//
// Neden ayrı yöntemler, neden QueryMetric'i genişletmek değil: QueryMetric'in
// imzası *chstore.Store ile BİREBİR aynı (api seam'i; sapma derleme hatası).
// Araç ise cevabın veri DIŞI yarısını ister — VM `isPartial`, 1000 seri
// tavanı, kullanılan adım, cevabın alındığı an (gecikme tespiti) — ve
// pencereye bağlı (sohbet çıpası) etiket keşfi. Bunlar İSTEĞE BAĞLI
// kabiliyetler: mcptools tip-iddiasıyla arar, ClickHouse kaynağı taşımaz,
// araç o zaman konvansiyon yoluna düşer. metricNoteSource (v0.9.1157) ile
// aynı desen.
//
// Hepsi SALT-OKUNUR ve hepsi mevcut uçları kullanır (/api/v1/labels,
// /api/v1/label/<k>/values, /api/v1/query_range) — yeni bir VM yüzeyi yok.

import (
	"context"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// BackendName — sourcestate.Status.Backend değeri.
const BackendName = "victoriametrics"

// QueryDetail — QueryMetricDetailed'ın sonucu.
type QueryDetail struct {
	Series []chstore.SpanMetricSeries
	// Step — query_range'e giden adım (saniye); noktalar kova BAŞLANGICIYLA
	// damgalı, kova sonu = Time + Step.
	Step int
	// Partial — VM `isPartial:true` (bazı vmstorage düğümleri cevap vermedi).
	Partial bool
	// Truncated — promapi.MaxSeriesParsed (1000) tavanı; Series kesik.
	Truncated bool
	// TotalSeries — tavandan ÖNCE VM'in döndürdüğü seri sayısı.
	TotalSeries int
	// AnsweredAt — cevabın alındığı an (UTC). Gecikme tespiti duvar saatine
	// göre yapılır; araç paketi pencere kurmak için time.Now() okuyamaz
	// (anchor kapısı), cevabın damgası adaptörün kenarında basılır.
	AnsweredAt time.Time
	// Note — boş sonucun açıklaması (QueryMetricNoted ile AYNI kural).
	Note string
}

// MetricBackend — okumanın gittiği depo adı.
func (s *Service) MetricBackend() string { return BackendName }

// MetricLabelMap — canlı yapılandırmanın etiket eşlemesi (kırpılmış).
func (s *Service) MetricLabelMap() LabelMap {
	if s == nil {
		return LabelMap{}
	}
	return s.CurrentSettings().LabelMap.Normalized()
}

// MetricLabelNamesIn — metriğin [from, to] penceresinde taşıdığı GERÇEK
// etiket adları (sıralı, __name__ hariç, TAVANSIZ). MetricAttrKeys'in 100
// tavanı bir seçici sınırıdır; doğrulama tam kümeye karşı yapılmalı (101.
// etiketi "yok" diye reddetmek MetricPresentKeys'in v0.9.1268 dersi).
//
// v0.10.944 — partial = VM `isPartial` (vmselect etiket uçlarında da
// döndürür): küme EKSİK olabilir; araç görülmeyen etiketi reddetmez, kısmi
// işaretler.
func (s *Service) MetricLabelNamesIn(ctx context.Context, metric string, from, to time.Time) ([]string, bool, error) {
	if strings.TrimSpace(metric) == "" {
		return nil, false, nil
	}
	cfg, err := s.ready()
	if err != nil {
		return nil, false, err
	}
	return s.labelNamesBetween(ctx, cfg, metric, "", from, to)
}

// MetricLabelValuesIn — bir etiketin [from, to] penceresindeki değerleri
// (sıralı, en çok limit; limit 1..1000). Çağıran `limit+1` isteyip fazlalığı
// has_more olarak okuyabilir. v0.10.944 — ikinci dönüş VM `isPartial`.
func (s *Service) MetricLabelValuesIn(ctx context.Context, metric, label string, from, to time.Time, limit int) ([]string, bool, error) {
	if strings.TrimSpace(metric) == "" || strings.TrimSpace(label) == "" {
		return nil, false, nil
	}
	cfg, err := s.ready()
	if err != nil {
		return nil, false, err
	}
	return s.labelValuesBetween(ctx, cfg, metric, label, from, to, "", limit)
}

// QueryMetricDetailed — QueryMetricNoted'ın bayraklı hâli: aynı çeviri
// (buildPromQL), aynı pencere normalizasyonu, aynı not kuralı; artı isPartial,
// seri tavanı, adım ve cevap anı.
func (s *Service) QueryMetricDetailed(ctx context.Context, f chstore.MetricQueryFilter) (QueryDetail, error) {
	cfg, err := s.ready()
	if err != nil {
		return QueryDetail{}, err
	}
	f = normalizeQueryWindow(f)
	q, err := buildPromQL(f, promOptions(cfg))
	if err != nil {
		return QueryDetail{}, err
	}
	d, err := s.runRangeQueryDetailed(ctx, cfg, q, f)
	if err != nil {
		return QueryDetail{}, err
	}
	d.Note = emptyResultNote(f, len(d.Series))
	return d, nil
}

// ResultUnit — v0.10.944 — sorgu SONUCUNUN birimi (ad + agregasyon birlikte).
// Açıkça seçilmiş `_count` / `_bucket` serisi gözlem SAYISI taşır; ailenin
// `_seconds` eki ona birim veremez (v0.9.1274 sınıfı). Yalnız kova + yüzdelik
// ailenin birimini taşır. Katalog birimi (describeMetricName) BİLEREK
// değişmedi: orada `_count` ailenin adıdır, burada ise okunan değerdir.
func ResultUnit(name, agg string) string {
	n := strings.TrimSpace(name)
	_, pct := promPercentile(agg)
	switch {
	case strings.HasSuffix(n, histogramCountSuffix):
		return ""
	case strings.HasSuffix(n, bucketSuffix) && !pct:
		return ""
	}
	u, _ := describeMetricName(n)
	return u
}
