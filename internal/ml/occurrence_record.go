package ml

// occurrence_record.go — v0.10.2253, anomali kartı sözleşmesi H13
// (docs/ml/anomaly-card-audit.md §9 S19, §10): tek seferlik oluşum türleri
// (new_template — ilk kez görülen Drain kalıbı; rare_error_code — serviste son
// 14 günde görülmemiş hata kodu) olay kaydını doldurur.
//
// Bu olayların terk edilecek bir bandı yok: sözleşmenin H1'den beri tanımlı
// `series.kind=occurrence` dalı kullanılır (yeni kayıt biçimi YOK) —
// doğrulayıcı seri/tepe/onset/bant kurallarını uygulamaz, kapılar seri
// kapılarını "değerlendirilemez" yazar. Kayıt: seri tanımı (dedektörün okuduğu
// kaynak: log_templates defteri ya da ml_series `code|<kod>`), taban 0
// (beklenen 0, bant [0, 0], yöntem adıyla), onset = ilk görülme, tespit = koşu
// başı, tepe = tespit değeri = satır sayısı, hacim = aynı sayı. Snapshot yok
// → kart (record_view.go) eski yolda kalır; başlık kayıttan. Sınıf
// service_anomaly (bugünkü davranış), kapılar gölgede.
// Maliyet: ek sorgu yok (olay kanıtından).

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
)

const (
	occNewTemplateMethod = "drain_family_first_seen"
	occRareCodeMethod    = "code_absent_in_lookback_14d"
)

// OccurrenceInput — BuildOccurrenceRecord girdisi (saf).
type OccurrenceInput struct {
	Detector, Service string
	Label             string // kalıp metni | hata kodu
	Field             string // rare_error_code: ES kod alanı
	Source, Query     string
	StepSec           int
	OnsetAt           time.Time
	DetectedAt        time.Time
	EndedAt           time.Time // sıfır = sürüyor
	Count             float64
	Confirmers        []string
}

// BuildOccurrenceRecord — SAF. ok=false: seri tanımı / sayı / zaman yok.
func BuildOccurrenceRecord(in OccurrenceInput) (EventRecord, bool) {
	if in.Source == "" || in.Query == "" || in.StepSec <= 0 || in.Count <= 0 || in.DetectedAt.IsZero() {
		return EventRecord{}, false
	}
	onset := in.OnsetAt
	if onset.IsZero() || onset.After(in.DetectedAt) {
		onset = in.DetectedAt
	}
	method, kind, summary := occNewTemplateMethod, "template", "İlk kez görülen log kalıbı (Drain defteri): "+truncRunes(in.Label, sampleBodyCap)
	if in.Detector == detRareErrorCode {
		method, kind, summary = occRareCodeMethod, "error_code", fmt.Sprintf("Hata kodu %s (alan %s): serviste son 14 günde hiç görülmedi", in.Label, in.Field)
	}
	cnt := in.Count
	rec := EventRecord{
		V:       RecordVersion,
		Subject: RecordSubject{EntityID: entity.ServiceID(in.Service), Service: in.Service, Resolved: true},
		Series: RecordSeries{Source: in.Source, Query: in.Query, Aggregation: "count", Unit: "count",
			StepSec: uint32(in.StepSec), Kind: SeriesKindOccurrence},
		Baseline:  RecordBaseline{Expected: 0, Lo: 0, Hi: 0, Method: method, StepSec: uint32(in.StepSec)},
		Direction: "up", OnsetAt: onset.UnixNano(), DetectedAt: in.DetectedAt.UnixNano(),
		PeakValue: cnt, ValueAtDetection: cnt, ExpectedValue: 0, Volume: &cnt,
		Class:    ClassServiceAnomaly,
		Evidence: []RecordEvidence{{Kind: kind, Source: in.Source, Summary: summary, Independent: false}},
	}
	if !in.EndedAt.IsZero() {
		rec.EndedAt = max(in.EndedAt.UnixNano(), rec.DetectedAt)
	}
	for _, id := range in.Confirmers {
		rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "confirm", Source: "ml",
			Summary: "Aynı serviste başka dedektörün olayı: " + id, Independent: true})
	}
	n := trInt(cnt) + " satır"
	plain, full := "İlk kez görülen log kalıbı: "+n, "İlk kez görülen log kalıbı \""+truncRunes(in.Label, 80)+"\": "+n
	if in.Detector == detRareErrorCode {
		plain, full = "Son 14 günde görülmemiş hata kodu: "+n, "Son 14 günde görülmemiş hata kodu "+in.Label+": "+n
	}
	// Etiket bir sayı taşıyorsa (ör. kod "503") başlık sayı kuralına takılır →
	// etiketsiz başlık; etiket kanıt satırında kalır.
	rec.Title = full
	if len(titleNumbersOutsideFields(rec)) > 0 {
		rec.Title = plain
	}
	return rec, true
}

// attachOccurrenceRecords — recorder: new_template / rare_error_code
// olaylarına kayıt (yazım öncesi, applyRecordContract doğrular). Sorgu yok.
func attachOccurrenceRecords(cfg chstore.MLConfig, evs []chstore.MLEvent, closed bool,
	detectedAt func(epID string) time.Time, confirmers func(ev chstore.MLEvent) []string, now time.Time) {
	for i := range evs {
		ev := evs[i]
		if (ev.Detector != detNewTemplate && ev.Detector != detRareErrorCode) || ev.Stage == chstore.MLStageChronic {
			continue
		}
		m := evidenceMap(ev.Evidence)
		det := detectedAt(ev.ID)
		if det.IsZero() || det.After(now) {
			det = now
		}
		in := OccurrenceInput{Detector: ev.Detector, Service: ev.Service, StepSec: cfg.BucketSec, DetectedAt: det,
			OnsetAt: time.Unix(0, ev.StartedAt), Count: ev.Observed}
		if bs, _ := m["bucketSec"].(float64); bs > 0 {
			in.StepSec = int(bs)
		}
		if ev.Detector == detNewTemplate {
			id := evStr(m, "templateId")
			if id == "" {
				continue // eski olay: kayıt yazılmaz
			}
			in.Label, in.Source = evStr(m, "template"), "ch:log_templates"
			if in.Label == "" {
				in.Label = ev.Code
			}
			in.Query = "SELECT id, total_count, first_seen FROM log_templates FINAL WHERE id = " + sqlLit(id)
			if fs, _ := m["firstSeen"].(float64); fs > 0 {
				in.OnsetAt = time.Unix(0, int64(fs))
			}
		} else {
			if ev.Code == "" {
				continue
			}
			in.Label, in.Field, in.Source = ev.Code, evStr(m, "field"), "ch:ml_series"
			in.Query = fmt.Sprintf("SELECT toUnixTimestamp(bucket) AS t, max(count) AS v FROM ml_series WHERE signal = %s AND service = %s AND part = %s AND bucket_sec = %d GROUP BY t ORDER BY t",
				sqlLit(chstore.MLSignalLog), sqlLit(logServiceOf(ev)), sqlLit(codePartPrefix+ev.Code), in.StepSec)
		}
		if closed {
			in.EndedAt = time.Unix(0, ev.LastSeen)
		}
		if confirmers != nil {
			in.Confirmers = confirmers(ev)
		}
		rec, ok := BuildOccurrenceRecord(in)
		if !ok {
			continue
		}
		raw, err := json.Marshal(rec)
		if err != nil {
			continue
		}
		ev.Record, ev.Class, ev.OnsetAt, ev.PublishState = string(raw), rec.Class, rec.OnsetAt, PublishPublished
		evs[i] = ev
	}
}
