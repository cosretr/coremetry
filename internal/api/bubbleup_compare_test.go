package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBubbleUpCompareWindows(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	h := time.Hour
	cases := []struct {
		name           string
		sf, st, bf, bt time.Time
		ok             bool
	}{
		{"seçim tabanın içinde", t0.Add(h), t0.Add(2 * h), t0, t0.Add(6 * h), true},
		{"eksik", time.Time{}, t0, t0, t0.Add(h), false},
		{"ters pencere", t0.Add(h), t0, t0, t0.Add(h), false},
		{"birleşim 24 sa üstü", t0, t0.Add(h), t0.Add(-25 * h), t0, false},
	}
	for _, c := range cases {
		err := bubbleUpCompareWindows(c.sf, c.st, c.bf, c.bt)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestBubbleUpCompareRejectsMissingWindow(t *testing.T) {
	s := &Server{}
	rr := httptest.NewRecorder()
	s.spanBubbleUpCompare(rr, httptest.NewRequest(http.MethodGet, "/api/spans/bubbleup/compare?selFrom=1", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code=%d want 400", rr.Code)
	}
}
