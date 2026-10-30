package tracechain

// critpath_test.go — v0.10.1625 — CoSRE kritik yol planı R1 (operatör: kritik
// yoldaki her span için ad, tür, servis, süre, yol-üstü öz süre ve pay; CLIENT
// için hedef url.full → server.address:server.port → peer.service →
// db.system/db.name → messaging.system/destination). v0.10.1639: DB CLIENT
// önce ve kendi kuralıyla — "<db.namespace|db.name>@<server.address|
// net.peer.name>", kısmi veride ad / <db.system>@sunucu / db.system; span
// adına asla düşmez (testdata/critpath/db-client-*.json).
//
// Pinlenen sözleşme:
//   - TargetOf operatörün sırasıyla çözer (PeerOf'un peer.service-önce sırası
//     DEĞİŞMEZ — o gruplama anahtarı, HATA ZİNCİRİ altınları ona bağlı);
//     SpanRow.DBName AI yolunda boş, db.name attribute'tan okunur.
//   - En büyük pay = en büyük yol-üstü öz süre / kök süresi; eşitlikte hata →
//     CLIENT/PRODUCER → en erken başlangıç → yol sırası.
//   - Dış bağımlılık beklemesi YALNIZ en büyük paylı adım CLIENT/PRODUCER ve
//     trace'te hiç çocuğu yoksa (operatör yanıtı 1 + 3: izlenen aşağı akış
//     SERVER'ı olan ya da çocuğu saat kaymasıyla zincirden düşen CLIENT dış
//     bağımlılık değildir).
//   - Block bayt bayt altın (12.21 s fikstürü), ≤ BlockMaxRunes, uzun zincirde
//     kök / en büyük pay / yaprak ve kapanış satırı her zaman kalır.
// Fikstürler sentetiktir.

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestTargetOf(t *testing.T) {
	cases := []struct {
		name  string
		sp    chstore.SpanRow
		want  Target
		attrs map[string]string
	}{
		{name: "url.full", attrs: map[string]string{"url.full": "https://gateway.ext.example/v1/score"},
			want: Target{Kind: "url", Value: "https://gateway.ext.example/v1/score", Source: "url.full"}},
		{name: "url.full + peer.service → url kazanır, Peer dolar", attrs: map[string]string{"url.full": "https://a.ext.example/x", "peer.service": "gateway"},
			want: Target{Kind: "url", Value: "https://a.ext.example/x", Source: "url.full", Peer: "gateway"}},
		{name: "http.url eski ad", attrs: map[string]string{"http.url": "http://legacy.ext.example:8080/v2"},
			want: Target{Kind: "url", Value: "http://legacy.ext.example:8080/v2", Source: "http.url"}},
		{name: "server.address + server.port", attrs: map[string]string{"server.address": "cache.ext.example", "server.port": "6380"},
			want: Target{Kind: "server", Value: "cache.ext.example:6380", Source: "server.address+server.port"}},
		{name: "net.peer.name + net.peer.port", attrs: map[string]string{"net.peer.name": "old.ext.example", "net.peer.port": "443"},
			want: Target{Kind: "server", Value: "old.ext.example:443", Source: "net.peer.name+net.peer.port"}},
		{name: "yalnız server.address", attrs: map[string]string{"server.address": "cache.ext.example"},
			want: Target{Kind: "server", Value: "cache.ext.example", Source: "server.address"}},
		{name: "http.host", attrs: map[string]string{"http.host": "host.ext.example"},
			want: Target{Kind: "server", Value: "host.ext.example", Source: "http.host"}},
		{name: "peer.service kolonu attribute'u yener", sp: chstore.SpanRow{PeerService: "billing"}, attrs: map[string]string{"peer.service": "other"},
			want: Target{Kind: "peer.service", Value: "billing", Source: "peer.service"}},
		{name: "peer.service attribute", attrs: map[string]string{"peer.service": "inventory"},
			want: Target{Kind: "peer.service", Value: "inventory", Source: "peer.service"}},
		// v0.10.1639 — DB CLIENT: "<db>@<sunucu>"; url.full geçmez, port eklenmez,
		// span adına asla düşülmez; takma adlar yeni → eski.
		{name: "db: ad + sunucu", attrs: map[string]string{"db.system": "postgresql", "db.name": "orders", "server.address": "db.ext.example", "server.port": "5432"},
			want: Target{Kind: "db", Value: "orders@db.ext.example", Source: "db.name+server.address"}},
		{name: "db: url.full geçmez", attrs: map[string]string{"db.system": "elasticsearch", "url.full": "https://search.ext.example:9200/orders/_search", "db.namespace": "orders", "server.address": "search.ext.example"},
			want: Target{Kind: "db", Value: "orders@search.ext.example", Source: "db.namespace+server.address"}},
		{name: "db: db.namespace db.name'i, server.address net.peer.name'i yener", attrs: map[string]string{"db.system.name": "postgresql", "db.namespace": "orders", "db.name": "legacy", "server.address": "db.ext.example", "net.peer.name": "old.ext.example"},
			want: Target{Kind: "db", Value: "orders@db.ext.example", Source: "db.namespace+server.address"}},
		{name: "db: eski adlar (db.name + net.peer.name)", sp: chstore.SpanRow{DBSystem: "oracle"}, attrs: map[string]string{"db.name": "ORDERSDB", "net.peer.name": "ora.ext.example", "net.peer.port": "1521"},
			want: Target{Kind: "db", Value: "ORDERSDB@ora.ext.example", Source: "db.name+net.peer.name"}},
		{name: "db: yalnız ad (DBName damgası AI yolunda yok)", sp: chstore.SpanRow{DBSystem: "oracle"}, attrs: map[string]string{"db.name": "ORDERSDB"},
			want: Target{Kind: "db", Value: "ORDERSDB", Source: "db.name"}},
		{name: "db: yalnız sunucu → <db.system>@sunucu", attrs: map[string]string{"db.system": "redis", "server.address": "cache.ext.example"},
			want: Target{Kind: "db", Value: "redis@cache.ext.example", Source: "db.system+server.address"}},
		{name: "db: yalnız db.system", attrs: map[string]string{"db.system": "redis"},
			want: Target{Kind: "db", Value: "redis", Source: "db.system"}},
		{name: "db: db.system.name eski adı yener", attrs: map[string]string{"db.system.name": "mysql", "db.system": "other"},
			want: Target{Kind: "db", Value: "mysql", Source: "db.system.name"}},
		{name: "db: peer.service ikincil ad", attrs: map[string]string{"db.system": "postgresql", "db.name": "orders", "peer.service": "orders-db"},
			want: Target{Kind: "db", Value: "orders", Source: "db.name", Peer: "orders-db"}},
		{name: "messaging.system + destination.name", attrs: map[string]string{"messaging.system": "kafka", "messaging.destination.name": "orders.audit"},
			want: Target{Kind: "messaging", Value: "kafka/orders.audit", Source: "messaging.system+messaging.destination.name"}},
		{name: "eski messaging.destination", attrs: map[string]string{"messaging.system": "rabbitmq", "messaging.destination": "q1"},
			want: Target{Kind: "messaging", Value: "rabbitmq/q1", Source: "messaging.system+messaging.destination"}},
		{name: "boşluklu değer kırpılır", attrs: map[string]string{"url.full": "  https://t.ext.example/  "},
			want: Target{Kind: "url", Value: "https://t.ext.example/", Source: "url.full"}},
		{name: "hiçbiri", want: Target{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sp := c.sp
			sp.Attributes = c.attrs
			if got := TargetOf(sp); got != c.want {
				t.Errorf("TargetOf = %+v, beklenen %+v", got, c.want)
			}
		})
	}
}

// cpSpan — ms ofsetli sentetik span.
func cpSpan(id, parent, svc, name, kind string, startMs, durMs int64) chstore.SpanRow {
	return chstore.SpanRow{SpanID: id, ParentSpanID: parent, ServiceName: svc, Name: name, Kind: kind,
		StartTime: startMs * 1_000_000, EndTime: (startMs + durMs) * 1_000_000}
}

func TestCriticalPathOfEdges(t *testing.T) {
	if cp := CriticalPathOf(nil); cp.OK || cp.Top != -1 || cp.Block() != "" {
		t.Errorf("boş girdi: %+v", cp)
	}
	if _, ok := (CriticalPath{Top: -1}).TopStep(); ok {
		t.Error("Top=-1 TopStep ok döndü")
	}
	// tek span CLIENT kök: kendisi yaprak, çocuğu yok → dış bağımlılık.
	one := CriticalPathOf([]chstore.SpanRow{cpSpan("x", "", "batch", "GET", "client", 0, 10)})
	if !one.OK || !one.TopIsExternalWait() || one.Steps[0].Share != 1 {
		t.Errorf("tek CLIENT: %+v", one)
	}
	// sıfır süreli kök: pay 0, bölme yok.
	z := CriticalPathOf([]chstore.SpanRow{cpSpan("z", "", "a", "op", "server", 5, 0)})
	if !z.OK || z.Steps[0].Share != 0 {
		t.Errorf("sıfır süre: %+v", z)
	}
	// node öz süresi (aralık birleşimi) ile yol-üstü öz süre ayrı: a[0,100]
	// çocukları b[10,60] c[40,90] → node öz 20, yol-üstü (zincir b) 50.
	cp := CriticalPathOf([]chstore.SpanRow{
		cpSpan("a", "", "s", "root", "server", 0, 100),
		cpSpan("b", "a", "s", "b", "internal", 10, 50),
		cpSpan("c", "a", "s", "c", "internal", 40, 50),
	})
	if cp.Steps[0].SelfNs != 20_000_000 || cp.Steps[0].SelfOnPathNs != 50_000_000 || !cp.Steps[0].HasChildren {
		t.Errorf("öz süreler: %+v", cp.Steps[0])
	}
}

func TestCriticalPathTopRule(t *testing.T) {
	cases := []struct {
		name  string
		spans []chstore.SpanRow
		want  string
	}{
		{"en büyük pay", []chstore.SpanRow{cpSpan("r", "", "s", "r", "server", 0, 100), cpSpan("c", "r", "s", "c", "client", 10, 80)}, "c"},
		{"eşitlikte hata önce", func() []chstore.SpanRow {
			r := cpSpan("r", "", "s", "r", "server", 0, 100)
			c := cpSpan("c", "r", "s", "c", "internal", 25, 50)
			c.StatusCode = "error"
			return []chstore.SpanRow{r, c}
		}(), "c"},
		{"eşitlikte CLIENT önce", []chstore.SpanRow{cpSpan("r", "", "s", "r", "server", 0, 100), cpSpan("c", "r", "s", "c", "client", 25, 50)}, "c"},
		{"eşitlikte en erken (yol sırası)", []chstore.SpanRow{cpSpan("r", "", "s", "r", "internal", 0, 100), cpSpan("c", "r", "s", "c", "internal", 25, 50)}, "r"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			top, ok := CriticalPathOf(c.spans).TopStep()
			if !ok || top.SpanID != c.want {
				t.Errorf("Top = %q, beklenen %q", top.SpanID, c.want)
			}
		})
	}
}

// cpGatewayFixture — testdata/critpath/external-gateway-12s.json ile aynı ağaç.
func cpGatewayFixture() []chstore.SpanRow {
	r := cpSpan("r1", "", "orders-api", "POST /orders/score", "server", 0, 12460)
	r.Attributes = map[string]string{"http.request.method": "POST", "http.route": "/orders/score", "http.response.status_code": "200"}
	i := cpSpan("i1", "r1", "orders-api", "com.acme.orders.ScoreService.evaluate", "internal", 100, 12300)
	c := cpSpan("c1", "i1", "orders-api", "POST gateway.ext.example", "client", 150, 12210)
	c.Attributes = map[string]string{"url.full": "https://gateway.ext.example/v1/score", "server.address": "gateway.ext.example", "server.port": "443", "http.request.method": "POST", "http.response.status_code": "200"}
	d := cpSpan("d1", "i1", "orders-api", "SELECT orders", "client", 110, 40)
	d.Attributes = map[string]string{"db.system": "postgresql", "db.name": "ordersdb", "server.address": "pg.internal.example"}
	p := cpSpan("p1", "r1", "orders-api", "orders.audit publish", "producer", 12400, 200)
	p.Attributes = map[string]string{"messaging.system": "kafka", "messaging.destination.name": "orders.audit"}
	return []chstore.SpanRow{r, i, c, d, p}
}

func TestCriticalPathBlockGolden(t *testing.T) {
	want := CritHeader + " Kök duvar saati 12.46 s; adımlar kök → yaprak; öz = adımın yolda tek başına geçen süresi, pay = kök süresine oranı; süreler TOPLANMAZ:\n" +
		"1. [kök] orders-api · SERVER · POST /orders/score · süre 12.46 s · öz 160 ms (%1) · HTTP 200\n" +
		"2. orders-api · INTERNAL · com.acme.orders.ScoreService.evaluate · süre 12.30 s · öz 90 ms (%1)\n" +
		"3. [en büyük pay] orders-api · CLIENT · POST gateway.ext.example · süre 12.21 s · öz 12.21 s (%98) · hedef https://gateway.ext.example/v1/score · HTTP 200\n" +
		"En büyük pay: orders-api · CLIENT · hedef https://gateway.ext.example/v1/score · 12.21 s (%98) — dış bağımlılık beklemesi (çağıranın altında izlenen çocuk span yok)"
	cp := CriticalPathOf(cpGatewayFixture())
	if got := cp.Block(); got != want {
		t.Errorf("blok:\n%s\n--- beklenen ---\n%s", got, want)
	}
	if got := cp.ExternalWaitLine(); got != "dış bağımlılık bekleme: https://gateway.ext.example/v1/score 12.21 s" {
		t.Errorf("ExternalWaitLine = %q", got)
	}
}

func TestCriticalPathBlockClosingVariants(t *testing.T) {
	base := cpGatewayFixture()
	down := append(append([]chstore.SpanRow(nil), base...), cpSpan("s2", "c1", "scoring-svc", "POST /v1/score", "server", 200, 12100))
	skew := append(append([]chstore.SpanRow(nil), base...), cpSpan("s2", "c1", "scoring-svc", "POST /v1/score", "server", 200, 12180))
	cases := []struct {
		name, want, not string
		spans           []chstore.SpanRow
	}{
		{"aşağı akış SERVER → servis içi", "En büyük pay: scoring-svc · SERVER · POST /v1/score · öz 12.10 s (%97) — servis içi süre", "dış bağımlılık", down},
		{"saat kayması → bağımlı servis", "En büyük pay: orders-api · CLIENT · hedef https://gateway.ext.example/v1/score · 12.21 s (%98) — bağımlı servis beklemesi: scoring-svc (çocuk span zincire girmedi: saat kayması ya da ebeveynden sonra bitti)", "dış bağımlılık", skew},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cp := CriticalPathOf(c.spans)
			b := cp.Block()
			lines := strings.Split(b, "\n")
			if last := lines[len(lines)-1]; last != c.want {
				t.Errorf("kapanış:\n%s\n--- beklenen ---\n%s", last, c.want)
			}
			if strings.Contains(b, c.not) || cp.ExternalWaitLine() != "" {
				t.Errorf("%q geçmemeli / önek boş olmalı:\n%s", c.not, b)
			}
		})
	}
}

func TestCriticalPathBlockCaps(t *testing.T) {
	// 30 adımlı zincir, en büyük pay ortada (adım 17), uzun adlar.
	var spans []chstore.SpanRow
	for i := 0; i < 30; i++ {
		parent := ""
		if i > 0 {
			parent = fmt.Sprintf("s%02d", i-1)
		}
		dur := int64(3000 - i*10)
		if i > 17 {
			dur = int64(3000 - 17*10 - 2000 - (i-17)*10)
		}
		spans = append(spans, cpSpan(fmt.Sprintf("s%02d", i), parent, "svc-"+strings.Repeat("ç", 40), fmt.Sprintf("op%02d-%s", i, strings.Repeat("x", 60)), "internal", int64(i*5), dur))
	}
	cp := CriticalPathOf(spans)
	if len(cp.Steps) != 30 {
		t.Fatalf("zincir %d adım", len(cp.Steps))
	}
	top, _ := cp.TopStep()
	if top.SpanID != "s17" {
		t.Fatalf("Top = %s", top.SpanID)
	}
	b := cp.Block()
	if n := utf8.RuneCountInString(b); n > BlockMaxRunes {
		t.Errorf("blok %d rune > %d", n, BlockMaxRunes)
	}
	for _, want := range []string{"1. [kök]", "18. [en büyük pay]", "30. ", "adım atlandı", "\nEn büyük pay: "} {
		if !strings.Contains(b, want) {
			t.Errorf("blok %q taşımıyor:\n%s", want, b)
		}
	}
	if strings.HasSuffix(b, "…") {
		t.Errorf("kapanış satırı kesildi:\n%s", b)
	}
}

func TestCriticalPathBlockFenceSafe(t *testing.T) {
	cp := CriticalPathOf([]chstore.SpanRow{cpSpan("r", "", "s", "GET /```x```", "server", 0, 10)})
	if strings.Contains(cp.Block(), "```") {
		t.Errorf("çit kaçmadı: %s", cp.Block())
	}
}
