package wiki

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/devops"
)

// sync.go — artımlı senkron (lider pod'da).
//
// Geçiş: projeler (izin listesi) → wiki'ler (izin listesi) → sayfa ağacı
// (recursionLevel=full, toplam tavan MaxPages) → değişim tespiti → yalnız
// DEĞİŞEN sayfanın içeriği okunur, parçalanır, (varsa) embed edilir, yazılır
// → kaynakta artık olmayan sayfa mezar taşıyla silinir.
//
// Değişim tespiti iki basamaklı:
//  1. wiki deposunun git öğe listesi (wiki başına TEK istek) → dosyanın
//     objectId'si (blob SHA). Saklanan sürüm "obj:<sha>" ile aynıysa sayfa
//     hiç istenmez.
//  2. öğe listesi alınamazsa sayfa başına KOŞULLU GET (If-None-Match:
//     saklanan ETag) → 304 değişmemiş; 200 gelirse içerik hash'i de
//     karşılaştırılır (hash aynıysa yazma yok).
//
// Silme güvenliği: bir wiki'nin sayfaları ancak o wiki'nin ağacı HATASIZ ve
// TAVANA TAKILMADAN alındıysa budanır; kapsamdan çıkan / silinen wiki'nin
// sayfaları ancak proje+wiki listelemesi HİÇ hata vermediyse budanır. Geçici
// bir 500 indeksi boşaltmaz.
//
// Sınırlar: eşzamanlılık ≤ syncWorkers, istek hızı ≤ rps (paylaşılan
// belirteç), her istek kendi 20 sn tavanında (devops), tüm geçiş ctx ile
// iptal edilebilir. Log satırları yalnız sayılar + konum taşır, içerik ASLA.

const (
	syncWorkers = 4
	defaultRPS  = 8.0
	// embedBatch — sayfa başına embed çağrısı zaten tek; çok parçalı dev
	// sayfa rag.Embed içinde 64'lük dilimlere bölünür.
	embedTextMaxRunes = 2000
)

// PageMeta — indeksteki sayfanın değişim-tespiti künyesi.
type PageMeta struct {
	WikiID   string
	Path     string
	Project  string
	WikiName string
	Version  string // "obj:<sha>" | "etag:<etag>"
	Hash     string // içerik sha256
	Chunks   uint32
}

// PageRecord — yazılacak/okunan sayfa.
type PageRecord struct {
	Project   string
	WikiID    string
	WikiName  string
	Path      string
	Title     string
	URL       string
	GitPath   string
	Version   string
	Hash      string
	Content   string
	Chunks    uint32
	UpdatedAt time.Time
}

// ChunkRecord — yazılacak parça.
type ChunkRecord struct {
	Idx        uint32
	Heading    string
	Text       string
	Tokens     []string
	HeadTokens []string
	Embedding  []float32
}

// Store — CH dilimi (chstore/wiki.go uygular).
type Store interface {
	SettingsStore
	WikiPageIndex(ctx context.Context) ([]PageMeta, error)
	UpsertWikiPage(ctx context.Context, p PageRecord, chunks []ChunkRecord, oldChunks uint32) error
	DeleteWikiPage(ctx context.Context, m PageMeta) error
	WikiTermStats(ctx context.Context, terms []string, project string) (Stats, error)
	WikiCandidates(ctx context.Context, terms []string, project string, limit int) ([]Candidate, error)
	WikiSemantic(ctx context.Context, emb []float32, project string, k int) ([]SemHit, error)
	GetWikiPage(ctx context.Context, project, wiki, path string) (*PageRecord, error)
	WikiCounts(ctx context.Context) (pages, chunks uint64, err error)
}

// ContentHash — sayfa içeriğinin sha256'sı (hex).
func ContentHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// BuildChunks — sayfa → parça kayıtları (jetonlar dahil, embedding'siz). SAF.
func BuildChunks(title, content string) []ChunkRecord {
	parts := ChunkMarkdown(content)
	out := make([]ChunkRecord, 0, len(parts))
	for i, c := range parts {
		head := title
		if c.Heading != "" {
			head += HeadingSep + c.Heading
		}
		ht := Tokens(head)
		out = append(out, ChunkRecord{
			Idx: uint32(i), Heading: c.Heading, Text: c.Text,
			Tokens:     append(ht, Tokens(c.Text)...),
			HeadTokens: ht,
		})
	}
	return out
}

// embedChunks — opsiyonel; hata → embedding'siz (metin-yalnız indeks).
func embedChunks(ctx context.Context, emb Embedder, title string, chunks []ChunkRecord) bool {
	if emb == nil || len(chunks) == 0 {
		return false
	}
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		t := ChunkContext(title, c.Heading, c.Text)
		if r := []rune(t); len(r) > embedTextMaxRunes {
			t = string(r[:embedTextMaxRunes])
		}
		texts[i] = t
	}
	vecs, err := emb(ctx, texts)
	if err != nil || len(vecs) != len(chunks) {
		return false
	}
	for i := range chunks {
		chunks[i].Embedding = vecs[i]
	}
	return true
}

// rateLimiter — paylaşılan belirteç (basit aralık; ctx-iptalli).
type rateLimiter struct {
	mu   sync.Mutex
	next time.Time
	gap  time.Duration
}

func newRateLimiter(rps float64) *rateLimiter {
	if rps <= 0 {
		rps = defaultRPS
	}
	return &rateLimiter{gap: time.Duration(float64(time.Second) / rps)}
}

func (r *rateLimiter) wait(ctx context.Context) error {
	r.mu.Lock()
	now := time.Now()
	at := r.next
	if at.Before(now) {
		at = now
	}
	r.next = at.Add(r.gap)
	r.mu.Unlock()
	if d := time.Until(at); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return ctx.Err()
}

// SyncDue — SAF: lider döngüsü bu tikte senkron başlatmalı mı.
func SyncDue(cfg Config, st Status, now time.Time) bool {
	if !cfg.Enabled {
		return false
	}
	if st.RequestedAt > st.LastStartedAt {
		return true
	}
	if st.LastStartedAt == 0 {
		return true
	}
	return now.Sub(time.UnixMilli(st.LastStartedAt)) >= cfg.Interval()
}

// pageJob — işçiye giden tek sayfa.
type pageJob struct {
	wiki devops.WikiInfo
	ref  devops.WikiPageRef
	ver  string // öğe listesinden "obj:<sha>" ya da ""
}

// Sync — tek senkron geçişi. Aynı süreçte ikinci çağrı beklemeden döner
// (ErrSyncRunning). Durum blobu başta ve sonda yazılır.
func (s *Service) Sync(ctx context.Context) (Status, error) {
	s.runMu.Lock()
	if s.running {
		s.runMu.Unlock()
		return s.Status(ctx, true), ErrSyncRunning
	}
	s.running = true
	s.runMu.Unlock()
	defer func() {
		s.runMu.Lock()
		s.running = false
		s.runMu.Unlock()
	}()

	cfg := s.Config()
	prev := s.Status(ctx, true)
	start := s.now()
	st := Status{LastStartedAt: start.UnixMilli(), RequestedAt: prev.RequestedAt, RequestedBy: prev.RequestedBy,
		IndexedPages: prev.IndexedPages, IndexedChunks: prev.IndexedChunks, Search: s.searchStateNow()}
	s.saveStatus(ctx, mergeRunning(prev, st))

	api := s.apiOrNil()
	if api == nil || !api.Configured() {
		st.addErr("Azure DevOps bağlantısı yapılandırılmamış (Ayarlar → Kod entegrasyonu)")
		return s.finish(ctx, st, start), errors.New("devops not configured")
	}
	s.syncPass(ctx, cfg, api, &st)
	return s.finish(ctx, st, start), nil
}

// ErrSyncRunning — bu süreçte zaten bir geçiş koşuyor.
var ErrSyncRunning = errors.New("wiki sync already running")

// mergeRunning — başlangıç yazımı: önceki sonuçları koru, yalnız başlangıcı işaretle.
func mergeRunning(prev, st Status) Status {
	out := prev
	out.LastStartedAt = st.LastStartedAt
	return out
}

func (s *Service) finish(ctx context.Context, st Status, start time.Time) Status {
	// İptal edilmiş (liderlik kaybı / kapanış) geçişin durumu da yazılmalı:
	// iptalden ayrılmış kısa bir bağlam.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	end := s.now()
	st.LastFinishedAt = end.UnixMilli()
	st.DurationMs = end.Sub(start).Milliseconds()
	st.LastOK = len(st.Errors) == 0
	if p, c, err := s.store.WikiCounts(ctx); err == nil {
		st.IndexedPages, st.IndexedChunks = p, c
	}
	// Geçiş sürerken gelen "Şimdi senkronize et" damgası ezilmesin.
	if cur := s.Status(ctx, true); cur.RequestedAt > st.RequestedAt {
		st.RequestedAt, st.RequestedBy = cur.RequestedAt, cur.RequestedBy
	}
	st.Search = s.searchStateNow()
	s.saveStatus(ctx, st)
	log.Printf("[wiki-sync] projects=%d wikis=%d pages=%d fetched=%d unchanged=%d deleted=%d errors=%d truncated=%v dur=%dms",
		st.Projects, st.Wikis, st.Pages, st.Fetched, st.Unchanged, st.Deleted, len(st.Errors), st.Truncated, st.DurationMs)
	return st
}

// syncPass — geçişin gövdesi.
func (s *Service) syncPass(ctx context.Context, cfg Config, api API, st *Status) {
	existing := map[string]PageMeta{}
	idx, err := s.store.WikiPageIndex(ctx)
	if err != nil {
		st.addErr("indeks okunamadı: " + err.Error())
		return // indeks bilinmeden silme/artımlılık kararı verilemez
	}
	for _, m := range idx {
		existing[m.WikiID+"\x00"+m.Path] = m
	}

	projects, err := api.ListWikiProjects(ctx)
	if err != nil {
		st.addErr("proje listesi: " + err.Error())
		return
	}
	listingClean := true
	var wikis []devops.WikiInfo
	for _, p := range projects {
		if !cfg.ProjectAllowed(p) {
			continue
		}
		st.Projects++
		ws, err := api.ListWikis(ctx, p)
		if err != nil {
			listingClean = false
			st.addErr(err.Error())
			continue
		}
		for _, w := range ws {
			if cfg.WikiAllowed(p, w.Name) {
				wikis = append(wikis, w)
			}
		}
		if ctx.Err() != nil {
			st.addErr("iptal edildi")
			return
		}
	}
	st.Wikis = len(wikis)

	limiter := newRateLimiter(s.rps)
	emb := s.embedderOrNil()
	budget := cfg.PageCap()
	seenWiki := map[string]bool{}
	seen := map[string]bool{}
	var mu sync.Mutex // st + seen

	for _, w := range wikis {
		seenWiki[w.ID] = true
		if ctx.Err() != nil {
			st.addErr("iptal edildi")
			return
		}
		if budget <= 0 {
			st.Truncated = true
			break
		}
		if err := limiter.wait(ctx); err != nil {
			st.addErr("iptal edildi")
			return
		}
		refs, truncated, err := api.WikiPageTree(ctx, w.Project, w.ID, budget)
		if err != nil {
			st.addErr(fmt.Sprintf("%s/%s sayfa ağacı: %v", w.Project, w.Name, err))
			continue
		}
		budget -= len(refs)
		if truncated {
			st.Truncated = true
		}
		st.Pages += len(refs)
		var versions map[string]string
		if err := limiter.wait(ctx); err == nil {
			if v, verr := api.WikiItemVersions(ctx, w); verr == nil {
				versions = v
			}
		}

		jobs := make(chan pageJob)
		var wg sync.WaitGroup
		for i := 0; i < syncWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range jobs {
					res, err := s.syncPage(ctx, api, limiter, emb, j, existing)
					mu.Lock()
					seen[j.wiki.ID+"\x00"+j.ref.Path] = true
					switch {
					case err != nil:
						st.addErr(fmt.Sprintf("%s/%s%s: %v", j.wiki.Project, j.wiki.Name, j.ref.Path, err))
					case res == pageUnchanged:
						st.Unchanged++
					case res == pageFetched:
						st.Fetched++
					case res == pageEmbedded:
						st.Fetched++
						st.Embedded = true
					}
					mu.Unlock()
				}
			}()
		}
		for _, ref := range refs {
			ver := ""
			if versions != nil && ref.GitItemPath != "" {
				if id, ok := versions[ref.GitItemPath]; ok {
					ver = "obj:" + id
				}
			}
			select {
			case jobs <- pageJob{wiki: w, ref: ref, ver: ver}:
			case <-ctx.Done():
			}
			if ctx.Err() != nil {
				break
			}
		}
		close(jobs)
		wg.Wait()
		if ctx.Err() != nil {
			st.addErr("iptal edildi")
			return
		}
		// Budama: ağaç tam alındıysa bu wiki'de görülmeyen sayfalar silinir.
		// Sayfa hatası budamayı ENGELLEMEZ (hata görülen sayfadadır, görülmeyende değil).
		if !truncated {
			for k, m := range existing {
				if m.WikiID == w.ID && !seen[k] {
					if err := s.store.DeleteWikiPage(ctx, m); err == nil {
						st.Deleted++
					} else {
						st.addErr(fmt.Sprintf("%s silinemedi: %v", m.Path, err))
					}
				}
			}
		}
	}
	// Kapsamdan çıkan / kaynakta silinen wiki'ler: yalnız listeleme temizse
	// ve tavana takılınmadıysa (tavan sonrası wiki'ler hiç gezilmedi).
	if listingClean && !st.Truncated {
		for _, m := range existing {
			if !seenWiki[m.WikiID] {
				if err := s.store.DeleteWikiPage(ctx, m); err == nil {
					st.Deleted++
				}
			}
		}
	}
}

type pageResult int

const (
	pageUnchanged pageResult = iota
	pageFetched
	pageEmbedded
)

// syncPage — tek sayfanın değişim tespiti + gerekirse yeniden indekslenmesi.
func (s *Service) syncPage(ctx context.Context, api API, lim *rateLimiter, emb Embedder, j pageJob, existing map[string]PageMeta) (pageResult, error) {
	old, had := existing[j.wiki.ID+"\x00"+j.ref.Path]
	if had && j.ver != "" && old.Version == j.ver {
		return pageUnchanged, nil
	}
	ifNone := ""
	if had && j.ver == "" && strings.HasPrefix(old.Version, "etag:") {
		ifNone = `"` + strings.TrimPrefix(old.Version, "etag:") + `"`
	}
	if err := lim.wait(ctx); err != nil {
		return pageUnchanged, err
	}
	pg, err := api.GetWikiPage(ctx, j.wiki.Project, j.wiki.ID, j.ref.Path, ifNone)
	if errors.Is(err, devops.ErrWikiNotModified) {
		return pageUnchanged, nil
	}
	if err != nil {
		return pageUnchanged, err
	}
	hash := ContentHash(pg.Content)
	ver := j.ver
	if ver == "" && pg.ETag != "" {
		ver = "etag:" + pg.ETag
	}
	if had && old.Hash == hash && old.Version == ver {
		return pageUnchanged, nil
	}
	remote := pg.RemoteURL
	if remote == "" {
		remote = j.ref.RemoteURL
	}
	rec := PageRecord{
		Project: j.wiki.Project, WikiID: j.wiki.ID, WikiName: j.wiki.Name,
		Path: j.ref.Path, Title: PageTitle(j.ref.Path),
		URL:     api.WikiPageWebURL(j.wiki.Project, j.wiki.Name, j.ref.Path, remote),
		GitPath: j.ref.GitItemPath, Version: ver, Hash: hash, Content: pg.Content,
		UpdatedAt: s.now(),
	}
	chunks := BuildChunks(rec.Title, pg.Content)
	res := pageFetched
	// Hash aynı ama sürüm damgası değiştiyse de yazılır (sonraki geçişin
	// koşullu GET'i / objectId karşılaştırması yeni damgayı görsün).
	if embedChunks(ctx, emb, rec.Title, chunks) {
		res = pageEmbedded
	}
	rec.Chunks = uint32(len(chunks))
	var oldN uint32
	if had {
		oldN = old.Chunks
	}
	if err := s.store.UpsertWikiPage(ctx, rec, chunks, oldN); err != nil {
		return pageUnchanged, err
	}
	return res, nil
}

// loopTick — lider döngüsünün kontrol aralığı (manuel istek gecikmesi tavanı).
const loopTick = 15 * time.Second

// RunLoop — lider-kapılı arka plan döngüsü. isLeader her tikte okunur;
// lider değilken hiçbir CH/DevOps çağrısı yapılmaz (ayar yenileme hariç).
func (s *Service) RunLoop(ctx context.Context, isLeader func() bool, reload func(context.Context)) {
	every := s.tick
	if every <= 0 {
		every = loopTick
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !isLeader() {
			continue
		}
		if reload != nil {
			reload(ctx)
		}
		cfg := s.Config()
		if !cfg.Enabled {
			continue
		}
		if !SyncDue(cfg, s.Status(ctx, true), s.now()) {
			continue
		}
		if _, err := s.syncWhileLeader(ctx, isLeader); err != nil && !errors.Is(err, ErrSyncRunning) {
			log.Printf("[wiki-sync] geçiş: %v", err)
		}
	}
}

// leaderWatch — liderlik kaybı denetim aralığı; Service.watch 0 ise bu.
const leaderWatch = time.Second

// syncWhileLeader — geçişi liderlik süresince koşturur: lider kilidi kaybedilirse
// (Redis kesintisi, lease süresi) türetilmiş ctx iptal edilir ve geçiş yarıda
// durur — yeni lider aynı anda yazmaya başlarken eski lider sürmesin.
func (s *Service) syncWhileLeader(ctx context.Context, isLeader func() bool) (Status, error) {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		every := s.watch
		if every <= 0 {
			every = leaderWatch
		}
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-sctx.Done():
				return
			case <-t.C:
				if !isLeader() {
					log.Printf("[wiki-sync] liderlik kaybedildi — geçiş iptal")
					cancel()
					return
				}
			}
		}
	}()
	return s.Sync(sctx)
}
