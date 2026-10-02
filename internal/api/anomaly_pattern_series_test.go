package api

// anomaly_pattern_series_test.go — v0.10.1060 (operatör: "Bunu doğru
// yakalamış ama artışın ne zaman başladığını göstermiyor. Elastic'e gidip
// bakınca barlardan net görüyorum."). GET /api/anomalies/log-pattern-series:
// pencere/kova sınırlaması, sıfır doldurma, anahtar, uç durumları.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/logstore"
)

var psNow = time.Date(2026, 10, 2, 12, 0, 30, 0, time.UTC)

func TestSnapPatternSeriesWindow(t *testing.T) {
	cases := []struct {
		name             string
		from, to         time.Time
		ok               bool
		wantFrom, wantTo time.Time
		wantBucket       int
	}{
		{"70 dk → 1 dk kova, uçlar hizalı", psNow.Add(-70 * time.Minute), psNow.Add(-30 * time.Second), true,
			time.Date(2026, 10, 2, 10, 50, 0, 0, time.UTC), time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), 60},
		{"to yok → now, yukarı hizalı", psNow.Add(-90 * time.Minute), time.Time{}, true,
			time.Date(2026, 10, 2, 10, 30, 0, 0, time.UTC), time.Date(2026, 10, 2, 12, 1, 0, 0, time.UTC), 60},
		{"gelecek to → now", psNow.Add(-60 * time.Minute), psNow.Add(time.Hour), true,
			time.Date(2026, 10, 2, 11, 0, 0, 0, time.UTC), time.Date(2026, 10, 2, 12, 1, 0, 0, time.UTC), 60},
		{"6 sa → 5 dk kova", psNow.Add(-6 * time.Hour), psNow.Add(-30 * time.Second), true,
			time.Date(2026, 10, 2, 6, 0, 0, 0, time.UTC), time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), 300},
		{"2.5 sa → 2 dk kova", psNow.Add(-150*time.Minute - 30*time.Second), psNow.Add(-30 * time.Second), true,
			time.Date(2026, 10, 2, 9, 30, 0, 0, time.UTC), time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), 120},
		{"30 gün → 7 güne kırpılır", psNow.Add(-30 * 24 * time.Hour), psNow, true,
			time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC), time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC), 7200},
		{"from yok", time.Time{}, psNow, false, time.Time{}, time.Time{}, 0},
		{"ters pencere", psNow, psNow.Add(-time.Hour), false, time.Time{}, time.Time{}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, to, b, ok := snapPatternSeriesWindow(c.from, c.to, psNow)
			if ok != c.ok {
				t.Fatalf("ok=%v, want %v", ok, c.ok)
			}
			if !ok {
				return
			}
			if !f.Equal(c.wantFrom) || !to.Equal(c.wantTo) || b != c.wantBucket {
				t.Fatalf("got %s → %s b=%d, want %s → %s b=%d", f.UTC(), to.UTC(), b, c.wantFrom, c.wantTo, c.wantBucket)
			}
			n := to.Sub(f) / (time.Duration(b) * time.Second)
			if n > patternSeriesMaxBuckets || n < 1 {
				t.Fatalf("kova sayısı %d sınır dışı", n)
			}
		})
	}
}

// Her istenebilir pencere boyunda kova sayısı ≤120 (yoğun tarama).
func TestSnapPatternSeriesWindow_NeverExceedsCap(t *testing.T) {
	for span := time.Minute; span <= 8*24*time.Hour; span += 37 * time.Minute {
		f, to, b, ok := snapPatternSeriesWindow(psNow.Add(-span), psNow, psNow)
		if !ok {
			t.Fatalf("span %s reddedildi", span)
		}
		if n := to.Sub(f) / (time.Duration(b) * time.Second); n > patternSeriesMaxBuckets {
			t.Fatalf("span %s → %d kova", span, n)
		}
	}
}

func TestFillPatternBuckets(t *testing.T) {
	from := time.Unix(1_000_020, 0) // 60'a hizalı
	to := from.Add(5 * time.Minute)
	ns := func(sec int64) int64 { return sec * int64(time.Second) }
	cases := []struct {
		name string
		in   []logstore.LogPoint
		want []int64
	}{
		{"boş → hepsi 0", nil, []int64{0, 0, 0, 0, 0}},
		{"seyrek → doldurulur", []logstore.LogPoint{{T: ns(1_000_080), V: 7}, {T: ns(1_000_200), V: 900}}, []int64{0, 7, 0, 900, 0}},
		{"kaymış hiza içine düştüğü kovaya", []logstore.LogPoint{{T: ns(1_000_110), V: 3}, {T: ns(1_000_150), V: 4}}, []int64{0, 3, 4, 0, 0}},
		{"ızgara dışı atılır", []logstore.LogPoint{{T: ns(999_000), V: 5}, {T: ns(1_000_320), V: 5}}, []int64{0, 0, 0, 0, 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := fillPatternBuckets(c.in, from, to, 60)
			if len(got) != len(c.want) {
				t.Fatalf("len %d, want %d", len(got), len(c.want))
			}
			for i, p := range got {
				if p.T != from.UnixNano()+int64(i)*int64(time.Minute) || p.V != c.want[i] {
					t.Fatalf("kova %d = %+v, want V=%d", i, p, c.want[i])
				}
			}
		})
	}
}

func TestLogPatternSeriesKey_HashesAllInputs(t *testing.T) {
	f, to := time.Unix(1_000_020, 0), time.Unix(1_007_220, 0)
	base := logPatternSeriesKey("Disk full", f, to, 60)
	variants := []string{
		logPatternSeriesKey("Go panic", f, to, 60),
		logPatternSeriesKey("Disk full", f.Add(time.Minute), to, 60),
		logPatternSeriesKey("Disk full", f, to.Add(time.Minute), 60),
		logPatternSeriesKey("Disk full", f, to, 120),
	}
	for i, v := range variants {
		if v == base {
			t.Fatalf("varyant %d anahtarı değiştirmedi: %s", i, v)
		}
	}
	if logPatternSeriesKey("Disk full", f, to, 60) != base {
		t.Fatal("anahtar kararlı değil")
	}
}

// patternSeriesLogStore — PatternHistogram dışındaki her çağrı gömülü nil
// arayüzde panic'ler (handler başka bir şeye dokunmamalı).
type patternSeriesLogStore struct {
	logstore.Store
	calls   int
	gotSpec logstore.PatternSpec
	gotB    int
	res     *logstore.PatternHistogramResult
	err     error
}

func (s *patternSeriesLogStore) PatternHistogram(_ context.Context, pat logstore.PatternSpec, _, _ time.Time, b int) (*logstore.PatternHistogramResult, error) {
	s.calls++
	s.gotSpec, s.gotB = pat, b
	return s.res, s.err
}
func (s *patternSeriesLogStore) Backend() string { return "test" }

func patternSeriesServer(st *patternSeriesLogStore) *Server {
	return &Server{logs: st, cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
}

func TestGetLogPatternSeries(t *testing.T) {
	now := time.Now()
	from := now.Add(-70 * time.Minute).UnixNano()
	url := func(q string) string { return "/api/anomalies/log-pattern-series?" + q }

	t.Run("desen yok → 400, arka uca gidilmez", func(t *testing.T) {
		st := &patternSeriesLogStore{}
		w := httptest.NewRecorder()
		patternSeriesServer(st).getLogPatternSeries(w, httptest.NewRequest("GET", url("from=1"), nil))
		if w.Code != 400 || st.calls != 0 {
			t.Fatalf("code=%d calls=%d", w.Code, st.calls)
		}
	})
	t.Run("bilinmeyen desen → 404", func(t *testing.T) {
		st := &patternSeriesLogStore{}
		w := httptest.NewRecorder()
		patternSeriesServer(st).getLogPatternSeries(w, httptest.NewRequest("GET", url("pattern=Nope&from=1"), nil))
		if w.Code != 404 || st.calls != 0 {
			t.Fatalf("code=%d calls=%d", w.Code, st.calls)
		}
	})
	t.Run("from yok → 400", func(t *testing.T) {
		st := &patternSeriesLogStore{}
		w := httptest.NewRecorder()
		patternSeriesServer(st).getLogPatternSeries(w, httptest.NewRequest("GET", url("pattern=Disk+full"), nil))
		if w.Code != 400 || st.calls != 0 {
			t.Fatalf("code=%d calls=%d", w.Code, st.calls)
		}
	})
	t.Run("veri → dedektörün spec'iyle sayılır, ızgara doldurulur", func(t *testing.T) {
		st := &patternSeriesLogStore{res: &logstore.PatternHistogramResult{Partial: true}}
		w := httptest.NewRecorder()
		patternSeriesServer(st).getLogPatternSeries(w,
			httptest.NewRequest("GET", url("pattern=Disk+full&from="+itoa64(from)), nil))
		if w.Code != 200 {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
		if st.gotSpec.Regex != `no space left on device|disk full|ENOSPC` || st.gotB != 60 {
			t.Fatalf("spec=%+v b=%d", st.gotSpec, st.gotB)
		}
		var body logPatternSeriesResponse
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		n := int((body.To - body.From) / int64(time.Minute))
		if body.Pattern != "Disk full" || body.BucketSec != 60 || len(body.Points) != n || n < 70 || n > 72 || !body.Partial {
			t.Fatalf("gövde: pattern=%q b=%d n=%d points=%d partial=%v", body.Pattern, body.BucketSec, n, len(body.Points), body.Partial)
		}
	})
	t.Run("yavaş arka uç → 502 (500 değil)", func(t *testing.T) {
		st := &patternSeriesLogStore{err: context.DeadlineExceeded}
		w := httptest.NewRecorder()
		patternSeriesServer(st).getLogPatternSeries(w,
			httptest.NewRequest("GET", url("pattern=Disk+full&from="+itoa64(from)), nil))
		if w.Code != 502 {
			t.Fatalf("code=%d", w.Code)
		}
	})
	t.Run("sorgu hatası → 5xx, yutulmaz", func(t *testing.T) {
		st := &patternSeriesLogStore{err: errors.New("parsing_exception")}
		w := httptest.NewRecorder()
		patternSeriesServer(st).getLogPatternSeries(w,
			httptest.NewRequest("GET", url("pattern=Go+panic&from="+itoa64(from)), nil))
		if w.Code < 500 {
			t.Fatalf("code=%d", w.Code)
		}
	})
}

func TestLogPatternSeriesRouteRegistered(t *testing.T) {
	if _, ok := extraRouteRegistrars["anomaly-pattern-series"]; !ok {
		t.Fatal("rota deftere kaydolmamış — kayıtsız rota 200 + boş SPA döner")
	}
}

func itoa64(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
