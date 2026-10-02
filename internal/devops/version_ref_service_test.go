package devops

// version_ref_service_test.go — v0.10.1047 (operatör: "Mono-repo'da sürüm
// etiketi: aynı depoda birden çok servis varsa, başka servisin etiketi bu
// servisin sürümü sanılabiliyor").
//
// Sözleşme:
//   - desen ikinci yer tutucu {service}'i taşıyabilir; {version} zorunlu,
//     başka {…} yok (kaydetme ve çözüm aynı kapı: NormalizeVersionRef)
//   - {service} = ResolveRepo konvansiyonunun soyduğu ad (önek + ortam eki),
//     TEK fonksiyon (conventionName); pinli mono-repoda da depo adı değil
//     servis adı
//   - boş ya da ref-güvenli olmayan servis adı → ref yok (tags/-1.4.2 asla),
//     istek yok, yankı yok
//   - {service}'siz desen (varsayılan dahil) → istekler bayt bayt eskisi
//     (golden, değişiklik öncesi kodla aynı fikstürle kaydedildi)
//   - frame linkleri ve kod incelemesi aynı servis için AYNI ref'i sorar

import (
	"context"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/stackparse"
)

func TestNormalizeVersionRef_ServicePlaceholder(t *testing.T) {
	for _, c := range []struct {
		in, want string
		errHas   string // boş = geçerli
	}{
		{"", DefaultVersionRef, ""},
		{"tags/{version}", "tags/{version}", ""},
		{"tags/{service}-{version}", "tags/{service}-{version}", ""},
		{" tags/{service}/v{version} ", "tags/{service}/v{version}", ""},
		{"heads/release/{service}/{version}", "heads/release/{service}/{version}", ""},
		{"tags/{version}-{service}", "tags/{version}-{service}", ""},
		// {version} zorunlu — {service} tek başına yetmez.
		{"tags/{service}", "", "{version} yer tutucusunu taşımalı"},
		{"tags/{Version}", "", "{version} yer tutucusunu taşımalı"},
		// Tanınmayan yer tutucu: adıyla anılır, izinli ikisi söylenir.
		{"tags/{svc}-{version}", "", "bilinmeyen yer tutucu {svc}"},
		{"tags/{version}-{env}", "", "bilinmeyen yer tutucu {env}"},
		{"tags/{{version}}", "", "bilinmeyen yer tutucu {}"},
		{"tags/{version}}", "", "eşlenmemiş"},
		{"tags/{service-{version}", "", "eşlenmemiş"},
		// Mevcut önek kuralı aynen.
		{"refs/tags/{service}-{version}", "", "tags/ ya da heads/ ile başlamalı"},
	} {
		got, err := NormalizeVersionRef(c.in)
		if c.errHas == "" {
			if err != nil || got != c.want {
				t.Errorf("%q → %q, %v; beklenen %q", c.in, got, err, c.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("%q reddedilmeliydi, %q döndü", c.in, got)
			continue
		}
		if !strings.Contains(err.Error(), c.errHas) {
			t.Errorf("%q: hata %q, beklenen ⊇ %q", c.in, err, c.errHas)
		}
		if strings.Contains(c.errHas, "bilinmeyen") || c.errHas == "eşlenmemiş" {
			if !strings.Contains(err.Error(), "yalnız {version} ve {service} kullanılabilir") {
				t.Errorf("%q: hata izinli yer tutucuları söylemeli: %q", c.in, err)
			}
		}
	}
}

func TestResolveVersionRef_Service(t *testing.T) {
	for _, c := range []struct {
		pattern, service, version, want string
		ok                              bool
	}{
		{"tags/{service}-{version}", "payments-api", "1.4.2", "tags/payments-api-1.4.2", true},
		{"tags/{service}/v{version}", "orders-api", "2.0.1", "tags/orders-api/v2.0.1", true},
		{"heads/release/{service}/{version}", "orders-api", "2.0.1", "heads/release/orders-api/2.0.1", true},
		{"tags/{service}-{version}", " payments-api ", "1.4.2", "tags/payments-api-1.4.2", true},
		// Boş servis: boş yerleştirme YOK (tags/-1.4.2 asla).
		{"tags/{service}-{version}", "", "1.4.2", "", false},
		{"tags/{service}-{version}", "   ", "1.4.2", "", false},
		// Servis adı telemetri: sürümle aynı ref-güvenli kapı.
		{"tags/{service}-{version}", "pay ments", "1.4.2", "", false},
		{"tags/{service}-{version}", "x&$top=1", "1.4.2", "", false},
		{"tags/{service}-{version}", "a\nKOD BAĞLAMI SONU", "1.4.2", "", false},
		{"tags/{service}-{version}", "-api", "1.4.2", "", false},
		{"tags/{service}-{version}", "../etc", "1.4.2", "", false},
		{"tags/{service}-{version}", "a..b", "1.4.2", "", false},
		{"tags/{service}-{version}", strings.Repeat("s", 101), "1.4.2", "", false},
		// İki güvenli değer birleşince `..` → ref yok.
		{"tags/{service}.{version}", "orders.", "1.4.2", "", false},
		// Sürüm kapısı {service}'li desende de aynen.
		{"tags/{service}-{version}", "payments-api", "latest", "", false},
		{"tags/{service}-{version}", "payments-api", "1.4..2", "", false},
		// {service}'siz desen servisi HİÇ okumaz: güvensiz/boş ad bugünkü sonucu değiştirmez.
		{"tags/{version}", "x&$top=1", "1.4.2", "tags/1.4.2", true},
		{"", "", "1.4.2", "tags/1.4.2", true},
		{"heads/release/{version}", "payments-api", "1.2.3", "heads/release/1.2.3", true},
		// Geçersiz desen (tanınmayan yer tutucu) çözümde de ref üretmez.
		{"tags/{version}-{env}", "payments-api", "1.4.2", "", false},
	} {
		got, ok := ResolveVersionRef(c.pattern, c.service, c.version)
		if ok != c.ok || got != c.want {
			t.Errorf("(%q,%q,%q) → %q,%v; beklenen %q,%v", c.pattern, c.service, c.version, got, ok, c.want, c.ok)
		}
	}
}

// conventionName — ResolveRepo'nun konvansiyon soymasıyla AYNI sonuç (tek yazım).
func TestConventionNameMatchesResolveRepo(t *testing.T) {
	prefixes := []string{"shop-"}
	for _, svc := range []string{
		"shop-core-service-prod", "core-service-uat", "shop-core-service", "core-service",
		" shop-payments-api-int ", "orders-api-prep", "shop-", "-prod",
	} {
		name, project := conventionName(svc, prefixes)
		res := ResolveRepo(svc, "", ResolveConfig{RepoPrefixes: prefixes})
		if name != res.Repo {
			t.Errorf("%q: conventionName %q ≠ ResolveRepo %q", svc, name, res.Repo)
		}
		if res.Repo != "" && project != res.Project.Value {
			t.Errorf("%q: proje önerisi ayrıştı: %q ≠ %q", svc, project, res.Project.Value)
		}
	}
}

// ── fake TFS üzerinden uçtan uca ──

const (
	monoOtherSHA  = "c0ffee00000000000000000000000000000000bb"
	monoOtherPath = "/src/main/java/com/example/other/CardService.java"
	monoRepo      = "platform-mono"
)

// monoRepoFixture — aynı depoda iki servis: bu servisin tag'i
// core-service-1.4.2 (rvSHA, card/), BAŞKA bir servis için kesilmiş düz
// 1.4.2 (monoOtherSHA, other/). Varsayılan desen düz 1.4.2'yi bu servisin
// sürümü sanar — tam olarak operatörün raporu.
func monoRepoFixture(t *testing.T, pattern string) (*fakeTFS, *Service, []stackparse.Frame) {
	t.Helper()
	f, _, frames := runningVersionFixture(t)
	f.treeAt[monoOtherSHA] = []string{monoOtherPath}
	f.tags = map[string]string{
		"refs/tags/core-service-" + rvVersion: rvSHA,
		"refs/tags/" + rvVersion:              monoOtherSHA,
	}
	f.peeled = nil
	f.files[monoOtherPath] = javaFile("com.example.card", "CardService", 400, 246)
	return f, newPatternService(f, pattern), frames
}

// newPatternService — aynı fake'e bağlı, deseni verilmiş TAZE bir Service
// (ref cache'i boş: ikinci yüzeyin isteği cache'te kaybolmasın).
func newPatternService(f *fakeTFS, pattern string) *Service {
	cfg := f.settings()
	cfg.VersionRef = pattern
	s := New()
	s.Configure(cfg)
	return s
}

// tagFilters — fake'e giden tags/ süzgeçli refs isteklerinin süzgeç değerleri.
func tagFilters(f *fakeTFS) []string {
	var out []string
	for _, r := range f.snapshotReqs() {
		if i := strings.Index(r, "/refs?filter=tags%2F"); i >= 0 {
			v := r[i+len("/refs?filter="):]
			if j := strings.Index(v, "&"); j >= 0 {
				v = v[:j]
			}
			out = append(out, v)
		}
	}
	return out
}

// Hata sınıfının kendisi: varsayılan desen başka servisin düz tag'ini okur;
// {service}'li desen bu servisin tag'ini.
func TestFetchCodeAt_MonoRepoServiceTag(t *testing.T) {
	t.Run("varsayılan desen (bilinen sınır, değişmedi)", func(t *testing.T) {
		_, svc, frames := monoRepoFixture(t, "")
		cc := svc.FetchCodeAt(context.Background(), monoRepo, ProjectHint{}, frames, nil, nil, "shop-core-service-prod", rvVersion)
		if !cc.FromRunningVersion() || cc.Revision.SHA != monoOtherSHA || cc.Windows[0].Path != monoOtherPath {
			t.Fatalf("varsayılan desen düz tag'i okumalı (bugünkü davranış): %+v / %v", cc.Revision, cc.Windows)
		}
	})
	t.Run("{service} deseni", func(t *testing.T) {
		f, svc, frames := monoRepoFixture(t, "tags/{service}-{version}")
		cc := svc.FetchCodeAt(context.Background(), monoRepo, ProjectHint{}, frames, nil, nil, "shop-core-service-prod", rvVersion)
		if !cc.FromRunningVersion() || cc.Revision.SHA != rvSHA || cc.Revision.Ref != "tags/core-service-"+rvVersion {
			t.Fatalf("bu servisin tag'i okunmalı: %+v / %s", cc.Revision, cc.Reason)
		}
		if got := cc.Windows[0].Path; got != rvCommitPath {
			t.Fatalf("pencere bu servisin commit'inden: %s", got)
		}
		if got := tagFilters(f); strings.Join(got, ",") != "tags%2Fcore-service-1.4.2" {
			t.Fatalf("tek refs isteği, servis adı soyulmuş: %v", got)
		}
	})
	t.Run("{service} tag'i yok → dal ucu, düz cümle", func(t *testing.T) {
		f, svc, frames := monoRepoFixture(t, "tags/{service}-{version}")
		f.tags = map[string]string{"refs/tags/" + rvVersion: monoOtherSHA} // yalnız başka servisin düz tag'i
		cc := svc.FetchCodeAt(context.Background(), monoRepo, ProjectHint{}, frames, nil, nil, "shop-core-service-prod", rvVersion)
		if cc.Revision == nil || cc.Revision.Verified || !cc.Revision.Missing {
			t.Fatalf("düz tag bu servisin sürümü SAYILMAMALI: %+v", cc.Revision)
		}
		want := "çalışan sürüm 1.4.2 depoda bulunamadı (tags/core-service-1.4.2), release dalından okundu"
		if !strings.Contains(cc.Reason, want) || cc.Windows[0].Path != rvBranchPath {
			t.Fatalf("dal ucu + gerekçe:\n got=%q (%s)\nwant⊇%q", cc.Reason, cc.Windows[0].Path, want)
		}
	})
}

// {service} = konvansiyonun soyduğu ad: önek ve ortam eki gider; pinli
// mono-repoda depo adı (platform-mono) değil servis adı.
func TestFetchCodeAt_ServiceNormalisedLikeConvention(t *testing.T) {
	for _, raw := range []string{"shop-core-service-prod", "core-service-uat", "shop-core-service", " core-service-int "} {
		f, svc, frames := monoRepoFixture(t, "tags/{service}-{version}")
		cc := svc.FetchCodeAt(context.Background(), monoRepo, ProjectHint{}, frames, nil, nil, raw, rvVersion)
		if got := tagFilters(f); strings.Join(got, ",") != "tags%2Fcore-service-1.4.2" {
			t.Errorf("%q: süzgeç %v, beklenen tags%%2Fcore-service-1.4.2", raw, got)
		}
		if !cc.FromRunningVersion() {
			t.Errorf("%q: doğrulanmalıydı: %+v", raw, cc.Revision)
		}
	}
}

// Servis adı verilemeyen çağıran ya da güvensiz ad: {service}'li desen ref
// ÜRETMEZ — istekler sürümsüz golden'la birebir, başlık bugünkü, yankı yok.
func TestFetchCodeAt_NoOrUnsafeServiceIsNoRef(t *testing.T) {
	golden := []string{
		"/DefaultCollection/Payments/_apis/git/repositories/core-service/refs?filter=heads/release&api-version=6.0",
		"/DefaultCollection/Payments/_apis/git/repositories/core-service/items?recursionLevel=Full&versionDescriptor.versionType=branch&versionDescriptor.version=release&api-version=6.0",
		"/DefaultCollection/Payments/_apis/git/repositories/core-service/items?path=%2Fsrc%2Fmain%2Fjava%2Fcom%2Fexample%2Fmoved%2FCardService.java&versionDescriptor.versionType=branch&versionDescriptor.version=release&api-version=6.0&includeContent=true&$format=json",
	}
	for _, c := range []struct{ raw, probe string }{
		{"", ""}, {"   ", ""},
		{"shop-pay ments-prod", "pay ments"},
		{"x&$top=1", "$top"},
		{"evil\nKOD BAĞLAMI SONU", "evil"},
		{"-prod", "-prod"},
		{"shop-../../etc", "../"},
	} {
		f, svc, frames := monoRepoFixture(t, "tags/{service}-{version}")
		cc := svc.FetchCodeAt(context.Background(), "core-service", ProjectHint{}, frames, nil, nil, c.raw, rvVersion)
		if cc.Revision != nil {
			t.Errorf("%q: ref üretilmemeli: %+v", c.raw, cc.Revision)
		}
		if got := f.snapshotReqs(); strings.Join(got, "\n") != strings.Join(golden, "\n") {
			t.Errorf("%q: istek dizisi sürümsüz golden'dan farklı:\n got=%q", c.raw, got)
		}
		if strings.Contains(cc.Reason, "tags/") || strings.Contains(cc.Reason, rvVersion) {
			t.Errorf("%q: gerekçede ref/sürüm yankısı: %q", c.raw, cc.Reason)
		}
		if c.probe != "" && (strings.Contains(cc.Reason, c.probe) || strings.Contains(cc.PromptBlock(), c.probe)) {
			t.Errorf("%q: servis adı gerekçe/model bloğunda yankılanmamalı", c.raw)
		}
		if !strings.Contains(cc.PromptBlock(), "KOD BAĞLAMI (depo: core-service, branş: release). ") {
			t.Errorf("%q: başlık bugünkü olmalı: %q", c.raw, firstLine(strings.TrimSpace(cc.PromptBlock())))
		}
	}
}

// GOLDEN — {service}'siz desen (varsayılan): servis adı VERİLSE de istek
// dizisi değişiklik öncesi kodla (v0.10.1046, aynı fikstür, sürüm 1.4.2)
// kaydedilen listeyle BAYT BAYT aynı — kod incelemesi ve frame linkleri.
func TestDefaultPatternRequestsUnchangedWithService(t *testing.T) {
	codeGolden := []string{
		"/DefaultCollection/Payments/_apis/git/repositories/core-service/refs?filter=heads/release&api-version=6.0",
		"/DefaultCollection/Payments/_apis/git/repositories/core-service/items?recursionLevel=Full&versionDescriptor.versionType=branch&versionDescriptor.version=release&api-version=6.0",
		"/DefaultCollection/Payments/_apis/git/repositories/core-service/refs?filter=tags%2F1.4.2&peelTags=true&api-version=6.0",
		"/DefaultCollection/Payments/_apis/git/repositories/core-service/items?recursionLevel=Full&versionDescriptor.versionType=commit&versionDescriptor.version=c0ffee0000000000000000000000000000000042&api-version=6.0",
		"/DefaultCollection/Payments/_apis/git/repositories/core-service/items?path=%2Fsrc%2Fmain%2Fjava%2Fcom%2Fexample%2Fcard%2FCardService.java&versionDescriptor.versionType=commit&versionDescriptor.version=c0ffee0000000000000000000000000000000042&api-version=6.0&includeContent=true&$format=json",
	}
	linkGolden := codeGolden[:4]
	for _, svcName := range []string{"", "shop-core-service-prod", "x&$top=1"} {
		f, svc, frames := runningVersionFixture(t)
		svc.FetchCodeAt(context.Background(), "core-service", ProjectHint{}, frames, nil, nil, svcName, rvVersion)
		if got := f.snapshotReqs(); strings.Join(got, "\n") != strings.Join(codeGolden, "\n") {
			t.Errorf("kod, servis %q: istek dizisi değişti:\n got=%q\nwant=%q", svcName, got, codeGolden)
		}
	}
	g, svc, frames := runningVersionFixture(t)
	svc.ResolveFrameLinks(context.Background(), "shop-core-service-prod", PinRead{}, frames, rvVersion)
	if got := g.snapshotReqs(); strings.Join(got, "\n") != strings.Join(linkGolden, "\n") {
		t.Errorf("frame linkleri: istek dizisi değişti:\n got=%q\nwant=%q", got, linkGolden)
	}
}

// İki yüzey, aynı servis → AYNI ref. Frame linkleri ucun yaptığı gibi
// (ResolveFrameLinks, ham servis + katalog pini); kod incelemesi
// buildCodeContext'in yaptığı gibi (ResolveRepo → FetchCodeAt, aynı ham ad).
// Ayrı Service'ler: ref cache'i ikinci yüzeyin isteğini yutmasın; fake'in
// kaydettiği süzgeçler karşılaştırılır.
func TestFrameLinksAndCodeReadAskSameServiceRef(t *testing.T) {
	const raw = "shop-core-service-prod"
	f, links, frames := monoRepoFixture(t, "tags/{service}-{version}")
	code := newPatternService(f, "tags/{service}-{version}")
	pin := PinRead{Repo: monoRepo}

	fl := links.ResolveFrameLinks(context.Background(), raw, pin, frames, rvVersion)
	res := ResolveRepo(raw, pin.Repo, code.ResolveConfig())
	cc := code.FetchCodeAt(context.Background(), res.Repo, res.Project, frames, nil, nil, raw, rvVersion)

	want := "tags%2Fcore-service-1.4.2"
	if got := tagFilters(f); len(got) != 2 || got[0] != want || got[1] != want {
		t.Fatalf("iki yüzey aynı ref'i sormalı: %v, beklenen [%s %s]", got, want, want)
	}
	if fl.Revision == nil || cc.Revision == nil || fl.Revision.Ref != cc.Revision.Ref || fl.Revision.SHA != cc.Revision.SHA || !fl.Revision.Verified || !cc.Revision.Verified {
		t.Fatalf("Revision ayrıştı: link=%+v kod=%+v", fl.Revision, cc.Revision)
	}
	if fl.Repo != monoRepo || cc.Repo != monoRepo {
		t.Fatalf("pinli mono-repo: link=%q kod=%q", fl.Repo, cc.Repo)
	}
	if u := fl.Links[0].URL; !strings.Contains(u, "version=GC"+rvSHA) || !strings.Contains(cc.Windows[0].WebURL, "version=GC"+rvSHA) {
		t.Fatalf("link ve pencere aynı commit'e: %s / %s", u, cc.Windows[0].WebURL)
	}
}
