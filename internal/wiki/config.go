package wiki

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/devops"
)

// config.go — ayar blobu (system_settings["wiki_knowledge"], invariant #6) ve
// senkron durum blobu (system_settings["wiki_sync_status"]): senkron LİDER
// pod'da koşar, Ayarlar sayfası hangi pod'a düşerse düşsün aynı durumu
// görmeli — durum bu yüzden bellekte değil CH'de.
//
// Kimlik bilgisi YOK: bağlantı Ayarlar → Kod entegrasyonu'ndaki Azure DevOps
// ayarından (URL + koleksiyon + PAT + TLS) gelir.

const (
	SettingsKey = "wiki_knowledge"
	StatusKey   = "wiki_sync_status"

	DefaultIntervalMin = 60
	MinIntervalMin     = 15
	MaxIntervalMin     = 7 * 24 * 60
	DefaultMaxPages    = 5000
	MaxMaxPages        = 50000

	// MinContextChars / MaxContextChars — v0.10.1136: sohbetin wiki bağlam
	// bütçesi elle ayarı (karakter); 0 = otomatik (modelin penceresinden).
	MinContextChars = 4000
	MaxContextChars = 48000
)

// Config — operatörün ayarı.
type Config struct {
	Enabled bool `json:"enabled"`
	// Projects — proje izin listesi (harf-duyarsız ad); boş = PAT'in gördüğü tüm projeler.
	Projects []string `json:"projects,omitempty"`
	// Wikis — wiki izin listesi: "WikiAdı" ya da "Proje/WikiAdı"; boş = tümü.
	Wikis []string `json:"wikis,omitempty"`
	// IntervalMin — senkron aralığı (dk); 0 → 60, alt sınır 15.
	IntervalMin int `json:"intervalMin,omitempty"`
	// MaxPages — senkron başına sayfa tavanı; 0 → 5000.
	MaxPages int `json:"maxPages,omitempty"`
	// DisableLiveSearch — Azure DevOps Search canlı yedeğini kapatır (mod
	// alanı boşken "sync" ile eşdeğer; Mode doluysa Mode kazanır).
	DisableLiveSearch bool `json:"disableLiveSearch,omitempty"`
	// Mode — v0.10.1124: hybrid (varsayılan: senkron + gerekirse canlı yedek)
	// | live (senkron YOK; her wiki erişimi Azure DevOps Search + API okuması,
	// sayfalar yalnız bellek önbelleğinde, CH'ye yazılmaz) | sync (yalnız
	// yerel indeks, canlı arama yok).
	Mode string `json:"mode,omitempty"`
	// ContextChars — v0.10.1136: sohbet cevabına giren wiki bağlamının toplam
	// bütçesi (karakter). 0 = otomatik (aktif modelin bağlam penceresinden;
	// bilinmiyorsa 16000). Aralık [MinContextChars, MaxContextChars].
	ContextChars int `json:"contextChars,omitempty"`
	// DisablePageSelect — v0.10.1136: iki aşamalı okuma (model önce aday
	// sayfaların başlık/kesitlerinden okunacakları seçer) KAPALI. Varsayılan
	// açık (alanın sıfır değeri) — bir ek LLM çağrısı, ~1-3 sn.
	DisablePageSelect bool `json:"disablePageSelect,omitempty"`
}

// Validate — v0.10.1136: yönetici PUT'unun aralık denetimi (Normalize yine
// kelepçeler; bu, sessiz kelepçe yerine 400 için).
func (c Config) Validate() error {
	if c.ContextChars < 0 || (c.ContextChars > 0 && (c.ContextChars < MinContextChars || c.ContextChars > MaxContextChars)) {
		return fmt.Errorf("wiki bağlam boyutu boş (otomatik) ya da %d–%d karakter olmalı", MinContextChars, MaxContextChars)
	}
	return nil
}

// Mod değerleri.
const (
	ModeHybrid = "hybrid"
	ModeLive   = "live"
	ModeSync   = "sync"
)

// EffectiveMode — yürürlükteki mod (boş/bilinmeyen → hybrid; eski
// DisableLiveSearch bayrağı mod boşken sync demek).
func (c Config) EffectiveMode() string {
	switch c.Mode {
	case ModeLive, ModeSync, ModeHybrid:
		return c.Mode
	}
	if c.DisableLiveSearch {
		return ModeSync
	}
	return ModeHybrid
}

// LiveSearchAllowed — canlı arama bu modda kullanılabilir mi.
func (c Config) LiveSearchAllowed() bool { return c.EffectiveMode() != ModeSync }

// Normalize — SAF: listeleri kırpar/tekilleştirir, sayıları aralığa oturtur.
func (c Config) Normalize() Config {
	switch c.Mode = strings.ToLower(strings.TrimSpace(c.Mode)); c.Mode {
	case ModeHybrid, ModeLive, ModeSync, "":
	default:
		c.Mode = ""
	}
	c.Projects = cleanList(c.Projects)
	c.Wikis = cleanList(c.Wikis)
	switch {
	case c.IntervalMin <= 0:
		c.IntervalMin = 0
	case c.IntervalMin < MinIntervalMin:
		c.IntervalMin = MinIntervalMin
	case c.IntervalMin > MaxIntervalMin:
		c.IntervalMin = MaxIntervalMin
	}
	switch {
	case c.ContextChars <= 0:
		c.ContextChars = 0
	case c.ContextChars < MinContextChars:
		c.ContextChars = MinContextChars
	case c.ContextChars > MaxContextChars:
		c.ContextChars = MaxContextChars
	}
	switch {
	case c.MaxPages <= 0:
		c.MaxPages = 0
	case c.MaxPages > MaxMaxPages:
		c.MaxPages = MaxMaxPages
	}
	return c
}

// Interval — yürürlükteki senkron aralığı.
func (c Config) Interval() time.Duration {
	n := c.Normalize().IntervalMin
	if n == 0 {
		n = DefaultIntervalMin
	}
	return time.Duration(n) * time.Minute
}

// PageCap — yürürlükteki sayfa tavanı.
func (c Config) PageCap() int {
	n := c.Normalize().MaxPages
	if n == 0 {
		return DefaultMaxPages
	}
	return n
}

func cleanList(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.Trim(strings.TrimSpace(s), "/")
		if s == "" || seen[strings.ToLower(s)] {
			continue
		}
		seen[strings.ToLower(s)] = true
		out = append(out, s)
		if len(out) >= 200 {
			break
		}
	}
	return out
}

// ProjectAllowed — SAF: proje izin listesinde mi (boş liste = hepsi).
func (c Config) ProjectAllowed(project string) bool {
	if len(c.Projects) == 0 {
		return true
	}
	for _, p := range c.Projects {
		if strings.EqualFold(p, strings.TrimSpace(project)) {
			return true
		}
	}
	return false
}

// WikiAllowed — SAF: proje VE wiki izinli mi. Wiki girdisi "Ad" ya da "Proje/Ad".
func (c Config) WikiAllowed(project, wikiName string) bool {
	if !c.ProjectAllowed(project) {
		return false
	}
	if len(c.Wikis) == 0 {
		return true
	}
	full := strings.TrimSpace(project) + "/" + strings.TrimSpace(wikiName)
	for _, w := range c.Wikis {
		if strings.EqualFold(w, strings.TrimSpace(wikiName)) || strings.EqualFold(w, full) {
			return true
		}
	}
	return false
}

// Status — son senkronun özeti (Ayarlar'da görünür).
type Status struct {
	LastStartedAt  int64    `json:"lastStartedAt,omitempty"`  // unix ms
	LastFinishedAt int64    `json:"lastFinishedAt,omitempty"` // unix ms
	LastOK         bool     `json:"lastOk"`
	DurationMs     int64    `json:"durationMs,omitempty"`
	Projects       int      `json:"projects"`
	Wikis          int      `json:"wikis"`
	Pages          int      `json:"pages"`     // bu geçişte görülen sayfa
	Fetched        int      `json:"fetched"`   // içeriği okunup (yeniden) indekslenen
	Unchanged      int      `json:"unchanged"` // sürüm/hash değişmemiş
	Deleted        int      `json:"deleted"`   // kaynakta artık olmayan
	Truncated      bool     `json:"truncated,omitempty"`
	Embedded       bool     `json:"embedded,omitempty"` // bu geçişte embedding üretildi mi
	IndexedPages   uint64   `json:"indexedPages"`
	IndexedChunks  uint64   `json:"indexedChunks"`
	Errors         []string `json:"errors,omitempty"`
	RequestedAt    int64    `json:"requestedAt,omitempty"` // "Şimdi senkronize et" damgası
	RequestedBy    string   `json:"requestedBy,omitempty"`
	// Search — canlı arama (Azure DevOps Search) durumu: unknown | available | unavailable.
	// v0.10.1124: okumada paylaşılan wiki_search_status blobundan (hangi pod
	// aradıysa) doldurulur; senkron lideri aramayı hiç denemediği için kendi
	// bellek-içi "unknown"u kartı sonsuza dek "henüz denenmedi"de tutuyordu.
	Search string `json:"search,omitempty"`
	// SearchLast — son canlı aramanın tanısı (paylaşılan blob; yazılmaz).
	SearchLast *SearchStatus `json:"searchLast,omitempty"`
	// TokenizerVersion — v0.10.1127: indeksin hangi jetonlayıcı sürümüyle
	// kurulduğu (tokenizerVersion'dan küçükse senkron saklı içerikten yeniden
	// jetonlar — reindex.go). ReindexCursor yarıda kalan işin imleci;
	// Reindexed bu geçişte yeniden jetonlanan sayfa.
	TokenizerVersion int    `json:"tokenizerVersion,omitempty"`
	ReindexCursor    string `json:"reindexCursor,omitempty"`
	Reindexed        int    `json:"reindexed,omitempty"`
	// ReindexScanned — tarama bitti, yalnız yeniden denemeler bekliyor;
	// ReindexRetry — yazılamayan sayfalar (tavanlı, deneme sayılı; reindex.go).
	ReindexScanned bool           `json:"reindexScanned,omitempty"`
	ReindexRetry   []ReindexRetry `json:"reindexRetry,omitempty"`
	// v0.10.1129 — atlanan (HATA DEĞİL) sayfalar: SkippedEmpty içeriksiz
	// klasör (kod wiki'si .md'siz klasör ya da ağaçta görünüp 404 dönen
	// düğüm), SkippedLarge okuma tavanını aşan sayfa. Skipped tavanlı
	// (statusSkippedMax) örnek liste; içerik ASLA, yalnız konum.
	SkippedEmpty int           `json:"skippedEmpty,omitempty"`
	SkippedLarge int           `json:"skippedLarge,omitempty"`
	Skipped      []SkippedPage `json:"skipped,omitempty"`
}

// Atlama nedenleri (SkippedPage.Reason).
const (
	SkipEmpty = "empty" // içeriksiz klasör / 404
	SkipLarge = "large" // okuma tavanını aşan sayfa
)

// SkippedPage — atlanan tek sayfanın künyesi.
type SkippedPage struct {
	Page   string `json:"page"`   // "Proje/Wiki/yol"
	Reason string `json:"reason"` // SkipEmpty | SkipLarge
}

const statusSkippedMax = 20

// addSkip — atlanan sayfayı sayar; örnek liste tavanlı.
func (st *Status) addSkip(page, reason string) {
	switch reason {
	case SkipLarge:
		st.SkippedLarge++
	default:
		reason = SkipEmpty
		st.SkippedEmpty++
	}
	if len(st.Skipped) >= statusSkippedMax {
		return
	}
	if r := []rune(page); len(r) > 300 {
		page = string(r[:300]) + "…"
	}
	st.Skipped = append(st.Skipped, SkippedPage{Page: page, Reason: reason})
}

// SearchStatusKey — son canlı arama sonucunun paylaşılan blobu (v0.10.1124).
const SearchStatusKey = "wiki_search_status"

// SearchStatus — son canlı aramanın içerik-SİZ özeti (hangi pod olursa).
type SearchStatus struct {
	State      string `json:"state"` // available | unavailable | error
	At         int64  `json:"at"`    // unix ms
	Class      string `json:"class,omitempty"`
	HTTPStatus int    `json:"httpStatus,omitempty"`
	APIVersion string `json:"apiVersion,omitempty"`
	Hits       int    `json:"hits"`
	Mode       string `json:"mode,omitempty"` // and | or
}

const statusErrorsMax = 20

// addErr — durum hatası (tavanlı; içerik ASLA, yalnız konum + sanitize edilmiş hata).
func (st *Status) addErr(msg string) {
	if len(st.Errors) >= statusErrorsMax {
		return
	}
	if r := []rune(msg); len(r) > 300 {
		msg = string(r[:300]) + "…"
	}
	st.Errors = append(st.Errors, msg)
}

// SettingsStore — system_settings dilimi.
type SettingsStore interface {
	GetSetting(ctx context.Context, key string) ([]byte, error)
	PutSetting(ctx context.Context, key string, value []byte) error
}

// API — devops.Service'in wiki dilimi (testte sahte sunucuya karşı gerçek
// istemci, saf testlerde sahte uygulama).
type API interface {
	Configured() bool
	ListWikiProjects(ctx context.Context) ([]string, error)
	ListWikis(ctx context.Context, project string) ([]devops.WikiInfo, error)
	WikiPageTree(ctx context.Context, project, wikiID string, limit int) ([]devops.WikiPageRef, bool, error)
	WikiItemVersions(ctx context.Context, w devops.WikiInfo) (map[string]string, error)
	GetWikiPage(ctx context.Context, project, wikiID, path, ifNoneMatch string) (devops.WikiPage, error)
	SearchWiki(ctx context.Context, text, project string, top int) ([]devops.WikiSearchHit, devops.WikiSearchInfo, error)
	WikiPageWebURL(project, wikiName, path, remoteURL string) string
}

// Embedder — opsiyonel embedding (rag.Service.Embed). nil = yok.
type Embedder func(ctx context.Context, texts []string) ([][]float32, error)

// Service — wiki bilgi katmanının süreç-içi yüzü.
type Service struct {
	mu  sync.RWMutex
	cfg Config

	store Store
	api   func() API      // nil dönebilir (DevOps yok)
	embed func() Embedder // nil dönebilir (embedding yok)

	runMu   sync.Mutex // aynı süreçte tek senkron
	running bool

	statusMu     sync.Mutex
	statusCache  Status
	statusLoaded time.Time

	searchMu      sync.Mutex
	searchState   string // unknown | available | unavailable
	searchChecked time.Time
	// searchBackoffUntil — geçici hata sonrası canlı arama bu ana dek denenmez.
	searchBackoffUntil time.Time
	// searchSaved / searchSavedSig — paylaşılan arama blobunun son yazımı
	// (yazma kısıtı: durum değişince ya da searchPersistEvery'de bir).
	searchSaved    time.Time
	searchSavedSig string
	searchCache    *SearchStatus

	// live — canlı mod sayfa önbelleği (bellek-içi, CH'ye yazılmaz).
	live *liveCache

	// now — test dikişi.
	now func() time.Time
	// rps — senkron istek hızı (saniyede); 0 → defaultRPS.
	rps float64
	// tick — lider döngüsü aralığı; 0 → loopTick (test dikişi).
	tick time.Duration
	// watch — liderlik kaybı denetim aralığı; 0 → leaderWatch (test dikişi).
	watch time.Duration
}

// New — store zorunlu; api/embed sağlayıcıları canlı bağımlılığı her çağrıda
// okur (Ayarlar'dan açılıp kapanan bağlantı bir sonraki turda görünür).
func New(store Store, api func() API, embed func() Embedder) *Service {
	return &Service{store: store, api: api, embed: embed, searchState: SearchUnknown, now: time.Now, live: newLiveCache()}
}

// Arama durumu değerleri.
const (
	SearchUnknown     = "unknown"
	SearchAvailable   = "available"
	SearchUnavailable = "unavailable"
)

// Config — canlı ayarın kopyası.
func (s *Service) Config() Config {
	if s == nil {
		return Config{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Configure — ayarı değiştirir (normalize edilmiş).
func (s *Service) Configure(c Config) {
	s.mu.Lock()
	s.cfg = c.Normalize()
	s.mu.Unlock()
}

// Enabled — wiki bilgisi açık VE DevOps bağlantısı var mı.
func (s *Service) Enabled() bool {
	if s == nil || !s.Config().Enabled {
		return false
	}
	a := s.apiOrNil()
	return a != nil && a.Configured()
}

func (s *Service) apiOrNil() API {
	if s == nil || s.api == nil {
		return nil
	}
	return s.api()
}

func (s *Service) embedderOrNil() Embedder {
	if s == nil || s.embed == nil {
		return nil
	}
	return s.embed()
}

// LoadPersisted — ayar blobunu okur (yoksa sıfır ayar).
func (s *Service) LoadPersisted(ctx context.Context, store SettingsStore) error {
	if s == nil || store == nil {
		return nil
	}
	raw, err := store.GetSetting(ctx, SettingsKey)
	if err != nil || len(raw) == 0 {
		return err
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return err
	}
	s.Configure(c)
	return nil
}

// SavePersisted — ayar blobunu yazar ve canlıya alır.
func (s *Service) SavePersisted(ctx context.Context, store SettingsStore, c Config) error {
	c = c.Normalize()
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := store.PutSetting(ctx, SettingsKey, raw); err != nil {
		return err
	}
	s.Configure(c)
	return nil
}

// statusTTL — durum blobunun süreç-içi önbelleği (sohbet yolu bayatlık
// kararını her soruda CH'ye sormasın).
const statusTTL = 30 * time.Second

// Status — paylaşılan durum (CH blobu, 30 sn önbellekli). fresh=true önbelleği atlar.
func (s *Service) Status(ctx context.Context, fresh bool) Status {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	if !fresh && !s.statusLoaded.IsZero() && s.now().Sub(s.statusLoaded) < statusTTL {
		st := s.statusCache
		st.Search, st.SearchLast = s.mergedSearchState(st.Search)
		return st
	}
	if raw, err := s.store.GetSetting(ctx, StatusKey); err == nil && len(raw) > 0 {
		var st Status
		if json.Unmarshal(raw, &st) == nil {
			s.statusCache = st
		}
	}
	if raw, err := s.store.GetSetting(ctx, SearchStatusKey); err == nil && len(raw) > 0 {
		var ss SearchStatus
		if json.Unmarshal(raw, &ss) == nil && ss.State != "" {
			s.searchMu.Lock()
			s.searchCache = &ss
			s.searchMu.Unlock()
		}
	}
	s.statusLoaded = s.now()
	st := s.statusCache
	st.Search, st.SearchLast = s.mergedSearchState(st.Search)
	return st
}

// mergedSearchState — kartın arama durumu: paylaşılan blob (son arama,
// hangi pod) > bu pod'un bilinen durumu > senkron blobundaki eski değer.
func (s *Service) mergedSearchState(fromSync string) (string, *SearchStatus) {
	s.searchMu.Lock()
	sc := s.searchCache
	local := s.searchState
	s.searchMu.Unlock()
	if sc != nil {
		cp := *sc
		st := cp.State
		if st == "error" {
			st = SearchUnknown
			if local == SearchAvailable || local == SearchUnavailable {
				st = local
			}
		}
		return st, &cp
	}
	if local == SearchAvailable || local == SearchUnavailable {
		return local, nil
	}
	if fromSync == "" {
		fromSync = SearchUnknown
	}
	return fromSync, nil
}

// saveStatus — durumu yazar (önbelleği de günceller).
func (s *Service) saveStatus(ctx context.Context, st Status) {
	st.SearchLast = nil // ayrı blobun (SearchStatusKey) kopyası senkron blobuna yazılmaz
	if raw, err := json.Marshal(st); err == nil {
		_ = s.store.PutSetting(ctx, StatusKey, raw)
	}
	s.statusMu.Lock()
	s.statusCache, s.statusLoaded = st, s.now()
	s.statusMu.Unlock()
}

// RequestSync — "Şimdi senkronize et": damgayı durum blobuna yazar; lider
// pod'un döngüsü bir sonraki tikte (≤ loopTick) görür ve koşar.
func (s *Service) RequestSync(ctx context.Context, by string) Status {
	st := s.Status(ctx, true)
	st.RequestedAt = s.now().UnixMilli()
	st.RequestedBy = by
	s.saveStatus(ctx, st)
	return st
}

// Running — bu süreçte senkron koşuyor mu.
func (s *Service) Running() bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.running
}

// MarkPurged — v0.10.1124 "İndeksi temizle" sonrası durum: indeks sayıları
// sıfır, son senkron damgası korunur (kart "0 sayfa" gösterir).
func (s *Service) MarkPurged(ctx context.Context) Status {
	st := s.Status(ctx, true)
	st.IndexedPages, st.IndexedChunks = 0, 0
	s.saveStatus(ctx, st)
	return s.Status(ctx, false)
}

// syncStopRequested — senkron sürerken mod canlıya alındı ya da özellik
// kapatıldıysa geçiş durmalı (inceleme F4).
func (s *Service) syncStopRequested() bool {
	c := s.Config()
	return !c.Enabled || c.EffectiveMode() == ModeLive
}
