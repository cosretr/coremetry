package logql

// lucene_test.go — v0.10.1819 (Log Search Faz 1 dilim 1.3, audit §3.3).
//
// KQL yazımları → kanonik Lucene (ES) + CH filtre modeli; Lucene geçiş kipi;
// ES'e gitmeden yakalanan yapısal kırıklar (konum + düz Türkçe). Altın test
// (TestPreExistingCorpusGolden): v0.10.1819 öncesi her test girdisi ES'e
// AYNI metinle gider — tek istisna küçük harf operatörlerin büyütülmesi
// (açık soru §9-7 kararı).

import (
	"flag"
	"os"
	"regexp"
	"strings"
	"testing"
)

var updateESGolden = flag.Bool("update", false, "testdata/es_query.golden'ı yeniden yaz")

// KQL yazımları ayrışır ve CH'nin kullandığı AST'yi (String() kanonik
// yazımıyla pinli) üretir.
func TestKQLSpellingsParse(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"boşluklu alan kısa ad", `level: error`, `level:error`},
		{"boşluklu alan noktalı", `service.name: orders-api`, `service.name:orders-api`},
		{"boşluklu alan çok boşluk", `log.level:   ERROR`, `log.level:ERROR`},
		{"boşluklu alan altçizgi", `http_status: 500`, `http_status:500`},
		{"boşluklu alan tırnaklı değer", `message: "connection refused"`, `message:"connection refused"`},
		{"boşluklu alan grup", `level: (error or warn)`, `level:error OR level:warn`},
		{"boşluklu alan joker", `k8s.pod.name: branch-teller-*`, `k8s.pod.name:branch-teller-*`},
		{"KQL >=", `latency_ms >= 500`, `latency_ms:>=500`},
		{"KQL >", `latency_ms > 5`, `latency_ms:>5`},
		{"KQL <", `latency_ms < 5`, `latency_ms:<5`},
		{"KQL <=", `http.status_code <= 499`, `http.status_code:<=499`},
		{"KQL karşılaştırma tırnaklı", `region >= "eu"`, `region:>="eu"`},
		{"KQL karşılaştırma + AND", `env: prod and status >= 400`, `env:prod AND status:>=400`},
		{"is-one-of küçük harf or", `service.name:(orders-api or payments-mobile)`, `service.name:orders-api OR service.name:payments-mobile`},
		{"not + grup", `not (level:debug or level:info)`, `NOT (level:debug OR level:info)`},
		{"varlık", `trace.id:*`, `_exists_:trace.id`},
		{"iç içe parantez küçük harf", `((a or b) and (c or not d))`, `(a OR b) AND (c OR NOT d)`},
		{"alan grubu + not alan", `level:(error or warn) and not service: payments-mobile`, `(level:error OR level:warn) AND NOT service:payments-mobile`},
		// Düz metin olarak KALANLAR (eski CH anlamı korunur).
		{"ERROR: boom düz metin kalır", `ERROR: boom`, `ERROR: AND boom`},
		{"Caused by: düz metin kalır", `Caused by: java.lang.NullPointerException`, `Caused AND by: AND java.lang.NullPointerException`},
		{"boşluklu alan + operatör alan değil", `level: AND x`, `level: AND x`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.in, err)
			}
			if got := e.String(); got != tc.want {
				t.Errorf("Parse(%q).String() = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestToES(t *testing.T) {
	for _, tc := range []struct {
		name, in, out string
		lang          Lang
		force         bool
	}{
		{"boş", ``, ``, LangKQL, false},
		{"düz metin aynen", `timeout`, `timeout`, LangKQL, false},
		{"büyük harf operatör aynen", `level:error AND NOT service.name:"x"`, `level:error AND NOT service.name:"x"`, LangKQL, false},
		{"küçük harf and/or/not büyür", `level:error and not service.name:x or y`, `level:error AND NOT service.name:x OR y`, LangKQL, false},
		{"alan grubunda küçük or", `service.name:(orders-api or payments-mobile)`, `service.name:(orders-api OR payments-mobile)`, LangKQL, false},
		{"boşluklu alan sıkışır", `level: error`, `level:error`, LangKQL, false},
		{"boşluklu alan tırnaklı", `message:  "connection refused" and level:error`, `message:"connection refused" AND level:error`, LangKQL, false},
		{"KQL karşılaştırma", `latency_ms >= 500`, `latency_ms:>=500`, LangKQL, false},
		{"KQL karşılaştırma + kalan aynen", `status > 499 AND (a OR b)`, `status:>499 AND (a OR b)`, LangKQL, false},
		{"aralık küçük to büyür", `status:[400 to 499]`, `status:[400 TO 499]`, LangKQL, false},
		{"joker + varlık aynen", `k8s.pod.name:branch-teller-* trace.id:*`, `k8s.pod.name:branch-teller-* trace.id:*`, LangKQL, false},
		{"kaçış aynen", `path:C\:\\tmp`, `path:C\:\\tmp`, LangKQL, false},
		{"tırnak içindeki and dokunulmaz", `"cash and carry"`, `"cash and carry"`, LangKQL, false},
		{"ERROR: boom aynen", `ERROR: boom`, `ERROR: boom`, LangKQL, false},
		{"fuzzy → Lucene aynen (küçük and dahil)", `foo~2 and bar`, `foo~2 and bar`, LangLucene, false},
		{"boost → Lucene", `level:error^3`, `level:error^3`, LangLucene, false},
		{"regex → Lucene", `body:/time.?out/`, `body:/time.?out/`, LangLucene, false},
		{"zorunlu + → Lucene", `+level:error -x`, `+level:error -x`, LangLucene, false},
		{"ayrışmayan ama sağlam → Lucene (alan içinde alan)", `a:(b:c)`, `a:(b:c)`, LangLucene, false},
		{"zorla Lucene: küçük and büyümez", `a and b`, `a and b`, LangLucene, true},
		{"zorla Lucene: kırık da geçer (ES karar verir)", `(a OR`, `(a OR`, LangLucene, true},
		{"http şeması Lucene değil", `http://host/x`, `http://host/x`, LangKQL, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr, pe := ToES(tc.in, tc.force)
			if pe != nil {
				t.Fatalf("ToES(%q): %v", tc.in, pe)
			}
			if tr.Query != tc.out || tr.Lang != tc.lang {
				t.Errorf("ToES(%q) = (%q, %s); want (%q, %s)", tc.in, tr.Query, tr.Lang, tc.out, tc.lang)
			}
		})
	}
}

// Yapısal kırıklar ES'e gitmeden: konum + düz Türkçe cümle. Aynı cümle CH
// yolunda da (Check).
func TestStructuralErrors(t *testing.T) {
	for _, tc := range []struct {
		name, in, msg string
		pos           int
	}{
		{"kapanmamış parantez", `level:error AND (foo OR bar`, "( kapatılmamış. Sonuna ) ekleyin.", 16},
		{"iç içe kapanmamış", `((a OR b)`, "( kapatılmamış. Sonuna ) ekleyin.", 0},
		{"fazla kapanış", `a) b`, "Fazladan ). Bu ) karakterini silin ya da öncesine ( ekleyin.", 1},
		{"kapanmamış tırnak", `message:"disk full`, "\" kapatılmamış. Sonuna \" ekleyin.", 8},
		{"kapanmamış aralık", `status:[400 TO 499`, "[ kapatılmamış. Aralığı ] ya da } ile kapatın.", 7},
		{"boş parantez", `a ()`, "Boş parantez. İçine bir ifade yazın ya da () silin.", 2},
		{"sonda AND", `level:error AND`, "Sorgu AND ile bitiyor. Sonuna bir terim ekleyin ya da AND kelimesini silin.", 12},
		{"sonda küçük and", `level:error and`, "Sorgu AND ile bitiyor. Sonuna bir terim ekleyin ya da AND kelimesini silin.", 12},
		{"başta OR", `OR level:error`, "Sorgu OR ile başlıyor. Önüne bir terim ekleyin ya da OR kelimesini silin.", 0},
		{"sonda NOT", `a AND NOT`, "Sorgu NOT ile bitiyor. Sonuna bir terim ekleyin ya da NOT kelimesini silin.", 6},
		{"art arda operatör", `a AND OR b`, "Art arda iki operatör (AND OR). Birini silin.", 6},
		{"parantez önünde asılı", `(a OR ) b`, "OR sonrasında ifade yok. ) önüne bir terim ekleyin ya da OR kelimesini silin.", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, pe := ToES(tc.in, false)
			if pe == nil {
				t.Fatalf("ToES(%q) hata vermedi", tc.in)
			}
			if pe.Msg != tc.msg || pe.Pos != tc.pos {
				t.Errorf("ToES(%q) = (%d, %q); want (%d, %q)", tc.in, pe.Pos, pe.Msg, tc.pos, tc.msg)
			}
			ce := Check(tc.in)
			if ce == nil || ce.Msg != tc.msg || ce.Pos != tc.pos {
				t.Errorf("Check(%q) = %v; ToES ile aynı olmalı", tc.in, ce)
			}
		})
	}
	// Tırnak / regex / kaçış içindeki parantezler sayılmaz → kırık değil.
	for _, ok := range []string{`"a (b"`, `body:/x(y/`, `a\(b`, `status:[1 TO 5}`, `status:{1 TO 5]`} {
		if pe := Lint(ok); pe != nil {
			t.Errorf("Lint(%q) yanlış alarm: %v", ok, pe)
		}
	}
	// CH: ayrışmayan ama yapısal sağlam → ayrıştırıcının kendi mesajı.
	if ce := Check(`a:(b:c)`); ce == nil || !strings.Contains(ce.Msg, "alan içinde alan") {
		t.Errorf("Check alan içinde alan: %v", ce)
	}
	if Check(`level: error`) != nil || Check("") != nil {
		t.Error("geçerli/boş sorgu Check hatası vermemeli")
	}
}

func TestLuceneOnly(t *testing.T) {
	for in, want := range map[string]bool{
		`foo~2`: true, `"a b"~3`: true, `x^2`: true, `/re.*/`: true, `body:/re/`: true, `+must`: true,
		`a-b+c`: false, `"a ~ b"`: false, `a\~b`: false, `http://h/x`: false, `level:error`: false, `12:30/4`: false,
	} {
		if got := LuceneOnly(in); got != want {
			t.Errorf("LuceneOnly(%q) = %v; want %v", in, got, want)
		}
	}
}

// preExistingCorpus — v0.10.1819 ÖNCESİ testlerin ve altın URL listesinin
// sorgu girdileri (parse_test TestParseCanonical + TestRoundTrip,
// compile_ch_test, frontend legacyUrls). Bu sorguların ES metni değişmez.
var preExistingCorpus = []string{
	"timeout", "connection refused", `"connection refused"`, `"connection refused: timeout"`,
	`service.name:"checkout"`, `level:error`, `a and b or not c`, `a OR b AND c`, `NOT a AND b`,
	`NOT (a OR b)`, `NOT NOT a`, `-level:debug body`, `!a b`, `k8s-pod-7f`, `((a OR b) AND (c OR d))`,
	`service.name:"db" AND (disk OR memory)`, `_exists_:trace_id`, `NOT _exists_:k8s.pod.name`,
	`error.type:*`, `channel:("MOBILE" OR "WEB")`, `NOT channel:("MOBILE" OR "WEB")`,
	`level:(error AND NOT warn)`, `http.status:>="500"`, `duration_ms:<=250`, `a:>1 b:<2`,
	`status:[400 TO 499]`, `ts:{* TO 2026-09-01}`, `n:[1 TO 5}`, `pod:api-*`, `host:node?`,
	`msg:"say \"hi\""`, `path:"C:\\tmp"`, `12:30 restart`, `ERROR: boom`, `http://host/x`,
	`a && b || c`, `@timestamp:>=x k8s-label:v`, `kubernetes.labels.app:api`, `AND:x`,
	`service.name:"checkout" AND (disk OR memory) AND NOT level:debug`,
	`channel:("MOBILE" OR "WEB") AND http.status:>="500"`, `status:[400 TO 499] OR _exists_:error.type`,
	`msg:"say \"hi\"" pod:api-*`,
	`disk 90%`, `4BF92F3577B34DA6A3CE929D0E0E4736`, `Order*`, `service:"checkout"`, `service:check*`,
	`trace_id:ABCDEF`, `message:"disk full"`, `severity_num:17`, `severity_num:>=17`, `http.status:"500"`,
	`http.status:>=500`, `region:<"eu"`, `_exists_:error.type`, `_exists_:service`, `NOT level:debug`,
	`severity_num:[9 TO 12]`, `severity_num:{5 TO *]`, `service:"db" AND NOT _exists_:trace_id AND (disk OR memory)`,
	`service:a_b%*`, `level:error AND service.name:"checkout"`, `legacy-alias`, `boom`,
}

var lowerOpRe = regexp.MustCompile(`\b(and|or|not)\b`)

func TestPreExistingCorpusGolden(t *testing.T) {
	var b strings.Builder
	for _, q := range preExistingCorpus {
		tr, pe := ToES(q, false)
		if pe != nil {
			t.Fatalf("eski sorgu %q yapısal hata verdi: %v", q, pe)
		}
		want := q
		if tr.Lang == LangKQL {
			want = lowerOpRe.ReplaceAllStringFunc(q, strings.ToUpper)
		}
		if tr.Query != want {
			t.Errorf("ES metni değişti: %q → %q (yalnız küçük harf operatörler büyüyebilir)", q, tr.Query)
		}
		b.WriteString(q + "\t→\t" + tr.Query + "\t" + string(tr.Lang) + "\n")
	}
	const path = "testdata/es_query.golden"
	if *updateESGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("altın dosya: %v (go test ./internal/logql -run Golden -update)", err)
	}
	if string(golden) != b.String() {
		t.Errorf("altın dosya farklı; bilinçli değişiklikse -update ile yeniden yazın")
	}
}
