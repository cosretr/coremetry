package api

// promql_console_history.go — v0.10.953 (PromQL konsolu, spec C6; karar 8 —
// docs/promql-console/audit.md §7 "Query history").
//
// Sorgu geçmişi SUNUCU TARAFINDA, kullanıcı başına TEK saved_views blobu:
// page='promql-history', id 'promql-history:<uid>', owner_id=uid,
// query_string=JSON (ai_conversations.go emsali; invariant #5 — yeni tablo
// YOK). localStorage DEĞİL: istek "localStorage yok" diyor ve Explore'un
// 4 yuvalı tarayıcı halkası (useQueryHistory.ts) cihazlar arası kaybolur.
// /api/preferences/{key} taşıyamaz: yalnız ≤16 KiB ColumnModel kabul eder.
//
//   GET    /api/promql/history   → {entries:[…en yeni önce], enabled, maxEntries}
//   DELETE /api/promql/history   → tombstone (name='' — preferences_routes.go)
//
// Yazan İSTEMCİ DEĞİL, query handler'ı: KOŞAN her sorgu (ok ve upstream
// hatası) eklenir; korkuluk reddi (aralık, 429), bozuk parametre ve iptal
// edilen koşu EKLENMEZ — geçmiş "ne koştu"yu anlatır, "ne denendi"yi audit
// anlatır. Böylece geçmiş sahte bir POST ile şişirilemez ve istemci
// kaydetmeyi unutamaz.
//
// Kurallar (SAF promqlHistoryPush, tablolu test):
//   - en yeni önde; son 50 giriş
//   - ARDIŞIK özdeş koşu (q + cluster id + mode) tek girişe iner — en yeni
//     damgayla yer değiştirir (Ctrl+Enter'a beş kez basmak beş satır değil)
//   - JSON ≤ 128 KiB: aşarsa EN ESKİ tek tek düşer (50 × 8 KiB sorgu
//     ~400 KiB olurdu; ai_conversations fitChatBlob dersi — sabit bir 413
//     geçmişi sessizce öldürürdü)
//
// API token çağıranları (UserID "token:<id>") geçmiş TUTMAZ: token bir
// otomasyon kimliği, kişi değil — her script koşusu bir CH yazımı ve
// kimsenin okumayacağı bir blob olurdu. GET boş + enabled:false döner.
//
// Oku-değiştir-yaz: ReplacingMergeTree son yazan kazanır. Pod içinde aynı
// kullanıcının iki eşzamanlı koşusu (perUserConcurrency 2) şeritli bir
// kilitle sıraya girer; pod'lar arası yarış kabul edilmiş (audit §7).
// Yazım isteğin iptaline bağlı DEĞİL (context.WithoutCancel + kısa son
// tarih): zaman aşımına uğrayan bir sorgu da geçmişe girer. v0.10.953 —
// kilit beklemesi AYRI sınırlanır; G/Ç son tarihi kilit alındıktan SONRA
// başlar (şerit komşusunun CH turu bu isteğin bütçesini yemez).
//
// Audit YOK (karar 2: geçmiş kişisel durum; audit satırını query handler'ı
// zaten yazıyor). serveCached YOK (kişisel, mutasyona uğrayan satır).

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	// promqlHistoryPage — saved_views ayrımı. DEĞİŞTİRMEK mevcut geçmişi
	// görünmez kılar.
	promqlHistoryPage = "promql-history"
	// promqlHistoryName — satır adı; boş ad tombstone demek (ListSavedViews
	// atlar), bu yüzden sabit ve boş değil.
	promqlHistoryName = "PromQL history"
	// promqlHistoryMaxEntries — karar 8: son 50.
	promqlHistoryMaxEntries = 50
	// promqlHistoryMaxBytes — karar 8: ~128 KiB blob duvarı.
	promqlHistoryMaxBytes = 128 << 10
)

// promqlHistoryWriteTimeout — ekleme/silme G/Ç son tarihi (CH nokta okuma +
// tek satır yazım) VE kilit beklemesinin ayrı tavanı: en kötü yanıt
// gecikmesi ≈ 2 × bu değer. var (const değil): regresyon testi kısaltır.
var promqlHistoryWriteTimeout = 3 * time.Second

// promqlHistoryEntry — tek koşu. `q` kısa: 50 giriş × alan adı baytı blob
// duvarından yer.
type promqlHistoryEntry struct {
	Query      string `json:"q"`
	ClusterID  string `json:"clusterId"` // yeniden koşmak için (ClusterByRef id kabul eder)
	Cluster    string `json:"cluster"`   // gösterim
	Mode       string `json:"mode"`      // instant | range
	Status     string `json:"status"`    // ok | error
	ErrorType  string `json:"errorType,omitempty"`
	Series     int    `json:"series"`
	DurationMs int64  `json:"durationMs"`
	At         int64  `json:"at"` // unix ms (koşunun başı)
}

// promqlHistoryBlob — query_string kolonundaki gövde.
type promqlHistoryBlob struct {
	V       int                  `json:"v"`
	Entries []promqlHistoryEntry `json:"entries"`
}

// promqlHistoryID — deterministik satır kimliği (SAF): upsert doğal.
func promqlHistoryID(uid string) string { return promqlHistoryPage + ":" + uid }

// promqlHistoryDisabledFor — token çağıranı ya da boş kimlik geçmiş tutmaz.
// Boş kimlik: boş owner_id saved_views'ta TAKIM-PAYLAŞIMLI demek
// (aiChatOwner gerekçesi) — kişisel geçmiş oraya asla yazılmaz.
func promqlHistoryDisabledFor(uid string) bool {
	return strings.TrimSpace(uid) == "" || strings.HasPrefix(uid, "token:")
}

// promqlHistorySame — ardışık tekilleştirmenin kimliği (karar 8).
func promqlHistorySame(a, b promqlHistoryEntry) bool {
	return a.Query == b.Query && a.ClusterID == b.ClusterID && a.Mode == b.Mode
}

// errPromQLHistoryEntryTooLarge — tek giriş bile duvara sığmıyor (sorgu
// ≤ 8 KiB olduğundan pratikte ulaşılmaz; savunma).
var errPromQLHistoryEntryTooLarge = errors.New("promql history entry exceeds the blob limit")

// promqlHistoryPush — SAF. e en öne; baştaki özdeş giriş onunla yer
// değiştirir; maxEntries'e kırpılır; JSON maxBytes'ı aşarsa en eski düşer.
// Dönen raw, depolanacak GERÇEK gövdedir (ölçüm marshal edilmiş hâlde).
// Girdi dilimi değiştirilmez.
func promqlHistoryPush(entries []promqlHistoryEntry, e promqlHistoryEntry, maxEntries, maxBytes int) ([]promqlHistoryEntry, string, error) {
	rest := entries
	if len(rest) > 0 && promqlHistorySame(rest[0], e) {
		rest = rest[1:]
	}
	out := make([]promqlHistoryEntry, 0, len(rest)+1)
	out = append(out, e)
	out = append(out, rest...)
	if maxEntries > 0 && len(out) > maxEntries {
		out = out[:maxEntries]
	}
	for {
		raw, err := json.Marshal(promqlHistoryBlob{V: 1, Entries: out})
		if err != nil {
			return nil, "", err
		}
		if len(raw) <= maxBytes {
			return out, string(raw), nil
		}
		if len(out) <= 1 {
			return nil, "", errPromQLHistoryEntryTooLarge
		}
		out = out[:len(out)-1]
	}
}

// promqlHistoryStore — saved_views'ın bu dosyanın kullandığı dar yüzeyi
// (*chstore.Store karşılar). Testler sahte depo enjekte eder: canlı CH yok.
type promqlHistoryStore interface {
	GetSavedView(ctx context.Context, id string) (*chstore.SavedView, error)
	UpsertSavedView(ctx context.Context, v chstore.SavedView) error
}

// promqlHistoryStoreOf — depo seçimi (test dikişi). nil *chstore.Store'u
// arayüze sarmak nil-olmayan bir arayüz üretirdi; açıkça nil döner.
var promqlHistoryStoreOf = func(s *Server) promqlHistoryStore {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store
}

// promqlHistoryLocks — kullanıcı başına oku-değiştir-yaz sıralaması, şeritli
// (sınırlı bellek, temizlik gerektirmez). Kapasite-1 kanal semaforu,
// sync.Mutex DEĞİL: bekleme süre-sınırlı olmalı. v0.10.953 — eskiden 3 s
// son tarih mu.Lock()'tan ÖNCE kuruluyordu; şerit komşusunun (başka
// kullanıcı, aynı fnv şeridi) CH turunu beklemek çağıranın G/Ç bütçesini
// yiyor, girişi sessizce düşürüyor ve sorgu yanıtını başkasının G/Ç'si
// kadar bekletiyordu. Şimdi bekleme ayrı sınırlı (promqlHistoryAcquire),
// G/Ç son tarihi kilit alındıktan sonra başlar: çakışma bir girişi yalnız
// şerit gerçekten tıkalıysa düşürür. Kullanıcı başına sayaçlı kilit
// haritası gerekmez: aynı kullanıcının koşuları (perUserConcurrency ≤ 10)
// zaten çekişir ve sınırlı bekleme iki durumu da kapsar.
var promqlHistoryLocks = func() (a [64]chan struct{}) {
	for i := range a {
		a[i] = make(chan struct{}, 1)
	}
	return
}()

// promqlHistoryAcquire — kilit beklemesi AYRI sınırlanır; G/Ç kendi son
// tarihini bundan SONRA alır (şerit komşusunun CH turu onu yemez). ok=false
// → wait içinde alınamadı; release nil.
func promqlHistoryAcquire(uid string, wait time.Duration) (release func(), ok bool) {
	h := fnv.New32a()
	h.Write([]byte(uid))
	ch := promqlHistoryLocks[h.Sum32()%uint32(len(promqlHistoryLocks))]
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, true
	case <-t.C:
		return nil, false
	}
}

// loadPromQLHistory — kullanıcının girişleri (en yeni önde). Satır yok /
// tombstone / başka sayfa / başka sahip → boş. Bozuk gövde → boş + log
// (geçmiş okunamıyor diye konsol kırılmaz; sonraki ekleme temiz yazar).
func loadPromQLHistory(ctx context.Context, st promqlHistoryStore, uid string) ([]promqlHistoryEntry, error) {
	v, err := st.GetSavedView(ctx, promqlHistoryID(uid))
	if err != nil {
		return nil, err
	}
	if v == nil || v.Name == "" || v.Page != promqlHistoryPage || v.OwnerID != uid {
		return nil, nil
	}
	var blob promqlHistoryBlob
	if err := json.Unmarshal([]byte(v.QueryString), &blob); err != nil {
		log.Printf("[promql] geçmiş gövdesi okunamadı (uid=%s): %v", uid, err)
		return nil, nil
	}
	return blob.Entries, nil
}

// appendPromQLHistory — query handler'ından, yanıttan ÖNCE (istemcinin
// hemen ardından yaptığı GET yeni girişi görsün). Hata yanıtı ETKİLEMEZ,
// yalnız loglanır. Okuma hatasında YAZILMAZ: boş listeyle üzerine yazmak
// geçmişi silerdi.
func (s *Server) appendPromQLHistory(ctx context.Context, uid string, e promqlHistoryEntry) {
	if promqlHistoryDisabledFor(uid) {
		return
	}
	st := promqlHistoryStoreOf(s)
	if st == nil {
		return
	}
	release, ok := promqlHistoryAcquire(uid, promqlHistoryWriteTimeout)
	if !ok {
		log.Printf("[promql] geçmiş kilidi beklenirken süre doldu, ekleme atlandı (uid=%s)", uid)
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), promqlHistoryWriteTimeout)
	defer cancel()
	cur, err := loadPromQLHistory(ctx, st, uid)
	if err != nil {
		log.Printf("[promql] geçmiş okunamadı, ekleme atlandı (uid=%s): %v", uid, err)
		return
	}
	_, raw, err := promqlHistoryPush(cur, e, promqlHistoryMaxEntries, promqlHistoryMaxBytes)
	if err != nil {
		log.Printf("[promql] geçmiş girişi eklenemedi (uid=%s): %v", uid, err)
		return
	}
	if err := st.UpsertSavedView(ctx, chstore.SavedView{
		ID: promqlHistoryID(uid), OwnerID: uid, Name: promqlHistoryName, Page: promqlHistoryPage,
		QueryString: raw, CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		log.Printf("[promql] geçmiş yazılamadı (uid=%s): %v", uid, err)
	}
}

// promqlHistoryResponse — GET gövdesi. entries asla null (FE .map()'ler).
type promqlHistoryResponse struct {
	Entries    []promqlHistoryEntry `json:"entries"`
	Enabled    bool                 `json:"enabled"` // token çağıranında false
	MaxEntries int                  `json:"maxEntries"`
}

// getPromQLHistory — GET /api/promql/history. CH hatası ham yankılanmaz
// (writeErr varsayılan dalı host/port sızdırır): temiz 503 + log.
func (s *Server) getPromQLHistory(w http.ResponseWriter, r *http.Request) {
	uid, ok := promqlUser(w, r)
	if !ok {
		return
	}
	resp := promqlHistoryResponse{Entries: []promqlHistoryEntry{}, Enabled: !promqlHistoryDisabledFor(uid), MaxEntries: promqlHistoryMaxEntries}
	st := promqlHistoryStoreOf(s)
	if !resp.Enabled || st == nil {
		writeJSON(w, resp)
		return
	}
	entries, err := loadPromQLHistory(r.Context(), st, uid)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			w.WriteHeader(statusClientClosedRequest)
			return
		}
		log.Printf("[promql] geçmiş GET (uid=%s): %v", uid, err)
		writeJSONError(w, http.StatusServiceUnavailable, "query history is temporarily unavailable")
		return
	}
	if len(entries) > 0 {
		resp.Entries = entries
	}
	writeJSON(w, resp)
}

// deletePromQLHistory — DELETE /api/promql/history: tombstone. Token
// çağıranında no-op (geçmişi yok). İdempotent: satır yoksa da tombstone
// yazılır, sonuç aynı.
func (s *Server) deletePromQLHistory(w http.ResponseWriter, r *http.Request) {
	uid, ok := promqlUser(w, r)
	if !ok {
		return
	}
	if promqlHistoryDisabledFor(uid) {
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	st := promqlHistoryStoreOf(s)
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "query history store is not connected")
		return
	}
	release, ok := promqlHistoryAcquire(uid, promqlHistoryWriteTimeout)
	if !ok {
		log.Printf("[promql] geçmiş DELETE: kilit beklenirken süre doldu (uid=%s)", uid)
		writeJSONError(w, http.StatusServiceUnavailable, "query history is temporarily unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), promqlHistoryWriteTimeout)
	err := st.UpsertSavedView(ctx, chstore.SavedView{
		ID: promqlHistoryID(uid), OwnerID: uid, Name: "", Page: promqlHistoryPage,
		QueryString: "", CreatedAt: time.Now().UnixNano(),
	})
	cancel()
	release()
	if err != nil {
		log.Printf("[promql] geçmiş DELETE (uid=%s): %v", uid, err)
		writeJSONError(w, http.StatusServiceUnavailable, "query history is temporarily unavailable")
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
