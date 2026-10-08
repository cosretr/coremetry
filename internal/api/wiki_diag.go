package api

// wiki_diag.go — v0.10.1124: wiki bilgisinin operatör tanı uçları.
//
//	POST /api/wiki/test-search — "Aramayı test et": yerel isabetler, canlı
//	     denemenin sonucu (sınıf + http durumu + api-version + isabet), son
//	     liste ve hangi tabanın neyi düşüreceği. Yalnız oturum açmış YÖNETİCİ
//	     (API token'ı değil), audit wiki.test_search (sorgu metni değil, yalnız
//	     uzunluğu ve sayılar). İçerik dönmez — isabet başına ≤160 rune kesit.
//	     Geri çekilme/"unavailable" önbelleği bu çağrıda atlanır.
//	GET  /api/wiki/pages?q=&offset=&limit= — indeksteki sayfalar (sunucu-
//	     sayfalı, ≤100). Oturum kullanıcısı (her rol; API token'ı değil);
//	     içerik önizlemesi (ilk 1000 karakter) YALNIZ yöneticiye.
//
// Hiçbir log satırı sayfa içeriği ya da PAT taşımaz.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/wiki"
)

func init() { registerRoutesExtra("wiki-diag", (*Server).registerWikiDiagRoutes) }

func (s *Server) registerWikiDiagRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/wiki/test-search", auth.RequireRole(auth.RoleAdmin, wikiSessionOnly(s.postWikiTestSearch)))
	mux.HandleFunc("GET /api/wiki/pages", wikiSessionOnly(s.getWikiPages))
	mux.HandleFunc("POST /api/wiki/purge", auth.RequireRole(auth.RoleAdmin, wikiSessionOnly(s.postWikiPurge)))
}

// wikiPurger — "İndeksi temizle"nin depo dilimi (test dikişi).
type wikiPurger interface {
	WikiPurge(ctx context.Context) error
}

var wikiPurgeSource = func(s *Server) wikiPurger {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store
}

// postWikiPurge — v0.10.1124 (inceleme F4): indeksteki TÜM wiki sayfa ve
// parçalarını siler (canlı moda geçen operatör eski içeriği temizleyebilsin).
// Oturumlu yönetici, audit wiki.purge. Karma/senkron modda bir sonraki senkron
// indeksi baştan kurar.
func (s *Server) postWikiPurge(w http.ResponseWriter, r *http.Request) {
	ws := wikiKB()
	src := wikiPurgeSource(s)
	if ws == nil || src == nil {
		http.Error(w, "wiki bilgisi bu kurulumda yok", http.StatusServiceUnavailable)
		return
	}
	if ws.Running() {
		http.Error(w, "senkron sürüyor — bitince yeniden deneyin", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := src.WikiPurge(ctx); err != nil {
		writeErr(w, err)
		return
	}
	st := ws.MarkPurged(ctx)
	s.audit(r, "wiki.purge", "wiki", "index", `{"tables":["wiki_pages","wiki_chunks"]}`)
	writeJSON(w, map[string]any{"purged": true, "status": wikiStatusOf(st)})
}

// wikiTestSearchTimeout — tanı çağrısının tavanı (canlı bütçe 8 sn + yerel).
const wikiTestSearchTimeout = 15 * time.Second

// wikiTestHit — tanı isabeti + taban kararları.
type wikiTestHit struct {
	wiki.DiagHit
	PassesRAG      bool `json:"passesRag"`
	PassesWikiTier bool `json:"passesWikiTier"`
}

func wikiTestHits(h []wiki.DiagHit) []wikiTestHit {
	out := make([]wikiTestHit, 0, len(h))
	for _, x := range h {
		out = append(out, wikiTestHit{DiagHit: x, PassesRAG: x.Score >= ragWikiFloor, PassesWikiTier: x.Score >= wikiTierFloor})
	}
	return out
}

// wikiTestVerdict — SAF: tanının tek cümlelik özeti (Türkçe).
func wikiTestVerdict(d wiki.Diagnosis) string {
	switch {
	case d.Live.Unavailable && d.Mode == wiki.ModeLive:
		return wiki.NoteSearchUnavailableLive + "."
	case len(d.Final) == 0 && d.Live.Attempted && d.Live.Results == 0:
		return "Ne yerel indeks ne Azure DevOps Search bu sorguya sayfa döndürdü."
	case len(d.Final) == 0:
		return "Sonuç yok."
	case d.Final[0].Score < wikiTierFloor:
		return "Sonuç var ama en iyi skor wiki kademesi tabanının (0.3) altında — cevaba girmez."
	case d.Final[0].Score < ragWikiFloor:
		return "Açık wiki sorusunda cevaba girer; işaretsiz soruda RAG tabanının (0.5) altında kalır."
	}
	return "Sonuç cevaba girer (her iki taban da geçiliyor)."
}

func (s *Server) postWikiTestSearch(w http.ResponseWriter, r *http.Request) {
	ws := wikiKB()
	if ws == nil {
		http.Error(w, "wiki bilgisi bu kurulumda yok", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	q := strings.TrimSpace(body.Query)
	if q == "" {
		http.Error(w, "query zorunlu", http.StatusBadRequest)
		return
	}
	if utf8.RuneCountInString(q) > 300 {
		q = string([]rune(q)[:300])
	}
	ctx, cancel := context.WithTimeout(r.Context(), wikiTestSearchTimeout)
	defer cancel()
	d, err := ws.Diagnose(ctx, q, 5)
	if errors.Is(err, wiki.ErrNoTerms) {
		writeJSONError(w, http.StatusBadRequest, "sorguda aranabilir terim yok (soru sözcükleri elenir) — anahtar sözcük ya da tanımlayıcı yazın")
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	details, _ := json.Marshal(map[string]any{
		"queryRunes": utf8.RuneCountInString(q), "local": len(d.Local), "final": len(d.Final),
		"liveAttempted": d.Live.Attempted, "liveClass": d.Live.Info.Class, "liveStatus": d.Live.Info.HTTPStatus,
	})
	s.audit(r, "wiki.test_search", "wiki", "test-search", string(details))
	writeJSON(w, map[string]any{
		"mode": d.Mode, "terms": d.Terms, "liveQueries": d.LiveQueries, "stale": d.Stale,
		"local": wikiTestHits(d.Local), "live": d.Live, "final": wikiTestHits(d.Final),
		"floors":  map[string]float64{"rag": ragWikiFloor, "wikiTier": wikiTierFloor},
		"verdict": wikiTestVerdict(d),
		// v0.10.1126 — kart "henüz denenmedi"de kalıyordu: durum yalnız sayfa
		// açılışında okunuyordu. Test, paylaşılan durumu (wiki_search_status)
		// tazeleyip okuyarak döner; kart bunu doğrudan çizer.
		"status": wikiStatusOf(ws.Status(ctx, true)),
	})
}

// wikiPageLister — sayfa listesinin depo dilimi (test dikişi).
type wikiPageLister interface {
	WikiListPages(ctx context.Context, q string, offset, limit int, preview bool) ([]chstore.WikiPageRow, uint64, error)
}

// wikiPagesSource — canlı depo; nil *chstore.Store arayüze SARILMAZ.
var wikiPagesSource = func(s *Server) wikiPageLister {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store
}

// wikiPagesQuery — SAF: sorgu parametreleri (sınırlı).
func wikiPagesQuery(r *http.Request) (q string, offset, limit int) {
	q = strings.TrimSpace(r.URL.Query().Get("q"))
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	if offset < 0 {
		offset = 0
	}
	if offset > 1_000_000 {
		offset = 1_000_000
	}
	if limit <= 0 || limit > chstore.WikiListMax {
		limit = chstore.WikiListMax
	}
	return q, offset, limit
}

func (s *Server) getWikiPages(w http.ResponseWriter, r *http.Request) {
	q, offset, limit := wikiPagesQuery(r)
	c := auth.FromContext(r.Context())
	preview := c != nil && c.Role == auth.RoleAdmin
	out := map[string]any{"rows": []chstore.WikiPageRow{}, "total": 0, "offset": offset, "limit": limit, "preview": preview}
	if ws := wikiKB(); ws != nil {
		out["mode"] = ws.Config().EffectiveMode()
	}
	src := wikiPagesSource(s)
	if src == nil || wikiKB() == nil {
		writeJSON(w, out)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	rows, total, err := src.WikiListPages(ctx, q, offset, limit, preview)
	if err != nil {
		writeErr(w, err)
		return
	}
	for i := range rows {
		if !preview {
			rows[i].Preview = "" // savunma: depo yanlışlıkla doldursa da
		}
		// Yalnız http(s) bağlantı (javascript:/data: başlık linki olmasın).
		if u := strings.ToLower(strings.TrimSpace(rows[i].URL)); !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			rows[i].URL = ""
		}
	}
	out["rows"], out["total"] = rows, total
	writeJSON(w, out)
}
