package api

// problem_source_event.go — v0.10.1106 (operatör kuyruğu, onaylı): terfi
// Problem'inin (`anomaly-auto:`) detayı kaynak olayın "Desen sayısı"
// grafiğini çizsin; operatör artışın ne zaman başladığını kaynak olayı
// açmadan görsün.
//
//   GET /api/problems/{id}/source-event  → {"sourceEvent"?: PromotedSourceEvent}
//
// Neden AYRI uç, /api/problems/{id} yükünü genişletmek DEĞİL: Problem detayı
// (AlertProblemHost) Problem'i önce zaten yüklü LİSTEDEN çözer, by-id ucu
// yalnız derin-link yedeği (useProblemByID). By-id yükünü genişletmek grafiği
// çoğu açılışta göstermezdi; liste yüküne koymak sıcak /api/problems'e satır
// başına okuma eklerdi. Ayrı uç: yalnız terfi Problem'inin detayı açıkken,
// tek istek.
//
// Neden /api/anomalies/event?id= DEĞİL: o uç tam satır + dört zenginleştirme
// okuması (cluster / deploy / kök neden / karar) yapar; grafiğin ihtiyacı
// yedi kolon. Ayrıca kimlik ayrıştırması FE'ye ikinci bir kopya olarak
// düşerdi — burada tek kaynak chstore.PromotedAnomalyEventID.
//
// Kimlik sorgusuz: Problem kimliği `anomaly-auto:<fp>:<servis>` olay
// kimliğini taşır; problems tablosu okunmaz. Terfi Problem'i değilse okuma
// YOK, alan yok (200). Olay yoksa (TTL 30 gün) alan yok. Okuma hatası →
// writeErr (boş cevap önbelleğe yazılmaz). Okuma sınırı chstore
// promotedSourceEventSQL'de (PK eşitliği + FINAL + LIMIT 1 + 2 sn).
//
// Rol kapısı YOK — salt-okunur (viewer görür). serveCached 30 s; tek girdi
// olay kimliği (status `now`'a göre türer, 30 s FE staleTime ile aynı).
// api.go BÜYÜMEZ: route_registry defteri.

import (
	"context"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() {
	registerRoutesExtra("problem-source-event", (*Server).registerProblemSourceEventRoutes)
}

func (s *Server) registerProblemSourceEventRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/problems/{id}/source-event", s.getProblemSourceEvent)
}

const problemSourceEventTTL = 30 * time.Second

// problemSourceEventResponse — alan yalnız terfi Problem'inin kaynak olayı
// okunabildiyse dolu (FE: ProblemSourceEventResponse).
type problemSourceEventResponse struct {
	SourceEvent *chstore.PromotedSourceEvent `json:"sourceEvent,omitempty"`
}

// promotedSourceEventReader — tek okuma dikişi (*chstore.Store karşılar).
type promotedSourceEventReader interface {
	GetPromotedSourceEvent(ctx context.Context, eventID string) (*chstore.PromotedSourceEvent, error)
}

// problemSourceEventKey — SAF; tek girdi olay kimliği (v0.5.187: anahtar
// cevabı değiştiren HER girdiyi taşır — problem kimliğinin servis son eki
// cevabı değiştirmez, aynı olayın iki Problem'i aynı girdiyi paylaşır).
func problemSourceEventKey(eventID string) string {
	return "problem-source-event:v1:" + eventID
}

// resolveProblemSourceEvent — problem kimliği → kaynak olay özeti. Terfi
// Problem'i değilse okuma yapılmaz.
func resolveProblemSourceEvent(ctx context.Context, rd promotedSourceEventReader, problemID string) (problemSourceEventResponse, error) {
	eventID, ok := chstore.PromotedAnomalyEventID(problemID)
	if !ok {
		return problemSourceEventResponse{}, nil
	}
	ev, err := rd.GetPromotedSourceEvent(ctx, eventID)
	if err != nil {
		return problemSourceEventResponse{}, err
	}
	return problemSourceEventResponse{SourceEvent: ev}, nil
}

func (s *Server) getProblemSourceEvent(w http.ResponseWriter, r *http.Request) {
	problemID := r.PathValue("id")
	eventID, ok := chstore.PromotedAnomalyEventID(problemID)
	if !ok {
		// Terfi Problem'i değil: okuma da önbellek girdisi de yok.
		writeJSON(w, problemSourceEventResponse{})
		return
	}
	s.serveCached(w, r, problemSourceEventKey(eventID), problemSourceEventTTL, func(ctx context.Context) (any, error) {
		return resolveProblemSourceEvent(ctx, s.store, problemID)
	})
}
