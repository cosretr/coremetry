package api

// chat_doc_rows_test.go — v0.10.1176 / v0.10.1183: tablo satırı kademesi.

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/rag"
	"github.com/cilcenk/coremetry/internal/wiki"
)

func docRowsFixture(t *testing.T) []docRowChunk {
	t.Helper()
	var b strings.Builder
	b.WriteString("HOSTNAME;DOMAIN;ENVIRONMENT;PRIMARY IP;USAGE PURPOSE;Not\n")
	b.WriteString("CCFWKAPP;AKNET;PROD;10.215.4.120;IIS SERVER;a|b\n")
	b.WriteString("srvappl01.corp.local;AKNET;PROD;10.1.1.1;kredi-onay-api;\n")
	b.WriteString("ABMVDICON1;AKNET;PROD;10.215.14.194;APPLICATION SERVER;\n")
	for i := 0; i < 60; i++ { // PROD / AKNET yaygın değerler
		b.WriteString("FILLER;AKNET;PROD;10.0.0.1;APPLICATION SERVER;\n")
	}
	text, err := rag.TableToText("envanter.csv", []byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	fn, _ := rag.TableToText("fonksiyon.csv", []byte("BT Ürün Adı;Sorumlu;Service Domain\nÇağrı Merkezi IVR;Mehmet Y.;Çağrı Merkezi\nATM;Levent T.;Şube\n"))
	return []docRowChunk{{Title: "envanter", Text: text}, {Title: "fonksiyon", Text: fn}}
}

func TestDocRowsCellValueMatch(t *testing.T) {
	chunks := docRowsFixture(t)
	cases := []struct {
		q      string
		strict bool
		want   []string // beklenen satırların ilk hücreleri
	}{
		{"CCFWKAPP sunucusunun ip adresi nedir", true, []string{"CCFWKAPP"}},  // rakamsız hostname (1183 kök neden)
		{"ccfwkapp hangi ortamda", false, []string{"CCFWKAPP"}},               // küçük harf: gevşek kipte
		{"srvappl01 hangi uygulama", true, []string{"srvappl01.corp.local"}},  // kimlik alt-dize
		{"ATM ürününün sorumlusu kim", true, []string{"ATM"}},                 // tek kelimelik ürün
		{"çağrı merkezi ivr sorumlusu", false, []string{"Çağrı Merkezi IVR"}}, // katlanmış çok kelime
		{"prod ortamındaki sunucular", false, nil},                            // yaygın değer satır seçmez
	}
	for _, c := range cases {
		rows, _ := matchDocRows(chunks, c.q, c.strict)
		var got []string
		for _, r := range rows {
			got = append(got, r.Cells[0].Val)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%q strict=%v: got %v want %v", c.q, c.strict, got, c.want)
		}
	}
	// erken (strict) kip küçük harfli düz kelimeyi almaz → genel soru kaçmaz
	if rows, _ := matchDocRows(chunks, "ccfwkapp hangi ortamda", true); len(rows) != 0 {
		t.Fatalf("strict kip küçük harfli düz kelimeyle satır seçmemeli: %+v", rows)
	}
	rows, vals := matchDocRows(chunks, "CCFWKAPP ip", true)
	md := docRowsMarkdown(vals, rows)
	if !strings.Contains(md, "| 2 | CCFWKAPP | AKNET | PROD | 10.215.4.120 | IIS SERVER | a\\|b |") {
		t.Fatalf("tablo:\n%s", md)
	}
}

func TestDocRowsWiring(t *testing.T) {
	b, _ := os.ReadFile("copilot_chat.go")
	src := string(b)
	w, d, g := strings.Index(src, "s.wikiChatAnswer(ctx, emit, req.Messages, wikiTC)"), strings.Index(src, "s.docRowsAnswer(ctx, emit, req.Messages, true)"), strings.Index(src, "s.ragChatAnswer(ctx, emit, req.Messages")
	// v0.10.1551 — sıkı satır kademesi wiki'den ÖNCE (birebir hücre eşleşmesi
	// varsa tüm satırlar; wiki anlatımı tek parçayı okuyordu).
	if w < 0 || d < 0 || g < 0 || !(d < w && w < g) {
		t.Fatal("sıra: sıkı satır kademesi → wiki kademesi → … → doküman adımı")
	}
	r, _ := os.ReadFile("rag.go")
	if !strings.Contains(string(r), "s.docRowsAnswer(ctx, emit, msgs, false)") {
		t.Fatal("doküman adımı gevşek satır aramasını denemeli")
	}
	if !hasLocalDocHit([]wiki.Hit{{ChunkRef: wiki.ChunkRef{WikiID: wiki.LocalWikiID}}}) || hasLocalDocHit(nil) {
		t.Fatal("hasLocalDocHit")
	}
}

// v0.10.1551 operator-reported: "CRMHouse ait vserver adreslerini getir" →
// 16 satırın hepsi (CC Owner = CRMHouse); karışık harfli kimlik güçlü; değer
// 50'den çok satırda da geçse (tek seçici değer) satırlar gelir.
func TestDocRowsMixedCaseOwnerAllRows(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 80; i++ {
		fmt.Fprintf(&b, "vserver · satır %d — Load Balancer: lb-%d_443 · Load Balancer IpAddress: 10.240.84.%d · Environment: PROD · CC Owner: CRMHouse · Application Name: Kampanya Datamarti\n", i+1, i, i)
	}
	b.WriteString("vserver · satır 99 — Load Balancer: other_443 · Load Balancer IpAddress: 10.0.0.1 · Environment: PROD · CC Owner: Payments\n")
	chunks := []docRowChunk{{Title: "vserver_cc_iliskisi", Text: b.String()}}
	rows, vals := matchDocRows(chunks, "CRMHouse ait vserver adreslerini getir", true)
	if len(rows) != 80 || len(vals) != 1 || vals[0] != "crmhouse" {
		t.Fatalf("satır %d, değerler %v", len(rows), vals)
	}
	for _, c := range []struct {
		w    string
		want bool
	}{{"CRMHouse", true}, {"OrderService", true}, {"orderId", true}, {"Opentelemetry", false}, {"Kampanya", false}, {"prod", false}} {
		if got := isMixedCaseIdent(c.w); got != c.want {
			t.Errorf("isMixedCaseIdent(%q) = %v", c.w, got)
		}
	}
}

// v0.10.1554 — parça sınırında bölünen satır tam sayfadan gelir; tam metni
// olmayan sayfa parçalarıyla kalır, aynı sayfanın parçaları tekilleşir.
func TestDocRowsMergeFullPage(t *testing.T) {
	row := func(i int) string {
		return fmt.Sprintf("vserver · satır %d — Load Balancer: lb-%d_443 · Environment: PROD · CC Owner: CRMHouse\n", i, i)
	}
	full := row(1) + row(2) + row(3)
	cut := strings.Index(full, "CC Owner: CRMHouse\nvserver · satır 3")
	chunks := []docRowChunk{
		{Title: "vserver", Path: "/vserver.xlsx", Text: full[:cut+3]}, // satır 2 "CC " ile kesik
		{Title: "vserver", Path: "/vserver.xlsx", Text: full[cut+3:]},
		{Title: "diger", Path: "/diger.xlsx", Text: "diger · satır 7 — Host: x · CC Owner: CRMHouse\n"},
	}
	if rows, _ := matchDocRows(chunks, "CRMHouse satırlarını getir", true); len(rows) != 3 {
		t.Fatalf("parçalardan %d satır (kesik satır 2 kaçar) — beklenen 3", len(rows))
	}
	merged := docRowsMergeFull(chunks, map[string]string{"/vserver.xlsx": full})
	if len(merged) != 2 || merged[0].Text != full || merged[1].Title != "diger" {
		t.Fatalf("birleşme: %+v", merged)
	}
	rows, vals := matchDocRows(merged, "CRMHouse satırlarını getir", true)
	if len(rows) != 4 || vals[0] != "crmhouse" {
		t.Fatalf("tam sayfadan %d satır %v — beklenen 4", len(rows), vals)
	}
}
