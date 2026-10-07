package chstore

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	chproto "github.com/ClickHouse/clickhouse-go/v2/lib/proto"
)

// ch_bind_roundtrip_test.go — v0.10.1117. v0.8.356 clickhouse-go PARAMETRE
// TUZAĞININ sürücü düzeyinde round-trip'i.
//
// Tuzak: sürücü (query_parameters.go bindQueryOrAppendParameters) HAM sorgu
// metni `{.+:.+}` desenine uyarsa ifadeyi sunucu tarafı parametre kipine alır
// ve her konumsal argüman "unsupported query parameter type" ile düşer — v0.8.356
// "Group by shape" bunu yaşadı (satır içi `[0-9a-fA-F]{8}` + `':id'`).
// Şekil süzgeci (http.route_shape / name_shape) opSigWrap'in `':id'`
// sabitlerini /traces sorgularına taşıdığından, testler yalnız parçanın süslü
// parantezsiz olduğuna bakmakla yetinmez: GERÇEK sürücü, listenin kullandığı
// bağlama yolundan (driver.Conn.Query → bindQueryOrAppendParameters → bind)
// geçirilir. Sunucu yerine sahte bir http.RoundTripper (clickhouse.HTTP +
// Options.TransportFunc): el sıkışma sorgusuna Native bir blokla cevap verir,
// diğer her sorgunun BAĞLANMIŞ metnini kaydedip boş gövde döner. Store
// metotları (GetTraces, TraceErrorHistogram, QuerySpanMetricMulti) bu
// bağlantıyla koşar — sorgu metnini kuran kodun TAMAMI (WHERE + HAVING + SELECT
// + SETTINGS) sürücünün desen kontrolünden geçer.

// bindRecorder — sahte ClickHouse HTTP uç noktası: bağlanmış sorgu metinlerini
// kaydeder.
type bindRecorder struct {
	mu      sync.Mutex
	queries []string
}

func (b *bindRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	q := string(body)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Request: req}
	if strings.Contains(q, "displayName(), version(), revision(), timezone()") {
		hello := helloNativeBlock()
		resp.Body, resp.ContentLength = io.NopCloser(strings.NewReader(string(hello))), int64(len(hello))
		return resp, nil
	}
	b.mu.Lock()
	b.queries = append(b.queries, q)
	b.mu.Unlock()
	if strings.HasPrefix(strings.TrimSpace(q), "SELECT count() FROM (") {
		one := countNativeBlock()
		resp.Body, resp.ContentLength = io.NopCloser(strings.NewReader(string(one))), int64(len(one))
		return resp, nil
	}
	resp.Body, resp.ContentLength = io.NopCloser(strings.NewReader("")), 0
	return resp, nil
}

func (b *bindRecorder) reset() {
	b.mu.Lock()
	b.queries = nil
	b.mu.Unlock()
}

func (b *bindRecorder) all() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.queries...)
}

// bindRoundTripRevision — el sıkışmada döndürülen sunucu revizyonu; sonraki
// cevaplar bununla kodlanır (sürücü handshake'ten sonra onu kullanır).
const bindRoundTripRevision = 54472

// nativeCol — Native blokta tek satırlık bir sütun (ad, tip, kodlanmış değer).
type nativeCol struct {
	name, typ string
	data      []byte
}

func nativeStr(s string) []byte {
	return append(binary.AppendUvarint(nil, uint64(len(s))), s...)
}

// nativeBlock — tek satırlık Native blok, verilen revizyonla (lib/proto
// Block.Encode biçimi): blok bilgisi, sütun/satır sayısı, sütun başına ad +
// tip + özel serileştirme bayrağı + veri.
func nativeBlock(rev uint64, cols ...nativeCol) []byte {
	var b []byte
	uv := func(x uint64) { b = binary.AppendUvarint(b, x) }
	if rev > 0 { // blok bilgisi: alan 1 is_overflows=false, alan 2 bucket_num=-1, son
		uv(1)
		b = append(b, 0)
		uv(2)
		b = binary.LittleEndian.AppendUint32(b, 0xFFFFFFFF)
		uv(0)
	}
	uv(uint64(len(cols)))
	uv(1)
	for _, c := range cols {
		b = append(b, nativeStr(c.name)...)
		b = append(b, nativeStr(c.typ)...)
		if rev >= chproto.DBMS_MIN_REVISION_WITH_CUSTOM_SERIALIZATION {
			b = append(b, 0)
		}
		b = append(b, c.data...)
	}
	return b
}

// helloNativeBlock — el sıkışma sorgusunun (SELECT displayName(), version(),
// revision(), timezone()) cevabı, sürücünün o anda kullandığı revizyonla
// (ClientTCPProtocolVersion).
func helloNativeBlock() []byte {
	return nativeBlock(uint64(clickhouse.ClientTCPProtocolVersion),
		nativeCol{"displayName()", "String", nativeStr("bind-roundtrip")},
		nativeCol{"version()", "String", nativeStr("24.8.1.1")},
		nativeCol{"revision()", "UInt32", binary.LittleEndian.AppendUint32(nil, bindRoundTripRevision)},
		nativeCol{"timezone()", "String", nativeStr("UTC")},
	)
}

// countNativeBlock — `SELECT count() FROM (…)` tek-değer sorgularına 1
// (QueryRow.Scan "no rows" ile erken dönmesin; Errors şeridinin probu span
// kipine geçip asıl histogram sorgusunu da sürücüden geçirsin).
func countNativeBlock() []byte {
	return nativeBlock(bindRoundTripRevision, nativeCol{"count()", "UInt64", binary.LittleEndian.AppendUint64(nil, 1)})
}

// newBindRoundTripConn — gerçek clickhouse-go sürücüsü, sahte HTTP taşıyıcı.
func newBindRoundTripConn(t *testing.T) (driver.Conn, *bindRecorder) {
	t.Helper()
	rec := &bindRecorder{}
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr:          []string{"bind-roundtrip.invalid:8123"},
		Protocol:      clickhouse.HTTP,
		TransportFunc: func(*http.Transport) (http.RoundTripper, error) { return rec, nil },
	})
	if err != nil {
		t.Fatalf("clickhouse.Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, rec
}

// isBindErr — sürücünün bağlama aşamasında (sorgu gönderilmeden) verdiği
// hatalar: tuzak (sunucu tarafı parametre kipi), karışık biçim, eksik argüman.
func isBindErr(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, clickhouse.ErrUnsupportedQueryParameter) ||
		errors.Is(err, clickhouse.ErrBindMixedParamsFormats) ||
		strings.Contains(err.Error(), "bindQueryOrAppendParameters") ||
		strings.Contains(err.Error(), "have no arg for")
}

// boundShape — şekil yükleminin sürücü bağladıktan SONRAKİ metni: desenler ve
// değer tek tırnaklı sabitler olarak yerinde.
func boundShape(col, value string) string {
	s := wrapped(col) + " = ?"
	for _, a := range []string{OpSigReUUID, OpSigReHex, OpSigReNum, value} {
		s = strings.Replace(s, "?", "'"+a+"'", 1)
	}
	return s
}

func findQuery(qs []string, needle string) bool {
	for _, q := range qs {
		if strings.Contains(q, needle) {
			return true
		}
	}
	return false
}

// Harness kontrolü: tuzak bu taşıyıcıda GERÇEKTEN tetiklenir (v0.8.356'nın
// satır içi deseni) ve doğru kalıp (bağlı desen) geçer. Bu iki satır olmadan
// aşağıdaki "hata yok" iddiaları hiçbir şey kanıtlamazdı.
//
// Not — tetikleyicinin şekli: `{.+:.+}` satır içinde bir `{`, SONRA bir `:`,
// SONRA bir `}` ister (`.` satır sonunu geçmez). v0.8.356 öncesi opSigWrap
// UUID deseninin `{8}`ini, ardından `':id'`yi, ardından hex deseninin
// `{16,}`ını aynı satıra diziyordu — üçü birden. Tek desen + `':id'` (sonrasında
// `}` yok) tetiklemez; kontrol bu yüzden tam v0.8.356 öncesi metni kurar.
func TestBindRoundTripHarnessCatchesParamTrap(t *testing.T) {
	conn, rec := newBindRoundTripConn(t)
	ctx := context.Background()
	pre356 := "replaceRegexpAll(replaceRegexpAll(replaceRegexpAll(http_route, '" + OpSigReUUID + "', ':id'), '" +
		OpSigReHex + "', '/:id'), '" + OpSigReNum + "', '/:id')"
	inlined := "SELECT count() FROM spans WHERE " + pre356 + " = ? LIMIT 1"
	if _, err := conn.Query(ctx, inlined, routeShape); !errors.Is(err, clickhouse.ErrUnsupportedQueryParameter) {
		t.Fatalf("satır içi desen (v0.8.356 öncesi) tuzağa düşmeliydi, hata: %v", err)
	}
	if len(rec.all()) != 0 {
		t.Fatalf("tuzaklı sorgu sunucuya gitmemeliydi: %q", rec.all())
	}
	safe := "SELECT count() FROM spans WHERE " + opSigWrap("http_route") + " = ? LIMIT 1"
	rows, err := conn.Query(ctx, safe, append(opSigArgs(), routeShape)...)
	if err != nil {
		t.Fatalf("bağlı desen geçmeliydi: %v", err)
	}
	_ = rows.Close()
	if qs := rec.all(); len(qs) != 1 || !strings.Contains(qs[0], boundShape("http_route", routeShape)) {
		t.Fatalf("bağlanmış metin beklenen yüklemi taşımıyor:\n%q\nwant ⊃ %s", qs, boundShape("http_route", routeShape))
	}
}

// Şekil çipi /traces'in her okuma yolunda sürücüden geçer: liste (ham WHERE,
// HTTP katmanının sayımsız kipi), arama + çip (trace düzeyi HAVING), Errors
// şeridi (prob + histogram) ve hacim şeridi (metric-batch, ham spans).
// Kullanıcı değeri süslü parantez / iki nokta taşısa bile (`/users/{id}:x`)
// bağlıdır, tuzağa düşmez; başka çiplerle (süslü regex değeri, cluster
// türetmesi) aynı satırda da.
func TestShapeFilterDriverBindRoundTrip(t *testing.T) {
	from := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	for _, tc := range []struct {
		name, key, col, value string
		extra                 []FilterExpr
	}{
		{name: "route shape", key: RouteShapeFilterKey, col: "http_route", value: routeShape},
		{name: "süslü değer", key: RouteShapeFilterKey, col: "http_route", value: "/users/{id}:x"},
		{name: "name shape", key: NameShapeFilterKey, col: "name", value: "process order/:id"},
		{name: "diğer çiplerle", key: RouteShapeFilterKey, col: "http_route", value: routeShape, extra: []FilterExpr{
			{Key: "http.target", Op: "=~", Values: []string{"^/v[0-9]{1,2}/users/.+"}},
			{Key: "cluster", Op: "=", Values: []string{"eu-1"}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, rec := newBindRoundTripConn(t)
			s := &Store{conn: conn}
			ctx := context.Background()
			chips := append([]FilterExpr{{Key: tc.key, Op: "=", Values: []string{tc.value}}}, tc.extra...)
			want := boundShape(tc.col, tc.value)
			dump := func() string { return strings.Join(rec.all(), "\n---\n") }

			// Liste — ham WHERE (HTTP katmanı CountMode=skip gönderir).
			f := TraceFilter{Service: "svc-orders", Filters: chips, From: from, To: to, Limit: 50, CountMode: "skip"}
			if _, _, _, err := s.GetTraces(ctx, f); err != nil {
				t.Fatalf("liste: %v (bağlama hatası mı: %v)", err, isBindErr(err))
			}
			if !findQuery(rec.all(), "AS root_name") || !findQuery(rec.all(), want) {
				t.Fatalf("liste sorgusu bağlanmış yüklemi taşımıyor (want ⊃ %s):\n%s", want, dump())
			}

			// Arama + çip — trace düzeyi HAVING.
			rec.reset()
			fs := f
			fs.Search = "timeout"
			if _, _, _, err := s.GetTraces(ctx, fs); isBindErr(err) {
				t.Fatalf("arama + çip bağlama hatası: %v", err)
			}
			if !findQuery(rec.all(), "countIf("+want+") > 0") {
				t.Fatalf("HAVING bağlanmış yüklemi taşımıyor:\n%s", dump())
			}

			// Errors şeridi — listeyle aynı süzgeç (prob + histogram).
			rec.reset()
			fe := TraceFilter{Service: "svc-orders", Filters: chips, From: from, To: to, HasError: true}
			if _, err := s.TraceErrorHistogram(ctx, fe, 60, 0.5); err != nil {
				t.Fatalf("Errors şeridi: %v (bağlama hatası mı: %v)", err, isBindErr(err))
			}
			if !findQuery(rec.all(), "GROUP BY bucket") || !findQuery(rec.all(), want) {
				t.Fatalf("Errors şeridi bağlanmış yüklemi taşımıyor:\n%s", dump())
			}

			// Hacim şeridi (metric-batch) — ham spans.
			rec.reset()
			bf := SpanMetricBatchFilter{
				From: from, To: to, StepSeconds: 60,
				Filters: append([]FilterExpr{{Key: "service.name", Op: "=", Values: []string{"svc-orders"}}}, chips...),
				Aggs:    []SpanMetricAggSpec{{Name: "count", Aggregation: "count"}},
			}
			if _, _, err := s.QuerySpanMetricMulti(ctx, bf); err != nil {
				t.Fatalf("hacim şeridi: %v (bağlama hatası mı: %v)", err, isBindErr(err))
			}
			if !findQuery(rec.all(), want) {
				t.Fatalf("hacim şeridi bağlanmış yüklemi taşımıyor:\n%s", dump())
			}
		})
	}
}
