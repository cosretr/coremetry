package logstore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/sourcestate"
)

// v0.10.944 — ES adaptörünün 401/403'ü TİPLİ taşıması (CoSRE kaynak
// durumu). Öncesi: parseESError düz metin döndürüyordu; tool katmanı
// yetki reddini "sınıflandırılamayan hata"dan ayıramıyordu ve model
// "log yok" ile "log kaynağına yetkimiz yok"u aynı boş listede okuyordu.
// httptest ES taklidi: kök (Info) 200 + X-Elastic-Product başlığı
// (go-elasticsearch v8 ürün denetimi), diğer uçlar teste göre.

type esStubServer struct {
	mu              sync.Mutex
	searchStatus    int
	searchBody      string
	searchDelay     time.Duration
	mappingStatus   int
	fieldCapsStatus int
	fieldCapsBody   string
	fieldCapsHits   atomic.Int32
	lastSearchBody  []byte // v0.10.944 — gönderilen _search gövdesi (yumuşak timeout pini)
}

func (st *esStubServer) set(fn func(*esStubServer)) {
	st.mu.Lock()
	defer st.mu.Unlock()
	fn(st)
}

func (st *esStubServer) handler(w http.ResponseWriter, r *http.Request) {
	st.mu.Lock()
	searchStatus, searchBody, delay := st.searchStatus, st.searchBody, st.searchDelay
	mappingStatus, fcStatus, fcBody := st.mappingStatus, st.fieldCapsStatus, st.fieldCapsBody
	st.mu.Unlock()
	w.Header().Set("X-Elastic-Product", "Elasticsearch")
	w.Header().Set("Content-Type", "application/json")
	write := func(code int, body string) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}
	p := r.URL.Path
	switch {
	case p == "/":
		write(200, `{"name":"n1","cluster_name":"logs-a","version":{"number":"8.13.0"},"tagline":"You Know, for Search"}`)
	case strings.HasPrefix(p, "/_cat/indices"):
		write(403, `{"error":{"type":"security_exception","reason":"action [indices:monitor] is unauthorized"},"status":403}`)
	case strings.HasSuffix(p, "/_search"):
		body, _ := io.ReadAll(r.Body)
		st.mu.Lock()
		st.lastSearchBody = body
		st.mu.Unlock()
		if delay > 0 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(delay):
			}
		}
		write(searchStatus, searchBody)
	case strings.HasSuffix(p, "/_mapping"):
		write(mappingStatus, `{"error":{"type":"security_exception","reason":"unauthorized"},"status":`+strconv.Itoa(mappingStatus)+`}`)
	case strings.HasSuffix(p, "/_field_caps"):
		st.fieldCapsHits.Add(1)
		write(fcStatus, fcBody)
	default:
		write(404, `{"error":{"type":"not_found","reason":"stub"},"status":404}`)
	}
}

func newStubES(t *testing.T, fields ESFieldMap) (*ESStore, *esStubServer, *httptest.Server) {
	t.Helper()
	st := &esStubServer{searchStatus: 200, searchBody: `{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
		mappingStatus: 200, fieldCapsStatus: 200, fieldCapsBody: `{"indices":[],"fields":{}}`}
	srv := httptest.NewServer(http.HandlerFunc(st.handler))
	t.Cleanup(srv.Close)
	es, err := NewES(ESConfig{Addresses: []string{srv.URL}, Fields: fields})
	if err != nil {
		t.Fatalf("NewES: %v", err)
	}
	return es, st, srv
}

func TestESSearchUnauthorizedIsTyped(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"401 security_exception", 401, `{"error":{"type":"security_exception","reason":"unable to authenticate with provided credentials"},"status":401}`},
		{"403 read denied", 403, `{"error":{"type":"security_exception","reason":"action [indices:data/read/search] is unauthorized"},"status":403}`},
		{"401 proxy body (JSON değil)", 401, `<html>Authorization Required</html>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			es, st, _ := newStubES(t, ESFieldMap{})
			st.set(func(s *esStubServer) { s.searchStatus, s.searchBody = c.status, c.body })
			now := time.Now()
			_, err := SearchWithTimeout(context.Background(), es,
				Filter{Service: "checkout", From: now.Add(-time.Hour), To: now}, 5*time.Second)
			if err == nil {
				t.Fatal("yetki reddi hata olmalı")
			}
			if !errors.Is(err, sourcestate.ErrUnauthorized) {
				t.Fatalf("ErrUnauthorized ile sarılmalı: %v", err)
			}
			if errors.Is(err, ErrBackendSlow) {
				t.Fatalf("yetki reddi 'yavaş/erişilemez' sayılmamalı: %v", err)
			}
			if got := sourcestate.Classify(err); got != sourcestate.Unauthorized {
				t.Fatalf("Classify = %q", got)
			}
		})
	}
}

// 400 (sorgu hatası) yetki reddi DEĞİL — tipli sarmalama yalnız 401/403.
// v0.10.944 — gerçek ES zarfı iç içe: üst seviye yalnız "all shards failed
// (search_phase_execution_exception)"; asıl gerekçe root_cause'ta ve
// eskiden düşüyordu. Sözdizimi reddi ErrBadQuery ile TİPLİ (tool katmanı
// bunu kaynak arızası değil argüman hatası sayar).
func TestESSearchBadRequestNotUnauthorized(t *testing.T) {
	es, st, _ := newStubES(t, ESFieldMap{})
	st.set(func(s *esStubServer) {
		s.searchStatus = 400
		s.searchBody = `{"error":{"root_cause":[{"type":"query_shard_exception","reason":"Failed to parse query [level:(error OR]","index":"app-2026.09.26"}],` +
			`"type":"search_phase_execution_exception","reason":"all shards failed","phase":"query","grouped":true},"status":400}`
	})
	_, err := es.Search(context.Background(), Filter{Search: "level:(error OR"})
	if err == nil || errors.Is(err, sourcestate.ErrUnauthorized) {
		t.Fatalf("400 yetki reddi olarak sarılmamalı: %v", err)
	}
	if got := sourcestate.Classify(err); got == sourcestate.Unauthorized {
		t.Fatalf("Classify = %q", got)
	}
	if !errors.Is(err, ErrBadQuery) {
		t.Fatalf("sözdizimi reddi ErrBadQuery ile sarılmalı: %v", err)
	}
	if !strings.Contains(err.Error(), "Failed to parse query") || !strings.Contains(err.Error(), "search_phase_execution_exception") {
		t.Fatalf("root_cause gerekçesi korunmalı: %v", err)
	}
}

// Sözdizimi DIŞI 400 (ör. illegal_argument — bizim gövdemiz) ErrBadQuery
// DEĞİL: model kendi query'sini suçlamasın.
func TestESSearchNonSyntax400NotBadQuery(t *testing.T) {
	es, st, _ := newStubES(t, ESFieldMap{})
	st.set(func(s *esStubServer) {
		s.searchStatus = 400
		s.searchBody = `{"error":{"root_cause":[{"type":"illegal_argument_exception","reason":"Result window is too large"}],` +
			`"type":"search_phase_execution_exception","reason":"all shards failed"},"status":400}`
	})
	_, err := es.Search(context.Background(), Filter{Search: "level:error"})
	if err == nil || errors.Is(err, ErrBadQuery) {
		t.Fatalf("sözdizimi dışı 400 ErrBadQuery olmamalı: %v", err)
	}
	if !strings.Contains(err.Error(), "Result window is too large") {
		t.Fatalf("root_cause gerekçesi korunmalı: %v", err)
	}
}

// v0.10.944 — yumuşak ES timeout'u çağıranın bütçesine sığar (yalnız
// kısaltır; operatörün daha düşük env değeri korunur).
func TestESSoftTimeout(t *testing.T) {
	cases := []struct {
		name string
		env  string
		hint time.Duration
		want string
	}{
		{"ipucu yok → varsayılan", "", 0, "10s"},
		{"negatif ipucu → varsayılan", "", -2 * time.Second, "10s"},
		{"1 ms altı ipucu → varsayılan", "", 500 * time.Microsecond, "10s"},
		{"search_logs bütçesi", "", 6 * time.Second, "6000ms"},
		{"varsayılandan uzun ipucu yükseltmez", "", 12 * time.Second, "10s"},
		{"operatörün düşük env'i korunur", "5s", 6 * time.Second, "5s"},
		{"env'den kısa ipucu kazanır", "20s", 6 * time.Second, "6000ms"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("COREMETRY_LOGS_PATTERNS_ES_TIMEOUT", c.env)
			if got := esSoftTimeout(c.hint); got != c.want {
				t.Fatalf("esSoftTimeout(%v) env=%q = %q want %q", c.hint, c.env, got, c.want)
			}
		})
	}
}

// Gövde pini: Filter.SoftTimeout Search gövdesinin `timeout`una ulaşır;
// sıfır değer bugünkü 10s.
func TestESSearchBodySoftTimeout(t *testing.T) {
	es, st, _ := newStubES(t, ESFieldMap{})
	now := time.Now()
	for _, c := range []struct {
		hint time.Duration
		want string
	}{{0, "10s"}, {6 * time.Second, "6000ms"}} {
		if _, err := es.Search(context.Background(), Filter{From: now.Add(-time.Hour), To: now, SoftTimeout: c.hint}); err != nil {
			t.Fatal(err)
		}
		st.mu.Lock()
		raw := st.lastSearchBody
		st.mu.Unlock()
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("gövde: %v (%s)", err, raw)
		}
		if body["timeout"] != c.want {
			t.Fatalf("SoftTimeout %v → timeout %v want %s", c.hint, body["timeout"], c.want)
		}
	}
}

func TestESListFieldsUnauthorizedIsTyped(t *testing.T) {
	es, st, _ := newStubES(t, ESFieldMap{})
	st.set(func(s *esStubServer) { s.mappingStatus = 403 })
	_, err := es.ListFieldsBounded(context.Background())
	if !errors.Is(err, sourcestate.ErrUnauthorized) {
		t.Fatalf("mapping 403 tipli olmalı: %v", err)
	}
}

func TestCatIndicesErrorIsTypedUnauthorized(t *testing.T) {
	if err := catIndicesError(403, nil, "app-*"); !errors.Is(err, sourcestate.ErrUnauthorized) {
		t.Fatalf("_cat/indices 403 tipli olmalı: %v", err)
	}
}

// Rol keşfi uçtan uca: tek field_caps, karar önbellekte (ikinci çağrı ağa
// çıkmaz), yapılandırılmış rol keşfe bakmaz.
func TestESFieldMappingProbe(t *testing.T) {
	es, st, _ := newStubES(t, ESFieldMap{Namespace: "labels.ns"})
	st.set(func(s *esStubServer) {
		s.fieldCapsBody = `{"indices":["app-2026.09.26"],"fields":{
			"service.name":{"keyword":{"type":"keyword","searchable":true,"aggregatable":true}},
			"trace.id":{"keyword":{"type":"keyword","searchable":true,"aggregatable":true}},
			"kubernetes.pod_name":{"keyword":{"type":"keyword","searchable":true,"aggregatable":true}},
			"@timestamp":{"date":{"type":"date","searchable":true,"aggregatable":true}}}}`
	})
	m := es.FieldMapping(context.Background(), true)
	want := map[string]string{
		RolePod: "kubernetes.pod_name (discovered)", RoleCluster: "none",
		RoleNamespace: "labels.ns (configured)", RoleTraceID: "trace.id (discovered)",
	}
	labels := m.Labels()
	for role, w := range want {
		if labels[role] != w {
			t.Errorf("%s = %q want %q", role, labels[role], w)
		}
	}
	_ = es.FieldMapping(context.Background(), true)
	_ = es.FieldMapping(context.Background(), false)
	if n := st.fieldCapsHits.Load(); n != 1 {
		t.Fatalf("karar önbelleklenmeli: field_caps %d kez çağrıldı", n)
	}
}

// Prob yetki reddi aldıysa yokluk iddia edilmez: unverified.
func TestESFieldMappingProbeFailureIsUnverified(t *testing.T) {
	es, st, _ := newStubES(t, ESFieldMap{Cluster: "labels.cluster"})
	st.set(func(s *esStubServer) {
		s.fieldCapsStatus = 401
		s.fieldCapsBody = `{"error":{"type":"security_exception","reason":"unauthorized"},"status":401}`
	})
	m := es.FieldMapping(context.Background(), true)
	if m.Role(RolePod).Source != FieldUnverified || m.Role(RoleTraceID).Source != FieldUnverified {
		t.Fatalf("prob düştüyse unverified: %v", m.Labels())
	}
	if m.Role(RoleCluster).Source != FieldConfigured {
		t.Fatalf("yapılandırılmış rol prob hatasından etkilenmez: %v", m.Labels())
	}
	// probe=false taze önbellek yokken ağa çıkmaz.
	es2, st2, _ := newStubES(t, ESFieldMap{})
	_ = es2.FieldMapping(context.Background(), false)
	if st2.fieldCapsHits.Load() != 0 {
		t.Fatal("probe=false field_caps çağırmamalı")
	}
}
