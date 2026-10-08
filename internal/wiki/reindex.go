package wiki

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

// reindex.go — v0.10.1127: jetonlayıcı sürümü değişince SAKLI içerikten
// yeniden jetonlama (Azure DevOps'a HİÇ gidilmez).
//
// Kök neden: Türkçe kök biçimleri (stem.go) yalnız yeni yazılan parçalara
// girerdi; artımlı senkron değişmeyen sayfayı hiç okumadığı için 12k sayfalık
// bir indeks aylarca eski jetonlarla kalırdı. Sayfa içeriği wiki_pages'te
// duruyor: parçalar oradan yeniden kurulur.
//
// Sınırlar: geçiş başına en çok reindexPerPass sayfa (sıralı anahtar
// imleci durum blobunda — ReindexCursor; tarama bitince ReindexScanned),
// eşzamanlılık syncWorkers, ctx iptali / mod değişimi durdurur. İş sürerken
// SyncDue reindexPassGap aralıkla art arda geçiş ister.
//
// Hata (inceleme F1): yazılamayan sayfa ATLANMAZ — yeniden deneme listesine
// (ReindexRetry, tavan reindexRetryMax) girer, sonraki geçişlerde en çok
// reindexMaxAttempts kez denenir. Sürüm ancak tarama bitmiş VE liste boş ya da
// kalan her girdi tükenmişken kaydedilir; tükenen sayfalar durum hatası olur.
//
// Embedding YENİDEN HESAPLANMAZ: parça metni (ve başlığı) aynıysa saklı vektör
// taşınır; yalnız metni değişen parça (embedder varsa) yeniden embed edilir —
// aynı parçalayıcıyla bu hiç olmaz. Parça sayısı değişmediyse wiki_pages
// satırı yeniden yazılmaz (yalnız parçalar).
//
// Canlı mod (CH yok): senkron koşmaz; bellek önbelleği zaten yeni
// jetonlayıcıyla kurulur.

// reindexPerPass — tek geçişte (yeni) taranan en çok sayfa.
var reindexPerPass = 5000 // var: test dikişi

const (
	// reindexRetryMax — yeniden deneme listesinin tavanı (durum blobu sınırlı).
	reindexRetryMax = 200
	// reindexMaxAttempts — sayfa başına en çok deneme (ilk deneme dahil).
	reindexMaxAttempts = 3
	// reindexPassGap — iş sürerken art arda geçişlerin en kısa aralığı.
	reindexPassGap = 2 * time.Minute
)

// ReindexRetry — yeniden jetonlanamayan sayfa (durum blobunda).
type ReindexRetry struct {
	Key      string `json:"key"` // wiki_id \x00 path
	Attempts int    `json:"attempts"`
	Err      string `json:"err,omitempty"`
}

// pageKey — indeks anahtarı (wiki_id \x00 path) — imleç sırası.
func pageKey(m PageMeta) string { return m.WikiID + "\x00" + m.Path }

// reindexPending — SAF: yeniden jetonlama başladı ve bitmedi mi (SyncDue
// art arda geçiş ister).
func reindexPending(st Status) bool {
	return st.TokenizerVersion != tokenizerVersion &&
		(st.ReindexCursor != "" || st.ReindexScanned || len(st.ReindexRetry) > 0)
}

// reindexPass — st.TokenizerVersion güncel değilse saklı içerikten yeniden
// jetonlar; iş bitince sürümü kaydeder. ADO çağrısı yapmaz.
func (s *Service) reindexPass(ctx context.Context, st *Status) {
	if st.TokenizerVersion == tokenizerVersion {
		return
	}
	idx, err := s.store.WikiPageIndex(ctx)
	if err != nil {
		st.addErr("yeniden jetonlama: indeks okunamadı: " + err.Error())
		return
	}
	byKey := make(map[string]PageMeta, len(idx))
	for _, m := range idx {
		byKey[pageKey(m)] = m
	}
	sort.Slice(idx, func(i, j int) bool { return pageKey(idx[i]) < pageKey(idx[j]) })

	type job struct {
		m     PageMeta
		retry int // ReindexRetry indeksi; -1 = tarama
	}
	var jobs []job
	// 1) bekleyen yeniden denemeler (sayfa artık yoksa düşer).
	retries := make([]ReindexRetry, 0, len(st.ReindexRetry))
	for _, r := range st.ReindexRetry {
		m, ok := byKey[r.Key]
		if !ok {
			continue
		}
		retries = append(retries, r)
		if r.Attempts < reindexMaxAttempts {
			jobs = append(jobs, job{m: m, retry: len(retries) - 1})
		}
	}
	// 2) imleçten sonraki tarama partisi.
	more := false
	var lastKey string
	if !st.ReindexScanned {
		n := 0
		for _, m := range idx {
			k := pageKey(m)
			if st.ReindexCursor != "" && k <= st.ReindexCursor {
				continue
			}
			if n >= reindexPerPass {
				more = true
				break
			}
			jobs = append(jobs, job{m: m, retry: -1})
			lastKey = k
			n++
		}
	}

	emb := s.embedderOrNil()
	errs := make([]error, len(jobs))
	ran := make([]bool, len(jobs))
	ch := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < syncWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ch {
				errs[i] = s.reindexPage(ctx, emb, jobs[i].m)
				ran[i] = true
			}
		}()
	}
	stopped := false
	for i := range jobs {
		if ctx.Err() != nil || s.syncStopRequested() {
			stopped = true
			break
		}
		select {
		case ch <- i:
		case <-ctx.Done():
			stopped = true
		}
		if stopped {
			break
		}
	}
	close(ch)
	wg.Wait()
	if ctx.Err() != nil {
		stopped = true
	}

	done := 0
	var fresh []ReindexRetry
	for i, j := range jobs {
		if !ran[i] {
			continue
		}
		e := errs[i]
		if e == nil {
			done++
		}
		switch {
		case j.retry >= 0 && e == nil:
			retries[j.retry].Attempts = -1 // başarı: aşağıda düşer
		case j.retry >= 0:
			retries[j.retry].Attempts++
			retries[j.retry].Err = trimErr(e)
		case e != nil && !stopped:
			fresh = append(fresh, ReindexRetry{Key: pageKey(j.m), Attempts: 1, Err: trimErr(e)})
		}
	}
	st.Reindexed = done
	kept := retries[:0]
	for _, r := range retries {
		if r.Attempts >= 0 {
			kept = append(kept, r)
		}
	}
	for _, r := range fresh {
		if len(kept) >= reindexRetryMax {
			// Liste dolu: deneme hakkı tükenmiş sayılır (hata olarak kalır).
			m := byKey[r.Key]
			st.addErr(fmt.Sprintf("yeniden jetonlama %s/%s%s: %s (yeniden deneme listesi dolu)", m.Project, m.WikiName, m.Path, r.Err))
			continue
		}
		kept = append(kept, r)
	}
	st.ReindexRetry = kept
	if stopped {
		// İmleç ilerlemez: parti bir sonraki geçişte baştan (yazım idempotent).
		return
	}
	if !st.ReindexScanned {
		if more {
			st.ReindexCursor = lastKey
		} else {
			st.ReindexCursor, st.ReindexScanned = "", true
		}
	}
	if st.ReindexScanned {
		pending := false
		for _, r := range st.ReindexRetry {
			if r.Attempts < reindexMaxAttempts {
				pending = true
			}
		}
		if !pending {
			for _, r := range st.ReindexRetry {
				m := byKey[r.Key]
				st.addErr(fmt.Sprintf("yeniden jetonlama %s/%s%s: %d denemede yazılamadı: %s", m.Project, m.WikiName, m.Path, r.Attempts, r.Err))
			}
			st.TokenizerVersion = tokenizerVersion
			st.ReindexCursor, st.ReindexScanned, st.ReindexRetry = "", false, nil
		}
	}
	if len(jobs) > 0 {
		log.Printf("[wiki-sync] reindex tokenizer=v%d pages=%d retry=%d more=%v", tokenizerVersion, done, len(st.ReindexRetry), more)
	}
}

func trimErr(e error) string {
	if e == nil {
		return ""
	}
	m := e.Error()
	if r := []rune(m); len(r) > 200 {
		m = string(r[:200]) + "…"
	}
	return m
}

// reindexPage — tek sayfa: saklı içerik → parçalar (yeni jetonlar) → yazım.
// Metni aynı kalan parçanın embedding'i taşınır; parça sayısı değişmediyse
// yalnız parçalar yazılır (sayfa satırı aynen).
func (s *Service) reindexPage(ctx context.Context, emb Embedder, m PageMeta) error {
	rec, err := s.store.GetWikiPage(ctx, m.Project, m.WikiID, m.Path)
	if err != nil {
		return err
	}
	if rec == nil {
		return nil // arada silinmiş
	}
	old, err := s.store.WikiPageChunks(ctx, m.WikiID, m.Path)
	if err != nil {
		return err
	}
	chunks := BuildChunks(rec.Title, rec.Content)
	if reuseEmbeddings(chunks, old) && emb != nil {
		// Metni değişen parça var (parçalayıcı değişmişse): yalnız onlar.
		var miss []ChunkRecord
		var at []int
		for i, c := range chunks {
			if c.Embedding == nil {
				miss = append(miss, c)
				at = append(at, i)
			}
		}
		if embedChunks(ctx, emb, rec.Title, miss) {
			for k, i := range at {
				chunks[i].Embedding = miss[k].Embedding
			}
		}
	}
	if uint32(len(chunks)) == m.Chunks {
		return s.store.UpsertWikiChunks(ctx, *rec, chunks, m.Chunks)
	}
	rec.Chunks = uint32(len(chunks))
	return s.store.UpsertWikiPage(ctx, *rec, chunks, m.Chunks)
}

// reuseEmbeddings — SAF: aynı sıradaki eski parçanın metni ve başlığı aynıysa
// embedding'i taşınır. Dönüş: eski sayfa embedding'liydi VE en az bir parça
// eşleşmedi (yeniden embed gerekir).
func reuseEmbeddings(chunks, old []ChunkRecord) bool {
	byIdx := make(map[uint32]ChunkRecord, len(old))
	embedded := false
	for _, o := range old {
		byIdx[o.Idx] = o
		if len(o.Embedding) > 0 {
			embedded = true
		}
	}
	missing := false
	for i := range chunks {
		o, ok := byIdx[chunks[i].Idx]
		if ok && len(o.Embedding) > 0 && o.Text == chunks[i].Text && o.Heading == chunks[i].Heading {
			chunks[i].Embedding = o.Embedding
			continue
		}
		missing = true
	}
	return embedded && missing
}
