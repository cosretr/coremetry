package api

import "net/http"

// reqTagWriter — v0.10.918 (operatör prod logu: "[api] error: code: 241 …
// memory limit" ve "code: 159 … Timeout exceeded" satırları hangi uçtan
// geldiğini söylemiyordu). writeErr yalnız ResponseWriter alır (296 çağrı
// yeri); imzayı değiştirmek yerine mux'a giren her istek METOT + YOL ile
// etiketlenir ve writeErr etiketi yazıcıdan okur. Sorgu dizesi YAZILMAZ
// (arama metni / token taşıyabilir).
//
// Flush + Unwrap korunur: SSE uçları `w.(http.Flusher)` bekler,
// http.ResponseController Unwrap zincirini izler.
type reqTagWriter struct {
	http.ResponseWriter
	tag string
}

func (w *reqTagWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *reqTagWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func withReqTag(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&reqTagWriter{ResponseWriter: w, tag: r.Method + " " + r.URL.Path}, r)
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
