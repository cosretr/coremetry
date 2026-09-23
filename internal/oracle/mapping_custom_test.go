package oracle

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// mapping_custom_test.go — v0.10.902: ön-toplanmış satır eşlemesi. Epoch
// zaman (sayı / rakam dizesi, ölçek büyüklükten, localize EDİLMEZ),
// "DD.MM.YYYY HH24:MI" (kaynağın dilimi), count ağırlığı (attribute olarak
// da kalır), trace listesi patlatma (geçerli/geçersiz/tekrar/artan/boş).

func TestParseTimeEpochAndDottedLayouts(t *testing.T) {
	m, err := NewMapper(baseSrc()) // Europe/Istanbul
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 23, 15, 21, 0, 0, time.UTC)
	cases := []struct {
		name string
		v    any
		want time.Time
		ok   bool
	}{
		{"float saniye (TimeSlice)", float64(want.Unix()), want, true},
		{"int64 saniye", want.Unix(), want, true},
		{"rakam dizesi saniye", "1790176860", want, true},
		{"milisaniye", float64(want.UnixMilli()), want, true},
		{"mikrosaniye", float64(want.UnixMicro()), want, true},
		{"nanosaniye", float64(want.UnixNano()), want, true},
		{"1e9 altı sayı çöp", int64(12345), time.Time{}, false},
		{"sıfır", float64(0), time.Time{}, false},
		{"kısa rakam dizesi çöp", "12345", time.Time{}, false},
		// Zaman: İstanbul 18:21 → 15:21Z (kaynağın dilimi).
		{"DD.MM.YYYY HH24:MI", "23.09.2026 18:21", want, true},
		{"DD.MM.YYYY HH24:MI:SS", "23.09.2026 18:21:00", want, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := m.parseTime(c.v)
			if ok != c.ok || (ok && !got.Equal(c.want)) {
				t.Fatalf("%v → %v ok=%v, beklenen %v ok=%v", c.v, got, ok, c.want, c.ok)
			}
		})
	}
}

func aggregatedSrc() SourceConfig {
	s := baseSrc()
	s.TimestampColumn = "TIMESLICE"
	s.TypeColumn = "SONUC"
	s.Columns = map[string]string{
		FieldService: "OPERATIONCODE", FieldCode: "FUNCTIONCODE", FieldChannel: "KANALKOD",
		FieldHost: "HOSTNAME", FieldCount: "ADET", FieldTraceIDs: "TRACEIDS",
	}
	return s
}

func aggregatedRow(adet any, traceids string) map[string]any {
	return map[string]any{
		"TIMESLICE": float64(1790176860), "ZAMAN": "23.09.2026 18:21", "KANALKOD": "050121", "FUNCTIONCODE": "SPEM200",
		"OPERATIONCODE": "CHATBOT_INBOUND", "HOSTNAME": "WEB01", "SONUC": "TFAIL",
		"ADET": adet, "TRACEIDADET": float64(2), "DURATION": float64(99), "TRACEIDS": traceids,
	}
}

func TestMapAggregatedRowWeightAndAttrs(t *testing.T) {
	m, err := NewMapper(aggregatedSrc())
	if err != nil {
		t.Fatal(err)
	}
	r, ok, bad := m.Map(aggregatedRow(float64(5), ""))
	if !ok || bad || r.Weight != 5 || r.OperationCode != "CHATBOT_INBOUND" || r.ErrorCode != "SPEM200" ||
		r.ChannelCode != "050121" || r.HostName != "WEB01" || r.ErrorType != "TFAIL" || r.TraceID != "" {
		t.Fatalf("eşleme: ok=%v bad=%v %+v", ok, bad, r)
	}
	if !r.Time.Equal(time.Unix(1790176860, 0).UTC()) {
		t.Fatalf("epoch zaman localize edilmemeli: %v", r.Time)
	}
	// count kolonu attribute olarak KALIR; traceIds kolonu (boş) ve tüketilenler kalmaz;
	// ZAMAN/TRACEIDADET/DURATION verbatim.
	attrs := map[string]string{}
	for i, k := range r.AttrKeys {
		attrs[k] = r.AttrValues[i]
	}
	if attrs["ADET"] != "5" || attrs["ZAMAN"] != "23.09.2026 18:21" || attrs["DURATION"] != "99" || attrs["TRACEIDADET"] != "2" {
		t.Fatalf("attribute'lar: %v", attrs)
	}
	for _, k := range []string{"TIMESLICE", "KANALKOD", "FUNCTIONCODE", "OPERATIONCODE", "HOSTNAME", "SONUC", "TRACEIDS"} {
		if _, has := attrs[k]; has {
			t.Fatalf("tüketilen kolon attribute olmamalı: %s", k)
		}
	}
	// Sayı biçimleri: dize, int64, geçersiz → 0 (sayaç 1 sayar), negatif → 0, tavan.
	for _, c := range []struct {
		v    any
		want uint32
	}{{"7", 7}, {int64(2), 2}, {"x", 0}, {float64(-3), 0}, {nil, 0}, {float64(2.6), 3}, {float64(5e9), 1e9}} {
		if got := cellCount(c.v); got != c.want {
			t.Fatalf("cellCount(%v) = %d, beklenen %d", c.v, got, c.want)
		}
	}
	if got := rowWeight(chstore.OracleErrorRow{}); got != 1 {
		t.Fatalf("ağırlık 0 → 1: %v", got)
	}
}

func TestExpandTraceList(t *testing.T) {
	m, err := NewMapper(aggregatedSrc())
	if err != nil {
		t.Fatal(err)
	}
	const a, b = "4bf92f3577b34da6a3ce929d0e0e4736", "0AF76519-16CD-43DD-8448-EB211C80319C"
	rows, st := m.MapAll([]map[string]any{
		// 3 hata, 2 trace (biri büyük harf/tireli), listede tekrar + geçersiz parça.
		aggregatedRow(float64(3), a+", "+b+", "+a+", bozuk-id"),
		// count eşlenmiş ama liste boş → tek trace'siz satır, ağırlık 4.
		aggregatedRow(float64(4), ""),
		// liste tamamen geçersiz → tek trace'siz satır, ağırlık 2, 1 bozuk.
		aggregatedRow(float64(2), "nope"),
		// count > liste değil (2 trace, Adet 2) → artan satır YOK.
		aggregatedRow(float64(2), a+";"+b),
		// count yok (nil) → yalnız trace satırları.
		aggregatedRow(nil, a+"\n"+b),
	})
	if st.Rows != 5 || st.Mapped != 5 || st.NoTimestamp != 0 {
		t.Fatalf("istatistik: %+v", st)
	}
	if st.BadTraceID != 2 { // "bozuk-id" + "nope"
		t.Fatalf("bozuk trace sayısı %d", st.BadTraceID)
	}
	// Satır 1: a, b, artan(1) = 3; satır 2: 1; satır 3: 1; satır 4: 2; satır 5: 2 → 9.
	if len(rows) != 9 || st.Expanded != 3+1+2+2 {
		t.Fatalf("patlatma: %d satır, expanded %d", len(rows), st.Expanded)
	}
	sumW := 0
	ids := map[string]int{}
	for _, r := range rows {
		sumW += int(rowWeight(r))
		if r.TraceID != "" {
			ids[r.TraceID]++
			if r.Weight != 1 {
				t.Fatalf("trace satırı ağırlık 1: %+v", r)
			}
		}
	}
	// Ağırlık toplamı = Adet toplamı (3+4+2+2) + count'suz satırın 2 trace'i = 13.
	if sumW != 13 {
		t.Fatalf("ağırlık toplamı %d", sumW)
	}
	if ids[a] != 3 || ids["0af7651916cd43dd8448eb211c80319c"] != 3 {
		t.Fatalf("trace kimlikleri normalize/tekrarsız: %v", ids)
	}
	// row_id: aynı grup + farklı trace → farklı; artan satır her koşuda aynı kimlik.
	first, _ := m.MapAll([]map[string]any{aggregatedRow(float64(3), a+", "+b)})
	again, _ := m.MapAll([]map[string]any{aggregatedRow(float64(3), a+", "+b)})
	if len(first) != 3 || first[0].RowID == first[1].RowID || first[2].RowID != again[2].RowID || first[0].RowID != again[0].RowID {
		t.Fatalf("row_id kararlılığı: %v / %v", first, again)
	}
	if parts := splitTraceList(" a, b;c|d\te\nf "); len(parts) != 6 {
		t.Fatalf("ayırıcılar: %v", parts)
	}
}

// TestBucketRowsWeighted — sayaç ön-toplanmış ağırlıkla sayar: aynı dakika
// aynı anahtar 3 + 1(trace satırı) = 4; tablo kipi satırı (Weight 0) 1.
func TestBucketRowsWeighted(t *testing.T) {
	t0 := time.Date(2026, 9, 23, 15, 21, 0, 0, time.UTC)
	rows := []chstore.OracleErrorRow{
		{Time: t0.Add(10 * time.Second), OperationCode: "OP", ErrorCode: "F1", ChannelCode: "C", Weight: 3},
		{Time: t0.Add(20 * time.Second), OperationCode: "OP", ErrorCode: "F1", ChannelCode: "C", Weight: 1, TraceID: "4bf92f3577b34da6a3ce929d0e0e4736"},
		{Time: t0.Add(30 * time.Second), OperationCode: "OP", ErrorCode: "F2", ChannelCode: "C"},
	}
	res := BucketRows("src", rows, t0, t0.Add(2*time.Minute), nil, nil, 0, nil)
	got := map[string]float64{}
	for _, p := range res.Points {
		if p.Time.Equal(t0) {
			got[p.AttrValues[1]] = p.Value
		}
	}
	if got["F1"] != 4 || got["F2"] != 1 {
		t.Fatalf("ağırlıklı sayım: %v", got)
	}
}
