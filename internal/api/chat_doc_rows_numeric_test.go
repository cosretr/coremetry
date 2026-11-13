package api

// chat_doc_rows_numeric_test.go — v0.10.2112. Operator-reported (prod v2
// çekmecesi, 2026-11-13): "ekrana 10000 adet 1 yaz" envanter netleştirmesine
// düştü ("200 satırla eşleşiyor (10000)"). Kök neden: rowNeedles salt rakam
// jetonu kimlik biçimli (güçlü) sayıyordu; hücrede ALT-DİZE eşleşmesi her
// port / sayı / kimlik parçasını topluyordu. Salt rakam jeton (ve salt rakam
// hücre) artık tek başına eşleşme kurmaz; harf ya da ayraç taşıyan değerler
// (sunucu adı, IP, uygulama kodu, sürüm) aynen eşleşir.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/rag"
)

func numericDocRowsFixture(t *testing.T) []docRowChunk {
	t.Helper()
	var b strings.Builder
	b.WriteString("HOSTNAME;PORT;PRIMARY IP;APP CODE;VERSION\n")
	b.WriteString("appsrv01;10000;10.0.0.5;ACME10000;2.14.0\n")
	b.WriteString("websrv02;8080;10.0.0.6;ACME20000;3.1.0\n")
	for i := 0; i < 250; i++ { // 10000 alt-dizesini taşıyan çok satır (eski tavan 200)
		fmt.Fprintf(&b, "node%03d;%d;10.1.%d.%d;CODE%d;1.0.0\n", i, 100000+i, i/250, i%250, 10000+i)
	}
	text, err := rag.TableToText("envanter.csv", []byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	return []docRowChunk{{Title: "envanter", Text: text}}
}

func TestDocRowsNumericTokenDoesNotMatch(t *testing.T) {
	chunks := numericDocRowsFixture(t)
	for _, q := range []string{"ekrana 10000 adet 1 yaz", "10000", "bana 100000 kere merhaba de"} {
		for _, strict := range []bool{true, false} {
			if rows, vals := matchDocRows(chunks, q, strict); len(rows) != 0 {
				t.Errorf("%q strict=%v: salt sayı %d satır eşledi (%v)", q, strict, len(rows), vals)
			}
		}
		for _, n := range rowNeedles(q) {
			if isDigitsOnly(n.folded) {
				t.Errorf("%q: salt rakam aday %q", q, n.raw)
			}
		}
	}
}

func TestDocRowsMeaningfulValuesStillMatch(t *testing.T) {
	chunks := numericDocRowsFixture(t)
	for _, c := range []struct {
		q    string
		want string // ilk hücre
	}{
		{"appsrv01 hangi portta", "appsrv01"},       // sunucu adı
		{"10.0.0.5 hangi sunucu", "appsrv01"},       // IP (ayraçlı sayı)
		{"ACME10000 uygulaması nerede", "appsrv01"}, // harf + rakam uygulama kodu
		{"3.1.0 sürümü hangi sunucuda", "websrv02"}, // sürüm (kararsız: mevcut davranış)
	} {
		rows, _ := matchDocRows(chunks, c.q, true)
		if len(rows) == 0 || rows[0].Cells[0].Val != c.want {
			var got []string
			for _, r := range rows {
				got = append(got, r.Cells[0].Val)
			}
			t.Errorf("%q: got %v, want ilk satır %s", c.q, got, c.want)
		}
	}
}

func TestIsDigitsOnly(t *testing.T) {
	for in, want := range map[string]bool{"10000": true, "1": true, "": false, "10.0": false, "a1": false, "١٢": false} {
		if got := isDigitsOnly(in); got != want {
			t.Errorf("isDigitsOnly(%q) = %v", in, got)
		}
	}
}
