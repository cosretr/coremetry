package anomaly

import (
	"math"
	"testing"
)

// v0.10.887 (paritesi #4 dilim 1) — heap bandı: n < minSamples+dwell → bant
// yok (sıfır değil); düz + son 3 kova sıçrama → critical; tek kova sıçrama →
// dwell tutmaz → ok; küçük sapma taban kapılarına takılır; MAD tabanı 2 puan;
// yön yalnız yukarı; bant medyan ± openZ·MAD/0.6745.
func TestComputeHeapBandGates(t *testing.T) {
	need := HeapBandMinBuckets()
	if need != 15 {
		t.Fatalf("minSamples(12)+dwell(3) = 15, %d", need)
	}
	short := make([]float64, need-1)
	for i := range short {
		short[i] = 60
	}
	if hb := ComputeHeapBand(short); hb.Status != HeapBandNoBaseline || hb.Need != 1 || hb.Upper != 0 {
		t.Fatalf("kısa tarihçe bant vermemeli: %+v", hb)
	}
	flat := make([]float64, 40)
	for i := range flat {
		flat[i] = 60 + float64(i%3) // 60/61/62 — MAD ≈ 1 → taban 2 puan
	}
	hb := ComputeHeapBand(flat)
	if hb.Status != HeapBandOK || hb.MAD < HeapBandMinMAD {
		t.Fatalf("düz seri ok + MAD tabanı: %+v", hb)
	}
	wantHalf := openZ * hb.MAD / madScale
	if math.Abs((hb.Upper-hb.Median)-wantHalf) > 1e-9 || hb.N != 37 {
		t.Fatalf("bant genişliği openZ·MAD/madScale: %+v", hb)
	}
	spike := append(append([]float64{}, flat[:37]...), 90, 91, 92)
	if hb := ComputeHeapBand(spike); hb.Status != HeapBandCritical || hb.Z < 6 {
		t.Fatalf("son 3 kova 90+ → critical: %+v", hb)
	}
	one := append(append([]float64{}, flat[:39]...), 92)
	if hb := ComputeHeapBand(one); hb.Status != HeapBandOK {
		t.Fatalf("tek kova sıçrama dwell'i tutmaz → ok: %+v", hb)
	}
	mild := append(append([]float64{}, flat[:37]...), 64, 64, 64) // +3 puan: z ≈ 1 → ok
	if hb := ComputeHeapBand(mild); hb.Status != HeapBandOK {
		t.Fatalf("küçük sapma ok: %+v", hb)
	}
	dev := append(append([]float64{}, flat[:37]...), 75, 75, 75) // +14: z≈4.7 → deviating (critical değil)
	if hb := ComputeHeapBand(dev); hb.Status != HeapBandDeviating {
		t.Fatalf("orta sapma deviating: %+v", hb)
	}
	down := append(append([]float64{}, flat[:37]...), 20, 20, 20)
	if hb := ComputeHeapBand(down); hb.Status != HeapBandOK {
		t.Fatalf("düşüş alarm değil: %+v", hb)
	}
	if hb := ComputeHeapBand(nil); hb.Status != HeapBandNoBaseline || hb.Lower != 0 {
		t.Fatalf("boş: %+v", hb)
	}
}
