package thanos

// console_test.go — v0.10.951 — PromQL konsol istemcisi sözleşmeleri, SAHTE
// Thanos'a karşı (client_test.go fakeQuerier emsali; canlı çağrı YOK).
// Kapsam (spec C2): dört resultType, warnings/infos, 4xx/5xx errorType
// eşlemesi + konum, seri tavanı 499/500/501, gövde tavanı (Content-Length
// ve chunked), match[] enjeksiyonu/sentezi, timeout, iptal, partial_response
// ve diğer karar-5 parametreleri, hiçbir hata mesajında uç nokta URL'si
// yok, paylaşımlı 15 s istemcilere dokunulmaz.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// consoleReq — sahte Thanos'un gördüğü istek.
type consoleReq struct {
	Method, Path, ContentType, Auth string
	Form                            url.Values
}

type fakeConsoleThanos struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []consoleReq
}

// newFakeConsoleThanos — her isteği kaydeder (form: GET sorgu dizesi + POST
// gövdesi) ve respond'a devreder.
func newFakeConsoleThanos(t *testing.T, useTLS bool, respond func(w http.ResponseWriter, r *http.Request, req consoleReq)) *fakeConsoleThanos {
	t.Helper()
	f := &fakeConsoleThanos{}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		req := consoleReq{Method: r.Method, Path: r.URL.Path, ContentType: r.Header.Get("Content-Type"),
			Auth: r.Header.Get("Authorization"), Form: r.Form}
		f.mu.Lock()
		f.reqs = append(f.reqs, req)
		f.mu.Unlock()
		respond(w, r, req)
	})
	if useTLS {
		f.Server = httptest.NewTLSServer(h)
	} else {
		f.Server = httptest.NewServer(h)
	}
	t.Cleanup(f.Close)
	return f
}

func (f *fakeConsoleThanos) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func (f *fakeConsoleThanos) last(t *testing.T) consoleReq {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) == 0 {
		t.Fatal("sahte Thanos hiç istek almadı")
	}
	return f.reqs[len(f.reqs)-1]
}

func (f *fakeConsoleThanos) host() string {
	return strings.TrimPrefix(strings.TrimPrefix(f.URL, "https://"), "http://")
}

// respondWith — sabit durum kodu + gövde.
func respondWith(status int, body string) func(http.ResponseWriter, *http.Request, consoleReq) {
	return func(w http.ResponseWriter, _ *http.Request, _ consoleReq) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}
}

// consoleTestCluster — sentetik kayıt; label boş = cluster başına URL modeli.
func consoleTestCluster(u, label string) ClusterConfig {
	return ClusterConfig{ID: "c-test0001", Name: "cluster-a", URL: u, ThanosLabelName: label, Enabled: true}
}

// seriesBody — n seri: vector (value) ya da matrix (values); pod etiketi p-<i>.
func seriesBody(resultType string, n int) string {
	var b strings.Builder
	b.WriteString(`{"status":"success","data":{"resultType":"` + resultType + `","result":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		if resultType == "matrix" {
			fmt.Fprintf(&b, `{"metric":{"__name__":"up","pod":"p-%d"},"values":[[1784271000,"1"],[1784271015,"0"]]}`, i)
		} else {
			fmt.Fprintf(&b, `{"metric":{"__name__":"up","pod":"p-%d"},"value":[1784271068,"1"]}`, i)
		}
	}
	b.WriteString(`]}}`)
	return b.String()
}

func compactJSON(t *testing.T, s string) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(s)); err != nil {
		t.Fatalf("geçersiz JSON %q: %v", s, err)
	}
	return b.String()
}

func asConsoleError(t *testing.T, err error) *ConsoleError {
	t.Helper()
	var ce *ConsoleError
	if !errors.As(err, &ce) {
		t.Fatalf("*ConsoleError bekleniyordu, alınan %T: %v", err, err)
	}
	return ce
}

// ── sonuç türleri ───────────────────────────────────────────────────────

func TestConsoleQueryResultTypes(t *testing.T) {
	cases := []struct {
		name, body, wantType, wantResult string
		wantSeries, wantTotal            int
		wantWarn, wantInfo               []string
	}{
		{name: "vector",
			body:     `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"up","job":"api"},"value":[1784271068.5,"1"]}]}}`,
			wantType: "vector", wantSeries: 1, wantTotal: 1,
			wantResult: `[{"metric":{"__name__":"up","job":"api"},"value":[1784271068.5,"1"]}]`},
		{name: "matrix",
			body:     `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"job":"api"},"values":[[1784271000,"1"],[1784271015,"NaN"]]}]}}`,
			wantType: "matrix", wantSeries: 1, wantTotal: 1,
			wantResult: `[{"metric":{"job":"api"},"values":[[1784271000,"1"],[1784271015,"NaN"]]}]`},
		{name: "scalar",
			body:     `{"status":"success","data":{"resultType":"scalar","result":[1784271068,"2"]}}`,
			wantType: "scalar", wantResult: `[1784271068,"2"]`},
		{name: "string",
			body:     `{"status":"success","data":{"resultType":"string","result":[1784271068,"hello"]}}`,
			wantType: "string", wantResult: `[1784271068,"hello"]`},
		{name: "boş vector",
			body:     `{"status":"success","data":{"resultType":"vector","result":[]}}`,
			wantType: "vector", wantResult: `[]`},
		{name: "null result → []",
			body:     `{"status":"success","data":{"resultType":"matrix","result":null}}`,
			wantType: "matrix", wantResult: `[]`},
		{name: "native histogram verbatim",
			body:     `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"h"},"histogram":[1784271068,{"count":"3","sum":"1.5","buckets":[[0,"0.5","1","3"]]}]}]}}`,
			wantType: "vector", wantSeries: 1, wantTotal: 1,
			wantResult: `[{"metric":{"__name__":"h"},"histogram":[1784271068,{"count":"3","sum":"1.5","buckets":[[0,"0.5","1","3"]]}]}]`},
		{name: "warnings + infos",
			body:     `{"status":"success","data":{"resultType":"vector","result":[]},"warnings":["partial: store s1 down"],"infos":["PromQL info: metric might not be a counter"]}`,
			wantType: "vector", wantResult: `[]`,
			wantWarn: []string{"partial: store s1 down"}, wantInfo: []string{"PromQL info: metric might not be a counter"}},
		{name: "alan sırası serbest + stats atlanır",
			body:     `{"infos":["i"],"data":{"result":[{"metric":{},"value":[1,"1"]}],"stats":{"timings":{}},"resultType":"vector"},"status":"success"}`,
			wantType: "vector", wantSeries: 1, wantTotal: 1, wantResult: `[{"metric":{},"value":[1,"1"]}]`,
			wantInfo: []string{"i"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeConsoleThanos(t, false, respondWith(200, c.body))
			res, err := New().ConsoleQuery(context.Background(), consoleTestCluster(f.URL, ""),
				ConsoleInstantQuery{Query: "up"}, ConsoleLimits{})
			if err != nil {
				t.Fatalf("ConsoleQuery: %v", err)
			}
			if res.ResultType != c.wantType || res.Series != c.wantSeries || res.TotalSeries != c.wantTotal || res.Truncated {
				t.Fatalf("tür/sayı: %+v", res)
			}
			if got := compactJSON(t, string(res.Result)); got != compactJSON(t, c.wantResult) {
				t.Fatalf("result\n alınan:   %s\n beklenen: %s", got, c.wantResult)
			}
			if strings.Join(res.Warnings, "|") != strings.Join(c.wantWarn, "|") || strings.Join(res.Infos, "|") != strings.Join(c.wantInfo, "|") {
				t.Fatalf("warnings/infos: %q / %q", res.Warnings, res.Infos)
			}
			if res.EffectiveQuery != "up" {
				t.Fatalf("URL başına modelde effectiveQuery aynen: %q", res.EffectiveQuery)
			}
		})
	}
}

// ── karar-5 parametreleri, POST form, auth ──────────────────────────────

func TestConsoleQueryRequestParams(t *testing.T) {
	f := newFakeConsoleThanos(t, false, respondWith(200, seriesBody("matrix", 0)))
	s := New()
	c := consoleTestCluster(f.URL, "")
	c.AuthType, c.Token = "bearer", "tok-1"
	ctx := context.Background()

	// Anlık: zaman milisaniyeli; partial_response=false varsayılan; timeout ayardan.
	if _, err := s.ConsoleQuery(ctx, c, ConsoleInstantQuery{Query: "up", Time: time.Unix(1784271068, 500e6)}, ConsoleLimits{}); err != nil {
		t.Fatal(err)
	}
	r := f.last(t)
	want := map[string]string{"query": "up", "time": "1784271068.500", "partial_response": "false", "timeout": "30s"}
	for k, v := range want {
		if got := r.Form.Get(k); got != v {
			t.Errorf("anlık %s = %q, beklenen %q", k, got, v)
		}
	}
	if r.Method != http.MethodPost || r.Path != "/api/v1/query" || r.ContentType != "application/x-www-form-urlencoded" || r.Auth != "Bearer tok-1" {
		t.Errorf("istek: %+v", r)
	}
	for _, k := range []string{"dedup", "max_source_resolution"} {
		if _, ok := r.Form[k]; ok {
			t.Errorf("anlık sorguda %s gönderilmemeli", k)
		}
	}

	// Zaman verilmezse time gönderilmez.
	if _, err := s.ConsoleQuery(ctx, c, ConsoleInstantQuery{Query: "up"}, ConsoleLimits{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.last(t).Form["time"]; ok {
		t.Error("sıfır zaman gönderilmemeli")
	}

	// Aralık: start/end/step saniye; max_source_resolution=auto; partial_response ayardan.
	start := time.Unix(1784267468, 0)
	_, err := s.ConsoleQueryRange(ctx, c, ConsoleRangeQuery{Query: "rate(x[5m])", Start: start, End: start.Add(time.Hour), Step: 15 * time.Second},
		ConsoleLimits{Timeout: 45 * time.Second, PartialResponse: true})
	if err != nil {
		t.Fatal(err)
	}
	r = f.last(t)
	want = map[string]string{"query": "rate(x[5m])", "start": "1784267468", "end": "1784271068", "step": "15",
		"max_source_resolution": "auto", "partial_response": "true", "timeout": "45s"}
	for k, v := range want {
		if got := r.Form.Get(k); got != v {
			t.Errorf("aralık %s = %q, beklenen %q", k, got, v)
		}
	}
	if r.Path != "/api/v1/query_range" || r.Method != http.MethodPost {
		t.Errorf("aralık isteği: %+v", r)
	}
	if _, ok := r.Form["dedup"]; ok {
		t.Error("dedup querier varsayılanında kalmalı (gönderilmez)")
	}
}

// v0.10.272 TokenRef + konsol: çözülmüş referans saklı token'a tercih edilir.
func TestConsoleUsesResolvedTokenRef(t *testing.T) {
	f := newFakeConsoleThanos(t, false, respondWith(200, seriesBody("vector", 0)))
	s := newRefTestService(map[string]string{"CONSOLE_TOKEN": "tok-ref"}, nil)
	c := consoleTestCluster(f.URL, "")
	c.AuthType, c.Token, c.TokenRef = "bearer", "stale", "env:CONSOLE_TOKEN"
	s.Configure(Settings{Clusters: []ClusterConfig{c}})
	if _, err := s.ConsoleQuery(context.Background(), c, ConsoleInstantQuery{Query: "up"}, ConsoleLimits{}); err != nil {
		t.Fatal(err)
	}
	if got := f.last(t).Auth; got != "Bearer tok-ref" {
		t.Fatalf("Authorization = %q, beklenen çözülmüş ref", got)
	}
}

// ── akışlı seri tavanı: 499 / 500 / 501 ────────────────────────────────

func TestConsoleSeriesCapEdges(t *testing.T) {
	for _, rt := range []string{"vector", "matrix"} {
		for _, n := range []int{0, 1, 499, 500, 501, 1200} {
			t.Run(fmt.Sprintf("%s/%d", rt, n), func(t *testing.T) {
				f := newFakeConsoleThanos(t, false, respondWith(200, seriesBody(rt, n)))
				res, err := New().ConsoleQuery(context.Background(), consoleTestCluster(f.URL, ""),
					ConsoleInstantQuery{Query: "up"}, ConsoleLimits{MaxSeries: 500})
				if err != nil {
					t.Fatal(err)
				}
				kept := min(n, 500)
				if res.Series != kept || res.TotalSeries != n || res.Truncated != (n > 500) {
					t.Fatalf("series=%d total=%d truncated=%v; beklenen %d/%d/%v", res.Series, res.TotalSeries, res.Truncated, kept, n, n > 500)
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
}

// ── hata eşlemesi (karar 11) + URL sızıntısı yok ────────────────────────

func TestConsoleErrorMapping(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantType   string
		wantHTTP   int
		wantPos    string
		wantMsg    string // alt dize
		forbidMsg  string // mesajda OLMAMALI
		wantStatus int    // UpstreamStatus
	}{
		{name: "bad_data + konum", status: 400,
			body:     `{"status":"error","errorType":"bad_data","error":"invalid parameter \"query\": 1:5: parse error: unexpected identifier \"foo\""}`,
			wantType: ConsoleErrBadData, wantHTTP: 400, wantPos: "1:5", wantMsg: "parse error", wantStatus: 400},
		{name: "execution 422", status: 422,
			body:     `{"status":"error","errorType":"execution","error":"found duplicate series for the match group"}`,
			wantType: ConsoleErrExecution, wantHTTP: 422, wantMsg: "duplicate series", wantStatus: 422},
		{name: "upstream timeout 503", status: 503,
			body:     `{"status":"error","errorType":"timeout","error":"query timed out in expression evaluation"}`,
			wantType: ConsoleErrTimeout, wantHTTP: 504, wantMsg: "timed out", wantStatus: 503},
		{name: "unavailable 503", status: 503,
			body:     `{"status":"error","errorType":"unavailable","error":"no StoreAPIs matched for this query"}`,
			wantType: ConsoleErrUnavailable, wantHTTP: 502, wantMsg: "StoreAPIs", wantStatus: 503},
		{name: "internal 500", status: 500,
			body:     `{"status":"error","errorType":"internal","error":"rpc error"}`,
			wantType: ConsoleErrInternal, wantHTTP: 502, wantMsg: "rpc error", wantStatus: 500},
		{name: "JSON olmayan 5xx gövdesi yankılanmaz", status: 502,
			body:     `<html><body>upstream connect error SECRET-HTML</body></html>`,
			wantType: ConsoleErrUnavailable, wantHTTP: 502, wantMsg: "HTTP 502", forbidMsg: "SECRET-HTML", wantStatus: 502},
		{name: "401 kimlik reddi", status: 401, body: `Unauthorized SECRET-401`,
			wantType: ConsoleErrUnavailable, wantHTTP: 502, wantMsg: "credentials", forbidMsg: "SECRET-401", wantStatus: 401},
		{name: "404 yanlış yol", status: 404, body: `404 page not found`,
			wantType: ConsoleErrUnavailable, wantHTTP: 502, wantMsg: "not found", wantStatus: 404},
		{name: "düz 504", status: 504, body: `gateway timeout`,
			wantType: ConsoleErrTimeout, wantHTTP: 504, wantMsg: "HTTP 504", wantStatus: 504},
		{name: "bilinmeyen errorType 400 → bad_data", status: 400,
			body:     `{"status":"error","errorType":"not_acceptable","error":"nope"}`,
			wantType: ConsoleErrBadData, wantHTTP: 400, wantMsg: "nope", wantStatus: 400},
		{name: "200 + status error, çok satırlı konum", status: 200,
			body:     `{"status":"error","errorType":"bad_data","error":"2:3: parse error: unexpected \")\""}`,
			wantType: ConsoleErrBadData, wantHTTP: 400, wantPos: "2:3", wantStatus: 200},
		{name: "eski konum biçimi", status: 400,
			body:     `{"status":"error","errorType":"bad_data","error":"parse error at char 7: unexpected end of input"}`,
			wantType: ConsoleErrBadData, wantHTTP: 400, wantPos: "1:7", wantStatus: 400},
		{name: "kesik JSON", status: 200, body: `{"status":"success","data":{"resultType":"vector","result":[`,
			wantType: ConsoleErrInternal, wantHTTP: 502, wantMsg: "malformed", wantStatus: 200},
		{name: "200 HTML (oauth giriş sayfası) yankılanmaz", status: 200, body: `<html>login SECRET-LOGIN</html>`,
			wantType: ConsoleErrInternal, wantHTTP: 502, wantMsg: "malformed", forbidMsg: "SECRET-LOGIN", wantStatus: 200},
		{name: "bilinmeyen resultType", status: 200, body: `{"status":"success","data":{"resultType":"table","result":[]}}`,
			wantType: ConsoleErrInternal, wantHTTP: 502, wantMsg: "unknown resultType", wantStatus: 200},
		{name: "resultType/şekil uyuşmazlığı", status: 200, body: `{"status":"success","data":{"resultType":"scalar","result":[{"metric":{}}]}}`,
			wantType: ConsoleErrInternal, wantHTTP: 502, wantStatus: 200},
		// v0.10.951 — upstream "canceled" BİZİM istemcimizin vazgeçmesi
		// değildir (yanıtı okuduk): unavailable/502, gövdesiz 499 DEĞİL.
		{name: "upstream canceled 499 → unavailable", status: 499,
			body:     `{"status":"error","errorType":"canceled","error":"query was canceled in expression evaluation"}`,
			wantType: ConsoleErrUnavailable, wantHTTP: 502, wantMsg: "canceled", wantStatus: 499},
		{name: "upstream canceled 503 → unavailable", status: 503,
			body:     `{"status":"error","errorType":"canceled","error":"query was canceled in expression evaluation"}`,
			wantType: ConsoleErrUnavailable, wantHTTP: 502, wantMsg: "canceled", wantStatus: 503},
		{name: "upstream canceled 400 → bad_data'ya DÜŞMEZ", status: 400,
			body:     `{"status":"error","errorType":"canceled","error":"query was canceled"}`,
			wantType: ConsoleErrUnavailable, wantHTTP: 502, wantMsg: "canceled", wantStatus: 400},
		{name: "200 + status error canceled → unavailable", status: 200,
			body:     `{"status":"error","errorType":"canceled","error":"query was canceled"}`,
			wantType: ConsoleErrUnavailable, wantHTTP: 502, wantMsg: "canceled", wantStatus: 200},
		{name: "JSON'suz 499 → unavailable", status: 499, body: `client closed request`,
			wantType: ConsoleErrUnavailable, wantHTTP: 502, wantMsg: "canceled the query (HTTP 499)", forbidMsg: "client closed", wantStatus: 499},
		{name: "upstream mesajındaki URL maskelenir", status: 422,
			body:     `{"status":"error","errorType":"execution","error":"store {{URL}}/api failed at {{HOST}}"}`,
			wantType: ConsoleErrExecution, wantHTTP: 422, wantMsg: "<thanos>", wantStatus: 422},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeConsoleThanos(t, false, func(w http.ResponseWriter, r *http.Request, _ consoleReq) {
				body := strings.ReplaceAll(c.body, "{{URL}}", "http://"+r.Host)
				body = strings.ReplaceAll(body, "{{HOST}}", r.Host)
				w.WriteHeader(c.status)
				fmt.Fprint(w, body)
			})
			_, err := New().ConsoleQuery(context.Background(), consoleTestCluster(f.URL, ""), ConsoleInstantQuery{Query: "up"}, ConsoleLimits{})
			ce := asConsoleError(t, err)
			if ce.Type != c.wantType || ce.StatusCode() != c.wantHTTP || ce.UpstreamStatus != c.wantStatus {
				t.Fatalf("tür=%s http=%d upstream=%d; beklenen %s/%d/%d (%s)", ce.Type, ce.StatusCode(), ce.UpstreamStatus, c.wantType, c.wantHTTP, c.wantStatus, ce.Message)
			}
			if ce.Position != c.wantPos || ce.EffectivePosition != c.wantPos {
				t.Fatalf("konum %q/%q, beklenen %q", ce.Position, ce.EffectivePosition, c.wantPos)
			}
			if !strings.Contains(ce.Message, c.wantMsg) {
				t.Fatalf("mesaj %q, %q içermeli", ce.Message, c.wantMsg)
			}
			if c.forbidMsg != "" && strings.Contains(err.Error(), c.forbidMsg) {
				t.Fatalf("upstream gövdesi yankılandı: %q", err.Error())
			}
			for _, leak := range []string{f.URL, f.host()} {
				if strings.Contains(err.Error(), leak) {
					t.Fatalf("hata uç nokta sızdırıyor (%q): %q", leak, err.Error())
				}
			}
		})
	}
}

// Taşıma hatası: ham *url.Error (URL taşır) istemciye GİTMEZ.
func TestConsoleUnreachableDoesNotLeakEndpoint(t *testing.T) {
	f := newFakeConsoleThanos(t, false, respondWith(200, "{}"))
	endpoint, host := f.URL, f.host()
	f.Close() // bağlantı reddedilir
	_, err := New().ConsoleQuery(context.Background(), consoleTestCluster(endpoint, ""), ConsoleInstantQuery{Query: "up"}, ConsoleLimits{})
	ce := asConsoleError(t, err)
	if ce.Type != ConsoleErrUnavailable || ce.StatusCode() != http.StatusBadGateway || ce.UpstreamStatus != 0 {
		t.Fatalf("erişilemez → unavailable/502: %+v", ce)
	}
	if !strings.Contains(ce.Message, "cluster-a") || !strings.Contains(ce.Message, "connection failed") {
		t.Fatalf("mesaj cluster adını ve kaba sebebi taşımalı: %q", ce.Message)
	}
	for _, leak := range []string{endpoint, host, "127.0.0.1", "dial tcp"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("hata %q sızdırıyor: %q", leak, err.Error())
		}
	}
	if errors.Unwrap(err) != nil {
		t.Fatalf("ham taşıma hatası Unwrap ile de verilmemeli: %v", errors.Unwrap(err))
	}
}

// ── gövde tavanı: response_too_large ────────────────────────────────────

func TestConsoleBodyCap(t *testing.T) {
	body := seriesBody("vector", 40) // ~3 KiB
	cases := []struct {
		name    string
		max     int64
		chunked bool
		wantErr bool
	}{
		{"Content-Length tavanı aşıyor", 1024, false, true},
		{"chunked gövde tavanı aşıyor", 1024, true, true},
		{"tam tavan boyutu geçer", int64(len(body)), true, false},
		{"tavanın altı geçer", int64(len(body)) + 1, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeConsoleThanos(t, false, func(w http.ResponseWriter, _ *http.Request, _ consoleReq) {
				if !c.chunked {
					w.Header().Set("Content-Length", strconv.Itoa(len(body)))
					fmt.Fprint(w, body)
					return
				}
				for i := 0; i < len(body); i += 512 {
					fmt.Fprint(w, body[i:min(i+512, len(body))])
					w.(http.Flusher).Flush()
				}
			})
			res, err := New().ConsoleQuery(context.Background(), consoleTestCluster(f.URL, ""),
				ConsoleInstantQuery{Query: "up"}, ConsoleLimits{MaxBodyBytes: c.max})
			if !c.wantErr {
				if err != nil || res.TotalSeries != 40 {
					t.Fatalf("tavan içinde başarı beklenirdi: %v %+v", err, res)
				}
				return
			}
			ce := asConsoleError(t, err)
			if ce.Type != ConsoleErrResponseTooLarge || ce.StatusCode() != http.StatusRequestEntityTooLarge {
				t.Fatalf("response_too_large/413 beklenirdi: %+v", ce)
			}
			if !strings.Contains(ce.Message, "1 KiB") || !strings.Contains(ce.Message, "larger step") {
				t.Fatalf("mesaj tavanı ve öneriyi söylemeli: %q", ce.Message)
			}
		})
	}
}

// ── timeout ve iptal ────────────────────────────────────────────────────

func TestConsoleTimeout(t *testing.T) {
	f := newFakeConsoleThanos(t, false, func(w http.ResponseWriter, r *http.Request, _ consoleReq) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	began := time.Now()
	_, err := New().ConsoleQuery(context.Background(), consoleTestCluster(f.URL, ""),
		ConsoleInstantQuery{Query: "up"}, ConsoleLimits{Timeout: 200 * time.Millisecond})
	ce := asConsoleError(t, err)
	if ce.Type != ConsoleErrTimeout || ce.StatusCode() != http.StatusGatewayTimeout || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout/504 beklenirdi: %+v", ce)
	}
	if el := time.Since(began); el > 2*time.Second {
		t.Fatalf("zaman aşımı ayara uymadı: %v", el)
	}
	if got := f.last(t).Form.Get("timeout"); got != "200ms" {
		t.Fatalf("Thanos timeout= parametresi %q, beklenen 200ms", got)
	}
	if strings.Contains(err.Error(), f.host()) {
		t.Fatalf("timeout mesajı uç nokta sızdırıyor: %q", err.Error())
	}
}

func TestConsoleCanceled(t *testing.T) {
	started := make(chan struct{}, 1)
	f := newFakeConsoleThanos(t, false, func(w http.ResponseWriter, r *http.Request, _ consoleReq) {
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	_, err := New().ConsoleQuery(ctx, consoleTestCluster(f.URL, ""), ConsoleInstantQuery{Query: "up"}, ConsoleLimits{Timeout: 10 * time.Second})
	ce := asConsoleError(t, err)
	if ce.Type != ConsoleErrCanceled || ce.StatusCode() != 499 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled/499 + errors.Is(Canceled) beklenirdi: %+v", ce)
	}
}

// v0.10.951 — ConsoleErrCanceled YALNIZ bizim iptalimizden doğar ve her
// zaman context.Canceled sarar; upstream bir "canceled" cevabı ÇEVRİLMEZ.
// Başlıklar geldikten hemen sonra bizim bağlamımız iptal edildiyse
// consoleStatusError upstream gövdesini değil iptali döner (gerçekten giden
// istemci gövdesiz 499'unu korur).
func TestConsoleStatusErrorOwnCancelWins(t *testing.T) {
	c := consoleTestCluster("http://thanos.example.invalid", "")
	lim := ConsoleLimits{}.normalized()
	body := `{"status":"error","errorType":"unavailable","error":"no StoreAPIs matched"}`
	resp := func() *http.Response {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader(body))}
	}
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	ce := consoleStatusError(gone, gone, c, lim, resp())
	if ce.Type != ConsoleErrCanceled || ce.StatusCode() != 499 || !errors.Is(ce, context.Canceled) {
		t.Fatalf("iptal edilmiş üst bağlam → canceled + context.Canceled, alınan %+v", ce)
	}
	live := context.Background()
	ce = consoleStatusError(live, live, c, lim, resp())
	if ce.Type != ConsoleErrUnavailable || errors.Is(ce, context.Canceled) {
		t.Fatalf("canlı bağlamda upstream hatası: %+v", ce)
	}
	// normalizeConsoleErrorType canceled'ı ASLA döndürmez.
	for _, code := range []int{200, 400, 422, 499, 500, 503, 504} {
		for _, typ := range []string{"", ConsoleErrCanceled} {
			if got := normalizeConsoleErrorType(typ, code); got == ConsoleErrCanceled {
				t.Errorf("normalizeConsoleErrorType(%q, %d) = canceled", typ, code)
			}
		}
	}
}

// ── paylaşımlı querier: effectiveQuery, match[] enjeksiyonu, konum ─────

func TestConsoleEffectiveQueryAndPosition(t *testing.T) {
	f := newFakeConsoleThanos(t, false, func(w http.ResponseWriter, _ *http.Request, req consoleReq) {
		q := req.Form.Get("query")
		if i := strings.Index(q, "BAD"); i >= 0 {
			l, c := lineColAt(q, i)
			w.WriteHeader(400)
			fmt.Fprintf(w, `{"status":"error","errorType":"bad_data","error":"invalid parameter \"query\": %d:%d: parse error: unexpected identifier \"BAD\""}`, l, c)
			return
		}
		fmt.Fprint(w, seriesBody("vector", 1))
	})
	s := New()
	c := consoleTestCluster(f.URL, "cluster")

	res, err := s.ConsoleQuery(context.Background(), c, ConsoleInstantQuery{Query: "sum(rate(up[5m])) # it's"}, ConsoleLimits{})
	if err != nil {
		t.Fatal(err)
	}
	const wantEff = `sum(rate(up{cluster="cluster-a"}[5m]))`
	if res.EffectiveQuery != wantEff || f.last(t).Form.Get("query") != wantEff {
		t.Fatalf("effectiveQuery %q / gönderilen %q, beklenen %q", res.EffectiveQuery, f.last(t).Form.Get("query"), wantEff)
	}

	// Konum: Thanos etkin sorgudaki yeri söyler; Position kullanıcının sorgusuna çevrilir.
	orig := "# it's a header\nsum(rate(http_requests_total[5m])) BAD"
	_, err = s.ConsoleQuery(context.Background(), c, ConsoleInstantQuery{Query: orig}, ConsoleLimits{})
	ce := asConsoleError(t, err)
	if ce.Type != ConsoleErrBadData || ce.Position != "2:36" {
		t.Fatalf("kullanıcı konumu 2:36 beklenirdi: tür=%s konum=%q etkin=%q", ce.Type, ce.Position, ce.EffectivePosition)
	}
	eff := c.EffectiveQuery(orig)
	if el, ec := lineColAt(eff, strings.Index(eff, "BAD")); ce.EffectivePosition != fmt.Sprintf("%d:%d", el, ec) {
		t.Fatalf("etkin konum %q, beklenen %d:%d", ce.EffectivePosition, el, ec)
	}

	// Aralık sorgusu da aynı enjeksiyondan geçer.
	start := time.Unix(1784267468, 0)
	if _, err := s.ConsoleQueryRange(context.Background(), c, ConsoleRangeQuery{Query: "up", Start: start, End: start.Add(time.Minute), Step: time.Second}, ConsoleLimits{}); err != nil {
		t.Fatal(err)
	}
	if got := f.last(t).Form.Get("query"); got != `up{cluster="cluster-a"}` {
		t.Fatalf("aralık sorgusu enjekte edilmeli: %q", got)
	}
}

func TestConsoleMetadataMatchInjection(t *testing.T) {
	body := map[string]string{
		"/api/v1/labels": `{"status":"success","data":["__name__","job"]}`,
		"/api/v1/series": `{"status":"success","data":[{"__name__":"up","job":"api"}]}`,
	}
	cases := []struct {
		name, label, api string
		match            []string
		want             []string // Thanos'a giden match[]
	}{
		{"labels: paylaşımlı, seçici yok → sentez", "cluster", "labels", nil, []string{`{cluster="cluster-a"}`}},
		{"series: paylaşımlı, her seçici enjekte", "cluster", "series", []string{"up", `{__name__=~"http_.*"}`},
			[]string{`up{cluster="cluster-a"}`, `{cluster="cluster-a",__name__=~"http_.*"}`}},
		{"values: paylaşımlı, yorumlu seçici", "cluster", "values", []string{"up # it's"}, []string{`up{cluster="cluster-a"}`}},
		{"labels: URL başına, seçici yok → yok", "", "labels", nil, nil},
		{"values: URL başına, aynen", "", "values", []string{"up"}, []string{"up"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeConsoleThanos(t, false, func(w http.ResponseWriter, _ *http.Request, req consoleReq) {
				if b, ok := body[req.Path]; ok {
					fmt.Fprint(w, b)
					return
				}
				fmt.Fprint(w, `{"status":"success","data":["a","b"]}`)
			})
			s := New()
			cl := consoleTestCluster(f.URL, c.label)
			q := ConsoleMetaQuery{Match: c.match}
			var eff []string
			var err error
			switch c.api {
			case "labels":
				var r *ConsoleLabelsResult
				r, err = s.ConsoleLabels(context.Background(), cl, q, ConsoleLimits{})
				if r != nil {
					eff = r.EffectiveMatch
				}
			case "values":
				var r *ConsoleLabelsResult
				r, err = s.ConsoleLabelValues(context.Background(), cl, "job", q, ConsoleLimits{})
				if r != nil {
					eff = r.EffectiveMatch
				}
			case "series":
				var r *ConsoleSeriesResult
				r, err = s.ConsoleSeries(context.Background(), cl, q, ConsoleLimits{})
				if r != nil {
					eff = r.EffectiveMatch
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			got := f.last(t).Form["match[]"]
			if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") || len(got) != len(c.want) {
				t.Fatalf("gönderilen match[] %q, beklenen %q", got, c.want)
			}
			if strings.Join(eff, "\x00") != strings.Join(c.want, "\x00") {
				t.Fatalf("EffectiveMatch %q, beklenen %q", eff, c.want)
			}
		})
	}
}

// Sunucu-uygulamalı limit (upstream uygulasa da uygulamasa da), metadata
// zaman sınırı (varsayılan son 1 saat), label values yol kaçışı ve GET.
func TestConsoleMetadataLimitsAndPaths(t *testing.T) {
	f := newFakeConsoleThanos(t, false, func(w http.ResponseWriter, _ *http.Request, req consoleReq) {
		switch {
		case req.Path == "/api/v1/series":
			fmt.Fprint(w, `{"status":"success","data":[{"a":"1"},{"a":"2"},{"a":"3"},{"a":"4"}],"warnings":["results truncated due to limit"]}`)
		default:
			fmt.Fprint(w, `{"status":"success","data":["v1","v2","v3","v4","v5"]}`)
		}
	})
	s := New()
	c := consoleTestCluster(f.URL, "")
	ctx := context.Background()
	fixed := time.Unix(1784271068, 0)
	prevNow := consoleNow
	consoleNow = func() time.Time { return fixed }
	t.Cleanup(func() { consoleNow = prevNow })

	r, err := s.ConsoleLabels(ctx, c, ConsoleMetaQuery{Limit: 3}, ConsoleLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Values) != 3 || r.Total != 5 || !r.Truncated || r.Values[2] != "v3" {
		t.Fatalf("limit 3: %+v", r)
	}
	req := f.last(t)
	if req.Form.Get("limit") != "4" || req.Form.Get("start") != "1784267468" || req.Form.Get("end") != "1784271068" || req.Form.Get("partial_response") != "false" {
		t.Fatalf("labels formu (limit+1, son 1 saat, partial_response): %v", req.Form)
	}
	if req.Method != http.MethodPost || req.Path != "/api/v1/labels" {
		t.Fatalf("labels POST olmalı: %+v", req)
	}

	// limit verilmezse 1000, tavan 10000; limit altında truncated=false.
	r, err = s.ConsoleLabels(ctx, c, ConsoleMetaQuery{}, ConsoleLimits{})
	if err != nil || r.Truncated || r.Total != 5 || len(r.Values) != 5 || f.last(t).Form.Get("limit") != "1001" {
		t.Fatalf("varsayılan limit: %+v %v %q", r, err, f.last(t).Form.Get("limit"))
	}
	if _, err := s.ConsoleLabels(ctx, c, ConsoleMetaQuery{Limit: 99999}, ConsoleLimits{}); err != nil || f.last(t).Form.Get("limit") != "10001" {
		t.Fatalf("limit tavanı 10000: %v %q", err, f.last(t).Form.Get("limit"))
	}

	// Label values: UTF-8 ad U__ kaçışı, GET.
	if _, err := s.ConsoleLabelValues(ctx, c, "k8s.pod.name", ConsoleMetaQuery{}, ConsoleLimits{}); err != nil {
		t.Fatal(err)
	}
	if req := f.last(t); req.Method != http.MethodGet || req.Path != "/api/v1/label/U__k8s_2e_pod_2e_name/values" {
		t.Fatalf("label values yolu/metodu: %+v", req)
	}
	if _, err := s.ConsoleLabelValues(ctx, c, "__name__", ConsoleMetaQuery{}, ConsoleLimits{}); err != nil || f.last(t).Path != "/api/v1/label/__name__/values" {
		t.Fatalf("eski sözdizimli ad aynen: %v %q", err, f.last(t).Path)
	}

	// Series: limit + warnings geçer.
	sr, err := s.ConsoleSeries(ctx, c, ConsoleMetaQuery{Match: []string{"up"}, Limit: 2}, ConsoleLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sr.Series) != 2 || sr.Total != 4 || !sr.Truncated || sr.Series[1]["a"] != "2" || len(sr.Warnings) != 1 {
		t.Fatalf("series limit 2: %+v", sr)
	}
}

// Yerel doğrulama: HTTP turu YAPILMADAN bad_data.
func TestConsoleLocalValidation(t *testing.T) {
	f := newFakeConsoleThanos(t, false, respondWith(200, seriesBody("vector", 0)))
	s := New()
	c := consoleTestCluster(f.URL, "")
	disabled := c
	disabled.Enabled = false
	start := time.Unix(1784267468, 0)
	ctx := context.Background()
	calls := []struct {
		name string
		call func() error
	}{
		{"boş sorgu", func() error {
			_, err := s.ConsoleQuery(ctx, c, ConsoleInstantQuery{Query: "  "}, ConsoleLimits{})
			return err
		}},
		{"aralık: start yok", func() error {
			_, err := s.ConsoleQueryRange(ctx, c, ConsoleRangeQuery{Query: "up", End: start, Step: time.Second}, ConsoleLimits{})
			return err
		}},
		{"aralık: end < start", func() error {
			_, err := s.ConsoleQueryRange(ctx, c, ConsoleRangeQuery{Query: "up", Start: start, End: start.Add(-time.Second), Step: time.Second}, ConsoleLimits{})
			return err
		}},
		{"aralık: step 0", func() error {
			_, err := s.ConsoleQueryRange(ctx, c, ConsoleRangeQuery{Query: "up", Start: start, End: start.Add(time.Hour)}, ConsoleLimits{})
			return err
		}},
		{"kapalı cluster", func() error {
			_, err := s.ConsoleQuery(ctx, disabled, ConsoleInstantQuery{Query: "up"}, ConsoleLimits{})
			return err
		}},
		{"label values: boş ad", func() error {
			_, err := s.ConsoleLabelValues(ctx, c, "", ConsoleMetaQuery{}, ConsoleLimits{})
			return err
		}},
		{"label values: geçersiz UTF-8", func() error {
			_, err := s.ConsoleLabelValues(ctx, c, "\xff", ConsoleMetaQuery{}, ConsoleLimits{})
			return err
		}},
		{"series: URL başına modelde seçici yok", func() error { _, err := s.ConsoleSeries(ctx, c, ConsoleMetaQuery{}, ConsoleLimits{}); return err }},
		{"metadata: end < start", func() error {
			_, err := s.ConsoleLabels(ctx, c, ConsoleMetaQuery{Start: start, End: start.Add(-time.Minute)}, ConsoleLimits{})
			return err
		}},
	}
	for _, cl := range calls {
		t.Run(cl.name, func(t *testing.T) {
			ce := asConsoleError(t, cl.call())
			if ce.Type != ConsoleErrBadData || ce.StatusCode() != http.StatusBadRequest {
				t.Fatalf("bad_data/400 beklenirdi: %+v", ce)
			}
		})
	}
	if n := f.count(); n != 0 {
		t.Fatalf("yerel doğrulama hatasında HTTP turu yapılmamalı; %d istek", n)
	}
}

// ── TLS: paylaşımlı istemcinin TLS davranışı birebir ────────────────────

func TestConsoleTLSSettings(t *testing.T) {
	f := newFakeConsoleThanos(t, true, respondWith(200, seriesBody("vector", 1)))
	c := consoleTestCluster(f.URL, "")
	c.InsecureSkipVerify = true
	if _, err := New().ConsoleQuery(context.Background(), c, ConsoleInstantQuery{Query: "up"}, ConsoleLimits{}); err != nil {
		t.Fatalf("skip-verify cluster'da öz-imzalı sertifika kabul edilmeli: %v", err)
	}
	c.InsecureSkipVerify = false
	_, err := New().ConsoleQuery(context.Background(), c, ConsoleInstantQuery{Query: "up"}, ConsoleLimits{})
	ce := asConsoleError(t, err)
	if ce.Type != ConsoleErrUnavailable || !strings.Contains(ce.Message, "TLS certificate verification failed") {
		t.Fatalf("doğrulayan cluster öz-imzalı sertifikayı reddetmeli: %+v", ce)
	}
	if strings.Contains(err.Error(), f.host()) || strings.Contains(err.Error(), "x509") {
		t.Fatalf("TLS hatası ham sızıyor: %q", err.Error())
	}
}

// POST'u gövdesiz GET'e çeviren yönlendirme İZLENMEZ (açık hata); metodu
// koruyan 307 izlenir.
func TestConsoleRedirectPolicy(t *testing.T) {
	f := newFakeConsoleThanos(t, false, func(w http.ResponseWriter, r *http.Request, req consoleReq) {
		switch req.Path {
		case "/found/api/v1/query":
			http.Redirect(w, r, "/login", http.StatusFound)
		case "/temp/api/v1/query":
			http.Redirect(w, r, "/api/v1/query", http.StatusTemporaryRedirect)
		default:
			if req.Form.Get("query") == "" {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"status":"error","errorType":"bad_data","error":"query missing"}`)
				return
			}
			fmt.Fprint(w, seriesBody("vector", 1))
		}
	})
	s := New()
	_, err := s.ConsoleQuery(context.Background(), consoleTestCluster(f.URL+"/found", ""), ConsoleInstantQuery{Query: "up"}, ConsoleLimits{})
	ce := asConsoleError(t, err)
	if ce.Type != ConsoleErrUnavailable || ce.UpstreamStatus != http.StatusFound || !strings.Contains(ce.Message, "redirected") {
		t.Fatalf("302 → açık 'redirected' hatası beklenirdi: %+v", ce)
	}
	res, err := s.ConsoleQuery(context.Background(), consoleTestCluster(f.URL+"/temp", ""), ConsoleInstantQuery{Query: "up"}, ConsoleLimits{})
	if err != nil || res.TotalSeries != 1 {
		t.Fatalf("307 metodu+gövdeyi korur, izlenmeli: %v %+v", err, res)
	}
}

// Karar 14: adanmış istemci; paylaşımlı 15 s istemciler DEĞİŞMEZ.
func TestConsoleLeavesSharedClientsAlone(t *testing.T) {
	f := newFakeConsoleThanos(t, false, respondWith(200, seriesBody("vector", 0)))
	if _, err := New().ConsoleQuery(context.Background(), consoleTestCluster(f.URL, ""), ConsoleInstantQuery{Query: "up"}, ConsoleLimits{Timeout: 90 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if thanosHTTPClient.Timeout != 15*time.Second || thanosClientFor(true).Timeout != 15*time.Second {
		t.Fatalf("paylaşımlı istemci zaman aşımları değişti: %v / %v", thanosHTTPClient.Timeout, thanosClientFor(true).Timeout)
	}
	for _, skip := range []bool{false, true} {
		cc := consoleHTTPClient(skip, 90*time.Second)
		if cc == thanosClientFor(skip) || cc.Timeout != 90*time.Second || cc.Transport != thanosClientFor(skip).Transport {
			t.Fatalf("skip=%v: konsol istemcisi adanmış olmalı, Transport paylaşımlı: %+v", skip, cc)
		}
	}
}

func TestConsoleLimitsNormalized(t *testing.T) {
	cases := []struct {
		in   ConsoleLimits
		want ConsoleLimits
	}{
		{ConsoleLimits{}, ConsoleLimits{Timeout: 30 * time.Second, MaxSeries: 500, MaxBodyBytes: 32 << 20}},
		{ConsoleLimits{Timeout: -1, MaxSeries: -1, MaxBodyBytes: -1}, ConsoleLimits{Timeout: 30 * time.Second, MaxSeries: 500, MaxBodyBytes: 32 << 20}},
		{ConsoleLimits{Timeout: time.Hour, MaxSeries: 1 << 20, MaxBodyBytes: 1 << 40, PartialResponse: true},
			ConsoleLimits{Timeout: 120 * time.Second, MaxSeries: 2000, MaxBodyBytes: 128 << 20, PartialResponse: true}},
		{ConsoleLimits{Timeout: 5 * time.Second, MaxSeries: 10, MaxBodyBytes: 1 << 20},
			ConsoleLimits{Timeout: 5 * time.Second, MaxSeries: 10, MaxBodyBytes: 1 << 20}},
	}
	for i, c := range cases {
		if got := c.in.normalized(); got != c.want {
			t.Errorf("#%d: %+v → %+v, beklenen %+v", i, c.in, got, c.want)
		}
	}
}

func TestConsoleFormatting(t *testing.T) {
	strs := []struct{ got, want string }{
		{formatPromTime(time.Unix(1784271068, 0)), "1784271068"},
		{formatPromTime(time.Unix(1784271068, 250e6)), "1784271068.250"},
		{formatPromStep(15 * time.Second), "15"},
		{formatPromStep(1500 * time.Millisecond), "1.5"},
		{formatPromTimeout(30 * time.Second), "30s"},
		{formatPromTimeout(1500 * time.Millisecond), "1500ms"},
		{labelNamePathSegment("job"), "job"},
		{labelNamePathSegment("__name__"), "__name__"},
		{labelNamePathSegment("k8s.pod.name"), "U__k8s_2e_pod_2e_name"},
		{labelNamePathSegment("1abc"), "U___31_abc"},
		{labelNamePathSegment("bölge"), "U__b_f6_lge"},
		{parsePromPosition(`invalid parameter "query": 3:14: parse error: unexpected`), "3:14"},
		{parsePromPosition("parse error at line 2, char 9: bad"), "2:9"},
		{parsePromPosition("store 10.0.0.1:10901: parse error: x"), ""},
		{parsePromPosition("many-to-many matching not allowed"), ""},
		{humanBytes(32 << 20), "32 MiB"},
		{humanBytes(1 << 10), "1 KiB"},
		{humanBytes(1500), "1500 bytes"},
	}
	for i, s := range strs {
		if s.got != s.want {
			t.Errorf("#%d: %q, beklenen %q", i, s.got, s.want)
		}
	}
	if g := consoleGrace(200 * time.Millisecond); g != 20*time.Millisecond {
		t.Errorf("pay 200ms → %v", g)
	}
	if g := consoleGrace(30 * time.Second); g != time.Second {
		t.Errorf("pay tavanı 1s → %v", g)
	}
}
