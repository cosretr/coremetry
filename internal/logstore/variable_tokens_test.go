package logstore

// variable_tokens_test.go — v0.10.1829: MaskVariableTokens kural tablosu.
// Her kural için olumlu VE olumsuz satır (unit-mixing dersi: her dal ayrı
// sınanır; birimli sayıda her birim). Değerler kurgusal.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/logstore/maskcorpus"
)

func TestMaskVariableTokens_Rules(t *testing.T) {
	cases := []struct{ rule, in, want string }{
		// 1 UUID/GUID
		{"uuid lower", "id 550e8400-e29b-41d4-a716-446655440000", "id <x>"},
		{"uuid upper", "ID 6BA7B810-9DAD-11D1-80B4-00C04FD430C8", "ID <x>"},
		{"uuid after _", "x_550e8400-e29b-41d4-a716-446655440000", "x_<x>"},
		{"uuid after =", "k=550e8400-e29b-41d4-a716-446655440000", "k=<x>"},
		{"uuid after :", "u:550e8400-e29b-41d4-a716-446655440000", "u:<x>"},
		{"uuid after /", "/550e8400-e29b-41d4-a716-446655440000", "/<x>"},
		{"uuid after #", "#550e8400-e29b-41d4-a716-446655440000", "#<x>"},
		{"uuid after letter", "abc550e8400-e29b-41d4-a716-446655440000", "abc<x>"},
		{"uuid braces", "{550e8400-e29b-41d4-a716-446655440000}", "<x>"},
		{"uuid parens", "(550e8400-e29b-41d4-a716-446655440000)", "(<x>)"},
		{"uuid urn", "urn:uuid:550e8400-e29b-41d4-a716-446655440000", "<x>"},
		{"uuid 32 hex", "6ba7b8109dad11d180b400c04fd430c8", "<x>"},
		{"uuid fragments from older mask", "<x>-e29b-41d4-a716-<x>", "<x>"},
		{"not uuid: two numbers", "range 1234-5678", "range <x>"},
		// 2 time
		{"iso T", "at 2026-09-02T10:15:30.123Z ok", "at <x> ok"},
		{"hh:mm:ss", "at 10:15:30 ok", "at <x> ok"},
		// 3 e-mail
		{"email", "to someone@acme.example", "to <x>"},
		{"email plus", "to j.doe+test@mail.acme.example", "to <x>"},
		{"not email: user@host", "user@host", "user@host"},
		// 4 IP
		{"ipv4 port", "from 10.0.0.1:8080", "from <x>"},
		{"ipv6 full", "c fe80::1ff:fe23:4567:890a", "c <x>"},
		{"ipv6 compressed", "c 2001:db8::8a2e:370:7334", "c <x>"},
		{"ipv6 bracket port", "c [::1]:8080", "c <x>"},
		{"ipv6 loopback", "c ::1", "c <x>"},
		{"not ipv6: c++ scope", "std::cout", "std::cout"},
		{"not ipv6: method ref", "Foo::bar", "Foo::bar"},
		{"not ipv6: a::b", "a::b", "a::b"},
		{"not ipv6: digitless mac", "aa:bb:cc:dd:ee:ff", "aa:bb:cc:dd:ee:ff"},
		// 5 Java hash
		{"java hash", "com.acme.OrderService@1a2b3c registered", "com.acme.OrderService@<x> registered"},
		{"java array hash", "[I@6d06d69c", "[I@<x>"},
		// 6 k8s pod suffix
		{"pod rs+pod", "orders-api-7d9f6-x2k4p", "orders-api-<x>"},
		{"pod long rs hash", "orders-api-5c8b7d9f4-m2n7q", "orders-api-<x>"},
		{"pod single suffix", "orders-api-x2k4p", "orders-api-<x>"},
		{"pod single suffix one dash", "pod checkout-x2k4p ready", "pod checkout-<x> ready"},
		{"pod partly masked", "pod-<x>-x2k4p", "pod-<x>"},
		{"not pod: all-letter suffix (residual)", "orders-api-bcdfg", "orders-api-bcdfg"},
		{"not pod: word", "orders-api-https", "orders-api-https"},
		{"not pod: suffix continues", "my-pod-x2k4p-extra", "my-pod-x2k4p-extra"},
		// 7 format placeholders
		{"fmt {0}", "found {0}.", "found <x>."},
		{"fmt {}", "found {}.", "found <x>."},
		{"fmt {12}", "found {12}.", "found <x>."},
		{"fmt %s", "found %s.", "found <x>."},
		{"fmt %d", "found %d", "found <x>"},
		{"fmt %v", "found %v", "found <x>"},
		{"sql bind", "id = ?", "id = <x>"},
		{"sql bind list", "(?, ?)", "(<x>, <x>)"},
		{"not fmt: named", "found {name}.", "found {name}."},
		{"not fmt: question", "ready?", "ready?"},
		{"not fmt: glued %s", "100%s", "100%s"},
		// 8 quoted
		{"quoted id single", `t 'TX9F3K2'`, `t '<x>'`},
		{"quoted id separators", `t "ab-12-cd-34"`, `t "<x>"`},
		{"not quoted: word", `t "orders-api"`, `t "orders-api"`},
		{"not quoted: categorical", `t "OK"`, `t "OK"`},
		{"not quoted: apostrophe", `it's 'x'`, `it's 'x'`},
		// 9 numbers — her birim ayrı satır
		{"int standalone", "Order 7 shipped", "Order <x> shipped"},
		{"int long", "Order 1234567890123 shipped", "Order <x> shipped"},
		{"thousands comma", "26,916,627 rows", "<x> rows"},
		{"thousands dot", "1.234.567 rows", "<x> rows"},
		{"thousands nbsp", "1 234 567 rows", "<x> rows"},
		{"thousands thin space", "1 234 rows", "<x> rows"},
		{"decimal dot", "ratio 18.45", "ratio <x>"},
		{"decimal comma", "ratio 3,14", "ratio <x>"},
		{"unit ns", "took 15ns", "took <x>ns"},
		{"unit us", "took 15us", "took <x>us"},
		{"unit µs", "took 15µs", "took <x>µs"},
		{"unit ms", "took 1500ms", "took <x>ms"},
		{"unit s", "took 30s", "took <x>s"},
		{"unit m", "took 5m", "took <x>m"},
		{"unit h", "took 2h", "took <x>h"},
		{"unit d", "kept 7d", "kept <x>d"},
		{"unit decimal ms", "took 2.5ms", "took <x>ms"},
		{"unit b", "read 512b", "read <x>b"},
		{"unit kb", "read 64kb", "read <x>kb"},
		{"unit KB upper", "read 64KB", "read <x>KB"},
		{"unit MB", "heap 18.45MB", "heap <x>MB"},
		{"unit MiB", "heap 512MiB", "heap <x>MiB"},
		{"unit GB", "disk 3GB", "disk <x>GB"},
		{"unit TB", "disk 2TB", "disk <x>TB"},
		{"unit %", "cpu 95%", "cpu <x>%"},
		{"thread suffix", "worker-12 task-7", "worker-<x> task-<x>"},
		{"thread pool", "pool-3-thread-7", "pool-<x>-thread-<x>"},
		{"ora code (unchanged v1 behaviour)", "ORA-00942", "ORA-<x>"},
		{"line number", "Foo.java:42", "Foo.java:<x>"},
		{"currency decimal", "$1000.50", "$<x>"},
		{"not number: non-unit word", "3DS 2fa 1st 5xx 12abc", "3DS 2fa 1st 5xx 12abc"},
		{"not number: version after letter", "v1.4.2", "v1.4.2"},
		{"not number: http version", "HTTP/1.1 HTTP/2", "HTTP/1.1 HTTP/2"},
		{"not number: glued to underscore", "x86_64", "x86_64"},
		// 10 token predicate
		{"mixed id", "TX9F3K2 QZ7M4P1", "<x> <x>"},
		{"hex id 11", "a07d33f9c40", "<x>"},
		{"hex id 6", "7d9f6c abc123", "<x> <x>"},
		{"underscore digits ≥4", "order_123456", "order_<x>"},
		{"path segment id", "/api/orders/ORD88213X/items", "/api/orders/<x>/items"},
		{"not id: class names", "Log4j2Plugins Base64Decoder NullPointerException", "Log4j2Plugins Base64Decoder NullPointerException"},
		{"not id: one digit group", "user12ab", "user12ab"},
		{"not id: letters-only hex word", "deadbeef", "deadbeef"},
		{"not id: path words", "/api/orders/items", "/api/orders/items"},
		// 11 collapse
		{"collapse comma run", "<x>,<x>,<x>", "<x>"},
		{"collapse braces", "{<x>}", "<x>"},
		{"no collapse across space", "<x> <x>", "<x> <x>"},
		{"no collapse of list in shared mask", "ids=[1, 2, 3]", "ids=[<x>, <x>, <x>]"},
	}
	for _, c := range cases {
		t.Run(c.rule, func(t *testing.T) {
			if got := MaskVariableTokens(c.in, "<x>"); got != c.want {
				t.Fatalf("MaskVariableTokens(%q)\n  got  %q\n  want %q", c.in, got, c.want)
			}
		})
	}
}

// Sözlük belirteçleri hiçbir kurala takılmaz (sayı kuralının sol sınırı +
// idTokenAllowlist). Liste brief'teki tokenların tamamı + izin listesi.
func TestMaskVariableTokens_DictionaryTokensKept(t *testing.T) {
	toks := []string{"http2", "utf8", "sha256", "x86_64", "v2", "p95", "k8s", "s3", "log4j", "oauth2",
		"i18n", "e2e", "ipv4", "ipv6", "md5", "h2", "junit5", "base64", "OP12", "utf8mb4", "log4j2", "iso8601"}
	for k := range idTokenAllowlist {
		toks = append(toks, k)
	}
	for _, tok := range toks {
		line := "negotiated " + tok + " ok"
		if got := MaskVariableTokens(line, "<x>"); got != line {
			t.Errorf("dictionary token %q masked: %q", tok, got)
		}
		if got := NormalizeSignature(line); got != line {
			t.Errorf("signature masked dictionary token %q: %q", tok, got)
		}
	}
}

// Liste birleştirmesi yalnız imza yolunda.
func TestCollapsePlaceholderLists(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ids=[<x>, <x>, <x>] q", "ids=[<x>] q"},
		{"a <x>; <x> b", "a <x> b"},
		{"a <x> <x> b", "a <x> <x> b"},
		{"no placeholder, here", "no placeholder, here"},
	}
	for _, c := range cases {
		if got := CollapsePlaceholderLists(c.in, "<x>"); got != c.want {
			t.Errorf("CollapsePlaceholderLists(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// İdempotent + boşluk sayısı korunur (iki yer tutucuyla da).
func TestMaskVariableTokens_IdempotentAndWhitespaceInvariant(t *testing.T) {
	var lines []string
	for _, g := range maskcorpus.Same {
		lines = append(lines, g.Lines...)
	}
	for _, p := range maskcorpus.Different {
		lines = append(lines, p.A, p.B)
	}
	for _, repl := range []string{"<x>", "<*>"} {
		for _, l := range lines {
			m := MaskVariableTokens(l, repl)
			if again := MaskVariableTokens(m, repl); again != m {
				t.Errorf("[%s] not idempotent: %q → %q → %q", repl, l, m, again)
			}
			if a, b := strings.Count(l, " ")+strings.Count(l, "\t"), strings.Count(m, " ")+strings.Count(m, "\t"); a != b {
				t.Errorf("[%s] whitespace count changed %d → %d: %q → %q", repl, a, b, l, m)
			}
		}
	}
}

func TestMaskerVersion(t *testing.T) {
	if MaskerVersion != 3 { // v0.10.2040 — anahtar=değer rastgele kimlik + JSON zarfı
		t.Fatalf("MaskerVersion = %d; bump it ONLY with a rule change that alters identities (templater quiet window keys on it)", MaskerVersion)
	}
}
