package promapi

// v0.10.944 — DecodeSeriesMeta'nın iki bayrağı (isPartial, seri tavanı) ve
// tipli HTTP hatası. Üçü de SESSİZ kusurdu: kısmi/kesik cevap tam cevap
// gibi, 401 düz metin gibi okunuyordu.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/sourcestate"
)

func TestDecodeSeriesMetaFlags(t *testing.T) {
	one := `{"metric":{"k":"a"},"values":[[1,"1"]]}`
	tests := []struct {
		name          string
		body          string
		wantPartial   bool
		wantTruncated bool
		wantTotal     int
		wantLen       int
	}{
		{"tam cevap", `{"status":"success","data":{"resultType":"matrix","result":[` + one + `]}}`, false, false, 1, 1},
		{"isPartial VM", `{"status":"success","isPartial":true,"data":{"resultType":"matrix","result":[` + one + `]}}`, true, false, 1, 1},
		{"isPartial boş sonuçla", `{"status":"success","isPartial":true,"data":{"resultType":"matrix","result":[]}}`, true, false, 0, 0},
		{"data yok", `{"status":"success"}`, false, false, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := DecodeSeriesMeta("vm", []byte(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if res.IsPartial != tt.wantPartial || res.Truncated != tt.wantTruncated ||
				res.Total != tt.wantTotal || len(res.Series) != tt.wantLen {
				t.Fatalf("got partial=%v truncated=%v total=%d len=%d", res.IsPartial, res.Truncated, res.Total, len(res.Series))
			}
		})
	}
}

// Tavan: MaxSeriesParsed'ten fazlası kesilir VE söylenir; Total gerçek sayı.
func TestDecodeSeriesMetaReportsTheCap(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"status":"success","data":{"resultType":"matrix","result":[`)
	n := MaxSeriesParsed + 5
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"metric":{"i":"%d"},"values":[]}`, i)
	}
	b.WriteString(`]}}`)
	res, err := DecodeSeriesMeta("vm", []byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || res.Total != n || len(res.Series) != MaxSeriesParsed {
		t.Fatalf("tavan ifşa edilmedi: truncated=%v total=%d len=%d", res.Truncated, res.Total, len(res.Series))
	}
	// Eski sözleşme: DecodeSeries aynı kesimi yapar, bayraksız.
	plain, err := DecodeSeries("vm", []byte(b.String()))
	if err != nil || len(plain) != MaxSeriesParsed {
		t.Fatalf("DecodeSeries sözleşmesi değişti: len=%d err=%v", len(plain), err)
	}
}

// status:error metni iki decoder'da bayt-aynı kalmalı (tek gövde).
func TestDecodeSeriesMetaErrorTextMatchesDecodeSeries(t *testing.T) {
	body := []byte(`{"status":"error","errorType":"bad_data","error":"unknown label"}`)
	_, e1 := DecodeSeries("vm", body)
	_, e2 := DecodeSeriesMeta("vm", body)
	if e1 == nil || e2 == nil || e1.Error() != e2.Error() {
		t.Fatalf("hata metni ayrıştı: %v | %v", e1, e2)
	}
}

// 401/403 → sourcestate.ErrUnauthorized (errors.Is); metin eskisiyle aynı;
// 503 sentinel taşımaz ama metin sinyaliyle unreachable'a düşer.
func TestDoTypedHTTPError(t *testing.T) {
	for _, tc := range []struct {
		code  int
		state sourcestate.State
		unau  bool
	}{
		{http.StatusUnauthorized, sourcestate.Unauthorized, true},
		{http.StatusForbidden, sourcestate.Unauthorized, true},
		{http.StatusServiceUnavailable, sourcestate.Unreachable, false},
		{http.StatusGatewayTimeout, sourcestate.Timeout, false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.code)
			_, _ = w.Write([]byte("  denied by proxy  "))
		}))
		_, err := Do(context.Background(), Request{Label: "victoriametrics", BaseURL: srv.URL, Path: "/api/v1/labels"})
		srv.Close()
		if err == nil {
			t.Fatalf("%d: hata bekleniyordu", tc.code)
		}
		var he *HTTPError
		if !errors.As(err, &he) || he.Status != tc.code {
			t.Fatalf("%d: tipli HTTPError değil: %T %v", tc.code, err, err)
		}
		want := fmt.Sprintf("victoriametrics: HTTP %d: denied by proxy", tc.code)
		if err.Error() != want {
			t.Fatalf("%d: metin değişti: %q (beklenen %q)", tc.code, err.Error(), want)
		}
		if errors.Is(err, sourcestate.ErrUnauthorized) != tc.unau {
			t.Fatalf("%d: ErrUnauthorized eşlemesi yanlış", tc.code)
		}
		if got := sourcestate.Classify(err); got != tc.state {
			t.Fatalf("%d: Classify = %s, beklenen %s", tc.code, got, tc.state)
		}
	}
}

// v0.10.944 — etiket uçlarının isPartial'ı: vmselect /api/v1/labels ve
// /api/v1/label/<k>/values cevaplarında da taşır; eksik liste "etiket yok"
// diye okunmamalı. Alan yoksa false; DecodeStrings sözleşmesi değişmez.
func TestDecodeStringsMetaPartial(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantPartial bool
		wantVals    string
	}{
		{"isPartial etiket listesi", `{"status":"success","isPartial":true,"data":["job"]}`, true, "job"},
		{"alan yok → false", `{"status":"success","data":["job","pod"]}`, false, "job,pod"},
		{"data null", `{"status":"success","isPartial":true,"data":null}`, true, ""},
		{"data yok", `{"status":"success"}`, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := DecodeStringsMeta("vm", []byte(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if res.IsPartial != tt.wantPartial || strings.Join(res.Values, ",") != tt.wantVals {
				t.Fatalf("got partial=%v vals=%v", res.IsPartial, res.Values)
			}
			plain, err := DecodeStrings("vm", []byte(tt.body))
			if err != nil || strings.Join(plain, ",") != tt.wantVals {
				t.Fatalf("DecodeStrings sözleşmesi değişti: %v %v", plain, err)
			}
		})
	}
	// Hata metni iki decoder'da bayt-aynı (tek gövde).
	for _, body := range []string{
		`{"status":"error","errorType":"bad_data","error":"unknown label"}`,
		`{"status":"success","data":{"not":"array"}}`,
		`not json`,
	} {
		_, e1 := DecodeStrings("vm", []byte(body))
		_, e2 := DecodeStringsMeta("vm", []byte(body))
		if e1 == nil || e2 == nil || e1.Error() != e2.Error() {
			t.Fatalf("%s: hata metni ayrıştı: %v | %v", body, e1, e2)
		}
	}
}

// QueryStringsMeta tel üzerinden isPartial'ı taşır.
func TestQueryStringsMetaWire(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","isPartial":true,"data":["namespace"]}`))
	}))
	defer srv.Close()
	res, err := QueryStringsMeta(context.Background(), Request{Label: "victoriametrics", BaseURL: srv.URL, Path: "/api/v1/labels"})
	if err != nil || !res.IsPartial || len(res.Values) != 1 {
		t.Fatalf("QueryStringsMeta: %+v %v", res, err)
	}
}
