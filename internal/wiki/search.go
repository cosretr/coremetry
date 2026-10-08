package wiki

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/devops"
)

// search.go — yerel arama (lexical + opsiyonel semantik) ve Azure DevOps
// Search canlı yedeği; sayfa okuma.
//
// Canlı yedek ("gerekirse search de kullansın") YALNIZ şu hâllerde koşar:
// yerel sonuç yok / zayıf (en iyi skor < WeakScore) ya da yerel indeks boş /
// bayat (son başarılı senkron 3 aralıktan eski). Uç varlığı bir kez tespit
// edilir ve önbelleğe alınır: 404 / sürüm aralığı dışı → "unavailable"
// (searchRecheck sonra yeniden denenir), geçici hata durumu DEĞİŞTİRMEZ.
// Canlı sonuç sayfa yoluna çevrilir, içerik API'den okunur ve yerel depoya
// yazılır (bir sonraki soru yerelden bulur).

const (
	// WeakScore — bunun altındaki en iyi yerel skor "zayıf" sayılır.
	WeakScore = 0.45
	// candidateLimit — CH'den istenen aday parça tavanı.
	candidateLimit = 300
	// semanticK — semantik aday sayısı.
	semanticK = 20
	// liveTop — canlı aramadan istenen sonuç; liveRead — içeriği okunan sayfa.
	liveTop  = 5
	liveRead = 3
	// liveBudget — canlı yedeğin toplam süre tavanı (sohbetin önünde durur).
	liveBudget = 8 * time.Second
	// searchRecheck — "unavailable" kararının yeniden denenme aralığı.
	searchRecheck = 6 * time.Hour
	// searchErrBackoff — geçici hata (401/403/5xx/zaman aşımı) sonrası
	// canlı aramanın susma süresi: kırık bir uç her soruya 8 sn eklemesin.
	searchErrBackoff = 15 * time.Minute
)

// SearchResult — arama çıktısı.
type SearchResult struct {
	Terms    []string `json:"terms"`
	Hits     []Hit    `json:"hits"`
	Mode     string   `json:"mode"` // lexical | hybrid
	Live     bool     `json:"live,omitempty"`
	LiveNote string   `json:"liveNote,omitempty"`
	Stale    bool     `json:"stale,omitempty"`
}

// ErrNoTerms — sorguda aranabilir terim yok.
var ErrNoTerms = errors.New("sorgu en az bir aranabilir terim içermeli")

// ErrOutOfScope — istenen proje/wiki izin listesinde değil.
var ErrOutOfScope = errors.New("bu wiki Coremetry'nin wiki ayarında kapsam dışı")

// indexStale — SAF: yerel indeks boş ya da bayat mı.
func indexStale(st Status, cfg Config, now time.Time) bool {
	if st.IndexedPages == 0 || st.LastFinishedAt == 0 {
		return true
	}
	return now.Sub(time.UnixMilli(st.LastFinishedAt)) > 3*cfg.Interval()
}

// LiveMode — canlı yedeğin ne zaman denendiği.
type LiveMode int

const (
	// LiveOnWeak — yerel sonuç zayıf/boş ya da indeks bayatsa (araç yolu:
	// operatör açıkça wiki soruyor).
	LiveOnWeak LiveMode = iota
	// LiveOnStale — yalnız yerel indeks boş/bayatsa (RAG kademesi: her serbest
	// sorunun önünde durur; telemetri sorusunda yerel sonucun zayıf olması
	// olağandır ve her soruya canlı arama gecikmesi eklenmemeli).
	LiveOnStale
	// LiveOff — hiç.
	LiveOff
)

// SearchOptions — Search ayarları.
type SearchOptions struct {
	Limit   int
	PerPage int
	Live    LiveMode
	// NoSemantic — semantik tarama (soru embed + kosinüs tam taraması) yok.
	// RAG kademesi her serbest sorunun önünde durur: orada yalnız lexical
	// (ikinci bir embed çağrısı ve FINAL kosinüs taraması yok).
	NoSemantic bool
}

// Search — araç yolu: canlı yedek LiveOnWeak.
func (s *Service) Search(ctx context.Context, query, project string, limit, perPage int) (SearchResult, error) {
	return s.SearchWith(ctx, query, project, SearchOptions{Limit: limit, PerPage: perPage, Live: LiveOnWeak})
}

// SearchWith — sorgu → sıralı parça listesi.
func (s *Service) SearchWith(ctx context.Context, query, project string, o SearchOptions) (SearchResult, error) {
	limit, perPage := o.Limit, o.PerPage
	terms := QueryTerms(query)
	if len(terms) == 0 {
		return SearchResult{}, ErrNoTerms
	}
	if limit <= 0 {
		limit = 5
	}
	cfg := s.Config()
	project = strings.TrimSpace(project)
	if project != "" && !cfg.ProjectAllowed(project) {
		return SearchResult{}, ErrOutOfScope
	}
	res := SearchResult{Terms: terms, Mode: "lexical"}
	st, err := s.store.WikiTermStats(ctx, terms, project)
	if err != nil {
		return res, err
	}
	var hits []Hit
	if st.N > 0 {
		cands, err := s.store.WikiCandidates(ctx, terms, project, candidateLimit)
		if err != nil {
			return res, err
		}
		hits = RankLexical(cands, st, terms)
		if emb := s.embedderOrNil(); emb != nil && !o.NoSemantic {
			if vecs, err := emb(ctx, []string{query}); err == nil && len(vecs) == 1 && len(vecs[0]) > 0 {
				if sem, err := s.store.WikiSemantic(ctx, vecs[0], project, semanticK); err == nil && len(sem) > 0 {
					hits = Blend(hits, sem, DefaultLexicalWeight)
					res.Mode = "hybrid"
				}
			}
		}
	}
	hits = filterScope(hits, cfg)
	res.Stale = indexStale(s.Status(ctx, false), cfg, s.now())
	if liveWanted(o.Live, hits, res.Stale) && !cfg.DisableLiveSearch {
		live, note := s.liveSearch(ctx, query, project, terms, st, cfg)
		res.LiveNote = note
		if len(live) > 0 {
			res.Live = true
			hits = mergeHits(hits, live)
		}
	}
	hits = DedupePages(hits, perPage)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	res.Hits = hits
	return res, nil
}

// liveWanted — SAF: canlı yedek bu sonuç için denenmeli mi.
func liveWanted(m LiveMode, hits []Hit, stale bool) bool {
	switch m {
	case LiveOff:
		return false
	case LiveOnStale:
		return stale
	}
	return stale || len(hits) == 0 || hits[0].Score < WeakScore
}

// filterScope — izin listesi sonradan daraldıysa eski satırlar görünmesin.
func filterScope(h []Hit, cfg Config) []Hit {
	if len(cfg.Projects) == 0 && len(cfg.Wikis) == 0 {
		return h
	}
	out := h[:0:0]
	for _, x := range h {
		if cfg.WikiAllowed(x.Project, x.WikiName) {
			out = append(out, x)
		}
	}
	return out
}

// mergeHits — canlı sonuçları yerel listeye katar (aynı parça → büyük skor).
func mergeHits(local, live []Hit) []Hit {
	by := map[string]int{}
	out := append([]Hit(nil), local...)
	for i, h := range out {
		by[h.Key()] = i
	}
	for _, h := range live {
		if i, ok := by[h.Key()]; ok {
			if h.Score > out[i].Score {
				out[i].Score = h.Score
			}
			out[i].Live = true
			continue
		}
		by[h.Key()] = len(out)
		out = append(out, h)
	}
	sortHits(out)
	return out
}

// searchStateNow — canlı arama durumu (kilitli okuma).
func (s *Service) searchStateNow() string {
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	if s.searchState == "" {
		return SearchUnknown
	}
	return s.searchState
}

// searchUsable — canlı arama denenmeli mi (unavailable + recheck süresi dolmadıysa hayır).
func (s *Service) searchUsable() bool {
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	now := s.now()
	if now.Before(s.searchBackoffUntil) {
		return false
	}
	if s.searchState != SearchUnavailable {
		return true
	}
	return now.Sub(s.searchChecked) >= searchRecheck
}

// searchFailed — geçici hata: durumu DEĞİŞTİRMEZ, yalnız geri çekilme.
func (s *Service) searchFailed() {
	s.searchMu.Lock()
	s.searchBackoffUntil = s.now().Add(searchErrBackoff)
	s.searchMu.Unlock()
}

func (s *Service) setSearchState(v string) {
	s.searchMu.Lock()
	s.searchState, s.searchChecked = v, s.now()
	s.searchMu.Unlock()
}

// liveSearch — Azure DevOps Search yedeği. Dönen isabetler aynı lexical
// skorla (yerel korpus istatistiği) puanlanır ki yerel sonuçlarla aynı
// ölçekte birleşsin; içerik okunan sayfalar yerel depoya yazılır.
func (s *Service) liveSearch(ctx context.Context, query, project string, terms []string, st Stats, cfg Config) ([]Hit, string) {
	api := s.apiOrNil()
	if api == nil || !api.Configured() || !s.searchUsable() {
		return nil, ""
	}
	lctx, cancel := context.WithTimeout(ctx, liveBudget)
	defer cancel()
	res, err := api.SearchWiki(lctx, query, project, liveTop)
	if errors.Is(err, devops.ErrWikiSearchUnavailable) {
		s.setSearchState(SearchUnavailable)
		return nil, "Azure DevOps wiki araması bu sunucuda yok (Search uzantısı)"
	}
	if err != nil {
		// İstemci iptali (sekme kapandı) herkes için geri çekilme sebebi değil.
		if ctx.Err() == nil {
			s.searchFailed()
		}
		return nil, "canlı wiki araması başarısız (15 dk ara verildi): " + err.Error()
	}
	s.setSearchState(SearchAvailable)
	var out []Hit
	read := 0
	for _, r := range res {
		if read >= liveRead || lctx.Err() != nil {
			break
		}
		if !cfg.WikiAllowed(r.Project, r.WikiName) {
			continue
		}
		rec, err := s.pageForLive(lctx, api, r)
		if err != nil || rec == nil {
			continue
		}
		read++
		chunks := BuildChunks(rec.Title, rec.Content)
		cands := make([]Candidate, 0, len(chunks))
		for _, c := range chunks {
			cands = append(cands, candidateFromChunk(*rec, c, terms))
		}
		// Korpus istatistiği yerelden; yerel indeks boşsa en az 1 belge say.
		lst := st
		if lst.N == 0 {
			lst = Stats{N: uint64(len(cands)), AvgDL: avgDL(cands), DF: make([]uint64, len(terms))}
		}
		for _, h := range RankLexical(cands, lst, terms) {
			h.Live = true
			out = append(out, h)
		}
	}
	return out, ""
}

func avgDL(c []Candidate) float64 {
	if len(c) == 0 {
		return 1
	}
	var n float64
	for _, x := range c {
		n += float64(x.DL)
	}
	return n / float64(len(c))
}

// candidateFromChunk — SAF: bellekteki parçadan aday (CH'nin countEqual'ının ikizi).
func candidateFromChunk(p PageRecord, c ChunkRecord, terms []string) Candidate {
	tf := make([]uint32, len(terms))
	htf := make([]uint32, len(terms))
	for i, t := range terms {
		for _, x := range c.Tokens {
			if x == t {
				tf[i]++
			}
		}
		for _, x := range c.HeadTokens {
			if x == t {
				htf[i]++
			}
		}
	}
	return Candidate{
		ChunkRef: ChunkRef{
			Project: p.Project, WikiID: p.WikiID, WikiName: p.WikiName, Path: p.Path,
			Title: p.Title, URL: p.URL, Heading: c.Heading, Idx: c.Idx, Text: c.Text,
		},
		TF: tf, HTF: htf, DL: uint32(len(c.Tokens)),
	}
}

// pageForLive — canlı isabetin sayfası: yerelde varsa oradan, yoksa API'den
// okunur ve yerel depoya yazılır.
func (s *Service) pageForLive(ctx context.Context, api API, r devops.WikiSearchHit) (*PageRecord, error) {
	if rec, err := s.store.GetWikiPage(ctx, r.Project, r.WikiID, r.Path); err == nil && rec != nil {
		return rec, nil
	}
	return s.fetchAndStore(ctx, api, r.Project, r.WikiID, r.WikiName, r.Path)
}

// fetchAndStore — sayfayı API'den okur, parçalar, (varsa) embed eder, yazar.
func (s *Service) fetchAndStore(ctx context.Context, api API, project, wikiID, wikiName, path string) (*PageRecord, error) {
	pg, err := api.GetWikiPage(ctx, project, wikiID, path, "")
	if err != nil {
		return nil, err
	}
	rec := PageRecord{
		Project: project, WikiID: wikiID, WikiName: wikiName, Path: pg.Path,
		Title: PageTitle(pg.Path), URL: api.WikiPageWebURL(project, wikiName, pg.Path, pg.RemoteURL),
		GitPath: pg.GitItemPath, Hash: ContentHash(pg.Content), Content: pg.Content, UpdatedAt: s.now(),
	}
	if pg.ETag != "" {
		rec.Version = "etag:" + pg.ETag
	}
	chunks := BuildChunks(rec.Title, rec.Content)
	embedChunks(ctx, s.embedderOrNil(), rec.Title, chunks)
	rec.Chunks = uint32(len(chunks))
	var oldN uint32
	if old, err := s.store.GetWikiPage(ctx, project, wikiID, pg.Path); err == nil && old != nil {
		oldN = old.Chunks
	}
	// Yazma hatası okumayı düşürmez: sayfa yine cevaba girer.
	_ = s.store.UpsertWikiPage(ctx, rec, chunks, oldN)
	return &rec, nil
}

// ReadPage — tek sayfanın içeriği. wiki adı ya da id'si kabul edilir.
// Yerelde yoksa (ve bağlantı varsa) API'den okunur ve yerel depoya yazılır.
// İzin listesi dışındaki wiki ErrOutOfScope.
func (s *Service) ReadPage(ctx context.Context, project, wiki, path string) (*PageRecord, error) {
	project, wiki = strings.TrimSpace(project), strings.TrimSpace(wiki)
	path = NormalizePagePath(path)
	if project == "" || wiki == "" || path == "" {
		return nil, errors.New("project, wiki ve path zorunlu")
	}
	cfg := s.Config()
	if !cfg.ProjectAllowed(project) {
		return nil, ErrOutOfScope
	}
	if rec, err := s.store.GetWikiPage(ctx, project, wiki, path); err == nil && rec != nil {
		if !cfg.WikiAllowed(rec.Project, rec.WikiName) {
			return nil, ErrOutOfScope
		}
		return rec, nil
	}
	api := s.apiOrNil()
	if api == nil || !api.Configured() {
		return nil, nil
	}
	rctx, cancel := context.WithTimeout(ctx, liveBudget)
	defer cancel()
	ws, err := api.ListWikis(rctx, project)
	if err != nil {
		return nil, err
	}
	for _, w := range ws {
		if strings.EqualFold(w.Name, wiki) || strings.EqualFold(w.ID, wiki) {
			if !cfg.WikiAllowed(project, w.Name) {
				return nil, ErrOutOfScope
			}
			rec, err := s.fetchAndStore(rctx, api, project, w.ID, w.Name, path)
			if err != nil {
				return nil, fmt.Errorf("sayfa okunamadı: %w", err)
			}
			return rec, nil
		}
	}
	return nil, nil
}

// NormalizePagePath — SAF: başa "/" eklenir, sondaki "/" ve ".md" atılır.
func NormalizePagePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = strings.TrimSuffix(p, ".md")
	p = strings.TrimRight(p, "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if p == "/" {
		return ""
	}
	return p
}
