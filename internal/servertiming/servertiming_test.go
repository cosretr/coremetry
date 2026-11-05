package servertiming

// servertiming_test.go — v0.10.1817 (Log Search Faz 1 dilim 1.1): Server-Timing
// başlığının biçimi ve kaydedicisiz bağlamın no-op sözleşmesi.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHeaderFormat(t *testing.T) {
	_, empty := With(context.Background())
	ctx, two := With(context.Background())
	Add(ctx, "es", 12*time.Millisecond+300*time.Microsecond)
	Add(ctx, "es", 2*time.Millisecond)
	Add(ctx, "ch", 1500*time.Microsecond)
	cases := []struct {
		name  string
		r     *Recorder
		total time.Duration
		tier  string
		want  string
	}{
		{"nil kaydedici yalnız total", nil, 3 * time.Millisecond, "", "total;dur=3.0"},
		{"boş kaydedici + katman", empty, 250 * time.Microsecond, "HIT-L1", `cache;desc="HIT-L1", total;dur=0.2`},
		{"iki metrik sıralı, çoklu istek desc", two, 20 * time.Millisecond, "MISS",
			`cache;desc="MISS", ch;dur=1.5, es;dur=14.3;desc="2 req", total;dur=20.0`},
	}
	for _, c := range cases {
		if got := c.r.Header(c.total, c.tier); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestAddWithoutRecorderIsNoop(t *testing.T) {
	Add(context.Background(), "es", time.Second) // panik yok
	if From(context.Background()) != nil {
		t.Fatal("kaydedicisiz bağlamda From nil olmalı")
	}
}

func TestTransportRecordsOnlyWithRecorder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	cli := &http.Client{Transport: Transport("es", nil)}

	ctx, rec := With(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	res, err := cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if rec.dur["es"] < 5*time.Millisecond || rec.n["es"] != 1 {
		t.Fatalf("es süresi kaydedilmedi: %v (n=%d)", rec.dur["es"], rec.n["es"])
	}

	req2, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	res2, err := cli.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
}
