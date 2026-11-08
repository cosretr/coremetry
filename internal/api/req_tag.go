package api

import (
	"net/http"
	"strings"
	"time"
)

// reqTagWriter — v0.10.918 (operatör prod logu: "[api] error: code: 241 …
// memory limit" ve "code: 159 … Timeout exceeded" satırları hangi uçtan
// geldiğini söylemiyordu). writeErr yalnız ResponseWriter alır (296 çağrı
// yeri); imzayı değiştirmek yerine mux'a giren her istek METOT + YOL ile
// etiketlenir ve writeErr etiketi yazıcıdan okur. Sorgu dizesi YAZILMAZ
// (arama metni / token taşıyabilir).
//
// Flush + Unwrap korunur: SSE uçları `w.(http.Flusher)` bekler,
// http.ResponseController Unwrap zincirini izler.
//
// v0.10.1422 — durum kodu da yakalanır (http_server_metrics.go): ilk
// WriteHeader ya da başlıksız ilk Write (= 200).
type reqTagWriter struct {
	http.ResponseWriter
	tag    string
	status int
	// v0.10.1971 — ham sorgu dizesi; YALNIZ ClickHouse kaynak hatasında (bellek / zaman aşımı) loga
	// yazılır (chResourceQuerySuffix). Prod'da system.query_log yok: patlayan Traces sorgusunun hangi
	// süzgeç + pencereyle geldiğini görmenin tek yolu bu.
	rawQuery string
}

func (w *reqTagWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *reqTagWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *reqTagWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *reqTagWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func withReqTag(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		tw := &reqTagWriter{ResponseWriter: w, tag: r.Method + " " + r.URL.Path, rawQuery: r.URL.RawQuery}
		next.ServeHTTP(tw, r)
		recordHTTPServer(r, tw, time.Since(start)) // v0.10.1422
	})
}

// reqTagSuffix — SAF: " [GET /api/x]" ya da "" (etiketsiz yazıcı; handler
// yazıcıyı kendi tipine sardıysa Unwrap zinciri izlenir).
func reqTagSuffix(w http.ResponseWriter) string {
	for i := 0; i < 8 && w != nil; i++ {
		if t, ok := w.(*reqTagWriter); ok {
			return " [" + t.tag + "]"
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return ""
		}
		w = u.Unwrap()
	}
	return ""
}

// chResourceErr — SAF: ClickHouse bellek sınırı (241 MEMORY_LIMIT_EXCEEDED) ya da sorgu zaman aşımı
// (159 TIMEOUT_EXCEEDED). İkisi de "bu süzgeç + pencere çok pahalı" der; teşhis için isteğin
// süzgeçleri gerekir.
func chResourceErr(err error) bool {
	if err == nil {
		return false
	}
	m := err.Error()
	return strings.Contains(m, "code: 241") || strings.Contains(m, "MEMORY_LIMIT_EXCEEDED") ||
		strings.Contains(m, "code: 159") || strings.Contains(m, "TIMEOUT_EXCEEDED")
}

// maxLoggedQuery — log satırını sınırlar; Traces süzgeç dizesi bunun çok altında.
const maxLoggedQuery = 1500

// chResourceQuerySuffix — v0.10.1971: ClickHouse kaynak hatasında " q=<sorgu dizesi>" (kırpılmış),
// diğer hatalarda "". v0.10.918'in "sorgu dizesi yazılmaz" kuralının TEK istisnası: yalnız bu hata
// sınıfı, yalnız teşhis için.
func chResourceQuerySuffix(w http.ResponseWriter, err error) string {
	if !chResourceErr(err) {
		return ""
	}
	for i := 0; i < 8 && w != nil; i++ {
		if t, ok := w.(*reqTagWriter); ok {
			q := t.rawQuery
			if q == "" {
				return ""
			}
			if len(q) > maxLoggedQuery {
				q = q[:maxLoggedQuery] + "…"
			}
			return " q=" + q
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return ""
		}
		w = u.Unwrap()
	}
	return ""
}
