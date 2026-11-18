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
