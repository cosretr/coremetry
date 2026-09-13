package chstore

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// trace_raw_stream_test.go — v0.10.708 (Operator-reported, prod: "çok trace
// ingest ediliyor ama arama az ve seyrek buluyor").
//
// BULUNAN HATA: akışkan 1. aşama (zaman/süre sıralı, HAVING'siz ham yol)
// LIMIT 4×k SPAN satırı çekip trace id'ye indiriyordu ve k'nın altında
// kalınca yeniden çekmiyordu. 50'lik sayfa = 200 span; 100-span'lı
// trace'lerde bu 2-5 trace eder; hasMore = len(cands) > limit → false, yani
// "başka yok". Operatör az ve seyrek trace görüyordu, sayfalayamıyordu.
//
// SÖZLEŞME: tekil trace wantK'ya ulaşana ya da kaynak tükenene (satır <
// limit) dek limit ×4 büyür; tavana çarpınca capped=true ve hasMore true.

func mkRows(limit, spansPerTrace int, offset int) []stage1Cand {
	out := make([]stage1Cand, 0, limit)
	for i := 0; i < limit; i++ {
		id := "t" + itoaClustersLocal((offset+i)/spansPerTrace)
		out = append(out, stage1Cand{id: id, t0: int64(1_000_000 - offset - i), t1: int64(1_000_000 - offset - i)})
	}
	return out
}

func itoaClustersLocal(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

func TestStreamStage1Cands_RefetchesUntilDistinct(t *testing.T) {
	// 100 span/trace: ilk tur 200 satır = 2 trace; ×4 büyüyerek 50 trace'e ulaşmalı.
	var limits []int
	cands, capped, err := streamStage1Cands(50, func(limit int) ([]stage1Cand, error) {
		limits = append(limits, limit)
		return mkRows(limit, 100, 0), nil // hep dolu: kaynak tükenmedi
	})
	if err != nil || capped {
		t.Fatalf("err=%v capped=%v", err, capped)
	}
	if len(cands) != 50 {
		t.Fatalf("50 tekil trace beklenir, %d (limitler %v)", len(cands), limits)
	}
	if limits[0] != 200 || len(limits) < 3 || limits[len(limits)-1] < 5000 {
		t.Fatalf("limit ×4 büyümeli: %v", limits)
	}
	if cands[0].id != "t0" || cands[49].id != "t49" {
		t.Fatalf("sıra fetch sırası (en yeni önce) korunmalı: %s..%s", cands[0].id, cands[49].id)
	}
}

func TestStreamStage1Cands_StopsOnExhaustion(t *testing.T) {
	// Pencerede toplam 120 span = 3 trace; ilk tur 200 istenir, 120 gelir → tükendi.
	calls := 0
	cands, capped, err := streamStage1Cands(50, func(limit int) ([]stage1Cand, error) {
		calls++
		return mkRows(120, 40, 0), nil
	})
	if err != nil || capped || calls != 1 || len(cands) != 3 {
		t.Fatalf("tükenince tek tur, 3 trace: calls=%d cands=%d capped=%v err=%v", calls, len(cands), capped, err)
	}
}

func TestStreamStage1Cands_CapIsHonest(t *testing.T) {
	// 100.000 span/trace (patolojik): tavana kadar büyür, capped=true → hasMore true.
	var last int
	cands, capped, err := streamStage1Cands(50, func(limit int) ([]stage1Cand, error) {
		last = limit
		return mkRows(limit, 100000, 0), nil
	})
	if err != nil || !capped || len(cands) >= 50 || last != traceRawStage1OverFetchMax {
		t.Fatalf("tavan: capped=%v cands=%d last=%d err=%v", capped, len(cands), last, err)
	}
	if traceRawStage1OverFetchNext(traceRawStage1OverFetchMax) != traceRawStage1OverFetchMax {
		t.Fatal("tavanda aynı değer")
	}
	if traceRawStage1OverFetchNext(200) != 800 {
		t.Fatal("×4")
	}
}

func TestStreamStage1Cands_ErrorPropagates(t *testing.T) {
	want := errors.New("code: 241")
	if _, _, err := streamStage1Cands(50, func(int) ([]stage1Cand, error) { return nil, want }); !errors.Is(err, want) {
		t.Fatalf("hata aynen yüzeye: %v", err)
	}
}

// Kablolama pini: repo.go akışkan dalı streamStage1Cands kullanır ve hasMore
// tavanı görür — "test edilmiş ama ulaşılamaz" sınıfı.
func TestStreamStage1Wiring(t *testing.T) {
	b, err := os.ReadFile("repo.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"got, stage1Capped, err = streamStage1Cands(wantK, func(limit int) ([]stage1Cand, error) {",
		"hasMore = len(cands) > f.Limit || stage1Capped",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("repo.go %q içermeli", want)
		}
	}
	if strings.Contains(src, "s1args = append(s1args, traceRawStage1OverFetch(wantK))") {
		t.Error("eski tek-tur overfetch geri gelmiş")
	}
}
