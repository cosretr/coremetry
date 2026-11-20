package ml

// record_view.go — v0.10.2247 (anomali kartı Dilim G): detay kartının
// okuduğu olay kaydı. Kart grafiği yalnız bu kayıttaki seriden (dedektörün
// yargıladığı seri, bant, onset, tespit) çizilir; render'da ek sorgu yok
// (audit §9 kuralları, ölçüt 7/9). Kayıt yok / çözülemiyor / doğrulayıcıdan
// kalmış (publish_state=invalid) → nil: kart eski yola düşer.

import (
	"encoding/json"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// RecordView — kayıt + kartın kayıtta olmayan iki kimlik alanı: dedektör ve
// metrik ifadesinin kimliği/etiketi (metrik olayında ölçünün adı; ayardan).
type RecordView struct {
	EventRecord
	Detector     string `json:"detector"`
	PublishState string `json:"publishState,omitempty"`
	MetricID     string `json:"metricId,omitempty"`
	MetricLabel  string `json:"metricLabel,omitempty"`
}

// CardRecordView — SAF: olay satırının yayımlanabilir kaydı; yoksa nil.
func CardRecordView(ev *chstore.MLEvent, in CardInput) *RecordView {
	if ev == nil || ev.Record == "" || ev.PublishState == PublishInvalid {
		return nil
	}
	var rec EventRecord
	if json.Unmarshal([]byte(ev.Record), &rec) != nil || rec.V != RecordVersion || len(rec.Snapshot.V) == 0 {
		return nil
	}
	v := &RecordView{EventRecord: rec, Detector: ev.Detector, PublishState: ev.PublishState}
	if ev.Class == ClassInfo { // v0.10.2343 (A2 açık): satıra yazılan sınıf; kapalıyken hiç info yazılmaz
		v.Class = ClassInfo
	}
	if in.Signal == chstore.MLSignalMetric {
		_, v.MetricID, _ = splitThanosPart(in.Part)
		v.MetricLabel = in.MetricLabel
	}
	return v
}

// RequestsCapable — SAF (v0.10.2380): kayıttan dakikada başarısız / başarılı
// istek çizilebilir mi. Koşul VERİDEN: servis hata oranı kaydı, oran yüzde ve
// 1 dakikalık pencerede (seri adımı = snapshot adımı = 60 sn), hacim sütunu
// aynı ızgarada. Ancak o zaman v/100 × n o dakikanın hata sayısıdır. Eski
// svc_error_rate kaydı (v0.10.2373 öncesi, kayan 5 dk oran) ve op_error_rate
// (kayan 5 dk oran × 1 dk sayım) bu koşulu sağlamaz → oran grafiği kalır.
// FE ikizi components/coremetry/anomalies/mlRequests.ts requestsCapable.
func RequestsCapable(detector string, rec EventRecord) bool {
	sn := rec.Snapshot
	return detector == detSvcErrorRate && rec.Series.Aggregation == "err_ratio" && rec.Series.Unit == "percent" &&
		rec.Series.StepSec == svcImpactStepSec && sn.StepSec == svcImpactStepSec && len(sn.V) > 0 && len(sn.N) == len(sn.V)
}

// RequestsRecordView — SAF (v0.10.2380): Problem detayının ilk grafiği için
// olayın kaydı (GET /api/problems/{id}/events satırına iliştirilir; ek sorgu
// yok, kayıt o okumada zaten geliyor). Yalnız istek görünümü çizilebilen
// kayıt; kartın metin alanları (kanıt, kapılar, yan seriler) atılır.
func RequestsRecordView(ev *chstore.MLEvent) *RecordView {
	v := CardRecordView(ev, CardInput{})
	if v == nil || !RequestsCapable(ev.Detector, v.EventRecord) {
		return nil
	}
	v.Evidence, v.Gates, v.Context = nil, nil, nil
	return v
}
