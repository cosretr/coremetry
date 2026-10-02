package devops

// code_running_version_test.go — v0.10.1044 (operatör: "Kod, dalın ucundan
// değil çalışan sürümden okunsun").
//
// Sözleşme:
//   - sürüm biliniyor + tag VAR → ağaç ve dosyalar o tag'in COMMIT'inden
//     (versionType=commit); bağlam "çalışan sürüm" der, link GC<sha>
//   - sürüm biliniyor + tag YOK → dal sırası (bugünkü), gerekçe bunu düz
//     cümleyle söyler, kod yine gelir
//   - sürüm yok / yer tutucu → istekler BAYT BAYT bugünkü (golden)
//   - ref→commit cevabı cache'li, YOKLUK dahil; hata cache'lenmez
//   - commit ağacı ile dal ağacı (ve alt-ağaçları) aynı cache girdisini
//     paylaşmaz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/stackparse"
)

const (
	rvSHA        = "c0ffee0000000000000000000000000000000042"
	rvVersion    = "1.4.2"
	rvBranchPath = "/src/main/java/com/example/moved/CardService.java"
	rvCommitPath = "/src/main/java/com/example/card/CardService.java"
)

// runningVersionFixture — dal ucunda dosya TAŞINMIŞ (moved/), çalışan
// sürümün commit'inde eski yerinde (card/). Yol ve içerik commit'ten
// gelmezse pencere moved/'dan kesilir: satır kayması sınıfının ta kendisi.
func runningVersionFixture(t *testing.T) (*fakeTFS, *Service, []stackparse.Frame) {
	t.Helper()
	f := newFakeTFS(t)
	f.tree = []string{rvBranchPath}
	f.treeAt = map[string][]string{rvSHA: {rvCommitPath}}
	f.tags = map[string]string{"refs/tags/" + rvVersion: "tagobj0042"}
	f.peeled = map[string]string{"refs/tags/" + rvVersion: rvSHA}
	f.files[rvBranchPath] = javaFile("com.example.card", "CardService", 400, 246)
	f.files[rvCommitPath] = javaFile("com.example.card", "CardService", 400, 246)
	svc := New()
	svc.Configure(f.settings())
	frames := []stackparse.Frame{frame("com.example.card.CardService", "charge", "CardService.java", 246)}
	return f, svc, frames
}

func (f *fakeTFS) snapshotReqs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reqs...)
}

func (f *fakeTFS) countReqs(frag string) int {
	n := 0
	for _, r := range f.snapshotReqs() {
		if strings.Contains(r, frag) {
			n++
		}
	}
	return n
}

func TestFetchCodeAt_VersionTagReadsCommit(t *testing.T) {
	f, svc, frames := runningVersionFixture(t)
	cc := svc.FetchCodeAt(context.Background(), "core-service", ProjectHint{}, frames, nil, nil, rvVersion)
	if cc.Empty() {
		t.Fatalf("kod gelmedi: %s", cc.Reason)
	}
	if !cc.FromRunningVersion() || cc.Revision.SHA != rvSHA || cc.Revision.Ref != "tags/"+rvVersion {
		t.Fatalf("çalışan sürümün commit'i kullanılmalıydı: %+v", cc.Revision)
	}
	if got := cc.Windows[0].Path; got != rvCommitPath {
		t.Fatalf("yol commit ağacından gelmeli (card/), dal ucundan (moved/) değil: %s", got)
	}
	f.mu.Lock()
	items := append([]string(nil), f.itemVersions...)
	trees := append([]string(nil), f.treeVersions...)
	f.mu.Unlock()
	if len(items) == 0 {
		t.Fatal("dosya isteği yok")
	}
	for _, v := range items {
		if v != "commit:"+rvSHA {
			t.Fatalf("dosya commit'ten (versionType=commit) okunmalı; görülen: %v", items)
		}
	}
	sawCommitTree := false
	for _, v := range trees {
		if v == "commit:"+rvSHA {
			sawCommitTree = true
		}
	}
	if !sawCommitTree {
		t.Fatalf("ağaç commit'ten okunmalı; görülen: %v", trees)
	}
	if u := cc.Windows[0].WebURL; !strings.Contains(u, "version=GC"+rvSHA) {
		t.Fatalf("pencere linki okunan commit'e gitmeli (GC): %s", u)
	}
	if strings.Contains(cc.Reason, "dalından okundu") {
		t.Fatalf("doğrulanmış sürümde geri-düşüş notu olmamalı: %q", cc.Reason)
	}
	pb := cc.PromptBlock()
	if !strings.Contains(pb, "KOD BAĞLAMI (depo: core-service, çalışan sürüm: 1.4.2, commit c0ffee00)") {
		t.Fatalf("model bloğu kodun hangi ref'ten geldiğini söylemeli: %q", firstLine(strings.TrimSpace(pb)))
	}
	if cc.Outcome != CodeOK {
		t.Fatalf("sınıf ok olmalı: %s (%s)", cc.Outcome, cc.Reason)
	}
}

func TestFetchCodeAt_VersionTagMissingFallsBackToBranch(t *testing.T) {
	f, svc, frames := runningVersionFixture(t)
	cc := svc.FetchCodeAt(context.Background(), "core-service", ProjectHint{}, frames, nil, nil, "9.9.9")
	if cc.Empty() {
		t.Fatalf("ref yokken de kod gelmeli (dal ucundan): %s", cc.Reason)
	}
	if cc.Revision == nil || cc.Revision.Verified || !cc.Revision.Missing {
		t.Fatalf("ref yok → doğrulanmamış + Missing: %+v", cc.Revision)
	}
	if got := cc.Windows[0].Path; got != rvBranchPath {
		t.Fatalf("dal ağacına düşmeli: %s", got)
	}
	want := "çalışan sürüm 9.9.9 depoda bulunamadı (tags/9.9.9), release dalından okundu"
	if !strings.Contains(cc.Reason, want) {
		t.Fatalf("gerekçe düz cümleyle söylemeli:\n got=%q\nwant⊇%q", cc.Reason, want)
	}
	f.mu.Lock()
	items := append([]string(nil), f.itemVersions...)
	f.mu.Unlock()
	for _, v := range items {
		if v != "branch:release" {
			t.Fatalf("dosya dal ucundan okunmalı: %v", items)
		}
	}
	if u := cc.Windows[0].WebURL; !strings.Contains(u, "version=GBrelease") {
		t.Fatalf("link dal ucunda kalmalı: %s", u)
	}
	pb := cc.PromptBlock()
	if !strings.Contains(pb, "(depo: core-service, branş: release; çalışan sürüm 9.9.9 depoda bulunamadı — satırlar çalışan koddan farklı olabilir)") {
		t.Fatalf("model bloğu dal ucunu ve eksik sürümü söylemeli: %q", firstLine(strings.TrimSpace(pb)))
	}
	if cc.Outcome != CodeOK {
		t.Fatalf("geri-düşüş kayıp değil, sınıf ok: %s", cc.Outcome)
	}
}

// Sorgu hatası: kod yine gelir, gerekçe "okunamadı" der; hata CACHE'LENMEZ
// (ikinci tık yeniden sorar — geçici arıza 10 dk "tag yok" sayılmaz).
func TestFetchCodeAt_RefLookupErrorFallsBackAndIsNotCached(t *testing.T) {
	f, svc, frames := runningVersionFixture(t)
	f.tagsFail = true
	for i := 0; i < 2; i++ {
		cc := svc.FetchCodeAt(context.Background(), "core-service", ProjectHint{}, frames, nil, nil, rvVersion)
		if cc.Empty() || cc.Windows[0].Path != rvBranchPath {
			t.Fatalf("hata da dal ucuna düşmeli, kod gelmeli: %+v / %s", cc.Windows, cc.Reason)
		}
		if !strings.Contains(cc.Reason, "çalışan sürüm 1.4.2 okunamadı (ref sorgusu başarısız") ||
			!strings.Contains(cc.Reason, "release dalından okundu") {
			t.Fatalf("gerekçe: %q", cc.Reason)
		}
	}
	if n := f.countReqs("filter=tags%2F"); n != 2 {
		t.Fatalf("hata cache'lenmemeli: tag sorgusu %d kez, 2 beklenen", n)
	}
}

// Ref→commit cevabı cache'li — YOKLUK DAHİL: tag'leri desene uymayan bir
// kurulum tık başına refs isteği ödemez.
func TestFetchCodeAt_RefLookupCachedIncludingNegative(t *testing.T) {
	for _, c := range []struct {
		name, version string
	}{{"tag var", rvVersion}, {"tag yok", "9.9.9"}} {
		t.Run(c.name, func(t *testing.T) {
			f, svc, frames := runningVersionFixture(t)
			for i := 0; i < 3; i++ {
				svc.FetchCodeAt(context.Background(), "core-service", ProjectHint{}, frames, nil, nil, c.version)
			}
			if n := f.countReqs("filter=tags%2F"); n != 1 {
				t.Fatalf("ref sorgusu cache'lenmeli: %d istek, 1 beklenen", n)
			}
		})
	}
}

// GOLDEN — sürüm yok ya da yer tutucu: istek dizisi bugünküyle BAYT BAYT
// aynı. Liste v0.10.1039 kodunda (değişiklik öncesi) aynı fikstürle
// kaydedildi; FetchCode (sürümsüz sarmalayıcı) ve FetchCodeAt(yer tutucu)
// aynı listeyi üretmeli. Tek ekstra refs isteği bile kırmızıdır.
func TestFetchCodeAt_NoVersionRequestsUnchanged(t *testing.T) {
	golden := []string{
		"/DefaultCollection/Payments/_apis/git/repositories/core-service/refs?filter=heads/release&api-version=6.0",
		"/DefaultCollection/Payments/_apis/git/repositories/core-service/items?recursionLevel=Full&versionDescriptor.versionType=branch&versionDescriptor.version=release&api-version=6.0",
		"/DefaultCollection/Payments/_apis/git/repositories/core-service/items?path=%2Fsrc%2Fmain%2Fjava%2Fcom%2Fexample%2Fmoved%2FCardService.java&versionDescriptor.versionType=branch&versionDescriptor.version=release&api-version=6.0&includeContent=true&$format=json",
	}
	for _, c := range []struct {
		name string
		run  func(*Service, []stackparse.Frame) CodeContext
	}{
		{"FetchCode (sürümsüz)", func(s *Service, fr []stackparse.Frame) CodeContext {
			return s.FetchCode(context.Background(), "core-service", ProjectHint{}, fr, nil, nil)
		}},
		{"boş sürüm", func(s *Service, fr []stackparse.Frame) CodeContext {
			return s.FetchCodeAt(context.Background(), "core-service", ProjectHint{}, fr, nil, nil, "")
		}},
		{"yer tutucu latest", func(s *Service, fr []stackparse.Frame) CodeContext {
			return s.FetchCodeAt(context.Background(), "core-service", ProjectHint{}, fr, nil, nil, "latest")
		}},
		{"yer tutucu SNAPSHOT", func(s *Service, fr []stackparse.Frame) CodeContext {
			return s.FetchCodeAt(context.Background(), "core-service", ProjectHint{}, fr, nil, nil, "2.3.0-SNAPSHOT")
		}},
		{"yer tutucu master", func(s *Service, fr []stackparse.Frame) CodeContext {
			return s.FetchCodeAt(context.Background(), "core-service", ProjectHint{}, fr, nil, nil, "master")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, svc, frames := runningVersionFixture(t)
			cc := c.run(svc, frames)
			got := f.snapshotReqs()
			if strings.Join(got, "\n") != strings.Join(golden, "\n") {
				t.Fatalf("istek dizisi değişti:\n got=%q\nwant=%q", got, golden)
			}
			if cc.Revision != nil {
				t.Fatalf("sürüm yokken Revision nil kalmalı: %+v", cc.Revision)
			}
			if pb := cc.PromptBlock(); !strings.Contains(pb, "KOD BAĞLAMI (depo: core-service, branş: release). ") {
				t.Fatalf("model bloğu başlığı bayt bayt eskisi olmalı: %q", firstLine(strings.TrimSpace(pb)))
			}
		})
	}
}

// Ek istek bütçesi (rapor sayıları bu testten): sürüm biliniyor + tag var →
// soğukta +1 refs +1 commit ağacı, sıcakta +0; tag yok → soğukta +1 refs,
// sıcakta +0. Dosya çekimi sayısı değişmez (yalnız ref'i değişir).
func TestFetchCodeAt_ExtraRequestBudget(t *testing.T) {
	count := func(f *fakeTFS) (refs, trees, items int) {
		for _, r := range f.snapshotReqs() {
			switch {
			case strings.Contains(r, "/refs?"):
				refs++
			case strings.Contains(r, "recursionLevel=Full"):
				trees++
			case strings.Contains(r, "/items?path="):
				items++
			}
		}
		return
	}
	run := func(version string, times int) (cold, warm [3]int) {
		f, svc, frames := runningVersionFixture(t)
		svc.FetchCodeAt(context.Background(), "core-service", ProjectHint{}, frames, nil, nil, version)
		r, tr, it := count(f)
		cold = [3]int{r, tr, it}
		for i := 1; i < times; i++ {
			svc.FetchCodeAt(context.Background(), "core-service", ProjectHint{}, frames, nil, nil, version)
		}
		r2, tr2, it2 := count(f)
		warm = [3]int{r2 - r, tr2 - tr, it2 - it}
		return
	}
	base, baseWarm := run("", 2)
	tagCold, tagWarm := run(rvVersion, 2)
	missCold, missWarm := run("9.9.9", 2)
	t.Logf("istek [refs, ağaç, dosya] — sürüm yok: soğuk %v sıcak %v; tag var: soğuk %v sıcak %v; tag yok: soğuk %v sıcak %v",
		base, baseWarm, tagCold, tagWarm, missCold, missWarm)
	if tagCold != [3]int{base[0] + 1, base[1] + 1, base[2]} {
		t.Errorf("tag var, soğuk: +1 refs +1 ağaç beklenir; taban %v, görülen %v", base, tagCold)
	}
	if tagWarm != baseWarm {
		t.Errorf("tag var, sıcak: ek istek olmamalı; taban %v, görülen %v", baseWarm, tagWarm)
	}
	if missCold != [3]int{base[0] + 1, base[1], base[2]} {
		t.Errorf("tag yok, soğuk: yalnız +1 refs beklenir; taban %v, görülen %v", base, missCold)
	}
	if missWarm != baseWarm {
		t.Errorf("tag yok, sıcak: negatif cache — ek istek olmamalı; taban %v, görülen %v", baseWarm, missWarm)
	}
}

// Ağaç cache'i: tag commit'i ile dal ucu AYNI girdiyi paylaşmaz — iki yönde.
func TestFetchCodeAt_CommitAndBranchTreesDoNotCollide(t *testing.T) {
	f, svc, frames := runningVersionFixture(t)
	ctx := context.Background()
	if cc := svc.FetchCodeAt(ctx, "core-service", ProjectHint{}, frames, nil, nil, rvVersion); cc.Windows[0].Path != rvCommitPath {
		t.Fatalf("ilk çekim commit'ten: %s", cc.Windows[0].Path)
	}
	if cc := svc.FetchCodeAt(ctx, "core-service", ProjectHint{}, frames, nil, nil, ""); cc.Windows[0].Path != rvBranchPath {
		t.Fatalf("sürümsüz çekim dal ağacını görmeli, cache'teki commit ağacını değil: %s", cc.Windows[0].Path)
	}
	if cc := svc.FetchCodeAt(ctx, "core-service", ProjectHint{}, frames, nil, nil, rvVersion); cc.Windows[0].Path != rvCommitPath {
		t.Fatalf("sürümlü çekim commit ağacını görmeli, cache'teki dal ağacını değil: %s", cc.Windows[0].Path)
	}
	cfg := f.settings()
	br, ok1 := svc.code.get(treeCacheKey(cfg, "core-service", refCacheName(RefSpec{Kind: "branch", Name: "release"})))
	cm, ok2 := svc.code.get(treeCacheKey(cfg, "core-service", refCacheName(RefSpec{Kind: "commit", Name: rvSHA})))
	if !ok1 || !ok2 || br.paths[0] != rvBranchPath || cm.paths[0] != rvCommitPath {
		t.Fatalf("iki ayrı girdi beklenir: dal=%v(%v) commit=%v(%v)", br.paths, ok1, cm.paths, ok2)
	}
}

// Kapsamlı alt-ağaç cache'i: aynı kapsam, commit ve dal için AYRI istek; aynı
// commit ikinci kez cache'ten. FetchCodeAt anahtarı refCacheName'den kurar.
func TestScopedTreeAtKeysByRefKind(t *testing.T) {
	f := newFakeTFS(t)
	f.tree = []string{"/src/main/java/com/example/card/CardService.java"}
	svc := New()
	cfg := f.settings()
	svc.Configure(cfg)
	cli := svc.clientFor(false)
	ctx := context.Background()
	commit := RefSpec{Kind: "commit", Name: rvSHA}
	branch := RefSpec{Kind: "branch", Name: "release"}
	const sp = "/src/main/java/com/example/card"
	keyFor := func(r RefSpec) string { return treeCacheKey(cfg, "core-service", refCacheName(r)) }

	if _, cached, err := svc.scopedTreeAt(ctx, cli, cfg, "6.0", "core-service", commit, sp, keyFor(commit)); err != nil || cached {
		t.Fatalf("commit alt-ağacı: cached=%v err=%v", cached, err)
	}
	if _, cached, err := svc.scopedTreeAt(ctx, cli, cfg, "6.0", "core-service", branch, sp, keyFor(branch)); err != nil || cached {
		t.Fatalf("dal alt-ağacı commit'in girdisinden gelmemeli: cached=%v err=%v", cached, err)
	}
	if _, cached, _ := svc.scopedTreeAt(ctx, cli, cfg, "6.0", "core-service", commit, sp, keyFor(commit)); !cached {
		t.Fatal("aynı commit kapsamı ikinci kez cache'ten gelmeli")
	}
	f.mu.Lock()
	scoped := f.hits["scoped"]
	f.mu.Unlock()
	if scoped != 2 {
		t.Fatalf("2 gerçek kapsamlı istek beklenir (commit + dal), görülen %d", scoped)
	}
	if f.countReqs("scopePath=") != 2 || f.countReqs("versionType=commit&versionDescriptor.version="+rvSHA) != 1 {
		t.Fatalf("commit kapsamı versionType=commit taşımalı: %v", f.snapshotReqs())
	}
	if keyFor(branch) != treeCacheKey(cfg, "core-service", "release") {
		t.Fatal("dal anahtarı bayt bayt eskisi kalmalı (mevcut cache/pin testleri)")
	}
	src := flatWSDevops(readDevopsSource(t, "code.go"))
	if !strings.Contains(src, "treeKey: treeCacheKey(cfg, repo, refCacheName(src)),") {
		t.Fatal("FetchCodeAt kapsamlı av anahtarını refCacheName(src)'den kurmalı — commit ile dal aynı alt-ağaç girdisini paylaşırdı")
	}
}

// Tek çözücü: frame linkleri ve kod incelemesi resolveRevision'dan geçer;
// ikinci bir sürüm→ref→commit yazımı ilk düzeltmede ayrışırdı.
func TestRevisionResolverIsShared(t *testing.T) {
	for _, file := range []string{"frame_links.go", "code.go"} {
		src := readDevopsSource(t, file)
		if !strings.Contains(src, "s.resolveRevision(ctx, ") {
			t.Errorf("%s resolveRevision'ı çağırmıyor", file)
		}
		if strings.Contains(src, "s.refCommit(") {
			t.Errorf("%s refCommit'i doğrudan çağırıyor — ikinci çözücü", file)
		}
	}
}

// Window link damgası: zincir penceresi commit'e, arama isabeti dal ucuna.
func TestStampWindowLinksAtCommit(t *testing.T) {
	cfg := Settings{BaseURL: "https://tfs.example.com", Collection: "DefaultCollection", Project: "SHOP"}
	ws := []CodeWindow{
		{Path: "/src/A.java", Line: 10},
		{Path: "other-repo:/src/B.java", Line: 20, Repo: "other-repo", Branch: "master"},
	}
	stampWindowLinksAt(cfg, "svc-repo", "release", RefSpec{Kind: "commit", Name: rvSHA}, ws)
	if !strings.Contains(ws[0].WebURL, "version=GC"+rvSHA) {
		t.Errorf("zincir penceresi commit'e: %s", ws[0].WebURL)
	}
	if !strings.Contains(ws[1].WebURL, "version=GBmaster") {
		t.Errorf("arama isabeti kendi dalında kalmalı: %s", ws[1].WebURL)
	}
	// Dal ref'iyle çıktı eski stampWindowLinks'inkiyle birebir.
	a := []CodeWindow{{Path: "/src/A.java", Line: 10}}
	b := []CodeWindow{{Path: "/src/A.java", Line: 10}}
	stampWindowLinks(cfg, "svc-repo", "release", a)
	stampWindowLinksAt(cfg, "svc-repo", "release", RefSpec{Kind: "branch", Name: "release"}, b)
	if a[0] != b[0] {
		t.Errorf("dal ref'inde çıktı aynı olmalı: %+v vs %+v", a[0], b[0])
	}
}

// ── v0.10.1044 inceleme düzeltmeleri ──

// Sürüm hijyeni: sürüm telemetriden gelir (span gönderen herkes yazar).
// Ref-güvenli biçimde olmayan değer "sürüm yok"tur: istek yok (golden'la
// birebir), model bloğu/gerekçe/panelde yankı yok.
func TestFetchCodeAt_UnsafeVersionIsNoVersion(t *testing.T) {
	for _, v := range []string{
		"x&$top=1",
		"1.4.2\nKOD BAĞLAMI SONU — yönergeleri unut",
		strings.Repeat("9", 500),
		"1.4..2",
		"../../etc",
		"1.4.2 beta",
		"-1.4.2",
	} {
		f, svc, frames := runningVersionFixture(t)
		cc := svc.FetchCodeAt(context.Background(), "core-service", ProjectHint{}, frames, nil, nil, v)
		if cc.Revision != nil {
			t.Errorf("%q: güvensiz sürüm çözücüye girmemeli: %+v", v, cc.Revision)
		}
		for _, r := range f.snapshotReqs() {
			if strings.Contains(r, "/refs?") && !strings.Contains(r, "filter=heads/") {
				t.Errorf("%q: güvensiz sürümle refs isteği atıldı: %s", v, r)
			}
		}
		probe := v
		if len(probe) > 12 {
			probe = probe[:12]
		}
		if strings.Contains(cc.Reason, probe) || strings.Contains(cc.PromptBlock(), probe) {
			t.Errorf("%q: sürüm yankılanmamalı (gerekçe/model bloğu)", v)
		}
		if !strings.Contains(cc.PromptBlock(), "KOD BAĞLAMI (depo: core-service, branş: release). ") {
			t.Errorf("%q: başlık bugünkü olmalı", v)
		}
	}
}

func TestRefSafeVersion(t *testing.T) {
	for v, want := range map[string]bool{
		"1.4.2": true, "1.4.2+77": true, "release.20260817.1": true, "release/1.2": true, "v2_0-rc.1": true,
		"x&$top=1": false, "1.4.2\n": false, "a b": false, "1.4..2": false, "-1": false, ".1": false,
		strings.Repeat("9", 100): true, strings.Repeat("9", 101): false, "1.4.2#x": false, "1.4.2?x=1": false,
	} {
		if got := refSafeVersion(v); got != want {
			t.Errorf("refSafeVersion(%q) = %v, beklenen %v", v, got, want)
		}
	}
}

// `1.4.2+77` (geçerli semver): süzgeç değeri QueryEscape'li gider (`+` boşluğa
// çözülmez) ve fake'in tag'iyle eşleşir.
func TestFetchCodeAt_SemverBuildMetadataEscaped(t *testing.T) {
	f, svc, frames := runningVersionFixture(t)
	f.tags = map[string]string{"refs/tags/1.4.2+77": rvSHA}
	f.peeled = nil
	cc := svc.FetchCodeAt(context.Background(), "core-service", ProjectHint{}, frames, nil, nil, "1.4.2+77")
	if !cc.FromRunningVersion() || cc.Revision.SHA != rvSHA {
		t.Fatalf("1.4.2+77 tag'i bulunmalı: %+v / %s", cc.Revision, cc.Reason)
	}
	if f.countReqs("refs?filter=tags%2F1.4.2%2B77&peelTags=true&") != 1 {
		t.Fatalf("süzgeç QueryEscape'li gitmeli: %v", f.snapshotReqs())
	}
}

// Annotated tag: commit YALNIZ peelTags=true ile gelir (fake gerçek API gibi);
// lightweight tag'de objectId zaten commit.
func TestFetchCodeAt_AnnotatedAndLightweightTags(t *testing.T) {
	t.Run("annotated", func(t *testing.T) {
		f, svc, frames := runningVersionFixture(t) // tagobj0042 → peeled rvSHA
		cc := svc.FetchCodeAt(context.Background(), "core-service", ProjectHint{}, frames, nil, nil, rvVersion)
		if !cc.FromRunningVersion() || cc.Revision.SHA != rvSHA {
			t.Fatalf("annotated tag peeled commit'e çözülmeli: %+v / %s", cc.Revision, cc.Reason)
		}
		if f.countReqs("peelTags=true") != 1 {
			t.Fatal("refs isteği peelTags=true taşımalı")
		}
		for _, v := range f.treeVersions {
			if v == "commit:tagobj0042" {
				t.Fatalf("tag NESNESİ id'siyle ağaç istenmemeli: %v", f.treeVersions)
			}
		}
	})
	t.Run("lightweight", func(t *testing.T) {
		f, svc, frames := runningVersionFixture(t)
		f.tags = map[string]string{"refs/tags/" + rvVersion: rvSHA}
		f.peeled = nil
		cc := svc.FetchCodeAt(context.Background(), "core-service", ProjectHint{}, frames, nil, nil, rvVersion)
		if !cc.FromRunningVersion() || cc.Revision.SHA != rvSHA || cc.Windows[0].Path != rvCommitPath {
			t.Fatalf("lightweight tag objectId=commit: %+v / %s", cc.Revision, cc.Reason)
		}
	})
}

// Çözülemeyen 2xx refs gövdesi HATADIR: cache'lenmez, gerekçe "okunamadı".
func TestFetchCodeAt_UnparsableRefsBodyIsError(t *testing.T) {
	f, svc, frames := runningVersionFixture(t)
	f.tagsRaw = `{"value": 42}`
	for i := 0; i < 2; i++ {
		cc := svc.FetchCodeAt(context.Background(), "core-service", ProjectHint{}, frames, nil, nil, rvVersion)
		if cc.Revision == nil || cc.Revision.Missing || !strings.Contains(cc.Reason, "okunamadı (ref sorgusu başarısız") {
			t.Fatalf("bozuk gövde 'ref yok' değil hata olmalı: %+v / %q", cc.Revision, cc.Reason)
		}
		if cc.Empty() {
			t.Fatal("kod yine dal ucundan gelmeli")
		}
	}
	if n := f.countReqs("filter=tags%2F"); n != 2 {
		t.Fatalf("hata cache'lenmemeli: %d istek, 2 beklenen", n)
	}
}

// Alt süre tavanı: commit ağacı kalan sürenin yarısında gelmezse dal ağacına
// düşülür; dal dosyalarına süre kalır, kod gelir, gerekçe dürüst.
func TestFetchCodeAt_SlowCommitTreeFallsBackInTime(t *testing.T) {
	f, svc, frames := runningVersionFixture(t)
	f.commitTreeDelay = 5 * time.Second
	svc.codeDeadline = 800 * time.Millisecond
	start := time.Now()
	cc := svc.FetchCodeAt(context.Background(), "core-service", ProjectHint{}, frames, nil, nil, rvVersion)
	if cc.Empty() || cc.Windows[0].Path != rvBranchPath {
		t.Fatalf("yavaş commit ağacında dal koduna düşmeli: %+v / %s", cc.Windows, cc.Reason)
	}
	if !strings.Contains(cc.Reason, "commit ağacı süre payında okunamadı") || !strings.Contains(cc.Reason, "release dalından okundu") {
		t.Fatalf("gerekçe: %q", cc.Reason)
	}
	if cc.Outcome != CodeOK {
		t.Fatalf("dal kodu geldi, sınıf ok olmalı: %s", cc.Outcome)
	}
	if el := time.Since(start); el > 800*time.Millisecond {
		t.Fatalf("toplam tavanı aşmamalı: %s", el)
	}
	if _, ok := svc.code.get(treeCacheKey(f.settings(), "core-service", refCacheName(RefSpec{Kind: "commit", Name: rvSHA}))); ok {
		t.Fatal("süresi dolan commit ağacı cache'e yazılmamalı")
	}
}

// Gövde YARIDA kesilirse (süre tavanı) hata döner; yarım ağaç "kesik ama
// kullanılabilir" diye 10 dk cache'lenmez. Dal ağaçları için de aynı kusur.
func TestCappedGetMidBodyStallIsError(t *testing.T) {
	stalled := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":2,"value":[{"path":"/src/A.java","gitObjectType":"blob"},`))
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-stalled:
		}
	}))
	defer srv.Close()
	defer close(stalled)
	svc := New()
	cfg := Settings{BaseURL: srv.URL, Collection: "DefaultCollection", Project: "Payments", PAT: "test-pat", Flavor: FlavorServer}
	svc.Configure(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := svc.repoTreeAt(ctx, svc.clientFor(false), cfg, "6.0", "core-service", RefSpec{Kind: "branch", Name: "release"})
	if err == nil {
		t.Fatal("yarım gövde başarı sayılmamalı")
	}
	if _, ok := svc.code.get(treeCacheKey(cfg, "core-service", "release")); ok {
		t.Fatal("yarım ağaç cache'e yazılmamalı")
	}
}
