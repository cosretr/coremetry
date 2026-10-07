package api

// rootcause_progressive.go — v0.10.1119 (operatör bildirimi: kök-neden
// paneli soğukta 30–45 sn, sıcakta hızlı). Kök neden: /rootcause demeti
// TEK yanıt ve yanıt en yavaş alt-okumayı bekliyor. Alt-okumaların hepsi
// MV / FINAL nokta okuması (deploy, korelasyon, topoloji, blast radius,
// hipotez; exemplar servis+zaman önekli top-1) — TEK istisna BubbleUp:
// 1 saate (gecikme ailesinde baseline ile 2 saate) kadar ham spans
// üzerinde arrayJoin(attr_keys) keşfi + 30'a dek anahtar başı attr dizisi
// taraması (v0.9.1082 ölçümü: anahtar başı medyan 1,3 sn). Panel bu yüzden
// her soğuk açılışta hiçbir şey çizmeden onlarca saniye bekliyordu.
//
// Çözüm: demet İKİ uca bölünür, UI ikisini AYNI ANDA ister ve çekirdeği
// gelir gelmez çizer (aşamalı çizim):
//
//	GET /api/problems/{id}/rootcause/core     → RootCause, bubbleUp YOK
//	GET /api/problems/{id}/rootcause/bubbleup → {fromNs, toNs, bubbleUp}
//	GET /api/anomalies/{id}/rootcause/core    → AnomalyRootCause, bubbleUp YOK
//
// Çekirdek tam demetin AYNI fan-out'u (problemRootCauseBundle withBubble=
// false) — alan alan aynı; bubbleUp ucu tam demetteki AYNI çağrıyı yapar
// (aynı pencere işlevi, aynı aile seçimi). Eşdeğerlik: rootcause_progressive
// _test.go. Tam /rootcause API sözleşmesi olarak AYNEN durur.
//
// Önbellek: serveCached 60 s (tam demetle aynı tazelik), anahtar tam demetin
// kimlik anahtarı + parça öneki (id + started + resolved — saat bileşeni
// YOK, v0.9.1082). Hesaplar istek iptalinden kopuk (rootCauseComputeCtx):
// çekmeceyi kapatan lider istemci paylaşılan hesabı öldürmez, sonuç yine
// önbelleğe girer. BubbleUp HATASI önbelleğe YAZILMAZ (500 döner; sonraki
// açılış yeniden dener) — tam demetteki gibi sessizce bubbleUp'sız bir
// gövdeyi 60 sn taze diye saklamak "ayrışma yok" ile "okunamadı"yı
// karıştırırdı. Eşzamanlı istekler singleflight ile TEK hesap paylaşır.
//
// Rol kapısı YOK — salt-okunur, /rootcause ile aynı duruş. api.go BÜYÜMEZ:
// defter (route_registry.go).

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() {
	registerRoutesExtra("rootcause-progressive", (*Server).registerRootCauseProgressiveRoutes)
}

func (s *Server) registerRootCauseProgressiveRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/problems/{id}/rootcause/core", s.getProblemRootCauseCore)
	mux.HandleFunc("GET /api/problems/{id}/rootcause/bubbleup", s.getProblemRootCauseBubbleUp)
	mux.HandleFunc("GET /api/anomalies/{id}/rootcause/core", s.getAnomalyRootCauseCore)
}

// rootCausePartTTL — tam demetle aynı tazelik.
const rootCausePartTTL = 60 * time.Second

// RootCauseBubbleUp — /rootcause/bubbleup gövdesi (FE: RootCauseBubbleUp).
// Pencere kendi hesabının penceresi (açık problemde end = hesap anı).
type RootCauseBubbleUp struct {
	FromNs   int64                   `json:"fromNs"`
	ToNs     int64                   `json:"toNs"`
	BubbleUp *chstore.BubbleUpResult `json:"bubbleUp,omitempty"`
}

// rootCausePartKey — parça anahtarı: tam demetin kimlik anahtarı + önek.
// Saat bileşeni YOK (v0.9.1082 ebedi-soğuk dersi).
func rootCausePartKey(part, id string, startedNs int64, resolvedNs *int64) string {
	return part + ":" + rootcauseCacheKey(id, startedNs, resolvedNs)
}

// loadRootCauseProblem — 404 kapısı (önbellek DIŞINDA: eksik problem
// önbelleğe boş demet olarak girmez).
func (s *Server) loadRootCauseProblem(w http.ResponseWriter, r *http.Request) *chstore.Problem {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "problem id required", http.StatusBadRequest)
		return nil
	}
	p, err := rootCauseStoreOf(s).GetProblem(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return nil
	}
	if p == nil {
		http.Error(w, "problem not found", http.StatusNotFound)
		return nil
	}
	return p
}

// getProblemRootCauseCore — GET /api/problems/{id}/rootcause/core.
func (s *Server) getProblemRootCauseCore(w http.ResponseWriter, r *http.Request) {
	p := s.loadRootCauseProblem(w, r)
	if p == nil {
		return
	}
	key := rootCausePartKey("core", p.ID, p.StartedAt, p.ResolvedAt)
	s.serveCached(w, r, key, rootCausePartTTL, func(ctx context.Context) (any, error) {
		dctx, cancel := rootCauseComputeCtx(ctx, rootCauseCoreBudget)
		defer cancel()
		out, _ := s.problemRootCauseBundle(dctx, nil, p, false)
		logRootCauseBudget(dctx, "çekirdek", "problem "+p.ID, rootCauseCoreBudget)
		return out, nil
	})
}

// getProblemRootCauseBubbleUp — GET /api/problems/{id}/rootcause/bubbleup.
func (s *Server) getProblemRootCauseBubbleUp(w http.ResponseWriter, r *http.Request) {
	p := s.loadRootCauseProblem(w, r)
	if p == nil {
		return
	}
	key := rootCausePartKey("bubbleup", p.ID, p.StartedAt, p.ResolvedAt)
	s.serveCached(w, r, key, rootCausePartTTL, func(ctx context.Context) (any, error) {
		// v0.10.1119 (inceleme) — yuva ÖZGÜN ctx yaşarken beklenir; bekleyen
		// iptal edilirse ya da bekleme dolarsa tarama başlamaz, hata önbelleğe
		// yazılmaz. Kopma yalnız yuva alındıktan SONRA.
		release, err := acquireRootCauseBubbleSlot(ctx)
		if err != nil {
			return nil, err
		}
		defer release()
		dctx, cancel := rootCauseComputeCtx(ctx, rootCauseBubbleBudget)
		defer cancel()
		return s.problemRootCauseBubbleUp(dctx, p)
	})
}

// problemRootCauseBubbleUp — tam demetteki (d) adımının AYNISI: aynı
// pencere işlevi, aynı aile (exemplarKindForMetric == hata → hatalı alt
// küme; değilse önceki eş-boy pencereye karşı). Hata yukarı çıkar
// (önbelleğe yazılmaz).
func (s *Server) problemRootCauseBubbleUp(ctx context.Context, p *chstore.Problem) (RootCauseBubbleUp, error) {
	started, end := problemRootCauseWindow(p, time.Now())
	bu, err := rootCauseStoreOf(s).ServiceBubbleUp(ctx, p.Service,
		exemplarKindForMetric(p.Metric) == chstore.ExemplarError, started, end)
	if err != nil {
		return RootCauseBubbleUp{}, fmt.Errorf("rootcause bubbleup: %w", err)
	}
	return RootCauseBubbleUp{FromNs: started.UnixNano(), ToNs: end.UnixNano(), BubbleUp: bu}, nil
}

// getAnomalyRootCauseCore — GET /api/anomalies/{id}/rootcause/core
// (RootCauseRibbon anomali satırı genişletmesi; şerit bubbleUp çizmez).
func (s *Server) getAnomalyRootCauseCore(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "anomaly id required", http.StatusBadRequest)
		return
	}
	ev, err := rootCauseStoreOf(s).GetAnomalyEvent(r.Context(), id, 0)
	if err != nil {
		writeErr(w, err)
		return
	}
	if ev == nil {
		http.Error(w, "anomaly not found", http.StatusNotFound)
		return
	}
	key := fmt.Sprintf("core:anomaly-rootcause:%s:%d", id, ev.StartedAt)
	s.serveCached(w, r, key, rootCausePartTTL, func(ctx context.Context) (any, error) {
		dctx, cancel := rootCauseComputeCtx(ctx, rootCauseCoreBudget)
		defer cancel()
		out, _ := s.anomalyRootCauseBundle(dctx, nil, id, ev, false)
		logRootCauseBudget(dctx, "çekirdek", "anomaly "+id, rootCauseCoreBudget)
		return out, nil
	})
}
