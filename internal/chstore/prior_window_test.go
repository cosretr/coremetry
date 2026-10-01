package chstore

// prior_window_test.go — v0.10.1025 regresyon testi (Databases dilim 3).
//
// Bug: /databases ve /messaging listelerinin ?compare=prior okuması prior
// pencereyi `[from − dur, from)` diye kuruyordu. Current okuma alt sınırı
// 5 dk kovaya İNDİRİYOR (`time_bucket >= floor5(from)`), prior'un üst
// sınırı ise HİZASIZ from'du (`time_bucket < from`): from=10:03 iken 10:00
// kovası iki pencereye de giriyordu. PriorWindow bu türetimin tek yeri.
//
// Test SQL yüklemini Go'da taklit ediyor (readBuckets) ve iki ÖZELLİĞİ
// her pencere için sınıyor: ortak kova YOK, kova sayısı EŞİT.

import (
	"math/rand"
	"testing"
	"time"
)

// readBuckets — `time_bucket >= alignBucketStart(lo) AND time_bucket < hi`
// yükleminin aldığı kova etiketleri. `hi` saniyeye iner: clickhouse-go
// konumsal `?` bağında time.Time'ı saniye hassasiyetinde yazar.
func readBuckets(lo, hi time.Time) []time.Time {
	end := hi.Truncate(time.Second)
	var out []time.Time
	for b := alignBucketStart(lo); b.Before(end); b = b.Add(mvBucketWidth) {
		out = append(out, b)
	}
	return out
}

func pwAt(h, m, s, ns int) time.Time {
	return time.Date(2026, 9, 30, h, m, s, ns, time.UTC)
}

func TestPriorWindowTable(t *testing.T) {
	cases := []struct {
		name               string
		from, to           time.Time
		wantFrom, wantTo   time.Time
		wantCurrentBuckets int
	}{
		{
			name: "hizalı 1 saat — klasik geri kayma",
			from: pwAt(10, 0, 0, 0), to: pwAt(11, 0, 0, 0),
			wantFrom: pwAt(9, 0, 0, 0), wantTo: pwAt(10, 0, 0, 0),
			wantCurrentBuckets: 12,
		},
		{
			// BUG BUYDU: eski prior [09:03, 10:03) → 10:00 kovası iki
			// pencerede. Current 10:00…11:00 = 13 kova okur (11:00 < 11:03).
			name: "hizasız from ve to — 13 kova, prior 08:55'ten",
			from: pwAt(10, 3, 0, 0), to: pwAt(11, 3, 0, 0),
			wantFrom: pwAt(8, 55, 0, 0), wantTo: pwAt(10, 0, 0, 0),
			wantCurrentBuckets: 13,
		},
		{
			name: "hizasız from, hizalı to",
			from: pwAt(10, 3, 0, 0), to: pwAt(11, 0, 0, 0),
			wantFrom: pwAt(9, 0, 0, 0), wantTo: pwAt(10, 0, 0, 0),
			wantCurrentBuckets: 12,
		},
		{
			// to = 11:00:00.700 → SQL `< 11:00:00` görür, 11:00 kovası
			// current'a girmez; prior da 12 kova okumalı, 13 değil.
			name: "saniye-altı uçlar — to saniyeye iner",
			from: pwAt(10, 0, 0, 500_000_000), to: pwAt(11, 0, 0, 700_000_000),
			wantFrom: pwAt(9, 0, 0, 0), wantTo: pwAt(10, 0, 0, 0),
			wantCurrentBuckets: 12,
		},
		{
			name: "saniye-altı to, kova sınırını bir saniye geçiyor",
			from: pwAt(10, 0, 0, 0), to: pwAt(11, 0, 1, 200_000_000),
			wantFrom: pwAt(8, 55, 0, 0), wantTo: pwAt(10, 0, 0, 0),
			wantCurrentBuckets: 13,
		},
		{
			name: "5 dk'dan kısa, tek kovanın içinde",
			from: pwAt(10, 1, 0, 0), to: pwAt(10, 3, 0, 0),
			wantFrom: pwAt(9, 55, 0, 0), wantTo: pwAt(10, 0, 0, 0),
			wantCurrentBuckets: 1,
		},
		{
			name: "5 dk'dan kısa ama kova sınırını kesiyor — iki kova",
			from: pwAt(10, 4, 0, 0), to: pwAt(10, 6, 0, 0),
			wantFrom: pwAt(9, 50, 0, 0), wantTo: pwAt(10, 0, 0, 0),
			wantCurrentBuckets: 2,
		},
		{
			name: "boş pencere (to == from) — prior da boş",
			from: pwAt(10, 0, 0, 0), to: pwAt(10, 0, 0, 0),
			wantFrom: pwAt(10, 0, 0, 0), wantTo: pwAt(10, 0, 0, 0),
			wantCurrentBuckets: 0,
		},
		{
			name: "ters pencere (to < from) — prior boş, panik yok",
			from: pwAt(10, 7, 0, 0), to: pwAt(10, 2, 0, 0),
			wantFrom: pwAt(10, 5, 0, 0), wantTo: pwAt(10, 5, 0, 0),
			wantCurrentBuckets: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pFrom, pTo := PriorWindow(c.from, c.to)
			if !pFrom.Equal(c.wantFrom) || !pTo.Equal(c.wantTo) {
				t.Fatalf("PriorWindow(%s, %s) = [%s, %s), beklenen [%s, %s)",
					c.from.Format("15:04:05.000"), c.to.Format("15:04:05.000"),
					pFrom.Format("15:04:05"), pTo.Format("15:04:05"),
					c.wantFrom.Format("15:04:05"), c.wantTo.Format("15:04:05"))
			}
			cur := readBuckets(c.from, c.to)
			if len(cur) != c.wantCurrentBuckets {
				t.Fatalf("current %d kova okudu, beklenen %d (test modeli kaymış)", len(cur), c.wantCurrentBuckets)
			}
			assertPriorProperties(t, c.from, c.to, pFrom, pTo)
		})
	}
}

// assertPriorProperties — iki özellik + bitişiklik + hiza.
func assertPriorProperties(t *testing.T, from, to, pFrom, pTo time.Time) {
	t.Helper()
	if !pFrom.Equal(alignBucketStart(pFrom)) || !pTo.Equal(alignBucketStart(pTo)) {
		t.Fatalf("prior uçları 5 dk ızgarasında değil: [%v, %v)", pFrom, pTo)
	}
	cur := readBuckets(from, to)
	prior := readBuckets(pFrom, pTo)
	seen := make(map[time.Time]bool, len(cur))
	for _, b := range cur {
		seen[b] = true
	}
	for _, b := range prior {
		if seen[b] {
			t.Fatalf("ORTAK KOVA %v — aynı beş dakika hem current hem prior sayılıyor (from=%v to=%v)",
				b, from, to)
		}
	}
	if len(prior) != len(cur) {
		t.Fatalf("kova sayısı eşit değil: current %d, prior %d (from=%v to=%v) — sayaç deltası sahte",
			len(cur), len(prior), from, to)
	}
	if len(cur) > 0 && !prior[len(prior)-1].Add(mvBucketWidth).Equal(cur[0]) {
		t.Fatalf("prior current'a BİTİŞİK değil: prior son %v, current ilk %v", prior[len(prior)-1], cur[0])
	}
}

// TestPriorWindowProperties — deterministik rastgele pencereler (sabit
// tohum): hizasız saniye + nanosaniye gürültüsü, 1 sn ile 3 gün arası boy.
func TestPriorWindowProperties(t *testing.T) {
	r := rand.New(rand.NewSource(1025))
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5000; i++ {
		from := base.Add(time.Duration(r.Int63n(int64(30 * 24 * time.Hour)))).Add(time.Duration(r.Intn(1e9)))
		length := time.Second + time.Duration(r.Int63n(int64(72*time.Hour)))
		to := from.Add(length).Add(time.Duration(r.Intn(1e9)))
		pFrom, pTo := PriorWindow(from, to)
		assertPriorProperties(t, from, to, pFrom, pTo)
	}
}

// TestOldPriorFormulaOverlapped — kusurun kendisi, belge olarak: eski
// `[from − dur, from)` hizasız from'da current'ın ilk kovasını prior'a da
// sayıyor. Bu test PriorWindow'u değil ESKİ formülü sınar; kırmızıya
// dönerse (örn. alignBucketStart değişti) bu dosyanın gerekçesi de
// yeniden okunmalı.
func TestOldPriorFormulaOverlapped(t *testing.T) {
	from, to := pwAt(10, 3, 0, 0), pwAt(11, 3, 0, 0)
	cur := readBuckets(from, to)
	old := readBuckets(from.Add(-to.Sub(from)), from)
	shared := 0
	for _, a := range old {
		for _, b := range cur {
			if a.Equal(b) {
				shared++
			}
		}
	}
	if shared != 1 {
		t.Fatalf("eski formülün ortak kova sayısı %d, beklenen 1 (10:00 kovası)", shared)
	}
}
