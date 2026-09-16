package api

// trace_root_def_settings.go — v0.10.733 (operatör onaylı spec 2026-09-16).
//
// Kök tanımı ayarı: strict (tam kök) | entry (giriş kökü). Prod ölçümü
// (2026-09-13): trace'lerin %40-53'ünde tam kök var; en büyük neden
// gateway'in server span'ine üst katmanın traceparent basması. Tanım
// operatör kararı, system_settings 'trace_root_def' blob'unda; boot'ta
// hidre edilir, 30 s'de bir yenilenir (çok-pod yakınsaması —
// metric_exclusions kablosunun aynısı, leader gerekmez: bu bir OKUMA).
//
//   GET /api/settings/trace-root-def   (tüm roller: Traces Root kutusu
//                                       başlığı hangi tanımın geçerli
//                                       olduğunu yazar; viewer GÖRÜR)
//   PUT /api/settings/trace-root-def   (admin; audit settings.update)
//
// api.go BÜYÜMEZ: rotalar route_registry defterine init() ile kaydolur.
// Cache anahtarları: traces listesi (yalnız rootOnly=true iken ek soneki
// taşır — tanım yalnız o hâlde cevabı değiştirir), metric-batch (root
// bayrağı açıkken) ve kök kapsaması (cevap etkin tanımı taşır) — v0.5.187
// "anahtar TÜM girdileri taşır" kuralı; girmeseydi tanım değişince eski
// liste servis edilirdi.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("trace-root-def", (*Server).registerTraceRootDefRoutes) }

func (s *Server) registerTraceRootDefRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/trace-root-def", s.getTraceRootDef)
	mux.Handle("PUT /api/settings/trace-root-def",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putTraceRootDef)))
}

// LoadTraceRootDef — boot hidrasyonu + yenileme tiki. Okuma hatası boot'u
// düşürmez: strict (dar tanım) ile devam edilir ve loglanır.
func (s *Server) LoadTraceRootDef(ctx context.Context) {
	s.store.SetTraceRootDef(s.store.GetTraceRootDefSetting(ctx))
}

func (s *Server) StartTraceRootDefRefresh(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.LoadTraceRootDef(ctx)
		}
	}
}

// tracesRootDefKeySuffix — SAF: traces listesi anahtarı yalnız Root süzgeci
// AÇIKKEN tanımı taşır (kapalıyken tanım cevabı değiştirmez; anahtar
// kararlı kalır, dağıtımda soğuk cache olmaz).
func tracesRootDefKeySuffix(q url.Values, def chstore.TraceRootDef) string {
	if q.Get("rootOnly") == "true" {
		return ":rd=" + string(def)
	}
	return ""
}

type traceRootDefPayload struct {
	Def string `json:"def"`
}

// getTraceRootDef — KAYITLI tanım (bu pod henüz yenilememiş olabilir;
// ayar yüzeyi kaydedileni göstermeli).
func (s *Server) getTraceRootDef(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, traceRootDefPayload{Def: string(s.store.GetTraceRootDefSetting(r.Context()))})
}

func (s *Server) putTraceRootDef(w http.ResponseWriter, r *http.Request) {
	var in traceRootDefPayload
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if in.Def != string(chstore.TraceRootDefStrict) && in.Def != string(chstore.TraceRootDefEntry) {
		writeJSONError(w, http.StatusBadRequest, "def 'strict' ya da 'entry' olmalı")
		return
	}
	def := chstore.ParseTraceRootDef(in.Def)
	if err := s.store.SaveTraceRootDefSetting(r.Context(), def); err != nil {
		writeErr(w, err)
		return
	}
	s.store.SetTraceRootDef(def) // bu pod anında; diğerleri yenileme tikinde
	s.audit(r, "settings.update", "trace_root_def", "trace_root_def", `{"def":"`+string(def)+`"}`)
	log.Printf("[settings] trace_root_def: %s", def)
	writeJSON(w, traceRootDefPayload{Def: string(def)})
}
