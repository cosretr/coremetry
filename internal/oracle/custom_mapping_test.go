package oracle

import "testing"

// v0.10.907 (operatör-bildirimli, prod 2026-09-23): özel SQL testi "0/0 trace,
// operasyon kodu (boş)" dedi ama eşleme rozeti SUSTU — kontrol yalnız açıkça
// eşlenmiş alanlara bakıyordu, operatör hiçbirini eşlememişti. Eşlenmemiş
// kritik alanlar artık eksik sayılır ve sorgu çıktısındaki tanıdık takma addan
// öneri alır ("Önerilen eşlemeyi uygula").
func TestMappingCheckFlagsUnmappedWithSuggestions(t *testing.T) {
	src := customSource()
	src.Columns = nil // operatörün prod durumu: hiçbir alan eşlenmemiş
	out := []string{"TIMESLICE", "ZAMAN", "KANALKOD", "FUNCTIONCODE", "OPERATIONCODE", "HOSTNAME", "SONUC", "ADET", "TRACEIDADET", "DURATION", "TRACEIDS"}
	norm := mustNormalize(t, one(src), Settings{}).Sources[0]
	mc := mappingCheckFromColumns(norm, out)
	want := map[string]string{
		FieldTraceIDs: "TRACEIDS", FieldService: "OPERATIONCODE", FieldCode: "FUNCTIONCODE",
		FieldCount: "ADET", FieldChannel: "KANALKOD", FieldHost: "HOSTNAME",
	}
	got := map[string]string{}
	for _, m := range mc.Missing {
		if m.Column != "" {
			t.Fatalf("eşlenmemiş alanın kolonu boş olmalı: %+v", m)
		}
		got[m.Field] = m.Suggest
	}
	if len(got) != len(want) {
		t.Fatalf("eksik listesi: %+v", mc.Missing)
	}
	for f, s := range want {
		if got[f] != s {
			t.Fatalf("%s önerisi %q, beklenen %q", f, got[f], s)
		}
	}
	// Tekil traceId eşlenmişse liste şart değil; hata kodu çıktıda ERRORCODE varsa onu öner.
	src2 := customSource()
	src2.Columns = map[string]string{FieldTraceID: "TRACEID", FieldService: "OPERATIONCODE", FieldCode: "ERRORCODE", FieldCount: "ADET"}
	mc2 := mappingCheckFromColumns(mustNormalize(t, one(src2), Settings{}).Sources[0], []string{"TIMESLICE", "SONUC", "TRACEID", "OPERATIONCODE", "ERRORCODE", "ADET"})
	if len(mc2.Missing) != 0 {
		t.Fatalf("tam eşlemede eksik olmamalı: %+v", mc2.Missing)
	}
	// Çıktıda karşılık yoksa öneri yok (uydurma yok).
	mc3 := mappingCheckFromColumns(norm, []string{"TIMESLICE", "X"})
	for _, m := range mc3.Missing {
		if m.Suggest != "" {
			t.Fatalf("çıktıda olmayan ad önerildi: %+v", m)
		}
	}
}
