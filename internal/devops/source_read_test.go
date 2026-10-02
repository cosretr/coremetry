package devops

// source_read_test.go — v0.10.1050 (operatör: "Sohbet kod okuyabilsin: takip
// soruları bugün kod okuyamıyor."): read_source_code'un DevOps yarısı.
//
// Sözleşme:
//   - giriş kapısı: `..`, mutlak yol, joker, kontrol karakteri, uzun girdi
//     İSTEKTEN ÖNCE reddedilir;
//   - yalnız kaynak dosya: yasak liste (yapılandırma/kimlik) izin listesini EZER;
//   - dosya YALNIZ ağaç eşleşmesiyle seçilir — ağacın listelemediği yol istenmez;
//   - birden çok eşleşme → aday listesi, içerik isteği YOK;
//   - sürüm biliniyor + tag var → commit'ten; yok → dal + not; biçimsiz → yankısız.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestNormalizeSourceQuery(t *testing.T) {
	ok := []string{
		"ChargeHandler.java", "com.example.cards.ChargeHandler", "handlers/charge.go",
		"ChargeHandler$1", "src/main/java/com/example/cards/ChargeHandler.java",
		"payments_api/views.py", "Ödeme.kt", "a-b+c@d.ts",
	}
	for _, q := range ok {
		if got, err := NormalizeSourceQuery("  " + q + " "); err != nil || got != q {
			t.Errorf("%q kabul edilmeli: got=%q err=%v", q, got, err)
		}
	}
	bad := map[string]string{
		"boş":                "",
		"yalnız boşluk":      "   ",
		"üst dizin":          "../etc/passwd",
		"içte üst dizin":     "src/../../secret.java",
		"çift nokta adı":     "a..b.java",
		"mutlak":             "/etc/passwd",
		"ev dizini":          "~/x.java",
		"joker *":            "*.java",
		"joker ?":            "Charge?.java",
		"joker []":           "Charge[1].java",
		"joker {}":           "Charge{a,b}.java",
		"satır sonu":         "Charge\nHandler.java",
		"NUL":                "Charge\x00.java",
		"sekme":              "Charge\tHandler.java",
		"DEL":                "Charge\x7f.java",
		"ters bölü":          `src\main\X.java`,
		"sürücü":             "C:/x.java",
		"URL kodlu":          "%2e%2e/x.java",
		"boşluk":             "Charge Handler.java",
		"çift bölü":          "src//X.java",
		"nokta parçası":      "./X.java",
		"sonda bölü":         "src/",
		"sorgu dizgisi":      "X.java&$top=1",
		"geçersiz UTF-8":     "X\xff.java",
		"201 bayt":           strings.Repeat("a", 196) + ".java",
		"unicode satır sonu": "X\u2028.java",
	}
	for name, q := range bad {
		if _, err := NormalizeSourceQuery(q); err == nil {
			t.Errorf("%s (%q) reddedilmeliydi", name, q)
		} else if !strings.Contains(err.Error(), "geçersiz") && !strings.Contains(err.Error(), "zorunlu") {
			t.Errorf("%s: hata metni bad_args sınıfına düşmeli (geçersiz/zorunlu): %v", name, err)
		}
	}
	if _, err := NormalizeSourceQuery(strings.Repeat("a", 195) + ".java"); err != nil {
		t.Errorf("200 bayt sınırı kabul edilmeli: %v", err)
	}
}

// TestClassifySourcePath — izin/yasak tablosu. Yasak liste İZNİ EZER: kaynak
// uzantılı ama yapılandırma/kimlik kokulu dosyalar (settings.py, config.go,
// SecretHolder.java) reddedilir — denylist kaldırılırsa bu satırlar kırmızı.
func TestClassifySourcePath(t *testing.T) {
	allow := []string{
		"/src/main/java/com/example/cards/ChargeHandler.java",
		"/app/src/main/kotlin/x/Y.kt", "/build-logic/plugin.kts", "/src/S.scala", "/src/G.groovy",
		"/svc/Payments/Controller.cs", "/cmd/api/main.go", "/app/views.py", "/web/app.js",
		"/web/App.jsx", "/web/app.ts", "/web/App.tsx", "/lib/x.rb", "/www/index.php",
		"/src/main/resources/mapper/OrderMapper.xml", "/src/main/resources/OrderMapper.xml",
		"/src/main/resources/mapper/order.sql", "/src/main/resources/mappers/OrderQueries.sql",
		"/src/main/java/com/example/Application.java", "/src/main/java/com/example/TokenService.java",
	}
	for _, p := range allow {
		if ok, why := ClassifySourcePath(p); !ok {
			t.Errorf("%s okunabilmeli: %s", p, why)
		}
	}
	deny := []string{
		// yapılandırma uzantıları
		"/src/main/resources/application.properties", "/src/main/resources/application-prod.yml",
		"/src/main/resources/application.yaml", "/config/appsettings.Production.json",
		"/k8s/deployment.yaml", "/.gitlab-ci.yml", "/.github/workflows/ci.yml",
		"/azure-pipelines.yml", "/infra/main.tfvars", "/terraform.tfstate", "/web.config",
		"/conf/app.conf", "/setup.cfg", "/pyproject.toml", "/tox.ini",
		// anahtar / sertifika
		"/certs/server.pem", "/certs/server.key", "/certs/store.pfx", "/certs/store.p12",
		"/certs/trust.jks", "/certs/ca.crt", "/x/app.keystore",
		// .env ailesi
		"/.env", "/.env.local", "/app/.env.production", "/.envrc", "/app/.env/x.js",
		// kimlik kokulu ad — kaynak uzantılı olsa bile (yasak liste EZER)
		"/src/main/java/com/example/SecretHolder.java", "/src/secrets/Loader.java",
		"/app/credentials.py", "/src/CredentialStore.cs", "/src/DbPasswordProvider.java",
		"/x/passwd.go", "/x/PrivateKeyLoader.java", "/x/private_key.py",
		// yapılandırma gibi görünen kaynak dosyalar
		"/app/settings.py", "/app/local_settings.py", "/cmd/config.go", "/src/Config.java",
		"/src/configuration.ts", "/src/conf.py", "/web/webpack.config.js", "/web/vite.config.ts",
		"/web/app.settings.js", "/build.gradle.kts", "/settings.gradle.kts", "/build.gradle",
		"/AppSettings.cs", "/env.js",
		// mapper olmayan XML / mapper dizini dışındaki SQL
		"/src/main/resources/mybatis/mappers/order.xml", "/db/migration/V1__init.sql",
		"/pom.xml", "/settings.xml", "/src/main/resources/logback.xml",
		"/standalone/configuration/standalone.xml", "/WEB-INF/web.xml", "/META-INF/persistence.xml",
		// kaynak değil
		"/Dockerfile", "/Jenkinsfile", "/README.md", "/bin/app.jar", "/x", "",
	}
	for _, p := range deny {
		if ok, _ := ClassifySourcePath(p); ok {
			t.Errorf("%s REDDEDİLMELİ (yapılandırma/kimlik/kaynak dışı)", p)
		}
	}
}

// TestClassifySourcePathReviewAdditions — v0.10.1050 güvenlik incelemesinin
// yasak-liste ekleri: HER kural en az bir satırla pinli (kural kalkarsa o
// satır kırmızı). Satır adı kuralı söyler.
func TestClassifySourcePathReviewAdditions(t *testing.T) {
	for name, p := range sourceReviewDenyRows {
		if ok, _ := ClassifySourcePath(p); ok {
			t.Errorf("%s: %s REDDEDİLMELİ", name, p)
		}
	}
	for _, p := range []string{
		"/src/main/java/com/example/cards/ChargeHandler.java", "/handlers/charge.go",
		"/src/main/resources/mapper/PaymentMapper.xml", "PaymentMapper.xml",
		"/src/main/resources/mappers/payment.sql",
		// dizin kuralı uzantıya bağlı: Go/Java config paketi kod sayılır.
		"/internal/config/loader.go", "/src/main/java/com/example/config/WebMvc.java",
	} {
		if ok, why := ClassifySourcePath(p); !ok {
			t.Errorf("%s okunabilmeli: %s", p, why)
		}
	}
}

// sourceReviewDenyRows — inceleme eklerinin tablosu (kural adı → örnek yol).
var sourceReviewDenyRows = map[string]string{
	"kök wp-config":                    "/www/wp-config.php",
	"kök localsettings":                "/wiki/LocalSettings.php",
	"kök environment":                  "/web/environment.prod.ts",
	"kök ormconfig":                    "/api/ormconfig.ts",
	"kök knexfile":                     "/api/knexfile.js",
	"ad apikey":                        "/src/main/java/com/example/ApiKeys.java",
	"ad api-key":                       "/web/api-key-store.ts",
	"ad api_key":                       "/app/api_key_loader.py",
	"ad datasource":                    "/src/main/java/com/example/DataSourceConfig.java",
	"dizin settings/ (.py)":            "/app/settings/production.py",
	"dizin config/ (.js)":              "/api/config/production.js",
	"dizin config/ (.js) database":     "/api/config/database.js",
	"dizin environments/ (.ts)":        "/web/src/environments/env.prod.ts",
	"dizin config/environments/":       "/rails/config/environments/production.rb",
	"dizin config/initializers/":       "/rails/config/initializers/x.rb",
	"dizin config/ (.php)":             "/www/config/app.php",
	"dizin config/ (.tsx)":             "/web/config/flags.tsx",
	"dizin config/ (.jsx)":             "/web/config/flags.jsx",
	"dizin environments/ yalnız":       "/web/src/environments/staging.ts",
	"dizin initializers/ yalnız":       "/lib/initializers/cache.rb",
	"sql mapper dizini dışında":        "/db/seed.sql",
	"xml ad …Mapper.xml değil":         "/src/main/resources/mybatis-config.xml",
	"xml mapper dizini yetmez":         "/src/main/resources/mapper/order.xml",
	"örnek src/environments (kök)":     "/src/environments/environment.prod.ts",
	"örnek config/production.js":       "config/production.js",
	"örnek settings/production.py":     "settings/production.py",
	"örnek config/initializers/x":      "config/initializers/x.rb",
	"örnek LocalSettings.php (göreli)": "LocalSettings.php",
}

// TestTrimTruncatedBody — tavana dayanan gövdenin yarım son satırı atılır.
func TestTrimTruncatedBody(t *testing.T) {
	if b, tr := trimTruncatedBody("a\nb\n", 100); tr || b != "a\nb\n" {
		t.Fatalf("tavan altı aynen: %q %v", b, tr)
	}
	if b, tr := trimTruncatedBody("line1\nline2\nyar", 15); !tr || b != "line1\nline2\n" {
		t.Fatalf("yarım satır atılmalı: %q %v", b, tr)
	}
	if b, tr := trimTruncatedBody("tek-uzun-satır", 5); !tr || b != "tek-uzun-satır" {
		t.Fatalf("satır sonu yoksa gövde kalır, kesik işaretlenir: %q %v", b, tr)
	}
}

// TestClassifySourceQuery — sorgunun kendisi istekten önce: uzantılı ve noktalı
// sınıf adları yol gibi sınıflanır; uzantısız ad yalnız yasak listeye bakar.
func TestClassifySourceQuery(t *testing.T) {
	for q, want := range map[string]bool{
		"ChargeHandler.java":              true,
		"ChargeHandler":                   true,
		"com.example.cards.ChargeHandler": true,
		"handlers/charge.go":              true,
		"handlers/charge":                 true,
		"OrderMapper.xml":                 true,
		"application.yml":                 false,
		"application.properties":          false,
		".env":                            false,
		"config/.env.local":               false,
		"Settings":                        false,
		"settings.py":                     false,
		"com.example.Config":              false,
		"com.example.Config.java":         false,
		"SecretHolder":                    false,
		"server.pem":                      false,
		"pom.xml":                         false,
		// Bilinmeyen uzantı noktalı sınıf adından ayırt edilemez (com.example.md?):
		// sorgu geçer, karar ağaçtaki gerçek yolda (ClassifySourcePath reddeder).
		"README.md": true,
	} {
		if ok, why := ClassifySourceQuery(q); ok != want {
			t.Errorf("ClassifySourceQuery(%q) = %v (%s), beklenen %v", q, ok, why, want)
		}
	}
}

func TestMatchSourcePaths(t *testing.T) {
	tree := []string{
		"/src/main/java/com/example/cards/ChargeHandler.java",
		"/src/main/java/com/example/cards/MyChargeHandler.java",
		"/src/main/java/com/example/legacy/ChargeHandler.java",
		"/src/main/java/com/example/cards/RefundHandler.java",
		"/src/main/java/com/example/cards/RefundHandler.kt",
		"/handlers/charge.go",
		"/web/handlers/charge.go",
		"/src/main/resources/application.yml",
		"/src/main/java/com/example/Application.java",
		"/app/settings.py",
		"/src/main/java/com/example/cards/Ledger.java",
		"/src/main/java/com/example/cards/ledger.java",
	}
	cases := []struct {
		q       string
		want    []string
		denied  int
		comment string
	}{
		{"ChargeHandler.java", []string{"/src/main/java/com/example/cards/ChargeHandler.java", "/src/main/java/com/example/legacy/ChargeHandler.java"}, 0, "ad iki pakette → aday listesi"},
		{"cards/ChargeHandler.java", []string{"/src/main/java/com/example/cards/ChargeHandler.java"}, 0, "dizin soneki tekilleştirir"},
		{"com.example.cards.ChargeHandler", []string{"/src/main/java/com/example/cards/ChargeHandler.java"}, 0, "noktalı sınıf adı"},
		{"com.example.cards.ChargeHandler.java", []string{"/src/main/java/com/example/cards/ChargeHandler.java"}, 0, "noktalı ad + uzantı"},
		{"com.example.cards.ChargeHandler.charge", []string{"/src/main/java/com/example/cards/ChargeHandler.java"}, 0, "metot soneki düşer"},
		{"ChargeHandler$1", []string{"/src/main/java/com/example/cards/ChargeHandler.java", "/src/main/java/com/example/legacy/ChargeHandler.java"}, 0, "iç sınıf"},
		{"MyChargeHandler.java", []string{"/src/main/java/com/example/cards/MyChargeHandler.java"}, 0, "tam parça"},
		{"RefundHandler", []string{"/src/main/java/com/example/cards/RefundHandler.kt", "/src/main/java/com/example/cards/RefundHandler.java"}, 0, "uzantısız ad iki dilde"},
		{"handlers/charge.go", []string{"/handlers/charge.go", "/web/handlers/charge.go"}, 0, "yol soneki"},
		{"web/handlers/charge.go", []string{"/web/handlers/charge.go"}, 0, ""},
		{"application", []string{"/src/main/java/com/example/Application.java"}, 1, "yml yasak, java okunur"},
		{"application.yml", nil, 1, "yalnız yasak eşleşme"},
		{"settings.py", nil, 1, ""},
		{"Ledger.java", []string{"/src/main/java/com/example/cards/Ledger.java"}, 0, "harf-duyarlı TEK eşleşme kazanır"},
		{"LEDGER.java", []string{"/src/main/java/com/example/cards/Ledger.java", "/src/main/java/com/example/cards/ledger.java"}, 0, "harf-duyarlı eşleşme yok → iki aday"},
		{"Nope.java", nil, 0, "ağaçta yok"},
		{"etc/passwd", nil, 0, "ağaçta yok"},
	}
	for _, c := range cases {
		got, denied := MatchSourcePaths(tree, c.q)
		if fmt.Sprint(got) != fmt.Sprint(c.want) || denied != c.denied {
			t.Errorf("%s (%s): got=%v denied=%d, beklenen %v denied=%d", c.q, c.comment, got, denied, c.want, c.denied)
		}
		for _, p := range got {
			found := false
			for _, tp := range tree {
				found = found || tp == p
			}
			if !found {
				t.Fatalf("%q ağaçta olmayan bir yol döndürdü: %s", c.q, p)
			}
		}
	}
}

func numberedFile(n int, mark func(i int) string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if m := mark(i); m != "" {
			b.WriteString(m + "\n")
			continue
		}
		fmt.Fprintf(&b, "    // satır %d\n", i)
	}
	return b.String()
}

func TestSourceWindow(t *testing.T) {
	body := numberedFile(200, func(i int) string {
		switch i {
		case 90:
			return "    public void charge(Card c) {"
		case 120:
			return "        ledger.post(c); // HEDEF"
		case 150:
			return strings.Repeat("x", 1000)
		}
		return ""
	})
	w, total, outside := sourceWindow(body, 120, 10)
	if total != 200 || outside || w.FromLine != 110 || w.ToLine != 130 {
		t.Fatalf("pencere: total=%d outside=%v %d-%d", total, outside, w.FromLine, w.ToLine)
	}
	if !strings.Contains(w.Content, "120|         ledger.post(c); // HEDEF") || !strings.HasPrefix(w.Content, "110| ") {
		t.Fatalf("numaralandırma yanlış:\n%s", w.Content)
	}
	if w.Signature != "public void charge(Card c) {" || w.SignatureLine != 90 {
		t.Fatalf("çevreleyen imza: %q @%d", w.Signature, w.SignatureLine)
	}
	// line yok → dosyanın ilk penceresi (1 … 2r+1), imza yok.
	w, _, _ = sourceWindow(body, 0, 10)
	if w.FromLine != 1 || w.ToLine != 21 || w.Signature != "" {
		t.Fatalf("ilk pencere: %d-%d sig=%q", w.FromLine, w.ToLine, w.Signature)
	}
	// dosya dışı satır → son pencere, outside.
	w, _, outside = sourceWindow(body, 999, 5)
	if !outside || w.FromLine != 195 || w.ToLine != 200 {
		t.Fatalf("dosya dışı: outside=%v %d-%d", outside, w.FromLine, w.ToLine)
	}
	// uzun satır kırpılır.
	w, _, _ = sourceWindow(body, 150, 0)
	if n := utf8.RuneCountInString(w.Content); n > sourceLineMaxRunes+40 || !strings.Contains(w.Content, "satır kırpıldı") {
		t.Fatalf("uzun satır kırpılmadı (%d rune)", n)
	}
	if w, total, _ := sourceWindow("", 1, 5); total != 0 || w.Content != "" {
		t.Fatal("boş dosya pencere üretmemeli")
	}
}

func TestClipSourceWindow(t *testing.T) {
	w, _, _ := sourceWindow(numberedFile(100, func(int) string { return "" }), 50, 30)
	cut, from, to := ClipSourceWindow(w.Content, 50, 400)
	if utf8.RuneCountInString(cut) > 400 || from > 50 || to < 50 || !strings.Contains(cut, "50| ") {
		t.Fatalf("merkez korunmalı: %d-%d (%d rune)", from, to, utf8.RuneCountInString(cut))
	}
	cut, from, _ = ClipSourceWindow(w.Content, 0, 400)
	if from != 20 || utf8.RuneCountInString(cut) > 400 {
		t.Fatalf("merkezsiz kesim baştan: from=%d", from)
	}
	if cut, _, _ := ClipSourceWindow(w.Content, 50, 3); cut != "" {
		t.Fatal("hiçbir satır sığmıyorsa boş dönmeli")
	}
	if cut, f, l := ClipSourceWindow(w.Content, 50, 1<<20); cut != w.Content || f != 20 || l != 80 {
		t.Fatalf("sığan pencere aynen dönmeli: %d-%d", f, l)
	}
}

// ── ReadSource uçtan uca (sahte TFS) ───────────────────────────────────

const srMarker = "ZEBRA_SOURCE_FIXTURE_7731"

const (
	srCharge  = "/src/main/java/com/example/cards/ChargeHandler.java"
	srLegacy  = "/src/main/java/com/example/legacy/ChargeHandler.java"
	srRefund  = "/src/main/java/com/example/cards/RefundHandler.java"
	srHidden  = "/src/main/java/com/example/cards/Hidden.java"
	srSecrets = "/app/settings.py"
	srBeans   = "/src/main/resources/Beans.xml"
	srAppYml  = "/src/main/resources/application.yml"
)

func sourceFixture(t *testing.T) (*fakeTFS, *Service) {
	t.Helper()
	f := newFakeTFS(t)
	f.tree = []string{srCharge, srLegacy, srRefund, srSecrets, srBeans, srAppYml}
	body := numberedFile(300, func(i int) string {
		if i == 120 {
			return "        ledger.post(card); // " + srMarker
		}
		return ""
	})
	for _, p := range f.tree {
		f.files[p] = body
	}
	// Ağaçta YOK ama item ucu sunuyor: ağaç eşleşmesi kalkarsa okunurdu.
	f.files[srHidden] = body
	f.files["/Hidden.java"] = body
	f.files["Hidden.java"] = body
	svc := New()
	svc.Configure(f.settings())
	return f, svc
}

func srRead(svc *Service, file string, line int, version string) SourceRead {
	return svc.ReadSource(context.Background(), SourceRequest{
		Repo: "payments-api", Service: "payments-api", File: file, Line: line, Version: version,
	})
}

func TestReadSourceNameMatchNumberedWindow(t *testing.T) {
	f, svc := sourceFixture(t)
	sr := srRead(svc, "cards/ChargeHandler.java", 120, "")
	if sr.Outcome != SourceOK {
		t.Fatalf("outcome %s: %s", sr.Outcome, sr.Reason)
	}
	if sr.Path != strings.TrimPrefix(srCharge, "/") || sr.TotalLines != 300 || sr.FromLine != 90 || sr.ToLine != 150 || sr.Line != 120 {
		t.Fatalf("referans: %+v", sr)
	}
	if !strings.Contains(sr.Content, "120|         ledger.post(card); // "+srMarker) {
		t.Fatalf("hedef satır numarasıyla gelmeli:\n%s", sr.Content)
	}
	if sr.RefKind != "branch" || sr.Branch != "release" || sr.RefLabel() != "release (dal)" {
		t.Fatalf("ref: %s %s %q", sr.RefKind, sr.Branch, sr.RefLabel())
	}
	if got := f.hits["item"]; got != 1 {
		t.Fatalf("tek dosya isteği bekleniyordu: %d", got)
	}
	// Kodsuz alanlar kod taşımaz (tarayıcıya/ai_calls'a giden yarı).
	for _, s := range []string{sr.Reason, sr.RefLabel(), sr.Path, sr.Repo} {
		if strings.Contains(s, srMarker) {
			t.Fatalf("referans alanı kod taşıyor: %q", s)
		}
	}
}

func TestReadSourceAmbiguousListsCandidatesWithoutContent(t *testing.T) {
	f, svc := sourceFixture(t)
	sr := srRead(svc, "ChargeHandler.java", 120, "")
	if sr.Outcome != SourceAmbiguous || sr.CandidatesTotal != 2 || len(sr.Candidates) != 2 {
		t.Fatalf("aday listesi: %+v", sr)
	}
	if strings.HasPrefix(sr.Candidates[0], "/") {
		t.Fatalf("aday depo-göreli olmalı (mutlak yol kapısıyla uyumlu): %v", sr.Candidates)
	}
	if sr.Content != "" || f.hits["item"] != 0 {
		t.Fatalf("birden çok eşleşmede İÇERİK isteği yok: item=%d", f.hits["item"])
	}
}

// Ağaçta olmayan dosya: dürüst ıska, içerik isteği YOK — dosya item ucunda
// gerçekten var olsa bile (ağaç eşleşmesi şartı; mutasyon pini).
func TestReadSourceNotInTreeIsHonestMissAndNeverFetched(t *testing.T) {
	f, svc := sourceFixture(t)
	for _, q := range []string{"Hidden.java", "cards/Hidden.java", "com.example.cards.Hidden"} {
		sr := srRead(svc, q, 10, "")
		if sr.Outcome != SourceNotFound || !strings.Contains(sr.Reason, "eşleşen kaynak dosya yok") {
			t.Fatalf("%s: outcome %s (%s)", q, sr.Outcome, sr.Reason)
		}
		if strings.Contains(sr.Reason, "kesildi") {
			t.Fatalf("ağaç tamken kesilme notu olmamalı: %q", sr.Reason)
		}
	}
	if f.hits["item"] != 0 {
		t.Fatalf("ağacın listelemediği yol İSTENMEMELİ: item=%d", f.hits["item"])
	}
	// Ağaç tavana dayandıysa ıskada kesilme söylenir.
	f2, svc2 := sourceFixture(t)
	svc2.treeMaxPaths = 2
	sr := srRead(svc2, "Nope.java", 0, "")
	if sr.Outcome != SourceNotFound || !strings.Contains(sr.Reason, "depo ağacı 2 yolda kesildi") || !sr.TreeCapped {
		t.Fatalf("kesik ağaç notu: %+v", sr)
	}
	if f2.hits["item"] != 0 {
		t.Fatal("ıskada dosya isteği olmamalı")
	}
}

// Traversal / joker / kontrol karakteri: istekten ÖNCE red, sunucuya TEK
// istek bile gitmez.
func TestReadSourceRejectsBeforeAnyRequest(t *testing.T) {
	f, svc := sourceFixture(t)
	for _, q := range []string{"../../etc/passwd", "/etc/passwd", "*.java", "a\x00.java", "Charge\nHandler.java", strings.Repeat("x", 300)} {
		sr := srRead(svc, q, 0, "")
		if sr.Outcome != SourceInvalid {
			t.Fatalf("%q: outcome %s", q, sr.Outcome)
		}
	}
	if n := len(f.snapshotReqs()); n != 0 {
		t.Fatalf("geçersiz girdide %d istek gitti: %v", n, f.snapshotReqs())
	}
}

// Yasak dosya: sorgu yasaksa HİÇ istek yok; sorgu geçip ağaçtaki yol yasaksa
// ağaç okunur ama İÇERİK istenmez. Yasak liste kalkarsa settings.py okunurdu.
func TestReadSourceDeniedFileNeverFetched(t *testing.T) {
	f, svc := sourceFixture(t)
	for _, q := range []string{"application.yml", "settings.py", "app/settings.py", ".env", "SecretHolder"} {
		sr := srRead(svc, q, 0, "")
		if sr.Outcome != SourceDenied || !strings.Contains(sr.Reason, "bu dosya türü okunmaz") {
			t.Fatalf("%s: outcome %s (%s)", q, sr.Outcome, sr.Reason)
		}
	}
	if n := len(f.snapshotReqs()); n != 0 {
		t.Fatalf("yasak sorguda %d istek gitti", n)
	}
	// "Beans" sorgusu geçer, ağaçtaki Beans.xml mapper değil → yasak, içerik yok.
	sr := srRead(svc, "Beans", 0, "")
	if sr.Outcome != SourceDenied || sr.DeniedMatches != 1 || sr.Content != "" {
		t.Fatalf("ağaçta yasak eşleşme: %+v", sr)
	}
	if f.hits["item"] != 0 {
		t.Fatalf("yasak dosyanın içeriği istendi: item=%d", f.hits["item"])
	}
}

func TestReadSourceVersionKnownReadsCommit(t *testing.T) {
	f, svc := sourceFixture(t)
	const sha = "c0ffee0000000000000000000000000000000077"
	f.treeAt = map[string][]string{sha: {srCharge}}
	f.tags = map[string]string{"refs/tags/1.4.2": "tagobj"}
	f.peeled = map[string]string{"refs/tags/1.4.2": sha}
	sr := srRead(svc, "ChargeHandler.java", 120, "1.4.2")
	// Commit ağacında tek ChargeHandler → aday değil, okuma.
	if sr.Outcome != SourceOK || sr.RefKind != "commit" || sr.Commit != sha || sr.Version != "1.4.2" {
		t.Fatalf("commit'ten okunmalı: %+v", sr)
	}
	if sr.RefLabel() != "1.4.2 (çalışan sürüm, commit c0ffee00)" {
		t.Fatalf("ref etiketi: %q", sr.RefLabel())
	}
	f.mu.Lock()
	items := append([]string(nil), f.itemVersions...)
	f.mu.Unlock()
	if len(items) != 1 || items[0] != "commit:"+sha {
		t.Fatalf("dosya commit'ten okunmalı: %v", items)
	}
}

func TestReadSourceVersionMissingFallsBackToBranch(t *testing.T) {
	f, svc := sourceFixture(t)
	sr := srRead(svc, "cards/ChargeHandler.java", 120, "9.9.9")
	if sr.Outcome != SourceOK || sr.RefKind != "branch" || sr.Version != "9.9.9" || sr.Commit != "" {
		t.Fatalf("dal ucuna düşmeli: %+v", sr)
	}
	if !strings.Contains(sr.Reason, "çalışan sürüm 9.9.9 depoda bulunamadı (tags/9.9.9), release dalından okundu") {
		t.Fatalf("geri-düşüş notu: %q", sr.Reason)
	}
	if f.countReqs("filter=tags%2F9.9.9") != 1 {
		t.Fatalf("tek refs sorgusu bekleniyordu: %v", f.snapshotReqs())
	}
}

// Biçimsiz / yer tutucu sürüm: refs sorgusu YOK, değer hiçbir yerde yankılanmaz.
func TestReadSourceUnusableVersionIsNoVersionAndNotEchoed(t *testing.T) {
	f, svc := sourceFixture(t)
	for _, v := range []string{"x&$top=1", "latest", "1.0-SNAPSHOT", "../../x"} {
		sr := srRead(svc, "cards/ChargeHandler.java", 120, v)
		if sr.Outcome != SourceOK || sr.RefKind != "branch" || sr.Version != "" {
			t.Fatalf("%q: %+v", v, sr)
		}
		if strings.Contains(sr.Reason, v) || !strings.Contains(sr.Reason, "verilen sürüm kullanılamadı") {
			t.Fatalf("%q: not yankısız olmalı: %q", v, sr.Reason)
		}
	}
	if f.countReqs("filter=tags") != 0 {
		t.Fatalf("kullanılamaz sürümle refs sorgusu atıldı: %v", f.snapshotReqs())
	}
}

func TestReadSourceUnconfiguredAndUnresolved(t *testing.T) {
	if sr := New().ReadSource(context.Background(), SourceRequest{Repo: "x", File: "X.java"}); sr.Outcome != SourceUnconfigured {
		t.Fatalf("yapılandırılmamış: %s", sr.Outcome)
	}
	var nilSvc *Service
	if sr := nilSvc.ReadSource(context.Background(), SourceRequest{Repo: "x", File: "X.java"}); sr.Outcome != SourceUnconfigured {
		t.Fatalf("nil servis: %s", sr.Outcome)
	}
	_, svc := sourceFixture(t)
	if sr := svc.ReadSource(context.Background(), SourceRequest{File: "X.java"}); sr.Outcome != SourceRepoUnresolved {
		t.Fatalf("depo yok: %s", sr.Outcome)
	}
}

// Çağıranın bağlamı: sohbet aracının süre bütçesi (DeadlineExceeded) "süre",
// istemci iptali "iptal" — ikisi de DevOps'un 25 sn tavanıyla karışmaz.
func TestReadSourceCallerDeadlineAndCancel(t *testing.T) {
	f, svc := sourceFixture(t)
	f.slowItemAfter, f.itemDelay = 1, 2*time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	sr := svc.ReadSource(ctx, SourceRequest{Repo: "payments-api", File: "cards/ChargeHandler.java", Line: 120})
	if sr.Outcome != SourceDeadline || !strings.Contains(sr.Reason, "araç süre bütçesi doldu") || sr.Content != "" {
		t.Fatalf("çağıranın bütçesi: %+v", sr)
	}
	cctx, ccancel := context.WithCancel(context.Background())
	ccancel()
	sr = svc.ReadSource(cctx, SourceRequest{Repo: "payments-api", File: "cards/ChargeHandler.java", Line: 120})
	if sr.Outcome != SourceCancelled || sr.Content != "" {
		t.Fatalf("iptal: %+v", sr)
	}
}

// İSTEK BÜTÇESİ (rapor sayıları bu testten): sürümsüz soğuk okuma = branş refs
// + ağaç + dosya; sıcak (ağaç 10 dk cache'li) = branş refs + dosya. Sürüm +
// tag: soğuk +1 refs(tag) +1 commit ağacı, sıcak +0.
func TestReadSourceRequestBudget(t *testing.T) {
	f, svc := sourceFixture(t)
	srRead(svc, "cards/ChargeHandler.java", 120, "")
	cold := len(f.snapshotReqs())
	srRead(svc, "cards/RefundHandler.java", 10, "")
	warm := len(f.snapshotReqs()) - cold
	if cold != 3 || warm != 2 {
		t.Fatalf("sürümsüz istek sayısı soğuk=%d sıcak=%d (beklenen 3/2): %v", cold, warm, f.snapshotReqs())
	}
	f2, svc2 := sourceFixture(t)
	const sha = "c0ffee0000000000000000000000000000000077"
	f2.treeAt = map[string][]string{sha: {srCharge, srRefund}}
	f2.tags = map[string]string{"refs/tags/1.4.2": "tagobj"}
	f2.peeled = map[string]string{"refs/tags/1.4.2": sha}
	srRead(svc2, "cards/ChargeHandler.java", 120, "1.4.2")
	vcold := len(f2.snapshotReqs())
	srRead(svc2, "cards/RefundHandler.java", 10, "1.4.2")
	vwarm := len(f2.snapshotReqs()) - vcold
	if vcold != 5 || vwarm != 2 {
		t.Fatalf("sürümlü istek sayısı soğuk=%d sıcak=%d (beklenen 5/2): %v", vcold, vwarm, f2.snapshotReqs())
	}
}
