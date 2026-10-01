package chstore

// db_detail_prior_test.go — v0.10.1025 (Databases dilim 3): /database
// detayının önceki-pencere agregesi.
//
// Çivilenen dört şey:
//  1. HasPrior yalnız BAŞARILI ve çağrılı bir prior'da true (sıfır çağrılı
//     prior'a karşı her karo "önce 0" ya da sonsuz artış basardı).
//  2. MV'nin 90 günlük TTL ufkunu aşan prior OKUNMAZ (eksik prior sayımı
//     current'ı FAZLA gösterir = sahte KÖTÜLEŞME, kırmızı ↑).
//  3. Prior* alanları omitempty TAŞIMAZ — sıfır bir ölçümdür, ayrımı
//     hasPrior yapar (OperationSummary.HasPrior emsali).
//  4. GetDatabaseDetail prior penceresini PAYLAŞILAN PriorWindow'dan alır
//     ve iki okuma da AYNI agregeden (dbDetailAggregate) geçer.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDBDetailHasPrior(t *testing.T) {
	boom := errors.New("prior read timed out")
	cases := []struct {
		name  string
		err   error
		spans uint64
		want  bool
	}{
		{"başarılı ve çağrılı → var", nil, 42, true},
		{"başarılı ama sıfır çağrı → YOK (kıyaslanacak şey yok)", nil, 0, false},
		{"okuma düştü → YOK, sayaç ne olursa olsun", boom, 42, false},
		{"okuma düştü ve sıfır → YOK", boom, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := dbDetailHasPrior(c.err, c.spans); got != c.want {
				t.Errorf("dbDetailHasPrior(%v, %d) = %v, beklenen %v", c.err, c.spans, got, c.want)
			}
		})
	}
}

func TestApplyDBDetailPrior(t *testing.T) {
	cur := func() *DBDetail {
		return &DBDetail{System: "oracle", Instance: "db-a", SpanCount: 100, ErrorCount: 3,
			ErrorRate: 3, AvgMs: 12, P50Ms: 8, P95Ms: 40, P99Ms: 90}
	}
	prior := dbDetailAgg{SpanCount: 80, ErrorCount: 0, ErrorRate: 0,
		AvgMs: 10, P50Ms: 7, P95Ms: 30, P99Ms: 70}

	t.Run("çağrılı prior kopyalanır, SIFIR hata sayısı dahil (ölçek 1)", func(t *testing.T) {
		d := cur()
		applyDBDetailPrior(d, prior, nil, 1)
		if !d.HasPrior {
			t.Fatal("HasPrior false — delta'lar hiç çizilmez")
		}
		if d.PriorSpanCount != 80 || d.PriorErrorCount != 0 || d.PriorErrorRate != 0 ||
			d.PriorAvgMs != 10 || d.PriorP50Ms != 7 || d.PriorP95Ms != 30 || d.PriorP99Ms != 70 ||
			d.PriorScale != 1 {
			t.Errorf("prior alanları yanlış kopyalandı: %+v", d)
		}
		if d.SpanCount != 100 || d.ErrorCount != 3 || d.AvgMs != 12 || d.P99Ms != 90 {
			t.Errorf("CURRENT alanları değişti: %+v", d)
		}
	})

	// v0.10.1025 R1 — canlı pencere: yalnız SAYAÇLAR ölçeklenir, oran ve
	// gecikme dokunulmaz; HasPrior ölçeksiz sayaçtan karar verir.
	t.Run("canlı kenar: sayaçlar ölçeklenir, oran/gecikme değil", func(t *testing.T) {
		d := cur()
		applyDBDetailPrior(d, dbDetailAgg{SpanCount: 80, ErrorCount: 9, ErrorRate: 11.25,
			AvgMs: 10, P50Ms: 7, P95Ms: 30, P99Ms: 70}, nil, 0.875)
		if d.PriorSpanCount != 70 || d.PriorErrorCount != 8 {
			t.Errorf("ölçekli sayaçlar (80, 9)×0.875 = (%d, %d), beklenen (70, 8)", d.PriorSpanCount, d.PriorErrorCount)
		}
		if d.PriorErrorRate != 11.25 || d.PriorAvgMs != 10 || d.PriorP95Ms != 30 || d.PriorP99Ms != 70 {
			t.Errorf("oran/gecikme ölçeklenmiş: %+v", d)
		}
		if d.PriorScale != 0.875 {
			t.Errorf("PriorScale = %v, beklenen 0.875 (arayüz bunu söyler)", d.PriorScale)
		}
	})

	t.Run("geçersiz ölçek (0, >1, NaN) → 1", func(t *testing.T) {
		for _, sc := range []float64{0, -1, 1.5} {
			d := cur()
			applyDBDetailPrior(d, prior, nil, sc)
			if d.PriorScale != 1 || d.PriorSpanCount != 80 {
				t.Errorf("ölçek %v: PriorScale=%v spans=%d", sc, d.PriorScale, d.PriorSpanCount)
			}
		}
	})

	t.Run("sıfır çağrılı prior → hiçbir alan yazılmaz", func(t *testing.T) {
		d := cur()
		applyDBDetailPrior(d, dbDetailAgg{SpanCount: 0, AvgMs: 5}, nil, 1)
		if d.HasPrior || d.PriorAvgMs != 0 || d.PriorSpanCount != 0 {
			t.Errorf("boş prior yüke sızdı: %+v", d)
		}
	})

	t.Run("hatalı prior → hiçbir alan yazılmaz", func(t *testing.T) {
		d := cur()
		applyDBDetailPrior(d, prior, errors.New("x"), 0.5)
		if d.HasPrior || d.PriorSpanCount != 0 || d.PriorP99Ms != 0 || d.PriorScale != 0 {
			t.Errorf("hatalı prior yüke sızdı: %+v", d)
		}
	})
}

func TestDBPriorReadable(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	cases := []struct {
		name       string
		pFrom, pTo time.Time
		want       bool
	}{
		{"son bir saat — okunur", now.Add(-2 * time.Hour), now.Add(-time.Hour), true},
		{"tam 89 gün önce başlıyor — sınır DAHİL", now.Add(-89 * day), now.Add(-60 * day), true},
		{"89 günden bir saniye eski — okunmaz (TTL günü kayabilir)", now.Add(-89*day - time.Second), now.Add(-60 * day), false},
		{"120 gün önce (60 günlük pencerenin prior'u) — okunmaz", now.Add(-120 * day), now.Add(-60 * day), false},
		{"boş prior penceresi — okunmaz", now.Add(-time.Hour), now.Add(-time.Hour), false},
		{"ters prior penceresi — okunmaz", now.Add(-time.Hour), now.Add(-2 * time.Hour), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := dbPriorReadable(c.pFrom, c.pTo, now); got != c.want {
				t.Errorf("dbPriorReadable = %v, beklenen %v", got, c.want)
			}
		})
	}
	// Ufuk sabitten TÜREMELİ: biri MV TTL'ini (ve dbMVHorizonDays'i)
	// değiştirirse bu kapı da kendiliğinden kayar, ayrı bir sayı yok.
	edge := now.Add(-time.Duration(dbMVHorizonDays-1) * day)
	if !dbPriorReadable(edge, now, now) || dbPriorReadable(edge.Add(-time.Second), now, now) {
		t.Error("ufuk dbMVHorizonDays − 1 günde değil")
	}
}

// Prior* alanları omitempty TAŞIMAZ: "önceki pencerede 0 hata" bir ölçüm.
// hasPrior=false iken de anahtarlar yükte durur; arayüz onları hasPrior'a
// bakmadan okumaz.
func TestDBDetailPriorJSONShape(t *testing.T) {
	b, err := json.Marshal(DBDetail{HasPrior: true, PriorSpanCount: 10})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, k := range []string{
		`"hasPrior":true`, `"priorSpanCount":10`, `"priorErrorCount":0`, `"priorErrorRate":0`,
		`"priorAvgDurationMs":0`, `"priorP50DurationMs":0`, `"priorP95DurationMs":0`, `"priorP99DurationMs":0`,
		`"priorScale":0`,
	} {
		if !strings.Contains(s, k) {
			t.Errorf("JSON'da %s yok — sıfır prior omitempty ile düşmüş olabilir:\n%s", k, s)
		}
	}
	b, _ = json.Marshal(DBDetail{})
	if !strings.Contains(string(b), `"hasPrior":false`) {
		t.Errorf("hasPrior=false yükte yok: %s", b)
	}
}

// Kaynak pini: GetDatabaseDetail prior penceresini paylaşılan
// PriorWindow'dan alır, ufuk ve kapsama kapısından geçirir, sayaçları
// kapsama oranıyla ölçekler ve current ile prior AYNI agregeden okunur.
// Yorumlar süzülür — kapı koda bakar, prozaya değil.
//
// v0.10.1025 inceleme R6c — prior okuması çağıran ve ifade okumalarından
// SONRA koşar: GetDatabaseDetail'de satır içi prior okuması YOK, yalnız ilk
// `return out, nil`dan ÖNCE kaydedilen bir `defer` var (erken dönüşler de
// kapsansın diye).
func TestGetDatabaseDetailUsesSharedPriorWindow(t *testing.T) {
	body := stripGoCommentsCH(funcSource(t, "dependencies.go", "func (s *Store) GetDatabaseDetail("))
	if n := strings.Count(body, "s.dbDetailAggregate(ctx, "); n != 1 {
		t.Errorf("GetDatabaseDetail dbDetailAggregate'i %d kez çağırıyor, beklenen 1 (current) — "+
			"prior attachDBDetailPrior'da, en sonda", n)
	}
	if strings.Contains(body, "PriorWindow(") {
		t.Error("GetDatabaseDetail prior'u satır içinde okuyor — çağıran/ifade okumalarından ÖNCE koşar (R6c)")
	}
	iDefer := strings.Index(body, "defer s.attachDBDetailPrior(ctx, out, from, to, system, mvInstance, mvNameSQL, mvNameArgs)")
	iReturn := strings.Index(body, "return out, nil")
	if iDefer < 0 {
		t.Fatal("GetDatabaseDetail prior'u defer ile eklemiyor")
	}
	if iReturn >= 0 && iDefer > iReturn {
		t.Error("defer ilk `return out, nil`dan SONRA kaydediliyor — erken dönüşlerde prior hiç eklenmez")
	}
	// Eski formül geri gelmesin: from'dan süre kadar geri kaydırma.
	if strings.Contains(body, "from.Add(-") {
		t.Error("GetDatabaseDetail elle geri kaydırma yapıyor — PriorWindow'u atlıyor")
	}

	attach := stripGoCommentsCH(funcSource(t, "dependencies.go", "func (s *Store) attachDBDetailPrior("))
	for _, want := range []string{
		"PriorWindow(from, to)",
		"dbPriorReadable(pFrom, pTo, now)",
		"s.sourceCovers(ctx, priorSrcDBCallerSummary, pFrom)",
		"applyDBDetailPrior(out, prior, perr, PriorCoverage(from, to, now))",
	} {
		if !strings.Contains(attach, want) {
			t.Errorf("attachDBDetailPrior %q içermiyor — kapı/ölçek paylaşılan türetimden kopmuş", want)
		}
	}
	if n := strings.Count(attach, "s.dbDetailAggregate(ctx, "); n != 1 {
		t.Errorf("attachDBDetailPrior dbDetailAggregate'i %d kez çağırıyor, beklenen 1", n)
	}
	// Kapılar okumadan ÖNCE.
	if strings.Index(attach, "s.sourceCovers(") > strings.Index(attach, "s.dbDetailAggregate(") {
		t.Error("kapsama kapısı okumadan SONRA — eksik prior okunup çizilir")
	}
	// Agregenin üst sınırı `<` (v0.9.1156 sözleşmesi) ve alt sınır >=.
	agg := funcSource(t, "dependencies.go", "func (s *Store) dbDetailAggregate(")
	if op := upperBoundOp(t, "dbDetailAggregate", agg); op != "<" {
		t.Errorf("dbDetailAggregate üst sınırı %q — `<` olmalı", op)
	}
}
