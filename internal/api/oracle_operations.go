package api

// oracle_operations.go — v0.10.1002 — Oracle OPERASYON ADI araması (operatör:
// "operasyon ismiyle trace bulabilir miyim" → "yap"). Komut paletinin (⌘K)
// sunucu taraflı kaynağı.
//
//	GET /api/oracle/operations?q=<metin>&limit=
//
// Operasyon adı (TRANSFER_CONFIRM_SERVICE gibi) trace'lerde YOK;
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
//
//	GET /api/oracle/function-codes            (v0.10.1003)
//
// TERS yön: fonksiyon kodu → operasyon adları SÖZLÜĞÜ. Trace sayfası span'de
// gördüğü FUNCTION_CODE değerini operasyon adına çevirir ("bu trace hangi
// operasyon"). Sözlüğün tamamı tek cevapta (kod başına en çok satırlı ≤3 ad +
// toplam ad sayısı): FE onu 5 dk tazelikle bir kez çeker, trace başına istek
// atmaz; sunucu da 5 dk önbellekler — eşleme dakikada değişen bir şey değil.
//
// v0.10.1133 — kaynak / kod kaynağı / span anahtarı çözümü oracleOpScope'ta;
// iki handler ve CoSRE'nin ad → kod çözümleyicisi (oracle_op_resolve.go) onu
// paylaşır — palet ile CoSRE aynı kuralla çevirir.

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
	mux.HandleFunc("GET /api/oracle/function-codes", s.getOracleFunctionCodes)
}

const (
	oracleFnDictTTL    = 5 * time.Minute
	oracleFnDictMaxOps = 3 // kod başına cevapta listelenen operasyon adı

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

// oracleOpScope — v0.10.1133: operasyon araması ile ad → kod çözümleyicisinin
// (oracle_op_resolve.go) ORTAK girdisi. İkisi aynı kuralı okumalı: kodun
// hangi kaynakta `code` alanından geldiği (oracle.CodeIsFunctionCode) ve
// span süzgecinin anahtar yazımı (chstore.PromotedAttrSpelling) bir yerde
// ayrışırsa palet doğru, CoSRE yanlış koda bakar.
type oracleOpScope struct {
	Enabled     bool              // etkin Oracle kaynağı + depo var
	SpanKey     string            // süzgeç anahtarı (kayıtlı yazım ya da kanonik ad)
	CodeSources []string          // `code` alanı fonksiyon kodu olan kaynak kimlikleri
	Names       map[string]string // kaynak kimliği → ad
}

// oracleOpSpanKey — süzgecin anahtar yazımı: terfi kolonu probe'unun
// doğruladığı yazım, kayıtlı değilse kanonik FUNCTION_CODE.
func oracleOpSpanKey() string {
	if k := chstore.PromotedAttrSpelling(oracleFunctionCodeAttr, strings.ToLower(oracleFunctionCodeAttr)); k != "" {
		return k
	}
	return oracleFunctionCodeAttr
}

// oracleOpScopeFrom — SAF (tablo testli): kaynak listesi → kod kaynakları + adlar.
func oracleOpScopeFrom(enabled bool, spanKey string, sources []oracle.SourceConfig) oracleOpScope {
	sc := oracleOpScope{Enabled: enabled, SpanKey: spanKey, Names: make(map[string]string, len(sources))}
	for _, src := range sources {
		sc.Names[src.ID] = src.Name
		if oracle.CodeIsFunctionCode(src) {
			sc.CodeSources = append(sc.CodeSources, src.ID)
		}
	}
	return sc
}

// oracleOpsEnabled — etkin Oracle kaynağı ve depo var mı (CH'ye gitmeden).
func (s *Server) oracleOpsEnabled() bool {
	if s.oracle == nil || s.store == nil || !s.oracle.HasEnabledSources() {
		return false
	}
	return true
}

func (s *Server) oracleOpScope() oracleOpScope {
	if !s.oracleOpsEnabled() {
		return oracleOpScopeFrom(false, oracleOpSpanKey(), nil)
	}
	return oracleOpScopeFrom(true, oracleOpSpanKey(), s.oracle.CurrentSettings().Sources)
}

func (s *Server) getOracleOperations(w http.ResponseWriter, r *http.Request) {
	sc := s.oracleOpScope()
	empty := oracleOperationsResponse{SpanAttrKey: sc.SpanKey, Operations: []oracleOperationHit{}}
	if !sc.Enabled {
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
	s.serveCached(w, r, oracleOpSearchKey(q, limit), 60*time.Second, func(ctx context.Context) (any, error) {
		now := time.Now()
		hits, err := s.store.OracleOperationSearch(ctx, q, sc.CodeSources, now.Add(-oracleOpSearchWindow), now, limit)
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
		return oracleOperationsResponse{Enabled: true, SpanAttrKey: sc.SpanKey,
			Operations: oracleOperationHits(hits, sc.Names, learned, now)}, nil
	})
}

// oracleFunctionCodeEntry — bir fonksiyon kodunun operasyon adları.
type oracleFunctionCodeEntry struct {
	// Ops — en çok satırlı önce, ≤ oracleFnDictMaxOps.
	Ops []string `json:"ops"`
	// Total — koda bağlı tekil operasyon adı sayısı (Ops kesilmiş olabilir).
	Total int `json:"total"`
}

type oracleFunctionCodesResponse struct {
	Enabled bool                               `json:"enabled"`
	Codes   map[string]oracleFunctionCodeEntry `json:"codes"`
	// Truncated — çift tavanı doldu; sözlük eksik olabilir.
	Truncated bool `json:"truncated,omitempty"`
}

// oracleFunctionCodeDict — SAF (tablo testli): (kod, operasyon, satır) çiftleri
// → sözlük. Girdi en çok satırlı önce gelir (chstore); sıra korunur.
func oracleFunctionCodeDict(pairs []chstore.OracleFnOp) map[string]oracleFunctionCodeEntry {
	out := map[string]oracleFunctionCodeEntry{}
	for _, p := range pairs {
		if p.Code == "" || p.Operation == "" {
			continue
		}
		e := out[p.Code]
		e.Total++
		if len(e.Ops) < oracleFnDictMaxOps {
			e.Ops = append(e.Ops, p.Operation)
		}
		out[p.Code] = e
	}
	return out
}

// oracleFnDictKey — SAF: sözlük, hangi kaynakların kodu `code` alanından
// okuduğuna bağlı (kaynak eşlemesi değişince anahtar da değişir).
func oracleFnDictKey(codeSources []string) string {
	set := make(map[string]bool, len(codeSources))
	for _, id := range codeSources {
		set[id] = true
	}
	return "oracle-function-codes:src=" + excludeKeyDigest(set) // sıralı + FNV (v0.5.187 kuralı)
}

func (s *Server) getOracleFunctionCodes(w http.ResponseWriter, r *http.Request) {
	sc := s.oracleOpScope()
	if !sc.Enabled {
		writeJSON(w, oracleFunctionCodesResponse{Codes: map[string]oracleFunctionCodeEntry{}})
		return
	}
	codeSources := sc.CodeSources
	s.serveCached(w, r, oracleFnDictKey(codeSources), oracleFnDictTTL, func(ctx context.Context) (any, error) {
		now := time.Now()
		pairs, truncated, err := s.store.OracleFunctionOperations(ctx, codeSources, now.Add(-oracleOpSearchWindow), now)
		if err != nil {
			return nil, err
		}
		return oracleFunctionCodesResponse{Enabled: true, Codes: oracleFunctionCodeDict(pairs), Truncated: truncated}, nil
	})
}
