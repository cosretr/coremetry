package wiki

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
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
//
// v0.10.1124 (operatör: "senkron değilse wiki'yi aramıyor"):
//   - Sorgu metni: soru cümlesi değil, stopword'süz sözcükler (önce AND,
//     boşsa OR — LiveSearchQueries). ADO çok terimli sorguyu AND'ler; soru
//     sözcükleri tipik bir Türkçe soruyu sıfır sonuca düşürüyordu.
//   - Skor: canlı isabet yalnız TAM-jeton lexical skorla puanlanırsa ADO'nun
//     kök/ek-duyarlı eşleşmesiyle bulduğu sayfa ("servisini" ↔ "servis")
//     kapsamı düşük kalıp RAG tabanının altında düşüyordu. Artık ADO'nun
//     SIRASI skordan türetilir (scoreLivePage): kanıtı olan (terim kökü
//     sayfada geçen ya da ADO vurgusu olan) ilk isabet tabanın üstünde başlar,
//     sıra ilerledikçe söner.
//   - Canlı mod (Config.Mode = live): yerel indeks hiç okunmaz, sayfalar
//     bellek önbelleğinde tutulur, CH'ye yazılmaz.
//   - Son arama sonucu paylaşılan blobda (SearchStatusKey) — durum kartı
//     hangi pod'a düşerse düşsün son denemeyi görür.

const (
	// WeakScore — bunun altındaki en iyi yerel skor "zayıf" sayılır.
	WeakScore = 0.45
	// candidateLimit — CH'den istenen aday parça tavanı.
	candidateLimit = 300
	// semanticK — semantik aday sayısı.
	semanticK = 20
	// liveTop — canlı aramadan istenen sonuç; liveRead — içeriği okunan sayfa
	// (eşzamanlı, en çok liveRead istek).
	liveTop  = 5
	liveRead = 3
	// liveBudget — canlı yedeğin toplam süre tavanı (sohbetin önünde durur).
	liveBudget = 8 * time.Second
	// searchRecheck — "unavailable" kararının yeniden denenme aralığı.
	searchRecheck = 6 * time.Hour
	// searchErrBackoff — geçici hata (401/403/5xx/zaman aşımı) sonrası
	// canlı aramanın susma süresi: kırık bir uç her soruya 8 sn eklemesin.
	searchErrBackoff = 15 * time.Minute
	// searchPersistEvery — paylaşılan arama blobunun durum değişmeden
	// yeniden yazılma aralığı (sohbet yolu her soruda CH'ye yazmasın).
	searchPersistEvery = 10 * time.Minute

	// Canlı isabet skoru (scoreLivePage): ADO sırası i için
	// liveTopScore × liveDecay^i; OR sorgusunda ayrıca kapsama çarpanı
	// liveORMin + (1-liveORMin)×kök-kapsamı. 0.66 → 0.58 → 0.51: kanıtlı ilk
	// üç isabet RAG tabanının (0.5) üstünde; OR'da kapsamsız ilk isabet
	// 0.66×0.8 = 0.528.
	liveTopScore   = 0.66
	liveDecay      = 0.88
	liveORMin      = 0.8
	liveOtherChunk = 0.75
)

// NoteSearchUnavailableLive — canlı modda Search uzantısı yokken sohbetin ve
// tanının cümlesi (TEK YAZIM).
const NoteSearchUnavailableLive = "Azure DevOps Search bu sunucuda yok; senkron modunu kullanın"

// NoteLiveSearchFailed — canlı arama hatasında sohbete/araca giden GENEL not
// (sınıf, http durumu, hata metni yalnız tanıda — LiveDiag.Error).
const NoteLiveSearchFailed = "Azure DevOps araması şu an yanıt vermedi"

// noteSearchUnavailable — karma modda aynı durumun notu.
const noteSearchUnavailable = "Azure DevOps wiki araması bu sunucuda yok (Search uzantısı) — yalnız yerel indeks"

// SearchResult — arama çıktısı.
type SearchResult struct {
	Terms    []string `json:"terms"`
	Hits     []Hit    `json:"hits"`
	Mode     string   `json:"mode"` // lexical | hybrid | live
	Live     bool     `json:"live,omitempty"`
	LiveNote string   `json:"liveNote,omitempty"`
	Stale    bool     `json:"stale,omitempty"`
	// SearchUnavailable — canlı arama gerekti ama sunucuda Search yok.
	SearchUnavailable bool `json:"searchUnavailable,omitempty"`
	// LiveOnly — canlı mod (yerel indeks okunmadı).
	LiveOnly bool `json:"liveOnly,omitempty"`
	// Diag — canlı denemenin tanısı (yalnız Diagnose/test uçları okur).
	Diag *LiveDiag `json:"-"`
}

// LiveDiag — canlı denemenin içerik-SİZ tanısı.
type LiveDiag struct {
	Attempted   bool                  `json:"attempted"`
	Skipped     string                `json:"skipped,omitempty"` // not_configured | backoff | unavailable | disabled | not_needed
	Query       string                `json:"query,omitempty"`   // gönderilen son sorgu metni (soru sözcükleri)
	QueryMode   string                `json:"queryMode,omitempty"`
	Info        devops.WikiSearchInfo `json:"info"`
	Results     int                   `json:"results"`
	Read        int                   `json:"read"`
	Hits        int                   `json:"hits"`
	Unavailable bool                  `json:"unavailable,omitempty"`
	Note        string                `json:"note,omitempty"`
	// Error — hata ayrıntısı (sanitize edilmiş; PAT yok). Yalnız tanı ucu döndürür.
	Error string `json:"error,omitempty"`
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
	// LiveOnWeak — yerel sonuç zayıf/boş ya da indeks bayatsa (araç yolu ve
	// açık wiki sorusu: operatör açıkça wiki soruyor).
	LiveOnWeak LiveMode = iota
	// LiveOnStale — yalnız yerel indeks boş/bayatsa (RAG kademesi: her serbest
	// sorunun önünde durur; telemetri sorusunda yerel sonucun zayıf olması
	// olağandır ve her soruya canlı arama gecikmesi eklenmemeli). Canlı modda
	// bu kademe wiki'ye HİÇ gitmez (her serbest soruya 1-3 sn eklenmesin).
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
	var hits []Hit
	if cfg.EffectiveMode() == ModeLive {
		res.Mode, res.LiveOnly = "live", true
		if o.Live != LiveOnWeak {
			return res, nil
		}
		live, d := s.liveSearch(ctx, query, project, terms, Stats{}, cfg, false)
		hits = applyLive(&res, nil, live, d)
	} else {
		st, err := s.store.WikiTermStats(ctx, terms, project)
		if err != nil {
			return res, err
		}
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
		if liveWanted(o.Live, hits, res.Stale) && cfg.LiveSearchAllowed() {
			live, d := s.liveSearch(ctx, query, project, terms, st, cfg, false)
			hits = applyLive(&res, hits, live, d)
		}
	}
	hits = DedupePages(hits, perPage)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	res.Hits = hits
	return res, nil
}

// applyLive — canlı denemenin sonucunu çıktıya katar.
func applyLive(res *SearchResult, hits, live []Hit, d LiveDiag) []Hit {
	dd := d
	res.Diag = &dd
	res.LiveNote = d.Note
	res.SearchUnavailable = d.Unavailable
	if len(live) > 0 {
		res.Live = true
		hits = mergeHits(hits, live)
	}
	return hits
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

// searchBlocked — canlı arama şu an denenmemeli mi: "backoff" (geçici hata
// sonrası susma) | "unavailable" (uç yok, recheck süresi dolmadı) | "".
func (s *Service) searchBlocked() string {
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	now := s.now()
	if now.Before(s.searchBackoffUntil) {
		return "backoff"
	}
	if s.searchState == SearchUnavailable && now.Sub(s.searchChecked) < searchRecheck {
		return "unavailable"
	}
	return ""
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

// recordSearch — son arama sonucunu paylaşılan blob'a yazar (durum
// değiştiyse ya da searchPersistEvery geçtiyse; iptalden ayrılmış kısa bağlam).
func (s *Service) recordSearch(ctx context.Context, state string, info devops.WikiSearchInfo, mode string) {
	now := s.now()
	ss := SearchStatus{State: state, At: now.UnixMilli(), Class: info.Class, HTTPStatus: info.HTTPStatus,
		APIVersion: info.APIVersion, Hits: info.Hits, Mode: mode}
	sig := state + "|" + info.Class + "|" + strconv.Itoa(info.HTTPStatus) + "|" + info.APIVersion
	s.searchMu.Lock()
	s.searchCache = &ss
	due := sig != s.searchSavedSig || now.Sub(s.searchSaved) >= searchPersistEvery
	if due {
		s.searchSaved, s.searchSavedSig = now, sig
	}
	s.searchMu.Unlock()
	if !due {
		return
	}
	raw, err := json.Marshal(ss)
	if err != nil {
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_ = s.store.PutSetting(wctx, SearchStatusKey, raw)
}

func unavailableNote(cfg Config) string {
	if cfg.EffectiveMode() == ModeLive {
		return NoteSearchUnavailableLive
	}
	return noteSearchUnavailable
}

// liveSearch — Azure DevOps Search yedeği. force=true geri çekilme ve
// "unavailable" önbelleğini atlar (yalnız yöneticinin "Aramayı test et"i).
func (s *Service) liveSearch(ctx context.Context, query, project string, terms []string, st Stats, cfg Config, force bool) ([]Hit, LiveDiag) {
	var d LiveDiag
	api := s.apiOrNil()
	if api == nil || !api.Configured() {
		d.Skipped = "not_configured"
		return nil, d
	}
	if !force {
		if why := s.searchBlocked(); why != "" {
			d.Skipped = why
			if why == "unavailable" {
				d.Unavailable, d.Note = true, unavailableNote(cfg)
			}
			return nil, d
		}
	}
	lctx, cancel := context.WithTimeout(ctx, liveBudget)
	defer cancel()
	andQ, orQ := LiveSearchQueries(query)
	if andQ == "" {
		andQ = strings.Join(terms, " ")
	}
	d.Attempted, d.Query, d.QueryMode = true, andQ, "and"
	res, info, err := api.SearchWiki(lctx, andQ, project, liveTop)
	// AND boş döndüyse — ya da sunucu AND sorgusunu sürümle ilgisiz 400 ile
	// reddettiyse (sorgu sözdizimi) — bir kez düz sözcüklü OR sorgusu.
	if orQ != "" && ((err == nil && len(res) == 0) || (err != nil && info.Class == devops.WikiSearchBadRequest)) {
		d.Query, d.QueryMode = orQ, "or"
		res, info, err = api.SearchWiki(lctx, orQ, project, liveTop)
	}
	d.Info = info
	if errors.Is(err, devops.ErrWikiSearchUnavailable) {
		s.setSearchState(SearchUnavailable)
		s.recordSearch(ctx, SearchUnavailable, info, d.QueryMode)
		d.Unavailable, d.Note = true, unavailableNote(cfg)
		return nil, d
	}
	if err != nil {
		d.Error = err.Error() // ayrıntı yalnız tanıda ("Aramayı test et")
		d.Note = NoteLiveSearchFailed
		// İstemci iptali (sekme kapandı) herkes için geri çekilme sebebi değil.
		if ctx.Err() == nil {
			s.recordSearch(ctx, "error", info, d.QueryMode)
			// Sürümle ilgisiz 400 bu SORGUYA özgüdür (sözdizimi, süzgeç): uç
			// sağlam, diğer sorular etkilenmesin — genel geri çekilme YOK.
			if info.Class != devops.WikiSearchBadRequest {
				s.searchFailed()
				d.Note = NoteLiveSearchFailed + " (15 dk sonra yeniden denenecek)"
			}
		}
		return nil, d
	}
	s.setSearchState(SearchAvailable)
	s.recordSearch(ctx, SearchAvailable, info, d.QueryMode)
	d.Results = len(res)
	picks := make([]devops.WikiSearchHit, 0, liveRead)
	for _, r := range res {
		if !cfg.WikiAllowed(r.Project, r.WikiName) {
			continue
		}
		picks = append(picks, r)
		if len(picks) >= liveRead {
			break
		}
	}
	pages := s.readLivePages(lctx, api, picks, cfg)
	var out []Hit
	for i, lp := range pages {
		if lp == nil {
			continue
		}
		d.Read++
		out = append(out, scoreLivePage(lp.rec, lp.chunks, terms, st, i, d.QueryMode, picks[i].Snippet)...)
	}
	d.Hits = len(out)
	return out, d
}

// livePage — okunan canlı sayfa + parçaları.
type livePage struct {
	rec    PageRecord
	chunks []ChunkRecord
}

// readLivePages — seçilen isabetlerin içeriği, EŞZAMANLI (en çok liveRead
// istek, her biri lctx tavanında). Sıra korunur; okunamayan nil.
func (s *Service) readLivePages(ctx context.Context, api API, picks []devops.WikiSearchHit, cfg Config) []*livePage {
	out := make([]*livePage, len(picks))
	var wg sync.WaitGroup
	for i, r := range picks {
		wg.Add(1)
		go func(i int, r devops.WikiSearchHit) {
			defer wg.Done()
			rec, chunks, err := s.pageForLive(ctx, api, r, cfg)
			if err == nil && rec != nil {
				out[i] = &livePage{rec: *rec, chunks: chunks}
			}
		}(i, r)
	}
	wg.Wait()
	return out
}

// scoreLivePage — SAF: canlı sayfanın parçalarını skorlar. Tam-jeton lexical
// skor korunur; ADO sırası rank'tan türetilen taban skoru KANITLI en iyi
// parçaya (terim kökü geçen ya da ADO vurgusunu taşıyan) uygulanır:
//
//	base = liveTopScore × liveDecay^rank   (OR sorgusunda × (liveORMin + (1-liveORMin)×kök-kapsamı))
//
// Kanıt yoksa (ne kök eşleşmesi ne vurgu) yalnız lexical skor kalır.
func scoreLivePage(rec PageRecord, chunks []ChunkRecord, terms []string, st Stats, rank int, mode, snippet string) []Hit {
	if len(chunks) == 0 || len(terms) == 0 {
		return nil
	}
	cands := make([]Candidate, 0, len(chunks))
	for _, c := range chunks {
		cands = append(cands, candidateFromChunk(rec, c, terms))
	}
	lst := st
	if lst.N == 0 {
		// Yerel indeks boş: korpus = bu sayfanın parçaları (en az 1 belge).
		lst = Stats{N: uint64(len(cands)), AvgDL: avgDL(cands), DF: make([]uint64, len(terms))}
	}
	lex := map[string]Hit{}
	for _, h := range RankLexical(cands, lst, terms) {
		lex[h.Key()] = h
	}
	covs := make([]float64, len(chunks))
	best, bestCov := -1, 0.0
	for i, c := range chunks {
		covs[i] = stemCoverage(c.Tokens, terms)
		if covs[i] > bestCov {
			best, bestCov = i, covs[i]
		}
	}
	if best < 0 && strings.TrimSpace(snippet) != "" {
		best = chunkWithSnippet(chunks, snippet)
	}
	base := liveTopScore * math.Pow(liveDecay, float64(rank))
	if mode == "or" {
		base *= liveORMin + (1-liveORMin)*bestCov
	}
	out := make([]Hit, 0, len(cands))
	for i, c := range cands {
		h, ok := lex[c.Key()]
		if !ok {
			h = Hit{ChunkRef: c.ChunkRef}
		}
		boost := 0.0
		switch {
		case i == best:
			boost = base
		case best >= 0 && covs[i] > 0 && bestCov > 0:
			boost = base * liveOtherChunk * covs[i] / bestCov
		}
		if boost > h.Score {
			h.Score = boost
		}
		if h.Score <= 0 {
			continue
		}
		h.Live = true
		out = append(out, h)
	}
	sortHits(out)
	return out
}

// stemMatch — SAF: sorgu terimi jetonla kök düzeyinde eşleşiyor mu
// (yalnız canlı isabetin KANIT kontrolü; yerel skor tam-jetonlu kalır).
// Bileşik/rakamlı terim tam eşleşmeli; ≥5 harfli sözcük ilk 5 harfte, 3-4
// harfli sözcük önek + en çok 4 harf ekle ("pod" ↔ "podu", "servisini" ↔ "servis").
func stemMatch(term, tok string) bool {
	if term == tok {
		return true
	}
	if strings.IndexFunc(term, isJoiner) >= 0 || strings.IndexFunc(term, func(r rune) bool { return r >= '0' && r <= '9' }) >= 0 {
		return false
	}
	tr, kr := []rune(term), []rune(tok)
	switch {
	case len(tr) >= 5:
		return len(kr) >= 5 && string(tr[:5]) == string(kr[:5])
	case len(tr) >= 3:
		return strings.HasPrefix(tok, term) && len(kr)-len(tr) <= 4
	}
	return false
}

// stemCoverage — SAF: terimlerin kaçı (oran) parçanın jetonlarında kök düzeyinde geçiyor.
func stemCoverage(tokens, terms []string) float64 {
	if len(terms) == 0 {
		return 0
	}
	n := 0
	for _, t := range terms {
		for _, x := range tokens {
			if stemMatch(t, x) {
				n++
				break
			}
		}
	}
	return float64(n) / float64(len(terms))
}

// chunkWithSnippet — SAF: ADO vurgusunun geçtiği parça (yoksa ilk parça).
func chunkWithSnippet(chunks []ChunkRecord, snippet string) int {
	sn := Fold(strings.Trim(strings.TrimSpace(snippet), "…."))
	if r := []rune(sn); len(r) > 40 {
		sn = string(r[:40])
	}
	if sn != "" {
		for i, c := range chunks {
			if strings.Contains(Fold(c.Text), sn) {
				return i
			}
		}
	}
	return 0
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

// pageForLive — canlı isabetin sayfası. Karma modda: yerelde varsa oradan,
// yoksa API'den okunur ve yerel depoya yazılır. Canlı modda: bellek
// önbelleği, yoksa API (CH'ye YAZILMAZ).
func (s *Service) pageForLive(ctx context.Context, api API, r devops.WikiSearchHit, cfg Config) (*PageRecord, []ChunkRecord, error) {
	if cfg.EffectiveMode() == ModeLive {
		if rec, ch, ok := s.live.get(liveKey(r.Project, r.WikiID, r.Path), s.now()); ok {
			return &rec, ch, nil
		}
		return s.fetchPage(ctx, api, r.Project, r.WikiID, r.WikiName, r.Path, false)
	}
	if rec, err := s.store.GetWikiPage(ctx, r.Project, r.WikiID, r.Path); err == nil && rec != nil {
		return rec, BuildChunks(rec.Title, rec.Content), nil
	}
	return s.fetchPage(ctx, api, r.Project, r.WikiID, r.WikiName, r.Path, true)
}

// fetchPage — sayfayı API'den okur ve parçalar. persist=true: (varsa) embed
// eder ve yerel depoya yazar; false (canlı mod): yalnız bellek önbelleği.
func (s *Service) fetchPage(ctx context.Context, api API, project, wikiID, wikiName, path string, persist bool) (*PageRecord, []ChunkRecord, error) {
	pg, err := api.GetWikiPage(ctx, project, wikiID, path, "")
	if err != nil {
		return nil, nil, err
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
	if !persist {
		s.live.put(rec, chunks, s.now())
		return &rec, chunks, nil
	}
	embedChunks(ctx, s.embedderOrNil(), rec.Title, chunks)
	rec.Chunks = uint32(len(chunks))
	var oldN uint32
	if old, err := s.store.GetWikiPage(ctx, project, wikiID, pg.Path); err == nil && old != nil {
		oldN = old.Chunks
	}
	// Yazma hatası okumayı düşürmez: sayfa yine cevaba girer.
	_ = s.store.UpsertWikiPage(ctx, rec, chunks, oldN)
	return &rec, chunks, nil
}

// ReadPage — tek sayfanın içeriği. wiki adı ya da id'si kabul edilir.
// Yerelde yoksa (ve bağlantı varsa) API'den okunur; karma modda yerel depoya
// yazılır, canlı modda yalnız bellek önbelleğine. İzin listesi dışındaki wiki
// ErrOutOfScope.
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
	liveMode := cfg.EffectiveMode() == ModeLive
	if liveMode {
		if rec, _, ok := s.live.get(liveKey(project, wiki, path), s.now()); ok {
			if !cfg.WikiAllowed(rec.Project, rec.WikiName) {
				return nil, ErrOutOfScope
			}
			return &rec, nil
		}
	} else if rec, err := s.store.GetWikiPage(ctx, project, wiki, path); err == nil && rec != nil {
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
			rec, _, err := s.fetchPage(rctx, api, project, w.ID, w.Name, path, !liveMode)
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
