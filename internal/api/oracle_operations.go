package api

// oracle_operations.go — v0.10.1002 — Oracle OPERASYON ADI araması (operatör:
// "operasyon ismiyle trace bulabilir miyim" → "yap"). Komut paletinin (⌘K)
// sunucu taraflı kaynağı.
//
//	GET /api/oracle/operations?q=<metin>&limit=
//
// Operasyon adı (DIGITAL_TRANSFER_EFT_CONFIRM_SERVICE gibi) trace'lerde YOK;
// köprü Oracle hata satırlarında: her isabet için
//
//   - functionCodes — operasyonun satırlarındaki fonksiyon kodları. Aynı değer
//     span'lerde FUNCTION_CODE attribute'u; FE bununla Traces süzgeci kurar
//     (başarılı + hatalı TÜM trace'ler). spanAttrKey süzgecin anahtar yazımı:
//     kullanıcı süzgeci harf duyarlı olduğu için prod'un gerçekten yazdığı
//     (terfi kolonu probe'unun doğruladığı) yazım döner.
//   - lastTraceId — hata satırındaki en yeni trace (kesin eşleşme).
//   - service — öğrenilmiş op → servis eşlemesi, YALNIZ onaylıysa.
//
// Rol kapısı YOK (/api/oracle/errors emsali): salt okuma; operasyon adı zaten
// Problem başlığında ve Trace › Logs'ta her role görünür. Etkin Oracle kaynağı
// yoksa ya da q < 3 karakterse CH'ye HİÇ gidilmez. Pencere son 7 gün
// (oracle_error_log TTL 30 gün); serveCached 60 sn, anahtar q + limit.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/oracle"
)

func init() { registerRoutesExtra("oracle-operations", (*Server).registerOracleOperationRoutes) }

func (s *Server) registerOracleOperationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/oracle/operations", s.getOracleOperations)
}

const (
	oracleOpSearchMinQuery = 3
	oracleOpSearchMaxQuery = 80
	oracleOpSearchWindow   = 7 * 24 * time.Hour
	oracleOpSearchLimit    = 6
	oracleOpSearchLimitMax = 20
	// oracleFunctionCodeAttr — span attribute'unun kanonik adı; yazım kayıtlı
	// değilse süzgeç bu adla kurulur.
	oracleFunctionCodeAttr = "FUNCTION_CODE"
)

type oracleOperationHit struct {
	Operation     string   `json:"operation"`
	Rows          uint64   `json:"rows"`
	LastSeen      int64    `json:"lastSeen"` // ms
	FunctionCodes []string `json:"functionCodes"`
	LastTraceID   string   `json:"lastTraceId,omitempty"`
	Service       string   `json:"service,omitempty"`
	Source        string   `json:"source,omitempty"`
}

type oracleOperationsResponse struct {
	Enabled     bool                 `json:"enabled"`
	SpanAttrKey string               `json:"spanAttrKey"`
	Operations  []oracleOperationHit `json:"operations"`
}

// oracleOpSearchQuery — SAF: ?q kırpılır; kısa ya da aşırı uzun sorgu "" döner
// (CH'ye gidilmez).
func oracleOpSearchQuery(raw string) string {
	q := strings.TrimSpace(raw)
	if len([]rune(q)) < oracleOpSearchMinQuery || len(q) > oracleOpSearchMaxQuery {
		return ""
	}
	return q
}

// oracleOpSearchKey — SAF: q harf duyarsız aranır → anahtarda küçük harf.
func oracleOpSearchKey(q string, limit int) string {
	return fmt.Sprintf("oracle-operations:q=%s:lim=%d", strings.ToLower(q), limit)
}

// oracleOperationHits — SAF (tablo testli): depo isabetleri + kaynak adları +
// öğrenilmiş haritalar → cevap satırları. Servis yalnız ONAYLI girdiden.
func oracleOperationHits(hits []chstore.OracleOpHit, names map[string]string, learned map[string]oracle.LearnedMap, now time.Time) []oracleOperationHit {
	out := make([]oracleOperationHit, 0, len(hits))
	for _, h := range hits {
		o := oracleOperationHit{Operation: h.Operation, Rows: h.Rows, LastSeen: h.LastSeenMs,
			FunctionCodes: h.FunctionCodes, LastTraceID: h.LastTraceID, Source: names[h.SourceID]}
		if o.FunctionCodes == nil {
			o.FunctionCodes = []string{}
		}
		if e := learned[h.SourceID].Entries[h.Operation]; e != nil && e.Confirmed(now) {
			o.Service = e.Service
		}
		out = append(out, o)
	}
	return out
}

func (s *Server) getOracleOperations(w http.ResponseWriter, r *http.Request) {
	spanKey := chstore.PromotedAttrSpelling(oracleFunctionCodeAttr, strings.ToLower(oracleFunctionCodeAttr))
	if spanKey == "" {
		spanKey = oracleFunctionCodeAttr
	}
	empty := oracleOperationsResponse{SpanAttrKey: spanKey, Operations: []oracleOperationHit{}}
	if s.oracle == nil || s.store == nil || !s.oracle.HasEnabledSources() {
		writeJSON(w, empty)
		return
	}
	empty.Enabled = true
	q := oracleOpSearchQuery(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, empty)
		return
	}
	limit := parseInt(r.URL.Query().Get("limit"), oracleOpSearchLimit)
	if limit <= 0 || limit > oracleOpSearchLimitMax {
		limit = oracleOpSearchLimit
	}
	sources := s.oracle.CurrentSettings().Sources
	s.serveCached(w, r, oracleOpSearchKey(q, limit), 60*time.Second, func(ctx context.Context) (any, error) {
		now := time.Now()
		var codeSources []string
		names := make(map[string]string, len(sources))
		for _, src := range sources {
			names[src.ID] = src.Name
			if oracle.CodeIsFunctionCode(src) {
				codeSources = append(codeSources, src.ID)
			}
		}
		hits, err := s.store.OracleOperationSearch(ctx, q, codeSources, now.Add(-oracleOpSearchWindow), now, limit)
		if err != nil {
			return nil, err
		}
		// Öğrenilmiş harita yalnız isabeti olan kaynaklar için okunur.
		learned := map[string]oracle.LearnedMap{}
		for _, h := range hits {
			if _, ok := learned[h.SourceID]; ok || h.SourceID == "" {
				continue
			}
			m := oracle.LearnedMap{Entries: map[string]*oracle.LearnedEntry{}}
			if raw, err := s.store.GetSetting(ctx, "oracle_opsvc:"+h.SourceID); err == nil && len(raw) > 0 {
				_ = json.Unmarshal(raw, &m)
			}
			learned[h.SourceID] = m
		}
		return oracleOperationsResponse{Enabled: true, SpanAttrKey: spanKey,
			Operations: oracleOperationHits(hits, names, learned, now)}, nil
	})
}
