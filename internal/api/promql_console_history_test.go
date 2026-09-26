package api

// v0.10.953 — PromQL konsolu sunucu tarafı geçmişi (spec C6, karar 8).
// Saf kural (promqlHistoryPush: en yeni önde, ardışık tekilleştirme, 50
// tavanı, ~128 KiB duvarı) tablolu; handler sözleşmeleri sahte Thanos +
// bellek içi saved_views ikiziyle: yalnız KOŞAN sorgular eklenir, kullanıcı
// yalıtımı, API token çağıranı hariç, okuma hatası geçmişi silmez, geçmiş
// uçları audit yazmaz. Canlı CH yok.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func histEntry(q, cluster, mode string) promqlHistoryEntry {
	return promqlHistoryEntry{Query: q, ClusterID: clusterIDOf(cluster), Cluster: cluster, Mode: mode, Status: "ok"}
}

func histQueries(es []promqlHistoryEntry) string {
	qs := make([]string, len(es))
	for i, e := range es {
		qs[i] = e.Query
	}
	return strings.Join(qs, ",")
}

func TestPromQLHistoryPush(t *testing.T) {
	a := histEntry("a", promqlClusterA, promqlModeInstant)
	b := histEntry("b", promqlClusterA, promqlModeInstant)
	aRange := histEntry("a", promqlClusterA, promqlModeRange)
	aOtherCluster := histEntry("a", promqlClusterB, promqlModeInstant)

	cases := []struct {
		name  string
		cur   []promqlHistoryEntry
		add   promqlHistoryEntry
		max   int
		bytes int
		want  string
	}{
		{"boş geçmiş", nil, a, 50, 1 << 20, "a"},
		{"en yeni önde", []promqlHistoryEntry{b}, a, 50, 1 << 20, "a,b"},
		{"ardışık özdeş tekilleşir", []promqlHistoryEntry{a, b}, a, 50, 1 << 20, "a,b"},
		{"ardışık OLMAYAN tekrar kalır", []promqlHistoryEntry{b, a}, a, 50, 1 << 20, "a,b,a"},
		{"farklı mode ayrı giriş", []promqlHistoryEntry{a}, aRange, 50, 1 << 20, "a,a"},
		{"farklı cluster ayrı giriş", []promqlHistoryEntry{a}, aOtherCluster, 50, 1 << 20, "a,a"},
		{"tavan: en eski düşer", []promqlHistoryEntry{b, histEntry("c", promqlClusterA, "instant"), histEntry("d", promqlClusterA, "instant")}, a, 3, 1 << 20, "a,b,c"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, raw, err := promqlHistoryPush(c.cur, c.add, c.max, c.bytes)
			if err != nil {
				t.Fatal(err)
			}
			if histQueries(got) != c.want {
				t.Fatalf("geçmiş %q, beklenen %q", histQueries(got), c.want)
			}
			if !strings.HasPrefix(raw, `{"v":1,"entries":[`) {
				t.Fatalf("raw gövde: %s", raw)
			}
		})
	}

	// Tekilleştirmede EN YENİ damga kalır.
	old := a
	old.At, old.Status = 1, "error"
	fresh := a
	fresh.At = 2
	got, _, _ := promqlHistoryPush([]promqlHistoryEntry{old}, fresh, 50, 1<<20)
	if len(got) != 1 || got[0].At != 2 || got[0].Status != "ok" {
		t.Fatalf("tekilleşen giriş en yeni koşuyu taşımalı: %+v", got)
	}

	// Girdi dilimi DEĞİŞMEZ.
	cur := []promqlHistoryEntry{a, b}
	_, _, _ = promqlHistoryPush(cur, histEntry("z", promqlClusterA, "instant"), 1, 1<<20)
	if histQueries(cur) != "a,b" {
		t.Fatalf("girdi değişti: %q", histQueries(cur))
	}

	// Bayt duvarı: 50 × 8 KiB ≈ 400 KiB > 128 KiB → en eskiler düşer, sonuç sığar.
	var full []promqlHistoryEntry
	for i := 0; i < promqlHistoryMaxEntries; i++ {
		full = append(full, histEntry(fmt.Sprintf("%03d-%s", i, strings.Repeat("x", maxPromQLQueryLen-4)), promqlClusterA, "instant"))
	}
	got, raw, err := promqlHistoryPush(full, histEntry("new", promqlClusterA, "instant"), promqlHistoryMaxEntries, promqlHistoryMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > promqlHistoryMaxBytes || len(got) >= promqlHistoryMaxEntries || got[0].Query != "new" {
		t.Fatalf("bayt duvarı: %d bayt, %d giriş, ilk %q", len(raw), len(got), got[0].Query)
	}
	if !strings.HasPrefix(got[1].Query, "000-") || !strings.HasPrefix(got[len(got)-1].Query, fmt.Sprintf("%03d-", len(got)-2)) {
		t.Fatalf("EN ESKİLER düşmeli (sıra korunur): ikinci %q, son %q", got[1].Query[:4], got[len(got)-1].Query[:4])
	}

	// Tek giriş bile sığmıyorsa hata (çağıran yazmaz).
	if _, _, err := promqlHistoryPush(nil, histEntry(strings.Repeat("y", 4096), promqlClusterA, "instant"), 50, 1024); !errors.Is(err, errPromQLHistoryEntryTooLarge) {
		t.Fatalf("sığmayan tek giriş: %v", err)
	}
}

func TestPromQLHistoryStoreOfNilStore(t *testing.T) {
	if st := promqlHistoryStoreOf(&Server{}); st != nil {
		t.Fatalf("store'suz Server nil depo vermeli (nil *chstore.Store arayüze sarılmamalı): %#v", st)
	}
	if !promqlHistoryDisabledFor("token:abc") || !promqlHistoryDisabledFor("") || promqlHistoryDisabledFor("u1") {
		t.Fatal("promqlHistoryDisabledFor: token ve boş kimlik kapalı, kullanıcı açık")
	}
	if promqlHistoryID("u1") != "promql-history:u1" {
		t.Fatalf("id: %s", promqlHistoryID("u1"))
	}
}

// Yalnız KOŞAN sorgular: ok ve upstream hatası girer; korkuluk reddi,
// 429, bozuk parametre ve bilinmeyen cluster girmez.
func TestPromQLHistoryAppendsExecutedRunsOnly(t *testing.T) {
	e := newPromQLTestEnv(t, func(w http.ResponseWriter, r *http.Request, req promqlFakeReq) {
		if strings.Contains(req.Form.Get("query"), "boom") {
			w.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprint(w, `{"status":"error","errorType":"execution","error":"boom"}`)
			return
		}
		promqlDefaultThanos(w, r, req)
	})
	cfg := defaultPromQLConsoleSettings()
	cfg.PerUserPerMin = 3
	withPromQLConsoleSettings(t, cfg)
	const week = int64(7 * 24 * 3600)
	run := func(path string, kv ...string) int {
		f := url.Values{"cluster": {promqlClusterA}}
		for i := 0; i+1 < len(kv); i += 2 {
			f.Set(kv[i], kv[i+1])
		}
		return e.do(t, "POST", path, f, "editor-1", auth.RoleEditor).Code
	}
	steps := []struct {
		name     string
		code     int
		path     string
		kv       []string
		wantHist string
	}{
		{"ok instant", 200, "/api/promql/query", []string{"query", "ok_q"}, "ok_q"},
		{"upstream hatası", 422, "/api/promql/query", []string{"query", "boom_q"}, "boom_q,ok_q"},
		{"ok range", 200, "/api/promql/query_range", []string{"query", "range_q", "start", unixStr(promqlT0 - 60), "end", unixStr(promqlT0)}, "range_q,boom_q,ok_q"},
		{"aralık korkuluğu", 400, "/api/promql/query_range", []string{"query", "wide_q", "start", unixStr(promqlT0 - week - 1), "end", unixStr(promqlT0)}, "range_q,boom_q,ok_q"},
		{"bozuk parametre", 400, "/api/promql/query", []string{"query", "bad_q", "time", "x"}, "range_q,boom_q,ok_q"},
		{"bilinmeyen cluster", 404, "/api/promql/query", []string{"query", "nocl_q", "cluster", "cluster-zzz"}, "range_q,boom_q,ok_q"},
		{"dakika bütçesi (3/dk) → 429", 429, "/api/promql/query", []string{"query", "limited_q"}, "range_q,boom_q,ok_q"},
	}
	for _, s := range steps {
		if code := run(s.path, s.kv...); code != s.code {
			t.Fatalf("%s: status %d, beklenen %d", s.name, code, s.code)
		}
		if got := histQueries(e.historyEntries(t, "editor-1")); got != s.wantHist {
			t.Fatalf("%s: geçmiş %q, beklenen %q", s.name, got, s.wantHist)
		}
	}
	h := e.historyEntries(t, "editor-1")
	if h[1].Status != "error" || h[1].ErrorType != "execution" || h[0].Mode != promqlModeRange || h[2].Status != "ok" ||
		h[2].ClusterID != clusterIDOf(promqlClusterA) || h[2].Cluster != promqlClusterA || h[2].Series != 2 || h[2].At == 0 {
		t.Fatalf("girişler: %+v", h)
	}
	e.hist.mu.Lock()
	row := e.hist.rows[promqlHistoryID("editor-1")]
	e.hist.mu.Unlock()
	if row.OwnerID != "editor-1" || row.Page != promqlHistoryPage || row.Name == "" {
		t.Fatalf("saved_views satırı: %+v", row)
	}
}

func TestPromQLHistoryDedupeAndGET(t *testing.T) {
	e := newPromQLTestEnv(t, nil)
	run := func(q, cluster string) {
		if w := e.do(t, "POST", "/api/promql/query", url.Values{"query": {q}, "cluster": {cluster}}, "editor-1", auth.RoleEditor); w.Code != 200 {
			t.Fatalf("%s: %d", q, w.Code)
		}
	}
	run("up", promqlClusterA)
	run("up", promqlClusterA) // ardışık özdeş → tek giriş
	run("up", promqlClusterB) // farklı cluster → ayrı giriş
	run("up", promqlClusterA)

	w := e.do(t, "GET", "/api/promql/history", nil, "editor-1", auth.RoleEditor)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %d", w.Code)
	}
	body := decodePromQLBody(t, w)
	entries := body["entries"].([]any)
	if len(entries) != 3 || body["enabled"] != true || body["maxEntries"] != float64(promqlHistoryMaxEntries) {
		t.Fatalf("GET gövdesi: %v", body)
	}
	first := entries[0].(map[string]any)
	if first["q"] != "up" || first["cluster"] != promqlClusterA || first["clusterId"] != clusterIDOf(promqlClusterA) ||
		first["mode"] != "instant" || first["status"] != "ok" {
		t.Fatalf("en yeni giriş: %v", first)
	}
	if entries[1].(map[string]any)["cluster"] != promqlClusterB {
		t.Fatalf("sıra en yeni önde olmalı: %v", entries)
	}
}

func TestPromQLHistoryCapViaHandler(t *testing.T) {
	e := newPromQLTestEnv(t, nil)
	cfg := defaultPromQLConsoleSettings()
	cfg.PerUserPerMin = 600
	withPromQLConsoleSettings(t, cfg)
	for i := 0; i < promqlHistoryMaxEntries+2; i++ {
		if w := e.do(t, "POST", "/api/promql/query", url.Values{"query": {fmt.Sprintf("q%02d", i)}, "cluster": {promqlClusterA}}, "editor-1", auth.RoleEditor); w.Code != 200 {
			t.Fatalf("koşu %d: %d", i, w.Code)
		}
	}
	h := e.historyEntries(t, "editor-1")
	if len(h) != promqlHistoryMaxEntries || h[0].Query != "q51" || h[len(h)-1].Query != "q02" {
		t.Fatalf("son 50 kalmalı, en yeni önde: %d giriş, ilk %q son %q", len(h), h[0].Query, h[len(h)-1].Query)
	}
}

func TestPromQLHistoryPerUserIsolation(t *testing.T) {
	e := newPromQLTestEnv(t, nil)
	for _, c := range []struct{ uid, q string }{{"editor-1", "mine"}, {"editor-2", "theirs"}} {
		if w := e.do(t, "POST", "/api/promql/query", url.Values{"query": {c.q}, "cluster": {promqlClusterA}}, c.uid, auth.RoleEditor); w.Code != 200 {
			t.Fatalf("%s: %d", c.uid, w.Code)
		}
	}
	for _, c := range []struct{ uid, want string }{{"editor-1", "mine"}, {"editor-2", "theirs"}} {
		body := decodePromQLBody(t, e.do(t, "GET", "/api/promql/history", nil, c.uid, auth.RoleEditor))
		entries := body["entries"].([]any)
		if len(entries) != 1 || entries[0].(map[string]any)["q"] != c.want {
			t.Fatalf("%s yalnız kendi geçmişini görmeli: %v", c.uid, entries)
		}
	}
	// Sahip uyuşmazlığı (kurcalanmış satır) → yok sayılır.
	e.hist.mu.Lock()
	row := e.hist.rows[promqlHistoryID("editor-2")]
	row.OwnerID = "someone-else"
	e.hist.rows[promqlHistoryID("editor-2")] = row
	e.hist.mu.Unlock()
	if entries := decodePromQLBody(t, e.do(t, "GET", "/api/promql/history", nil, "editor-2", auth.RoleEditor))["entries"].([]any); len(entries) != 0 {
		t.Fatalf("başka sahibin satırı okunmamalı: %v", entries)
	}
	// DELETE yalnız kendi satırını tombstone'lar.
	if w := e.do(t, "DELETE", "/api/promql/history", nil, "editor-1", auth.RoleEditor); w.Code != 200 {
		t.Fatalf("DELETE %d", w.Code)
	}
	e.hist.mu.Lock()
	mine, other := e.hist.rows[promqlHistoryID("editor-1")], e.hist.rows[promqlHistoryID("editor-2")]
	e.hist.mu.Unlock()
	if mine.Name != "" || mine.QueryString != "" || mine.OwnerID != "editor-1" || mine.Page != promqlHistoryPage {
		t.Fatalf("DELETE tombstone yazmalı: %+v", mine)
	}
	if other.Name == "" {
		t.Fatal("DELETE başka kullanıcının satırına dokundu")
	}
	if entries := decodePromQLBody(t, e.do(t, "GET", "/api/promql/history", nil, "editor-1", auth.RoleEditor))["entries"].([]any); len(entries) != 0 {
		t.Fatalf("silinen geçmiş boş dönmeli: %v", entries)
	}
	// Silme sonrası yeni koşu temiz başlar.
	e.do(t, "POST", "/api/promql/query", url.Values{"query": {"again"}, "cluster": {promqlClusterA}}, "editor-1", auth.RoleEditor)
	if got := histQueries(e.historyEntries(t, "editor-1")); got != "again" {
		t.Fatalf("silme sonrası geçmiş %q", got)
	}
	// Geçmiş uçları audit YAZMAZ: yalnız 3 sorgu satırı.
	for _, a := range e.audits() {
		if a.Action != "promql.query" {
			t.Fatalf("geçmiş ucu audit yazdı: %+v", a)
		}
	}
}

func TestPromQLHistoryTokenCallersExcluded(t *testing.T) {
	e := newPromQLTestEnv(t, nil)
	const tok = "token:t-1"
	w := e.do(t, "POST", "/api/promql/query", url.Values{"query": {"up"}, "cluster": {promqlClusterA}}, tok, auth.RoleEditor)
	if w.Code != http.StatusOK {
		t.Fatalf("token çağıranı sorgu koşabilmeli (rol editor): %d", w.Code)
	}
	if n := e.hist.upsertCount(); n != 0 {
		t.Fatalf("token çağıranı geçmiş yazmamalı: %d yazım", n)
	}
	if _, d := e.oneAudit(t); d["status"] != "ok" {
		t.Fatalf("token çağıranı da audit'lenir: %v", d)
	}
	body := decodePromQLBody(t, e.do(t, "GET", "/api/promql/history", nil, tok, auth.RoleEditor))
	if body["enabled"] != false || len(body["entries"].([]any)) != 0 {
		t.Fatalf("token GET: %v", body)
	}
	if w := e.do(t, "DELETE", "/api/promql/history", nil, tok, auth.RoleEditor); w.Code != http.StatusOK || e.hist.upsertCount() != 0 {
		t.Fatalf("token DELETE no-op olmalı: %d, %d yazım", w.Code, e.hist.upsertCount())
	}
}

// Okuma hatası geçmişi SİLMEZ (boş listeyle üzerine yazılmaz); GET ham CH
// hatasını yankılamaz.
func TestPromQLHistoryReadErrorNeverOverwrites(t *testing.T) {
	e := newPromQLTestEnv(t, nil)
	e.do(t, "POST", "/api/promql/query", url.Values{"query": {"keep"}, "cluster": {promqlClusterA}}, "editor-1", auth.RoleEditor)
	e.hist.mu.Lock()
	e.hist.getErr = errors.New("clickhouse [chnode.example.invalid:9000]: code: 159, timeout")
	e.hist.mu.Unlock()
	writes := e.hist.upsertCount()

	if w := e.do(t, "POST", "/api/promql/query", url.Values{"query": {"lost"}, "cluster": {promqlClusterA}}, "editor-1", auth.RoleEditor); w.Code != http.StatusOK {
		t.Fatalf("geçmiş hatası sorguyu bozmamalı: %d", w.Code)
	}
	if e.hist.upsertCount() != writes {
		t.Fatal("okuma hatasında geçmiş üzerine yazıldı")
	}
	w := e.do(t, "GET", "/api/promql/history", nil, "editor-1", auth.RoleEditor)
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "chnode") || strings.Contains(w.Body.String(), "code: 159") {
		t.Fatalf("GET 503 + temiz mesaj: %d %s", w.Code, w.Body.String())
	}
	e.hist.mu.Lock()
	e.hist.getErr = nil
	e.hist.mu.Unlock()
	if got := histQueries(e.historyEntries(t, "editor-1")); got != "keep" {
		t.Fatalf("geçmiş korunmalı: %q", got)
	}
}

func TestPromQLHistoryCorruptBlobStartsClean(t *testing.T) {
	e := newPromQLTestEnv(t, nil)
	e.hist.mu.Lock()
	e.hist.rows[promqlHistoryID("editor-1")] = chstore.SavedView{ID: promqlHistoryID("editor-1"), OwnerID: "editor-1",
		Name: promqlHistoryName, Page: promqlHistoryPage, QueryString: "{bozuk"}
	e.hist.mu.Unlock()
	body := decodePromQLBody(t, e.do(t, "GET", "/api/promql/history", nil, "editor-1", auth.RoleEditor))
	if entries := body["entries"].([]any); len(entries) != 0 {
		t.Fatalf("bozuk gövde boş geçmiş olarak okunmalı: %v", body)
	}
	e.do(t, "POST", "/api/promql/query", url.Values{"query": {"fresh"}, "cluster": {promqlClusterA}}, "editor-1", auth.RoleEditor)
	if got := histQueries(e.historyEntries(t, "editor-1")); got != "fresh" {
		t.Fatalf("sonraki ekleme temiz yazmalı: %q", got)
	}
}

// ── Kilit beklemesi G/Ç bütçesini yemez (v0.10.953) ────────────────────────

// promqlSlowHistoryStore — GetSavedView bağlama saygılı gecikir (CH turu
// ikizi); ilk girişte entered'a haber verir.
type promqlSlowHistoryStore struct {
	*promqlFakeHistoryStore
	delay   time.Duration
	entered chan struct{}
}

func (f *promqlSlowHistoryStore) GetSavedView(ctx context.Context, id string) (*chstore.SavedView, error) {
	select {
	case f.entered <- struct{}{}:
	default:
	}
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return f.promqlFakeHistoryStore.GetSavedView(ctx, id)
}

func (f *promqlSlowHistoryStore) UpsertSavedView(ctx context.Context, v chstore.SavedView) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.promqlFakeHistoryStore.UpsertSavedView(ctx, v)
}

func withPromQLHistoryTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	prev := promqlHistoryWriteTimeout
	promqlHistoryWriteTimeout = d
	t.Cleanup(func() { promqlHistoryWriteTimeout = prev })
}

// Eskiden 3 s son tarih kilitten ÖNCE kuruluyordu: A'nın CH turunu (0.6×T)
// bekleyen B'nin kendi okumasına 0.4×T kalıyor, okuma düşüyor ve giriş
// SESSİZCE kayboluyordu. Şimdi G/Ç son tarihi kilit alındıktan sonra başlar.
func TestPromQLHistoryLockWaitDoesNotConsumeIOBudget(t *testing.T) {
	const T = 500 * time.Millisecond
	withPromQLHistoryTimeout(t, T)
	st := &promqlSlowHistoryStore{promqlFakeHistoryStore: newPromQLFakeHistoryStore(), delay: T * 6 / 10, entered: make(chan struct{}, 1)}
	prevStore := promqlHistoryStoreOf
	promqlHistoryStoreOf = func(*Server) promqlHistoryStore { return st }
	t.Cleanup(func() { promqlHistoryStoreOf = prevStore })

	s := &Server{}
	const uid = "editor-lockwait" // aynı uid → aynı şerit
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.appendPromQLHistory(context.Background(), uid, histEntry("a", promqlClusterA, promqlModeInstant))
	}()
	<-st.entered // A kilidi tutuyor, CH okumasında
	s.appendPromQLHistory(context.Background(), uid, histEntry("b", promqlClusterA, promqlModeInstant))
	wg.Wait()

	if n := st.upsertCount(); n != 2 {
		t.Fatalf("iki ekleme de yazılmalı, %d upsert (kilit beklemesi G/Ç bütçesini yedi)", n)
	}
	st.mu.Lock()
	v := st.rows[promqlHistoryID(uid)]
	st.mu.Unlock()
	if !strings.Contains(v.QueryString, `"q":"b"`) || !strings.Contains(v.QueryString, `"q":"a"`) {
		t.Fatalf("son blob iki girişi de taşımalı: %s", v.QueryString)
	}
}

// Bekleme SINIRLI: şerit gerçekten tıkalıysa ekleme atlanır (sonsuz kuyruk
// yok) ve DELETE temiz 503 döner; şerit boşalınca ikisi de çalışır.
func TestPromQLHistoryAcquireBoundedWait(t *testing.T) {
	withPromQLHistoryTimeout(t, 40*time.Millisecond)
	e := newPromQLTestEnv(t, nil)
	const uid = "editor-stuck"

	hold, ok := promqlHistoryAcquire(uid, time.Second)
	if !ok {
		t.Fatal("boş şerit alınamadı")
	}
	released := false
	t.Cleanup(func() {
		if !released {
			hold()
		}
	})
	began := time.Now()
	if _, ok := promqlHistoryAcquire(uid, 20*time.Millisecond); ok {
		t.Fatal("tutulan şerit ikinci kez alındı")
	}
	if d := time.Since(began); d > time.Second {
		t.Fatalf("bekleme sınırlı olmalı: %v", d)
	}
	e.s.appendPromQLHistory(context.Background(), uid, histEntry("x", promqlClusterA, promqlModeInstant))
	if n := e.hist.upsertCount(); n != 0 {
		t.Fatalf("tıkalı şeritte ekleme atlanmalı: %d upsert", n)
	}
	w := e.do(t, "DELETE", "/api/promql/history", nil, uid, auth.RoleEditor)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "temporarily unavailable") {
		t.Fatalf("tıkalı şeritte DELETE → 503, %d %s", w.Code, w.Body.String())
	}
	hold()
	released = true
	e.s.appendPromQLHistory(context.Background(), uid, histEntry("x", promqlClusterA, promqlModeInstant))
	if n := e.hist.upsertCount(); n != 1 {
		t.Fatalf("şerit boşalınca ekleme yazılmalı: %d", n)
	}
	if w := e.do(t, "DELETE", "/api/promql/history", nil, uid, auth.RoleEditor); w.Code != http.StatusOK {
		t.Fatalf("şerit boşalınca DELETE → 200, %d", w.Code)
	}
}
