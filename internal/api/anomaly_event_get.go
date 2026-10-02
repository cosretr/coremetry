package api

import (
	"context"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// anomaly_event_get.go — v0.10.1032. Tekil anomali olayı okuması ve olay
// listesiyle ORTAK zenginleştirme zinciri. (Route kaydı api.go'da, değişmedi:
// GET /api/anomalies/event.)
//
// Operatör: "Anomali ve alert rule'lara girdiğimde drawer çıkıyor. Exception
// gibi detay gözükmüyor." Problems kuyruğundaki anomali satırı artık tam sayfa
// detay açıyor ve o sayfanın ana yolu BU uç: /inbox olay listesini hiç
// yüklemiyor. Uç eskiden çıplak satır döndürüyordu (v0.9.465 deep-link
// kurtarma ucu); liste ucunun eklediği cluster, son deploy, kök-neden özeti ve
// karar YOKTU. Sonuç: tam sayfadaki kök-neden çipi bir hipotez varken bile
// "no clear cause yet" diyor, deploy kutusu ve cluster'lar hiç görünmüyordu —
// ekran, verinin söylemediği bir şeyi söylüyordu.
//
// Düzeltme: iki uç AYNI zinciri, AYNI sırayla çağırır (enrichAnomalyEvents).
// Zincir tek yerde; birine eklenen zenginleştirme ötekine kendiliğinden geçer.

// anomalyEventEnricher — zincirin ihtiyaç duyduğu dört okuma (*chstore.Store
// karşılar). Arayüz yalnız test dikişi: sıra ve parametreler sahte bir
// zenginleştiriciyle çivilenir (anomaly_event_get_test.go).
type anomalyEventEnricher interface {
	EnrichAnomaliesWithClusters(ctx context.Context, events []chstore.AnomalyEvent, since time.Duration) []chstore.AnomalyEvent
	EnrichAnomaliesWithDeploys(ctx context.Context, events []chstore.AnomalyEvent, lookback time.Duration) []chstore.AnomalyEvent
	EnrichAnomaliesWithRootCause(ctx context.Context, events []chstore.AnomalyEvent) []chstore.AnomalyEvent
	EnrichAnomaliesWithVerdicts(ctx context.Context, events []chstore.AnomalyEvent) []chstore.AnomalyEvent
}

// enrichAnomalyEvents — okuma-anı zenginleştirme zinciri (her adım soft-fail:
// hata olursa satırlar zenginleştirilmeden geçer).
//
//  1. cluster'lar — olay çevresinde servisin koştuğu k8s/openshift küme(ler)i (1 sa).
//  2. v0.5.286 — olayın startedAt'inden önceki 30 dk içindeki en son deploy;
//     "deploy mu bozdu?" sorusu bağlam değiştirmeden cevaplansın. v0.5.283
//     etkin-sürüm zinciri (Helm etiketleri, imaj etiketi): service.version
//     taşımayan kurulumlar da eşleşir.
//  3. rc #3 — kalıcı kök-neden en-güçlü-şüpheli özeti TEK toplu okumayla
//     (GetHypotheses); hipotezi olmayan satır RootCause=nil kalır (dürüst
//     "henüz net neden yok").
//  4. v0.10.181 — operatörün «anomali / değil» kararı.
//
// Sıra liste ucunda (v0.5.286 → rc #3 → v0.10.181) neyse o; değiştirmek iki
// ucu birlikte değiştirir.
func enrichAnomalyEvents(ctx context.Context, st anomalyEventEnricher, rows []chstore.AnomalyEvent) []chstore.AnomalyEvent {
	rows = st.EnrichAnomaliesWithClusters(ctx, rows, time.Hour)
	rows = st.EnrichAnomaliesWithDeploys(ctx, rows, 30*time.Minute)
	rows = st.EnrichAnomaliesWithRootCause(ctx, rows)
	rows = st.EnrichAnomaliesWithVerdicts(ctx, rows)
	return rows
}

// getAnomalyEvent (v0.9.465, dürüstlük A9) — tek event, id ile.
// ?event= deep-link'i 200'lük liste penceresinin dışına düşünce sayfa
// sessiz hiçlik gösteriyordu; frontend bu uçtan kurtarır.
// v0.10.1032 — Problems kuyruğundaki tam sayfa anomali detayının ANA yolu;
// tek satırlık dilim liste ucunun zincirinden geçer (yukarıda). Önbelleksiz:
// kimlikle PK okuması + dört küçük zenginleştirme okuması, sayfa açılışı
// başına bir kez (istemci staleTime 30 s, yoklama yok).
func (s *Server) getAnomalyEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ev, err := s.store.GetAnomalyEvent(ctx, r.URL.Query().Get("id"), 0)
	if err != nil {
		writeErr(w, err)
		return
	}
	if ev == nil {
		http.Error(w, "anomaly not found", http.StatusNotFound)
		return
	}
	rows := enrichAnomalyEvents(ctx, s.store, []chstore.AnomalyEvent{*ev})
	if len(rows) == 0 {
		// Zincir satırı düşürmez; yine de boş dönerse çıplak satır, 404 değil.
		writeJSON(w, ev)
		return
	}
	writeJSON(w, rows[0])
}
