package chstore

import (
	"fmt"
	"math"
	"testing"
)

// v0.10.1063 regresyon pini — OPERATÖR BİLDİRİMİ: "<svc-B> ile ilgili
// olduğunu düşünüyor ama alakasız."
//
// Alarm-kuralı Problem'i: svc-a (bff) HTTP p99 3872 ms > 3000. Kök-neden
// paneli "LIKELY CAUSE: Co-moving with svc-b (score 404) — possible upstream /
// downstream propagation" diyordu. svc-b'nin satırı: hata %76.8 → %0 ↓,
// trafik 2.77/s → 0.01/s (−%99.5) ↓, p99 32.0 → 2.01 ms (−%93.7) ↓ — yani
// İYİLEŞEN/sönen bir servis ve svc-a ile topoloji kenarı YOK. Bileşik skor
// yönsüz büyüklük olduğu için en üste çıkıyor, panel de en üst satırı
// manşete taşıyordu.
//
// Kural (MarkCorrelationCauses): olası neden = özneyle DOĞRUDAN kenar VE
// (aşağı akışta: kötüleşen ya da trafiği kesilen; yukarı akışta: yalnız
// trafik sıçraması — çağıranın yavaşlaması öznenin sonucudur). Tablo satırları ham sayılardan
// scoreChangedService'e girer — yön sınıflaması da gerçek yoldan geçer.
// Adlar sentetik.
func TestMarkCorrelationCauses(t *testing.T) {
	const (
		baseSec = 2400.0 // taban = 4 × pencere (rootcause ucu)
		curSec  = 600.0
	)
	score := func(t *testing.T, svc string, baseCnt, curCnt, baseErr, curErr uint64, baseP99, curP99 float64) ChangedService {
		t.Helper()
		c, ok := scoreChangedService(svc, baseCnt, curCnt, baseErr, curErr, baseP99, curP99, baseSec, curSec)
		if !ok {
			t.Fatalf("%s: satır üretilmedi", svc)
		}
		return c
	}
	// Ekran görüntüsünün svc-b'si: 6648/2400 s = 2.77/s → 8/600 s = 0.013/s;
	// hata 5106/6648 = %76.8 → 0; p99 32.0 → 2.01 ms.
	svcBShot := func(t *testing.T) ChangedService {
		return score(t, "svc-b", 6648, 8, 5106, 0, 32.0, 2.01)
	}
	// Aynı svc-b, ama hata oranı YÜKSELİYOR (%0 → %75).
	svcBErrUp := func(t *testing.T) ChangedService {
		return score(t, "svc-b", 6648, 8, 0, 6, 32.0, 2.01)
	}
	// svc-c: yalnız hata düşüşü (%28.1 → %0), trafik/p99 sabit.
	svcCBetter := func(t *testing.T) ChangedService {
		return score(t, "svc-c", 4000, 1000, 1124, 0, 50, 50)
	}
	// svc-x: p99 ikiye katlanmış (kötüleşen).
	svcXWorse := func(t *testing.T) ChangedService {
		return score(t, "svc-x", 4000, 1000, 0, 0, 40, 90)
	}
	// svc-y: trafik +%60 (1.0/s → 1.6/s), hata/p99 sabit — yük sıçraması.
	svcYSurge := func(t *testing.T) ChangedService {
		return score(t, "svc-y", 2400, 960, 0, 0, 40, 40)
	}
	// svc-z: yalnız trafik −%50 (hata/p99 sabit) — "iyileşti" değil.
	svcZQuieter := func(t *testing.T) ChangedService {
		return score(t, "svc-z", 4000, 500, 0, 0, 40, 40)
	}
	// Öznenin kendisi (p99 ↑) — asla kendi nedeni değil.
	svcASelf := func(t *testing.T) ChangedService {
		return score(t, "svc-a", 4000, 1000, 0, 0, 900, 3872)
	}

	edge := func(src, dst string) ServiceEdge { return ServiceEdge{Source: src, Target: dst, CallCount: 100} }

	cases := []struct {
		name      string
		rows      func(t *testing.T) []ChangedService
		edges     []ServiceEdge
		topoKnown bool
		svc       string
		wantDir   string
		wantRel   string
		wantElig  bool
	}{
		{
			name:      "ekran görüntüsü: svc-b iyileşti/söndü, kenar YOK → olası neden DEĞİL",
			rows:      func(t *testing.T) []ChangedService { return []ChangedService{svcBShot(t)} },
			edges:     []ServiceEdge{edge("svc-a", "svc-d"), edge("svc-e", "svc-a")},
			topoKnown: true,
			svc:       "svc-b", wantDir: ChangeLost, wantRel: "", wantElig: false,
		},
		{
			name:      "aynı svc-b, kenar VAR ve hata oranı YÜKSELİYOR → uygun (downstream)",
			rows:      func(t *testing.T) []ChangedService { return []ChangedService{svcBErrUp(t)} },
			edges:     []ServiceEdge{edge("svc-a", "svc-b")},
			topoKnown: true,
			svc:       "svc-b", wantDir: ChangeWorse, wantRel: RelationDownstream, wantElig: true,
		},
		{
			name:      "aynı svc-b, hata ↑ ama kenar YOK → uygun DEĞİL (topoloji kuralı)",
			rows:      func(t *testing.T) []ChangedService { return []ChangedService{svcBErrUp(t)} },
			edges:     nil,
			topoKnown: true,
			svc:       "svc-b", wantDir: ChangeWorse, wantRel: "", wantElig: false,
		},
		{
			name:      "trafiği kesilen svc-b kenarla bağlı → uygun (söner ama bağlı: çağrılan karardı)",
			rows:      func(t *testing.T) []ChangedService { return []ChangedService{svcBShot(t)} },
			edges:     []ServiceEdge{edge("svc-a", "svc-b")},
			topoKnown: true,
			svc:       "svc-b", wantDir: ChangeLost, wantRel: RelationDownstream, wantElig: true,
		},
		{
			name:      "yalnız iyileşen svc-c kenarla bağlı bile → uygun DEĞİL (yön kuralı)",
			rows:      func(t *testing.T) []ChangedService { return []ChangedService{svcCBetter(t)} },
			edges:     []ServiceEdge{edge("svc-c", "svc-a")},
			topoKnown: true,
			svc:       "svc-c", wantDir: ChangeBetter, wantRel: RelationUpstream, wantElig: false,
		},
		{
			name:      "kötüleşen aşağı akış (p99 ↑) → uygun",
			rows:      func(t *testing.T) []ChangedService { return []ChangedService{svcXWorse(t)} },
			edges:     []ServiceEdge{edge("svc-a", "svc-x")},
			topoKnown: true,
			svc:       "svc-x", wantDir: ChangeWorse, wantRel: RelationDownstream, wantElig: true,
		},
		{
			name:      "çağıranın YALNIZ p99'u ↑ (upstream) → uygun DEĞİL (öznenin etki alanı, neden değil)",
			rows:      func(t *testing.T) []ChangedService { return []ChangedService{svcXWorse(t)} },
			edges:     []ServiceEdge{edge("svc-x", "svc-a")},
			topoKnown: true,
			svc:       "svc-x", wantDir: ChangeWorse, wantRel: RelationUpstream, wantElig: false,
		},
		{
			name:      "trafiği kesilen ÇAĞIRAN (upstream lost) → uygun DEĞİL",
			rows:      func(t *testing.T) []ChangedService { return []ChangedService{svcBShot(t)} },
			edges:     []ServiceEdge{edge("svc-b", "svc-a")},
			topoKnown: true,
			svc:       "svc-b", wantDir: ChangeLost, wantRel: RelationUpstream, wantElig: false,
		},
		{
			name:      "çağıranın trafik SIÇRAMASI (upstream surge) → uygun (özneye yük)",
			rows:      func(t *testing.T) []ChangedService { return []ChangedService{svcYSurge(t)} },
			edges:     []ServiceEdge{edge("svc-y", "svc-a")},
			topoKnown: true,
			svc:       "svc-y", wantDir: ChangeWorse, wantRel: RelationUpstream, wantElig: true,
		},
		{
			name:      "yalnız trafiği azalan aşağı akış (quieter) → uygun DEĞİL",
			rows:      func(t *testing.T) []ChangedService { return []ChangedService{svcZQuieter(t)} },
			edges:     []ServiceEdge{edge("svc-a", "svc-z")},
			topoKnown: true,
			svc:       "svc-z", wantDir: ChangeQuieter, wantRel: RelationDownstream, wantElig: false,
		},
		{
			name:      "iki yönlü kenar → both, uygun",
			rows:      func(t *testing.T) []ChangedService { return []ChangedService{svcXWorse(t)} },
			edges:     []ServiceEdge{edge("svc-x", "svc-a"), edge("svc-a", "svc-x")},
			topoKnown: true,
			svc:       "svc-x", wantDir: ChangeWorse, wantRel: RelationBoth, wantElig: true,
		},
		{
			name:      "özneye değmeyen kenar bağlantı sayılmaz",
			rows:      func(t *testing.T) []ChangedService { return []ChangedService{svcXWorse(t)} },
			edges:     []ServiceEdge{edge("svc-x", "svc-b")},
			topoKnown: true,
			svc:       "svc-x", wantDir: ChangeWorse, wantRel: "", wantElig: false,
		},
		{
			name:      "topoloji okunamadı → kenar listesi olsa da uygun DEĞİL (güvenli yön)",
			rows:      func(t *testing.T) []ChangedService { return []ChangedService{svcXWorse(t)} },
			edges:     []ServiceEdge{edge("svc-x", "svc-a")},
			topoKnown: false,
			svc:       "svc-x", wantDir: ChangeWorse, wantRel: "", wantElig: false,
		},
		{
			name:      "öznenin kendi satırı asla uygun değil",
			rows:      func(t *testing.T) []ChangedService { return []ChangedService{svcASelf(t)} },
			edges:     []ServiceEdge{edge("svc-a", "svc-a")},
			topoKnown: true,
			svc:       "svc-a", wantDir: ChangeWorse, wantRel: "", wantElig: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := MarkCorrelationCauses(tc.rows(t), "svc-a", tc.edges, tc.topoKnown)
			var got *ChangedService
			for i := range out {
				if out[i].Service == tc.svc {
					got = &out[i]
				}
			}
			if got == nil {
				t.Fatalf("%s çıktıda yok", tc.svc)
			}
			if got.Direction != tc.wantDir || got.Relation != tc.wantRel || got.CauseEligible != tc.wantElig {
				t.Fatalf("dir=%q rel=%q elig=%v, want dir=%q rel=%q elig=%v",
					got.Direction, got.Relation, got.CauseEligible, tc.wantDir, tc.wantRel, tc.wantElig)
			}
		})
	}

	t.Run("ekran görüntüsü skoru korunur (~404) — büyüklük dürüst, yalnız uygunluk değişir", func(t *testing.T) {
		c := svcBShot(t)
		if math.Abs(c.Score-404) > 1 {
			t.Fatalf("score=%.1f, want ~404 (ekran görüntüsü)", c.Score)
		}
	})

	t.Run("sıra: uygun aday önce, iyileşen/sakinleşen en sonda; skor değişmez; girdi bozulmaz", func(t *testing.T) {
		in := rankChangedServices([]ChangedService{svcZQuieter(t), svcBShot(t), svcCBetter(t), svcXWorse(t)})
		if in[0].Service != "svc-b" { // skor sırası: svc-b 404 > svc-c 112 > svc-x 62.5 > svc-z 25
			t.Fatalf("girdi sırası beklenmedik: %v", in)
		}
		out := MarkCorrelationCauses(in, "svc-a", []ServiceEdge{edge("svc-a", "svc-x")}, true)
		want := []string{"svc-x", "svc-b", "svc-c", "svc-z"} // uygun · kesilen(bağlantısız) · iyileşen · sakinleşen
		for i, w := range want {
			if out[i].Service != w {
				t.Fatalf("sıra[%d]=%s, want %s (tam: %+v)", i, out[i].Service, w, out)
			}
		}
		if in[0].CauseEligible || in[0].Relation != "" || in[0].Service != "svc-b" {
			t.Fatal("girdi dilimi değiştirildi — saf değil")
		}
		for _, c := range out {
			if c.Service == "svc-b" && math.Abs(c.Score-404) > 1 {
				t.Fatalf("skor değişti: %.1f", c.Score)
			}
		}
	})

	t.Run("havuz: yönsüz skorla 25. sıradaki bağlı aday işaretlemeden sonra ilk 20'de", func(t *testing.T) {
		// /rootcause: havuz (50) işaretlenir, tavan (20) SONRA kesilir.
		var pool []ChangedService
		for i := 0; i < 30; i++ {
			pool = append(pool, ChangedService{Service: fmt.Sprintf("svc-n%02d", i), Score: float64(500 - i), Direction: ChangeBetter})
		}
		pool = append(pool, svcXWorse(t)) // skor 62.5 → en sonda
		pool = rankChangedServicesTop(pool, ChangedServicesMarkPool)
		out := MarkCorrelationCauses(pool, "svc-a", []ServiceEdge{edge("svc-a", "svc-x")}, true)
		if len(out) > ChangedServicesTop {
			out = out[:ChangedServicesTop]
		}
		if out[0].Service != "svc-x" || !out[0].CauseEligible {
			t.Fatalf("bağlı aday tavan altında kayboldu: ilk=%s", out[0].Service)
		}
	})
}

// v0.10.1063 — yön sınıflaması (changeDirection) tablosu: düşük hacim, NaN,
// yalnız trafik düşüşü, karışık sinyaller. "better" YALNIZ hata ya da p99
// gerçekten düştüğünde.
func TestChangeDirection(t *testing.T) {
	cs := func(baseErr, curErr, p99D, rateD float64) ChangedService {
		return ChangedService{BaselineErr: baseErr, CurrentErr: curErr, P99DeltaPct: p99D, RateDeltaPct: rateD}
	}
	cases := []struct {
		name string
		c    ChangedService
		vol  uint64
		want string
	}{
		{"ekran görüntüsü svc-b: hata %76.8→0, trafik −%99.5, p99 −%93.7 → lost", cs(0.768, 0, -93.7, -99.5), 6656, ChangeLost},
		{"düşük hacim: p99 10× ↑ + hata −1.5 puan → worse (p99 hacim kapısız)", cs(0.02, 0.005, 900, 0), 60, ChangeWorse},
		{"düşük hacim: trafik −%95, hata −5 puan → better (lost hacim kapılı)", cs(0.05, 0, 0, -95), 60, ChangeBetter},
		{"NaN p99 deltası, hata/trafik sabit → unknown (asla better)", cs(0.01, 0.01, math.NaN(), 0), 5000, ChangeUnknown},
		{"NaN p99 + hata −10 puan → unknown", cs(0.1, 0, math.NaN(), 0), 5000, ChangeUnknown},
		{"NaN p99 ama hata +10 puan → worse (kötüleşme okunabildi)", cs(0, 0.1, math.NaN(), 0), 5000, ChangeWorse},
		{"yalnız trafik −%50 → quieter (iyileşti DEĞİL)", cs(0.01, 0.01, 0, -50), 5000, ChangeQuieter},
		{"yalnız trafik −%95 → lost", cs(0.01, 0.01, 0, -95), 5000, ChangeLost},
		{"karışık: hata +5 puan, trafik −%99 → worse", cs(0.01, 0.06, 0, -99), 5000, ChangeWorse},
		{"karışık: hata −28 puan, trafik −%50 → better", cs(0.281, 0, 0, -50), 5000, ChangeBetter},
		{"karışık: p99 −%40, trafik +%60 → worse (yük sıçraması)", cs(0, 0, -40, 60), 5000, ChangeWorse},
		{"düşük hacim trafik +%60 tek başına → unknown (sıçrama hacim kapılı)", cs(0, 0, 0, 60), 60, ChangeUnknown},
		{"p99 −%30 tek başına → better", cs(0, 0, -30, 0), 5000, ChangeBetter},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := changeDirection(tc.c, tc.vol); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
