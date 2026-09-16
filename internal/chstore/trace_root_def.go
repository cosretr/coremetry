package chstore

// trace_root_def.go — v0.10.733 (operatör onaylı spec 2026-09-16; prod
// ölçümü: trace'lerin yalnız %40-53'ünde TAM kök var, en büyük neden
// gateway'in server span'ine üst katmanın traceparent basması = Coremetry
// açısından bütün trace köksüz sayılıyor).
//
// İki kök tanımı, operatör seçer (Admin → ClickHouse → kök kapsaması):
//   strict — tam kök: parent boş + ad dolu + servis dolu (v0.10.732 öncesi
//            tek tanım; varsayılan, davranış değişmez).
//   entry  — giriş kökü: tam kök VAR ya da en az bir giriş span'i
//            (server/consumer, servis dolu) var. Öksüzlük (ebeveyn trace'te
//            yok) ŞART DEĞİL: ölçmek trace başına span id kümesi ister,
//            saatte 3M trace'te pahalı; spec açık soru 2, operatör onayı.
//
// Kapsam yalnız üç yüklem: Traces Root süzgeci (MV + ham + aşama-2 kök
// doğrulama), şeridin kök yüklemi (tablo ile aynı küme) ve kapsama ölçüsü.
// topology_root_flows / Problems kök kavramına DOKUNMAZ (spec soru 3).
// Yüklemler SAF fonksiyon — tablo testi iki tanımı da gezer.

import (
	"context"
	"encoding/json"
)

type TraceRootDef string

const (
	TraceRootDefStrict TraceRootDef = "strict"
	TraceRootDefEntry  TraceRootDef = "entry"
	traceRootDefKey                 = "trace_root_def"
)

// ParseTraceRootDef — bilinmeyen/boş → strict (varsayılan, sessiz yanlış yok:
// tanınmayan bir değer daha GENİŞ tanıma düşmez).
func ParseTraceRootDef(raw string) TraceRootDef {
	if raw == string(TraceRootDefEntry) {
		return TraceRootDefEntry
	}
	return TraceRootDefStrict
}

const (
	strictRootRaw = "countIf((parent_id = '' OR parent_id = '0000000000000000') AND name != '' AND service_name != '') > 0"
	entrySpanRaw  = "countIf((kind = 'server' OR kind = 'consumer') AND service_name != '' AND service_name != 'unknown') > 0"
	strictRootMV  = "argMaxIfMerge(root_service_state) != ''"
	entrySpanMV   = "argMinIfMerge(entry_service_state) != ''"
	entrySpanPred = "kind IN ('server', 'consumer')"
)

// rootHavingMV — trace_summary_5m üstünde GROUP BY trace_id HAVING gövdesi.
// entryCol=false (kolon henüz okunamıyor, store.go hasTraceEntrySvcCol)
// iken entry tanımı strict'e DÜŞER — yanlış SQL yerine dar tanım.
func rootHavingMV(def TraceRootDef, entryCol bool) string {
	if def == TraceRootDefEntry && entryCol {
		return "(" + strictRootMV + " OR " + entrySpanMV + ")"
	}
	return strictRootMV
}

// rootHavingRaw — ham spans üstünde GROUP BY trace_id HAVING gövdesi.
func rootHavingRaw(def TraceRootDef) string {
	if def == TraceRootDefEntry {
		return "(" + strictRootRaw + " OR " + entrySpanRaw + ")"
	}
	return strictRootRaw
}

// rootSpanPredicateFor — şeridin SPAN düzeyi yüklemi (spanmetric.go
// rootSpanPredicate'in tanım-duyarlı hâli): entry'de giriş span'leri de
// kök sayılır, tablo ile aynı küme.
func rootSpanPredicateFor(def TraceRootDef) string {
	if def == TraceRootDefEntry {
		return "(" + rootSpanPredicate + " OR " + entrySpanPred + ")"
	}
	return rootSpanPredicate
}

// TraceRootDef — canlı tanım (kilitsiz okuma; Store{} kuran testler strict).
func (s *Store) TraceRootDef() TraceRootDef {
	if s.traceRootDef.Load() == 1 {
		return TraceRootDefEntry
	}
	return TraceRootDefStrict
}

// SetTraceRootDef — canlı tanımı yayınlar (PUT anında ve yenileme tikinde).
func (s *Store) SetTraceRootDef(d TraceRootDef) {
	if d == TraceRootDefEntry {
		s.traceRootDef.Store(1)
	} else {
		s.traceRootDef.Store(0)
	}
}

func (s *Store) rootHavingMV() string { return rootHavingMV(s.TraceRootDef(), s.hasTraceEntrySvcCol) }

type traceRootDefBlob struct {
	Def string `json:"def"`
}

// GetTraceRootDefSetting — KAYITLI tanım (system_settings). Yok/bozuk → strict.
func (s *Store) GetTraceRootDefSetting(ctx context.Context) TraceRootDef {
	raw, err := s.GetSetting(ctx, traceRootDefKey)
	if err != nil || len(raw) == 0 {
		return TraceRootDefStrict
	}
	var b traceRootDefBlob
	if err := json.Unmarshal(raw, &b); err != nil {
		return TraceRootDefStrict
	}
	return ParseTraceRootDef(b.Def)
}

// SaveTraceRootDefSetting — system_settings'e yazar. Yeni şema YOK.
func (s *Store) SaveTraceRootDefSetting(ctx context.Context, d TraceRootDef) error {
	raw, err := json.Marshal(traceRootDefBlob{Def: string(ParseTraceRootDef(string(d)))})
	if err != nil {
		return err
	}
	return s.PutSetting(ctx, traceRootDefKey, raw)
}
