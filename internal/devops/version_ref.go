package devops

// version_ref.go — v0.10.590. Olay anındaki sürümü (image tag / service.version)
// VCS'te bir ref'e, oradan bir COMMIT'e bağlar. Bugüne dek kod branşın
// UCUndan geliyordu ve K2 uyarısı her linkteydi; çözülebilen sürümde link
// ve dosya ağacı AYNI commit'ten gider, uyarı "doğrulandı"ya döner.
// Çözülemeyen sürümde bugünkü davranış aynen kalır — hiçbir şey gerilemez.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// RefSpec — Azure DevOps versionDescriptor: branch | tag | commit.
type RefSpec struct {
	Kind string
	Name string
}

// DefaultVersionRef — ayar boşsa. En yaygın kural: image tag'i ile aynı adlı
// git tag'i. Bulunamazsa branş ucuna düşülür (uyarıyla); yani yanlış bir
// varsayılanın bedeli bir ekstra refs isteğidir, yanlış link değil.
const DefaultVersionRef = "tags/{version}"

// versionPlaceholders — chstore/deploys.go placeholderVersionList'in Go
// ikizi (devops chstore'a bağımlı değil). Yer tutucu bir sürümü ref'e
// bağlamak yanlış tag'e bağlanmaktır; bağlanmamaktan kötü.
//
// v0.10.1044 — SQL listesiyle EŞİTLENDİ (main/master/HEAD/null/n/a/NULL
// eksikti: "master" sürümü tags/master'a bağlanmaya çalışıyordu). SQL'deki
// 'snapshot' aşağıdaki SNAPSHOT alt-dizgi kuralıyla zaten kapsanıyor.
// running_version_test.go bu listeyi SQL'e ve frontend/src/lib/
// runningVersion.ts'e karşı KAYNAKTAN pinler — üçü ayrışamaz.
var versionPlaceholders = map[string]bool{
	"": true, "0.0.1": true, "0.0.1-SNAPSHOT": true, "0.1.0-SNAPSHOT": true,
	"1.0-SNAPSHOT": true, "1.0.0-SNAPSHOT": true, "${project.version}": true,
	"${version}": true, "unknown": true, "latest": true, "dev": true, "none": true,
	"main": true, "master": true, "HEAD": true, "null": true, "n/a": true, "NULL": true,
}

// IsPlaceholderVersion — yer tutucu / SNAPSHOT / boş.
func IsPlaceholderVersion(v string) bool {
	v = strings.TrimSpace(v)
	return versionPlaceholders[v] || strings.Contains(strings.ToUpper(v), "SNAPSHOT")
}

// runningVersionKeys — v0.10.1044: "olay anında koşan sürüm"ün resource
// attribute öncelik zinciri. frontend/src/lib/runningVersion.ts KEYS ile
// BİREBİR ve chstore/deploys.go effectiveVersionExpr'in ilk üç halkasıyla
// aynı sıra: image tag deployment gerçeğinin kendisi (tag değişimi ⇔
// rollout), service.version filoda sabit kalabiliyor. Sıra
// running_version_test.go'da iki kaynağa karşı pinli.
var runningVersionKeys = []string{"container.image.tag", "k8s.container.image.tag", "service.version"}

// RunningVersion — SAF: tek span'in resource attribute'larından koşan sürüm.
// runningVersion.ts'in Go ikizi: zincirdeki ilk yer-tutucu-OLMAYAN değer;
// hiçbiri yoksa "". Frame linkleri (tarayıcı) ve kod incelemesi (sunucu)
// aynı span için aynı sürümü seçmeli — yoksa link bir commit'e, kod başka
// bir commit'e bakar.
func RunningVersion(res map[string]string) string {
	for _, k := range runningVersionKeys {
		if v := strings.TrimSpace(res[k]); !IsPlaceholderVersion(v) {
			return v
		}
	}
	return ""
}

// VersionSample — PickRunningVersion'ın girdisi: bir span'in resource
// attribute'ları + başlangıç zamanı (unix ns). devops chstore'a bağımlı
// olmadığı için span tipi burada yok; çağıran doldurur.
type VersionSample struct {
	Resource map[string]string
	Start    int64
}

// PickRunningVersion — SAF: stack'i basan servisin span'lerinden çalışan
// sürüm (v0.10.1044, operatör: "Kod, dalın ucundan değil çalışan sürümden
// okunsun"). Her span RunningVersion'la çözülür, boşlar sayılmaz; EN SIK
// değer kazanır. Rolling deploy ortasında iki sürüm eşit sayıda görünebilir:
// eşitlikte EN YENİ span'in sürümü (en geç başlangıç) — yeni rollout'un
// kodu; o da eşitse sözlük sırasında küçük olan. Deterministik: girdi sırası
// sonucu değiştirmez. Hiç çözülen yoksa "" (çağıran bugünkü davranışta kalır).
func PickRunningVersion(samples []VersionSample) string {
	type agg struct {
		n      int
		latest int64
	}
	seen := map[string]*agg{}
	for _, sm := range samples {
		v := RunningVersion(sm.Resource)
		if v == "" {
			continue
		}
		a := seen[v]
		if a == nil {
			a = &agg{latest: sm.Start}
			seen[v] = a
		}
		a.n++
		if sm.Start > a.latest {
			a.latest = sm.Start
		}
	}
	best, bestAgg := "", (*agg)(nil)
	for v, a := range seen {
		switch {
		case bestAgg == nil,
			a.n > bestAgg.n,
			a.n == bestAgg.n && a.latest > bestAgg.latest,
			a.n == bestAgg.n && a.latest == bestAgg.latest && v < best:
			best, bestAgg = v, a
		}
	}
	return best
}

// NormalizeVersionRef — ayar deseni: boş → varsayılan; `{version}` taşımalı;
// `tags/` ya da `heads/` ile başlamalı (refs/ öneki YAZILMAZ, API filter
// biçimi). Geçersiz desen 400 — sessizce varsayılana düşmek operatörün
// yazdığını yok saymak olurdu.
func NormalizeVersionRef(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return DefaultVersionRef, nil
	}
	if !strings.Contains(p, "{version}") {
		return "", errors.New("desen {version} yer tutucusunu taşımalı")
	}
	if !strings.HasPrefix(p, "tags/") && !strings.HasPrefix(p, "heads/") {
		return "", errors.New("desen tags/ ya da heads/ ile başlamalı (refs/ öneksiz)")
	}
	return p, nil
}

// ResolveVersionRef — desen + sürüm → ref adı ("tags/release.1"). Yer tutucu
// sürüm ref ÜRETMEZ.
//
// v0.10.1044 — TEK GİRİŞ KAPISI: sürüm telemetriden gelir (service.version /
// image tag; span gönderebilen herkes yazar) ve buradan sonra PAT'lı bir
// isteğin sorgusuna, modelin bloğuna (çit dışında), gerekçe satırına ve
// panele gider. Biçimi ref-güvenli olmayan sürüm (refSafeVersion) "sürüm
// yok" sayılır: istek yok, bugünkü başlık, hiçbir yerde yankılanmaz.
func ResolveVersionRef(pattern, version string) (string, bool) {
	p, err := NormalizeVersionRef(pattern)
	if err != nil {
		return "", false
	}
	version = strings.TrimSpace(version)
	if IsPlaceholderVersion(version) || !refSafeVersion(version) {
		return "", false
	}
	return strings.ReplaceAll(p, "{version}", version), true
}

// refSafeVersionRe — v0.10.1044: muhafazakâr ref-güvenli sürüm biçimi. Harf/
// rakamla başlar, yalnız harf, rakam, `. _ + - /`; en çok 100 karakter.
// Boşluk, satır sonu, `& = $ ? # % @ { } ~ ^ :` girmez — sorgu dizesine
// parametre ekleyemez, model bloğunda yeni satır açamaz. Gerçek sürüm
// biçimleri (1.4.2, 1.4.2+77, release.20260817.1, release/1.2) geçer.
var refSafeVersionRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+\-/]{0,99}$`)

// refSafeVersion — SAF: sürüm ref adına girebilir mi? `..` (yol tırmanma /
// git'in yasak ref dizisi) ayrıca reddedilir.
func refSafeVersion(v string) bool {
	return refSafeVersionRe.MatchString(v) && !strings.Contains(v, "..")
}

// refCommitFromBody — SAF: refs cevabından TAM ad eşleşmesiyle commit SHA.
// Annotated tag'de objectId TAG NESNESİDİR, commit peeledObjectId'dedir —
// o yüzden peeled önce. Önek eşleşmesi kabul edilmez: release ≠ release.1.
func refCommitFromBody(body []byte, want string) (string, bool) {
	sha, found, err := parseRefCommit(body, want)
	return sha, found && err == nil
}

// parseRefCommit — refCommitFromBody'nin hata ayıran hâli (v0.10.1044):
// çözülemeyen bir 2xx gövde HATADIR (cache'lenmez), "ref yok" değil —
// bozuk bir yanıtı 10 dk "tag yok" diye saklamak geri-düşüşü kalıcı yapardı.
func parseRefCommit(body []byte, want string) (string, bool, error) {
	var rr struct {
		Value []struct {
			Name     string `json:"name"`
			ObjectID string `json:"objectId"`
			Peeled   string `json:"peeledObjectId"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &rr); err != nil {
		return "", false, fmt.Errorf("refs yanıtı çözümlenemedi: %w", err)
	}
	full := "refs/" + strings.TrimPrefix(want, "refs/")
	for _, r := range rr.Value {
		if r.Name != full {
			continue
		}
		if r.Peeled != "" {
			return r.Peeled, true, nil
		}
		if r.ObjectID != "" {
			return r.ObjectID, true, nil
		}
	}
	return "", false, nil
}

// refCommit — `refs?filter=<ref>` ile tek ref'in commit'i. Bulunamazsa ("",
// nil): yokluk hata değil, "bu sürümün tag'i yok" bilgisidir.
//
// v0.10.1044 — CACHE'Lİ (ağaç cache'iyle aynı ömür ve tavan: treeTTL /
// treeMaxRepos). Kod incelemesi artık her "Kodu da incele" tıkında bu
// sorguyu atıyor; tag'leri desene uymayan bir kurulum tık başına bir refs
// isteği ödemesin diye YOKLUK da cache'lenir. Hata cache'lenmez: geçici bir
// ağ arızası 10 dk boyunca "tag yok" diye okunmamalı. Bedel: yeni basılan
// bir tag en çok 10 dk görünmeyebilir (ağaçla aynı tazelik sözleşmesi).
func (s *Service) refCommit(ctx context.Context, cli *http.Client, cfg Settings, ver, repo, ref string) (string, error) {
	key := refCacheKey(cfg, repo, ref)
	if sha, ok := s.code.getRef(key); ok {
		return sha, nil
	}
	// v0.10.1044 — (a) süzgeç değerinin TAMAMI QueryEscape'li: PathEscape
	// `& = + $`'ı bırakıyordu — `x&$top=1` PAT'lı isteğe parametre ekliyor,
	// geçerli semver `1.4.2+77`'nin `+`'sı boşluğa çözülüp hiç eşleşmiyordu.
	// (b) peelTags=true: annotated tag'in commit'i (peeledObjectId) yalnız bu
	// parametreyle döner; yoksa objectId TAG NESNESİDİR, commit ağacı okuması
	// düşer ve özellik release-plugin tag'lerinde sessizce hiçbir şey yapmaz.
	u := repoURL(cfg, repo) + "/refs?filter=" + url.QueryEscape(ref) + "&peelTags=true&api-version=" + ver
	body, err := doGet(ctx, cli, u, cfg)
	if err != nil {
		return "", err
	}
	sha, found, perr := parseRefCommit(body, ref)
	if perr != nil {
		return "", perr
	}
	if !found {
		sha = ""
	}
	s.code.putRef(key, sha)
	return sha, nil
}

// refCacheKey — ref→commit cache anahtarı (v0.10.1044): taban adres,
// koleksiyon, proje, depo, TAM ref adı ("tags/1.4.2"). Desen değişirse ref
// adı da değişir, yani eski cevap yeni desene sızmaz.
func refCacheKey(cfg Settings, repo, ref string) string {
	return cfg.BaseURL + "|" + cfg.Collection + "|" + cfg.Project + "|" + repo + "|ref|" + ref
}

// resolveRevision — sürüm → ref → commit → o commit'in ağacı. v0.10.590'ın
// zinciri TEK yazımda (v0.10.1044'te frame_links.go'dan çıkarıldı): frame
// linkleri ve kod incelemesi (FetchCodeAt) AYNI fonksiyonu çağırır — iki
// kopya ilk düzeltmede ayrışır ve link bir commit'e, model başka bir koda
// bakardı (resolveChain'in v0.9.1242 kararı).
//
// Dönüş:
//   - nil — sürüm yok / yer tutucu / desen geçersiz: TEK istek bile atılmaz,
//     çağıran bugünkü davranışta kalır.
//   - Verified=true — commit ağacı ikinci dönüşte; çağıran yolu ve dosyayı
//     o commit'ten okur.
//   - Verified=false — Note nedeni söyler (Missing: ref depoda yok — hata
//     değil); ağaç boş, çağıran dal ucunda kalır. Sürüm yüzünden hiçbir şey
//     DÜŞMEZ.
func (s *Service) resolveRevision(ctx context.Context, cli *http.Client, cfg Settings, ver, repo, version string) (*Revision, treeResult) {
	ref, ok := ResolveVersionRef(cfg.VersionRef, version)
	if !ok || strings.TrimSpace(repo) == "" {
		return nil, treeResult{}
	}
	rev := &Revision{Version: strings.TrimSpace(version), Ref: ref}
	sha, err := s.refCommit(ctx, cli, cfg, ver, repo, ref)
	switch {
	case err != nil:
		rev.Note = "ref sorgusu başarısız: " + sanitize(err.Error(), cfg)
	case sha == "":
		rev.Note, rev.Missing = ref+" bulunamadı", true
	default:
		// v0.10.1044 — SÜRE DÜRÜSTLÜĞÜ: iki tam ağaç listelemesi (dal + commit)
		// artık tek süre tavanını paylaşıyor. Commit ağacı kalan sürenin
		// YARISIYLA okunur; yavaş sunucu bugünkü davranışa (dal ağacı zaten
		// elde) düşer — dal dosyalarına süre kalır ve "dalından okundu"
		// cümlesi doğru olur, ölü bir çekime dönmez.
		tctx, cancel := revisionTreeContext(ctx)
		tree, terr := s.repoTreeAt(tctx, cli, cfg, ver, repo, RefSpec{Kind: "commit", Name: sha})
		subTimeout := tctx.Err() != nil && ctx.Err() == nil
		cancel()
		if terr != nil || len(tree.paths) == 0 {
			rev.Note = "commit ağacı okunamadı: " + sha
			if subTimeout {
				rev.Note = "commit ağacı süre payında okunamadı: " + sha
			}
			break
		}
		rev.SHA, rev.Verified = sha, true
		return rev, tree
	}
	return rev, treeResult{}
}

// revisionTreeContext — commit ağacının alt süre tavanı: kalan sürenin
// yarısı (v0.10.1044). Tavansız bağlamda (yalnız testler) alt tavan yok.
func revisionTreeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	dl, ok := ctx.Deadline()
	if !ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, time.Until(dl)/2)
}

// shortSHA — gösterim için ilk 8 karakter (revisionVerifiedText'in kuralı).
func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// refKind — versionDescriptor.versionType değeri; boş tür = branch.
func refKind(ref RefSpec) string {
	if ref.Kind == "" {
		return "branch"
	}
	return ref.Kind
}

// revisionFallbackNote — v0.10.1044: sürüm biliniyordu ama kod DAL ucundan
// okundu; Reason'a giden düz cümle. "depoda bulunamadı" (ref yok — tag'ler
// desene uymuyor ya da sürüm etiketlenmemiş) ile "okunamadı" (sorgu ya da
// commit ağacı düştü) ayrı: operatörün aksiyonu farklı (desen mi, bağlantı mı).
func revisionFallbackNote(rev *Revision, branch string) string {
	if rev == nil || rev.Verified {
		return ""
	}
	if rev.Missing {
		return fmt.Sprintf("çalışan sürüm %s depoda bulunamadı (%s), %s dalından okundu", rev.Version, rev.Ref, branch)
	}
	return fmt.Sprintf("çalışan sürüm %s okunamadı (%s), %s dalından okundu", rev.Version, rev.Note, branch)
}

// refCacheName — ağaç cache anahtarı için ref adı. Branş bugünkü anahtarı
// AYNEN korur (mevcut cache/pin testleri bozulmasın); diğer türler ayrık.
func refCacheName(ref RefSpec) string {
	if ref.Kind == "" || ref.Kind == "branch" {
		return ref.Name
	}
	return "\x02" + ref.Kind + ":" + ref.Name
}
