package thanos

// worker_query_test.go — v0.10.955 — Rollouts v2 P1.1 + P1.2: işçi
// okuyucusu (WorkerQuery + WorkerLimits) ve NamespaceMatcher sözleşmeleri,
// SAHTE Thanos'a karşı (console_test.go newFakeConsoleThanos; canlı çağrı
// YOK). Kaynak: docs/rollouts/v2-audit.md §3.4, §4.10, §5.4, §12.1 P1.1;
// operatör onaylı kararlar 1–2 (2026-09-26).
//
// Kapsam: 49 999 / 50 000 / 50 001 seri (truncated + toplam), gövde tavanı
// → response_too_large (64 MiB tavanı dahil), warnings → Partial,
// çözülmeyen TokenRef → tipli hata + SIFIR istek, kapalı/bilinmeyen cluster
// → tipli hata + sıfır istek, dedup/partial_response/timeout telde, limit
// normalizasyonu, zaman aşımı, iptal; ConsoleLimits tavanları DEĞİŞMEDİ.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func boolPtr(b bool) *bool { return &b }

// workerTestService — verilen kayıtlarla yapılandırılmış servis; ortam ve
// dosya okuması enjekte (tokenref_test.go newRefTestService).
func workerTestService(env map[string]string, cs ...ClusterConfig) *Service {
	s := newRefTestService(env, nil)
	s.Configure(Settings{Clusters: cs})
	return s
}

// ── WorkerLimits normalizasyonu (karar 2) ───────────────────────────────

func TestWorkerLimitsNormalized(t *testing.T) {
	cases := []struct {
		name string
		in   WorkerLimits
		want WorkerLimits // Dedup nil = true beklenir
	}{
		{"sıfır → varsayılanlar", WorkerLimits{},
			WorkerLimits{MaxSeries: 50000, MaxBodyMiB: 64, TimeoutS: 30, Dedup: boolPtr(true)}},
		{"negatif → varsayılanlar", WorkerLimits{MaxSeries: -1, MaxBodyMiB: -1, TimeoutS: -1},
			WorkerLimits{MaxSeries: 50000, MaxBodyMiB: 64, TimeoutS: 30, Dedup: boolPtr(true)}},
		{"tavanın bir üstü → tavan", WorkerLimits{MaxSeries: 50001, MaxBodyMiB: 65, TimeoutS: 46},
			WorkerLimits{MaxSeries: 50000, MaxBodyMiB: 64, TimeoutS: 45, Dedup: boolPtr(true)}},
		{"çok büyük → tavan", WorkerLimits{MaxSeries: 1 << 30, MaxBodyMiB: 1 << 30, TimeoutS: 1 << 30},
			WorkerLimits{MaxSeries: 50000, MaxBodyMiB: 64, TimeoutS: 45, Dedup: boolPtr(true)}},
		{"tam tavan aynen", WorkerLimits{MaxSeries: 50000, MaxBodyMiB: 64, TimeoutS: 45},
			WorkerLimits{MaxSeries: 50000, MaxBodyMiB: 64, TimeoutS: 45, Dedup: boolPtr(true)}},
		{"aralık içi aynen + açık dedup=false + partial", WorkerLimits{MaxSeries: 1, MaxBodyMiB: 1, TimeoutS: 1, Dedup: boolPtr(false), PartialResponse: true},
			WorkerLimits{MaxSeries: 1, MaxBodyMiB: 1, TimeoutS: 1, Dedup: boolPtr(false), PartialResponse: true}},
		{"49 999 aynen", WorkerLimits{MaxSeries: 49999, MaxBodyMiB: 63, TimeoutS: 44, Dedup: boolPtr(true)},
			WorkerLimits{MaxSeries: 49999, MaxBodyMiB: 63, TimeoutS: 44, Dedup: boolPtr(true)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.in.normalized()
			if got.MaxSeries != c.want.MaxSeries || got.MaxBodyMiB != c.want.MaxBodyMiB || got.TimeoutS != c.want.TimeoutS ||
				got.PartialResponse != c.want.PartialResponse || got.Dedup == nil || *got.Dedup != *c.want.Dedup {
				t.Fatalf("%+v → %+v (dedup=%v), beklenen %+v (dedup=%v)", c.in, got, derefBool(got.Dedup), c.want, *c.want.Dedup)
			}
			// Konsol sınırlarına çeviri ConsoleLimits.normalized()'dan GEÇMEZ:
			// 50 000 seri 2000'e inmez.
			cl := got.consoleLimits()
			want := ConsoleLimits{Timeout: time.Duration(c.want.TimeoutS) * time.Second, MaxSeries: c.want.MaxSeries,
				MaxBodyBytes: int64(c.want.MaxBodyMiB) << 20, PartialResponse: c.want.PartialResponse}
			if cl != want {
				t.Fatalf("consoleLimits %+v, beklenen %+v", cl, want)
			}
		})
	}
	// Girdi değişmez (değer alıcı; nil Dedup girdide nil kalır).
	in := WorkerLimits{}
	_ = in.normalized()
	if in.Dedup != nil || in.MaxSeries != 0 {
		t.Fatalf("normalized girdiyi değiştirdi: %+v", in)
	}
}

func derefBool(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

// Karar 2: konsolun tavanları (2000 seri / 128 MiB / 120 s) işçi girişiyle
// DEĞİŞMEDİ.
func TestWorkerLeavesConsoleCeilingsAlone(t *testing.T) {
	got := ConsoleLimits{MaxSeries: 50000, MaxBodyBytes: 64 << 20, Timeout: time.Hour}.normalized()
	if got.MaxSeries != 2000 || got.MaxBodyBytes != 64<<20 || got.Timeout != 120*time.Second {
		t.Fatalf("konsol tavanları değişti: %+v", got)
	}
	got = ConsoleLimits{MaxBodyBytes: 1 << 40}.normalized()
	if got.MaxSeries != 500 || got.MaxBodyBytes != 128<<20 || got.Timeout != 30*time.Second {
		t.Fatalf("konsol varsayılan/tavanları değişti: %+v", got)
	}
}

// ── seri tavanı: 49 999 / 50 000 / 50 001 ───────────────────────────────

func TestWorkerQuerySeriesCapEdges(t *testing.T) {
	cases := []struct {
		n   int
		lim WorkerLimits
		cap int
	}{
		{49999, WorkerLimits{}, 50000},
		{50000, WorkerLimits{}, 50000},
		{50001, WorkerLimits{}, 50000},
		{50001, WorkerLimits{MaxSeries: 1 << 20}, 50000}, // tavan kırpılır
		{101, WorkerLimits{MaxSeries: 100}, 100},
		{0, WorkerLimits{}, 50000},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%d/max%d", c.n, c.lim.MaxSeries), func(t *testing.T) {
			f := newFakeConsoleThanos(t, false, respondWith(200, seriesBody("vector", c.n)))
			cl := consoleTestCluster(f.URL, "")
			res, err := workerTestService(nil, cl).WorkerQuery(context.Background(), cl.ID, "up", c.lim)
			if err != nil {
				t.Fatalf("WorkerQuery: %v", err)
			}
			kept := min(c.n, c.cap)
			if res.Series != kept || res.TotalSeries != c.n || res.Truncated != (c.n > c.cap) {
				t.Fatalf("series=%d total=%d truncated=%v; beklenen %d/%d/%v", res.Series, res.TotalSeries, res.Truncated, kept, c.n, c.n > c.cap)
			}
			if res.ResultType != "vector" || res.Partial() {
				t.Fatalf("tür %q / partial %v", res.ResultType, res.Partial())
			}
			var elems []struct {
				Metric map[string]string `json:"metric"`
			}
			if err := json.Unmarshal(res.Result, &elems); err != nil {
				t.Fatalf("result geçerli JSON dizisi değil: %v", err)
			}
			if len(elems) != kept {
				t.Fatalf("result %d öğe, beklenen %d", len(elems), kept)
			}
			if kept > 0 && elems[kept-1].Metric["pod"] != "p-"+strconv.Itoa(kept-1) {
				t.Fatalf("İLK %d seri tutulmalı; son tutulan %q", kept, elems[kept-1].Metric["pod"])
			}
		})
	}
}

// ── gövde tavanı: response_too_large ────────────────────────────────────

// respondDeclared — Content-Length başlığını beyan edilen değerle yazar,
// gövdeyi aynen. Beyan gövdeden büyükse bağlantı gövdeden sonra kapanır;
// istemci tavanı BAŞLIKTAN karar verir (64 MiB'ı gerçekten taşımadan sınır
// testi).
func respondDeclared(declared int64, body string) func(http.ResponseWriter, *http.Request, consoleReq) {
	return func(w http.ResponseWriter, _ *http.Request, _ consoleReq) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.FormatInt(declared, 10))
		fmt.Fprint(w, body)
	}
}

func TestWorkerQueryBodyCap(t *testing.T) {
	big := seriesBody("vector", 20000) // ~1.3 MiB > 1 MiB
	small := seriesBody("vector", 1000)
	chunked := func(body string) func(http.ResponseWriter, *http.Request, consoleReq) {
		return func(w http.ResponseWriter, _ *http.Request, _ consoleReq) {
			for i := 0; i < len(body); i += 64 << 10 {
				fmt.Fprint(w, body[i:min(i+64<<10, len(body))])
				w.(http.Flusher).Flush()
			}
		}
	}
	empty := seriesBody("vector", 0)
	cases := []struct {
		name    string
		lim     WorkerLimits
		respond func(http.ResponseWriter, *http.Request, consoleReq)
		wantErr string // tavan metni ("1 MiB"); boş = başarı
	}{
		{"1 MiB: Content-Length aşıyor", WorkerLimits{MaxBodyMiB: 1}, respondDeclared(int64(len(big)), big), "1 MiB"},
		{"1 MiB: chunked akış aşıyor", WorkerLimits{MaxBodyMiB: 1}, chunked(big), "1 MiB"},
		{"1 MiB: altı geçer", WorkerLimits{MaxBodyMiB: 1}, chunked(small), ""},
		{"varsayılan 64 MiB: tam tavan beyanı geçer", WorkerLimits{}, respondDeclared(64<<20, empty), ""},
		{"varsayılan 64 MiB: tavan+1 beyanı reddedilir", WorkerLimits{}, respondDeclared(64<<20+1, empty), "64 MiB"},
		{"tavan üstü ayar 64 MiB'a kırpılır", WorkerLimits{MaxBodyMiB: 1 << 20}, respondDeclared(64<<20+1, empty), "64 MiB"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeConsoleThanos(t, false, c.respond)
			cl := consoleTestCluster(f.URL, "")
			res, err := workerTestService(nil, cl).WorkerQuery(context.Background(), cl.ID, "up", c.lim)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("tavan içinde başarı beklenirdi: %v", err)
				}
				if res == nil || res.ResultType != "vector" {
					t.Fatalf("sonuç: %+v", res)
				}
				return
			}
			ce := asConsoleError(t, err)
			if ce.Type != ConsoleErrResponseTooLarge {
				t.Fatalf("response_too_large beklenirdi: %+v", ce)
			}
			if !strings.Contains(ce.Message, c.wantErr) || !strings.Contains(ce.Message, "worker") || strings.Contains(ce.Message, "console") {
				t.Fatalf("mesaj işçi tavanını (%s) söylemeli, konsolu değil: %q", c.wantErr, ce.Message)
			}
		})
	}
}

// ── warnings → Partial ──────────────────────────────────────────────────

func TestWorkerQueryWarningsMarkPartial(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantPartial bool
		wantSeries  int
	}{
		{"warning → partial (sonuç yine döner)",
			`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"pod":"p-0"},"value":[1,"1"]}]},"warnings":["partial response: store s1 unavailable"]}`,
			true, 1},
		{"yalnız infos → partial değil",
			`{"status":"success","data":{"resultType":"vector","result":[]},"infos":["PromQL info: metric might not be a counter"]}`,
			false, 0},
		{"notsuz → partial değil", seriesBody("vector", 2), false, 2},
		{"boş warnings dizisi → partial değil",
			`{"status":"success","data":{"resultType":"vector","result":[]},"warnings":[]}`, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeConsoleThanos(t, false, respondWith(200, c.body))
			cl := consoleTestCluster(f.URL, "")
			res, err := workerTestService(nil, cl).WorkerQuery(context.Background(), cl.ID, "up", WorkerLimits{})
			if err != nil {
				t.Fatal(err)
			}
			if res.Partial() != c.wantPartial || res.Series != c.wantSeries {
				t.Fatalf("partial=%v series=%d, beklenen %v/%d (%+v)", res.Partial(), res.Series, c.wantPartial, c.wantSeries, res.ConsoleNotes)
			}
		})
	}
	var nilRes *ConsoleResult
	if nilRes.Partial() {
		t.Fatal("nil sonuç partial olmamalı")
	}
}

// ── tel parametreleri: dedup / partial_response / timeout, POST form ────

func TestWorkerQueryRequestParams(t *testing.T) {
	f := newFakeConsoleThanos(t, false, respondWith(200, seriesBody("vector", 0)))
	cl := consoleTestCluster(f.URL, "")
	cl.AuthType, cl.Token = "bearer", "tok-plain"
	s := workerTestService(nil, cl)
	ctx := context.Background()

	cases := []struct {
		name string
		lim  WorkerLimits
		want map[string]string
	}{
		{"varsayılanlar", WorkerLimits{},
			map[string]string{"query": "up", "dedup": "true", "partial_response": "false", "timeout": "30s"}},
		{"açık değerler", WorkerLimits{Dedup: boolPtr(false), PartialResponse: true, TimeoutS: 45},
			map[string]string{"query": "up", "dedup": "false", "partial_response": "true", "timeout": "45s"}},
		{"açık dedup=true", WorkerLimits{Dedup: boolPtr(true), TimeoutS: 10},
			map[string]string{"query": "up", "dedup": "true", "partial_response": "false", "timeout": "10s"}},
		{"timeout tavanı 45 s", WorkerLimits{TimeoutS: 120},
			map[string]string{"query": "up", "dedup": "true", "partial_response": "false", "timeout": "45s"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := s.WorkerQuery(ctx, cl.ID, "up", c.lim); err != nil {
				t.Fatal(err)
			}
			r := f.last(t)
			for k, v := range c.want {
				if got := r.Form[k]; len(got) != 1 || got[0] != v {
					t.Errorf("%s = %q, beklenen tek değer %q", k, got, v)
				}
			}
			if r.Method != http.MethodPost || r.Path != "/api/v1/query" || r.ContentType != "application/x-www-form-urlencoded" {
				t.Errorf("istek POST form /api/v1/query olmalı: %+v", r)
			}
			if r.Auth != "Bearer tok-plain" {
				t.Errorf("Authorization = %q", r.Auth)
			}
			// Yalnız anlık sorgu: aralık/zaman parametreleri YOK.
			for _, k := range []string{"time", "start", "end", "step", "max_source_resolution"} {
				if _, ok := r.Form[k]; ok {
					t.Errorf("%s gönderilmemeli", k)
				}
			}
		})
	}
}

// Paylaşımlı querier: cluster matcher EffectiveQuery ile enjekte edilir;
// NamespaceMatcher çağıranın ifadesinde durur (P1.2 birlikte kullanım).
func TestWorkerQuerySharedQuerierInjection(t *testing.T) {
	f := newFakeConsoleThanos(t, false, respondWith(200, seriesBody("vector", 1)))
	cl := consoleTestCluster(f.URL, "cluster")
	expr := `max by (namespace, deployment) (kube_deployment_metadata_generation{deployment!=""` + NamespaceMatcher("team-a-.*") + `})`
	res, err := workerTestService(nil, cl).WorkerQuery(context.Background(), cl.ID, expr, WorkerLimits{})
	if err != nil {
		t.Fatal(err)
	}
	sent := f.last(t).Form.Get("query")
	if sent != cl.EffectiveQuery(expr) || res.EffectiveQuery != sent {
		t.Fatalf("gönderilen %q / effectiveQuery %q, beklenen %q", sent, res.EffectiveQuery, cl.EffectiveQuery(expr))
	}
	for _, m := range []string{`cluster="cluster-a"`, `namespace=~"team-a-.*"`, `deployment!=""`} {
		if !strings.Contains(sent, m) {
			t.Fatalf("gönderilen sorgu %q, %s içermeli", sent, m)
		}
	}
}

// ── fail-closed TokenRef ────────────────────────────────────────────────

func TestWorkerQueryTokenRef(t *testing.T) {
	f := newFakeConsoleThanos(t, false, respondWith(200, seriesBody("vector", 0)))
	ctx := context.Background()

	t.Run("çözülen ref → Bearer ref değeri", func(t *testing.T) {
		cl := consoleTestCluster(f.URL, "")
		cl.AuthType, cl.Token, cl.TokenRef = "bearer", "stale", "env:COREMETRY_THANOS_TOKEN_A"
		s := workerTestService(map[string]string{"COREMETRY_THANOS_TOKEN_A": "tok-ref"}, cl)
		if _, err := s.WorkerQuery(ctx, cl.ID, "up", WorkerLimits{}); err != nil {
			t.Fatal(err)
		}
		if got := f.last(t).Auth; got != "Bearer tok-ref" {
			t.Fatalf("Authorization = %q, beklenen çözülmüş ref", got)
		}
	})

	for _, auth := range []string{"bearer", "none", ""} {
		t.Run("çözülmeyen ref → hata, istek yok / authType="+auth, func(t *testing.T) {
			before := f.count()
			cl := consoleTestCluster(f.URL, "")
			cl.AuthType, cl.Token, cl.TokenRef = auth, "stale-SECRET", "env:COREMETRY_THANOS_TOKEN_MISSING"
			s := workerTestService(nil, cl)
			res, err := s.WorkerQuery(ctx, cl.ID, "up", WorkerLimits{})
			if res != nil || !errors.Is(err, ErrWorkerTokenUnresolved) {
				t.Fatalf("ErrWorkerTokenUnresolved beklenirdi: res=%+v err=%v", res, err)
			}
			if n := f.count() - before; n != 0 {
				t.Fatalf("fail-closed: HTTP turu yapılmamalı; %d istek", n)
			}
			for _, leak := range []string{"stale-SECRET", f.URL, f.host()} {
				if strings.Contains(err.Error(), leak) {
					t.Fatalf("hata %q sızdırıyor: %q", leak, err.Error())
				}
			}
			if !strings.Contains(err.Error(), "cluster-a") {
				t.Fatalf("hata cluster adını taşımalı: %q", err.Error())
			}
		})
	}
}

// v0.10.955 — fail-closed yarışı: denetim ile istek arasında 30 s
// yenilemenin Configure'u ref'i çözülmez yaparsa istek BAŞLIKSIZ
// gitmemeli. Sabitlemeden önce consoleCall token'ı ayrı bir kilitle yeniden
// okuyordu; bu yük testi 2000 turda başlıksız istekleri yakaladı. Sabitleme
// sonrası denetlenen token gönderilen token'dır → başlıksız istek
// DETERMİNİSTİK olarak sıfır.
func TestWorkerQueryTokenCheckedIsTokenSent(t *testing.T) {
	var sent, noAuth atomic.Int64
	f := newFakeConsoleThanos(t, false, func(w http.ResponseWriter, r *http.Request, req consoleReq) {
		sent.Add(1)
		if req.Auth != "Bearer tok-ref" {
			noAuth.Add(1)
		}
		respondWith(200, seriesBody("vector", 0))(w, r, req)
	})
	cl := consoleTestCluster(f.URL, "")
	cl.AuthType, cl.TokenRef = "bearer", "env:COREMETRY_THANOS_TOKEN_A"
	var present atomic.Bool
	present.Store(true)
	s := newRefTestService(nil, nil)
	s.getenv = func(k string) string {
		if k == "COREMETRY_THANOS_TOKEN_A" && present.Load() {
			return "tok-ref"
		}
		return ""
	}
	cfg := Settings{Clusters: []ClusterConfig{cl}}
	s.Configure(cfg)

	stop, done := make(chan struct{}), make(chan struct{})
	go func() { // 30 s yenilemenin sıkıştırılmış hâli: ref çözülür/çözülmez
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			present.Store(!present.Load())
			s.Configure(cfg)
			runtime.Gosched()
		}
	}()
	ctx := context.Background()
	for i := 0; i < 20000 && sent.Load() < 200; i++ {
		if _, err := s.WorkerQuery(ctx, cl.ID, "up", WorkerLimits{}); err != nil && !errors.Is(err, ErrWorkerTokenUnresolved) {
			close(stop)
			<-done
			t.Fatalf("beklenmeyen hata: %v", err)
		}
	}
	close(stop)
	<-done
	if n := noAuth.Load(); n != 0 {
		t.Fatalf("fail-closed delindi: %d/%d istek başlıksız gitti", n, sent.Load())
	}
	// Boş geçmesin: yenileme durunca çözülen ref ile istek başlıklı gider.
	present.Store(true)
	s.Configure(cfg)
	before := sent.Load()
	if _, err := s.WorkerQuery(ctx, cl.ID, "up", WorkerLimits{}); err != nil {
		t.Fatalf("çözülen ref: %v", err)
	}
	if sent.Load() != before+1 || noAuth.Load() != 0 || f.last(t).Auth != "Bearer tok-ref" {
		t.Fatalf("çözülen ref ile tek başlıklı istek beklenirdi: sent=%d noAuth=%d auth=%q", sent.Load()-before, noAuth.Load(), f.last(t).Auth)
	}
}

// ── cluster çözümü ve yerel doğrulama: istek YOK ────────────────────────

func TestWorkerQueryClusterLookup(t *testing.T) {
	f := newFakeConsoleThanos(t, false, respondWith(200, seriesBody("vector", 1)))
	on := consoleTestCluster(f.URL, "")
	off := ClusterConfig{ID: "c-test0002", Name: "cluster-b", URL: f.URL, Enabled: false}
	noURL := ClusterConfig{ID: "c-test0003", Name: "cluster-c", URL: "  ", Enabled: true}
	s := workerTestService(nil, on, off, noURL)
	ctx := context.Background()

	for _, c := range []struct{ name, id string }{
		{"bilinmeyen id", "c-unknown"},
		{"kapalı cluster", off.ID},
		{"URL'siz etkin kayıt", noURL.ID},
		{"boş id", ""},
		{"ad id değildir", on.Name},
	} {
		t.Run(c.name, func(t *testing.T) {
			res, err := s.WorkerQuery(ctx, c.id, "up", WorkerLimits{})
			if res != nil || !errors.Is(err, ErrWorkerClusterUnavailable) {
				t.Fatalf("ErrWorkerClusterUnavailable beklenirdi: res=%+v err=%v", res, err)
			}
			if strings.Contains(err.Error(), f.host()) {
				t.Fatalf("hata uç nokta sızdırıyor: %q", err.Error())
			}
		})
	}
	t.Run("boş ifade → bad_data", func(t *testing.T) {
		_, err := s.WorkerQuery(ctx, on.ID, "  \n", WorkerLimits{})
		if ce := asConsoleError(t, err); ce.Type != ConsoleErrBadData {
			t.Fatalf("bad_data beklenirdi: %+v", ce)
		}
	})
	t.Run("nil servis", func(t *testing.T) {
		var nilS *Service
		if _, err := nilS.WorkerQuery(ctx, on.ID, "up", WorkerLimits{}); !errors.Is(err, ErrWorkerClusterUnavailable) {
			t.Fatalf("nil servis → ErrWorkerClusterUnavailable, alınan %v", err)
		}
	})
	if n := f.count(); n != 0 {
		t.Fatalf("yerel ret yollarında HTTP turu yapılmamalı; %d istek", n)
	}
	// Etkin kayıt id ile çözülür (kontrol: aynı sahte sunucu cevap verir).
	if res, err := s.WorkerQuery(ctx, on.ID, "up", WorkerLimits{}); err != nil || res.Series != 1 {
		t.Fatalf("etkin cluster: %v %+v", err, res)
	}
}

// ── zaman aşımı ve iptal ────────────────────────────────────────────────

func hangUntilClientGone(started chan<- struct{}) func(http.ResponseWriter, *http.Request, consoleReq) {
	return func(_ http.ResponseWriter, r *http.Request, _ consoleReq) {
		if started != nil {
			started <- struct{}{}
		}
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}
}

func TestWorkerQueryTimeout(t *testing.T) {
	f := newFakeConsoleThanos(t, false, hangUntilClientGone(nil))
	cl := consoleTestCluster(f.URL, "")
	began := time.Now()
	_, err := workerTestService(nil, cl).WorkerQuery(context.Background(), cl.ID, "up", WorkerLimits{TimeoutS: 1})
	ce := asConsoleError(t, err)
	if ce.Type != ConsoleErrTimeout || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout + DeadlineExceeded beklenirdi: %+v", ce)
	}
	if el := time.Since(began); el > 3*time.Second {
		t.Fatalf("zaman aşımı ayara uymadı: %v", el)
	}
	if !strings.Contains(ce.Message, "1s worker timeout") {
		t.Fatalf("mesaj işçi zaman aşımını söylemeli: %q", ce.Message)
	}
	if got := f.last(t).Form.Get("timeout"); got != "1s" {
		t.Fatalf("Thanos timeout= %q, beklenen 1s", got)
	}
	if strings.Contains(err.Error(), f.host()) {
		t.Fatalf("timeout mesajı uç nokta sızdırıyor: %q", err.Error())
	}
}

func TestWorkerQueryCanceled(t *testing.T) {
	t.Run("uçuşta iptal", func(t *testing.T) {
		started := make(chan struct{}, 1)
		f := newFakeConsoleThanos(t, false, hangUntilClientGone(started))
		cl := consoleTestCluster(f.URL, "")
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-started
			cancel()
		}()
		_, err := workerTestService(nil, cl).WorkerQuery(ctx, cl.ID, "up", WorkerLimits{TimeoutS: 10})
		ce := asConsoleError(t, err)
		if ce.Type != ConsoleErrCanceled || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled + errors.Is(Canceled) beklenirdi: %+v", ce)
		}
	})
	t.Run("önceden iptal → istek yok", func(t *testing.T) {
		f := newFakeConsoleThanos(t, false, respondWith(200, seriesBody("vector", 1)))
		cl := consoleTestCluster(f.URL, "")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := workerTestService(nil, cl).WorkerQuery(ctx, cl.ID, "up", WorkerLimits{})
		ce := asConsoleError(t, err)
		if ce.Type != ConsoleErrCanceled || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled beklenirdi: %+v", ce)
		}
		if n := f.count(); n != 0 {
			t.Fatalf("iptal edilmiş bağlamla HTTP turu yapılmamalı; %d istek", n)
		}
	})
	t.Run("çağıranın son tarihi geçmiş → timeout, istek yok", func(t *testing.T) {
		f := newFakeConsoleThanos(t, false, respondWith(200, seriesBody("vector", 1)))
		cl := consoleTestCluster(f.URL, "")
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		_, err := workerTestService(nil, cl).WorkerQuery(ctx, cl.ID, "up", WorkerLimits{})
		ce := asConsoleError(t, err)
		if ce.Type != ConsoleErrTimeout || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout + DeadlineExceeded beklenirdi: %+v", ce)
		}
		if !strings.Contains(ce.Message, "caller") {
			t.Fatalf("mesaj çağıranın son tarihini söylemeli: %q", ce.Message)
		}
		if n := f.count(); n != 0 {
			t.Fatalf("süresi geçmiş bağlamla HTTP turu yapılmamalı; %d istek", n)
		}
	})
}

// Upstream/taşıma hataları konsol sözleşmesiyle aynı: URL sızmaz.
func TestWorkerQueryUpstreamErrorsDoNotLeakEndpoint(t *testing.T) {
	t.Run("erişilemez", func(t *testing.T) {
		f := newFakeConsoleThanos(t, false, respondWith(200, "{}"))
		endpoint, host := f.URL, f.host()
		f.Close()
		cl := consoleTestCluster(endpoint, "")
		_, err := workerTestService(nil, cl).WorkerQuery(context.Background(), cl.ID, "up", WorkerLimits{})
		ce := asConsoleError(t, err)
		if ce.Type != ConsoleErrUnavailable {
			t.Fatalf("unavailable beklenirdi: %+v", ce)
		}
		for _, leak := range []string{endpoint, host, "dial tcp"} {
			if strings.Contains(err.Error(), leak) {
				t.Fatalf("hata %q sızdırıyor: %q", leak, err.Error())
			}
		}
	})
	t.Run("partial_response=false iken store hatası → hata (sessiz boşluk değil)", func(t *testing.T) {
		f := newFakeConsoleThanos(t, false, respondWith(503,
			`{"status":"error","errorType":"unavailable","error":"no StoreAPIs matched for this query"}`))
		cl := consoleTestCluster(f.URL, "")
		_, err := workerTestService(nil, cl).WorkerQuery(context.Background(), cl.ID, "up", WorkerLimits{})
		if ce := asConsoleError(t, err); ce.Type != ConsoleErrUnavailable || ce.UpstreamStatus != 503 {
			t.Fatalf("unavailable/503 beklenirdi: %+v", ce)
		}
	})
}

// ── P1.2: NamespaceMatcher ──────────────────────────────────────────────

func TestNamespaceMatcher(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"team-a-.*", `,namespace=~"team-a-.*"`},
		{"(team-a|team-b)-prod", `,namespace=~"(team-a|team-b)-prod"`},
		{`a"b\c`, `,namespace=~"a\"b\\c"`}, // dize çerçevesi kaçışlanır, regex olduğu gibi
	}
	for _, c := range cases {
		got := NamespaceMatcher(c.in)
		if got != c.want {
			t.Errorf("NamespaceMatcher(%q) = %q, beklenen %q", c.in, got, c.want)
		}
		if got != nsMatcher(c.in) {
			t.Errorf("ince sarmalayıcı nsMatcher'dan ayrıştı: %q vs %q", got, nsMatcher(c.in))
		}
	}
	// Kullanım biçimi: mevcut bir matcher'ın arkasına eklenir.
	if q := `kube_replicaset_owner{owner_kind="Deployment"` + NamespaceMatcher("team-a-.*") + `}`; q != `kube_replicaset_owner{owner_kind="Deployment",namespace=~"team-a-.*"}` {
		t.Errorf("birleşik seçici %q", q)
	}
}
