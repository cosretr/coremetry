package chstore

// cluster_all_replicas_args_test.go — v0.10.826 kaynak pini.
//
// Operatör hatası (test kümesi pod günlüğü, v0.10.824):
//
//	[trace] <traceid>: tüm-replika yedek okuması başarısız: trace
//	all-replica read: code: 42, message: Table name was not found in
//	function arguments. Table function 'clusterAllReplicas' requires
//	from 0 to 4 parameters: [<cluster name or default if not specify>,
//	<name of remote database>, <name of remote table>] [, sharding_key]
//
// Kök neden: clusterAllReplicas'ın TABLO argümanı çıplak bir
// tanımlayıcıydı (spans_local). ClickHouse kullanıcı tablosunu
// veritabanıyla nitelenmiş ister; system.<ad> adları veritabanını kendi
// taşıdığı için depodaki onlarca kardeş okuma çalışıyor, kullanıcı
// tablosuna giden İKİ okuma her çağrıda reddediliyordu. Hata yalnız
// küme kipinde görünür (tek düğümde dal hiç koşmaz), bu yüzden
// v0.10.810'dan v0.10.824'e kadar sessiz kaldı ve şekil v0.10.823'te
// ikinci bir dosyaya kopyalandı. migrations/0009 ve 0010'daki elle
// koşulan runbook SQL'i aynı kuralı zaten uyguluyordu
// (clusterAllReplicas('uptrace_all', currentDatabase(), problems)) —
// Go tarafı o dersi almamıştı.
//
// Pin: chstore'un HER üretim dosyası taranır, clusterAllReplicas
// çağrılarının ikinci argümanı sınıflandırılır. Test dosyaları bilinçli
// olarak dışarıda — bu dosyanın kendi örnek dizeleri de çağrı şeklini
// içerir ve gate kendi metnini ısırmamalı.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// caRSite — bir clusterAllReplicas çağrısının kaynak izi.
type caRSite struct {
	Line  int    // 1 tabanlı satır
	Args  string // parantez içi ham kaynak metni
	Table string // ilk virgülden sonraki ham metin (ikinci argüman)
}

// findClusterAllReplicasSites — SAF: Go kaynağında clusterAllReplicas(
// çağrılarını bulur, ikinci argümanın HAM kaynak metnini çıkarır.
// Tümüyle yorum olan satırlar atlanır (dosya başlıklarında çağrı şekli
// anlatılıyor, çağrı yok). Parantez eşlemesi satır içinde yapılır:
// depoda çok satıra bölünmüş çağrı yok; olursa Table boş kalır ve
// sınıflandırma düşer — sessizce geçmez.
func findClusterAllReplicasSites(src string) []caRSite {
	const call = "clusterAllReplicas("
	var out []caRSite
	for i, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		rest := line
		for {
			idx := strings.Index(rest, call)
			if idx < 0 {
				break
			}
			rest = rest[idx+len(call):]
			site := caRSite{Line: i + 1}
			if args, ok := balancedCallArgs(rest); ok {
				site.Args = args
				site.Table = secondCallArg(args)
			}
			out = append(out, site)
		}
	}
	return out
}

// balancedCallArgs — SAF: açılış parantezinden SONRAKİ metinde eşleşen
// kapanışa kadar olan parçayı döner.
func balancedCallArgs(s string) (string, bool) {
	depth := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return s[:i], true
			}
			depth--
		}
	}
	return "", false
}

// secondCallArg — SAF: üst düzey ilk virgülden sonrasını döner.
func secondCallArg(args string) string {
	depth := 0
	for i, r := range args {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				return strings.TrimSpace(args[i+1:])
			}
		}
	}
	return ""
}

// clusterTableArgQualified — SAF: tablo argümanı veritabanını TAŞIYOR mu.
//
//	system.<ad>   → taşır, "system" adın parçası
//	`db`.`tablo`  → taşır, backtick'li iki parçalı ad (Go kaynağında araya
//	                string birleştirme girse de backtick-nokta-backtick
//	                dizisi görünür)
//
// Başka her şey (çıplak spans_local, " + localTable + " birleştirmesi,
// tek başına %s) TAŞIMAZ — ClickHouse kod 42 ile reddeder.
func clusterTableArgQualified(arg string) bool {
	a := strings.TrimSpace(arg)
	if a == "" {
		return false
	}
	if strings.HasPrefix(a, "system.") {
		return true
	}
	return strings.Contains(a, "`.`")
}

// caRIndirect — tablo argümanı kaynakta GÖRÜNMEYEN, doğrulanmış sahalar.
// Anahtar "dosya | argümanın ham metni": aynı dosyaya BAŞKA şekilde bir
// splice girerse pin yine düşer. Beyan tek başına yetmez — aşağıdaki
// assertWrapperFeedsSystemTables sarmalayıcının çağrı yerlerini okur ve
// yalnız system.* beslendiğini DOĞRULAR.
var caRIndirect = map[string]string{
	"entity_layer_admin.go | %s":    "q(system.*) — kolon/index/tablo envanteri",
	"rollout_layer_admin.go | %s":   "q(system.*) — kolon/index/tablo envanteri",
	`server_stats.go | " + tbl + "`: "wrap(system.*) — düğüm metrikleri",
}

// caRWrapperAssign — SAF: `q := func(t string) string {` / `wrap = func(…`
// satırından sarmalayıcı adını çıkarır; atama değilse boş döner.
func caRWrapperAssign(line string) string {
	i := strings.Index(line, "func(")
	if i < 0 {
		return ""
	}
	head := strings.TrimSpace(line[:i])
	head = strings.TrimSuffix(head, "=")
	head = strings.TrimSuffix(strings.TrimSpace(head), ":")
	head = strings.TrimSpace(head)
	if head == "" || strings.ContainsAny(head, " \t(){}.,\"`+") {
		return ""
	}
	return head
}

// TestClusterAllReplicasTableArgQualified — v0.10.826 pini: chstore'daki
// HER clusterAllReplicas çağrısının tablo argümanı veritabanını taşımalı.
func TestClusterAllReplicasTableArgQualified(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		lines := strings.Split(src, "\n")
		for _, site := range findClusterAllReplicasSites(src) {
			if clusterTableArgQualified(site.Table) {
				continue
			}
			key := f + " | " + site.Table
			if _, ok := caRIndirect[key]; ok {
				seen[key] = true
				assertWrapperFeedsSystemTables(t, f, lines, site)
				continue
			}
			t.Errorf("%s:%d: clusterAllReplicas tablo argümanı %q veritabanını taşımıyor. "+
				"ClickHouse bunu reddeder: \"code: 42, message: Table name was not found in function "+
				"arguments. Table function 'clusterAllReplicas' requires from 0 to 4 parameters: "+
				"[<cluster name or default if not specify>, <name of remote database>, "+
				"<name of remote table>] [, sharding_key]\" (operatör, v0.10.824 test kümesi). "+
				"Kullanıcı tablosunu nitelendir — db yapılandırmadan (s.cfg.Database), chObjRe ile "+
				"doğrulanmış, backtick'li iki parçalı ad; currentDatabase() DEĞİL (tablo fonksiyonunun "+
				"argümanı başlatan düğümde çözülür).", f, site.Line, site.Table)
		}
	}
	for key, why := range caRIndirect {
		if !seen[key] {
			t.Errorf("caRIndirect girdisi ölü: %q (%s) — saha değişmiş, beyaz liste güncellenmeli", key, why)
		}
	}
}

// assertWrapperFeedsSystemTables — dolaylı sahanın iddiasını doğrular:
// sarmalayıcıya YALNIZ system.* literalleri veriliyor mu. Sarmalayıcı adı
// çıkarılamazsa ya da hiç literal çağrı yoksa iddia doğrulanamaz → düşer.
func assertWrapperFeedsSystemTables(t *testing.T, file string, lines []string, site caRSite) {
	t.Helper()
	name := caRWrapperAssign(lines[site.Line-1])
	if name == "" {
		t.Errorf("%s:%d: dolaylı tablo argümanı %q — sarmalayıcı adı çıkarılamadı, beslendiği tablolar doğrulanamıyor (v0.10.826)", file, site.Line, site.Table)
		return
	}
	calls := 0
	for i, line := range lines {
		rest := line
		for {
			idx := strings.Index(rest, name+`("`)
			if idx < 0 {
				break
			}
			rest = rest[idx+len(name)+2:]
			end := strings.Index(rest, `"`)
			if end < 0 {
				break
			}
			arg := rest[:end]
			calls++
			if !strings.HasPrefix(arg, "system.") {
				t.Errorf("%s:%d: %s(%q) — dolaylı clusterAllReplicas sarmalayıcısına system.* olmayan tablo besleniyor; nitelenmiş iki parçalı ad gerekir (v0.10.826)", file, i+1, name, arg)
			}
		}
	}
	if calls == 0 {
		t.Errorf("%s:%d: %s sarmalayıcısına literal tablo çağrısı bulunamadı — dolaylı saha doğrulanamıyor (v0.10.826)", file, site.Line, name)
	}
}

// TestClusterAllReplicasArgClassifier — SAF ayıklayıcı + sınıflandırıcının
// tablosu. İlk iki satır v0.10.826'nın TAM hatalı şekilleri; onlar yeşile
// dönerse pin ısırmıyor demektir.
func TestClusterAllReplicasArgClassifier(t *testing.T) {
	cases := []struct {
		name string
		line string
		arg  string // beklenen ham ikinci argüman ("" = kontrol etme)
		want bool
	}{
		{
			"v0.10.810 hatalı: çıplak tablo",
			"\t\tFROM clusterAllReplicas('` + cluster + `', spans_local)",
			"spans_local", false,
		},
		{
			"v0.10.823 hatalı: çıplak birleştirme",
			"\t\tFROM clusterAllReplicas('` + cluster + `', ` + localTable + `)",
			"` + localTable + `", false,
		},
		{
			"düzeltme: nitelenmiş sabit tablo",
			"\t\tFROM clusterAllReplicas('` + cluster + \"', `\" + db + \"`.`spans_local`\" + `)",
			"", true,
		},
		{
			"düzeltme: nitelenmiş değişken tablo",
			"\t\tFROM clusterAllReplicas('` + cluster + \"', `\" + db + \"`.`\" + localTable + \"`\" + `)",
			"", true,
		},
		{
			"system tablosu literal",
			"\t\tsrc = fmt.Sprintf(\"clusterAllReplicas('%s', system.parts)\", cluster)",
			"system.parts", true,
		},
		{
			"system tablosu, küme adı bağlı",
			"\t\tFROM clusterAllReplicas(%s, system.tables)",
			"system.tables", true,
		},
		{
			"dolaylı tablo (beyaz liste + doğrulama ister)",
			"\t\tq := func(t string) string { return fmt.Sprintf(\"clusterAllReplicas('%s', %s)\", cluster, t) }",
			"%s", false,
		},
		{
			"üç parçalı ad: bu depoda kullanılmıyor, nitelenmiş sayılmaz",
			"\t\tFROM clusterAllReplicas('c', 'db', 'spans_local')",
			"'db', 'spans_local'", false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sites := findClusterAllReplicasSites(c.line)
			if len(sites) != 1 {
				t.Fatalf("çağrı bulunamadı (%d saha): %s", len(sites), c.line)
			}
			got := sites[0].Table
			if c.arg != "" && got != c.arg {
				t.Errorf("ikinci argüman %q, beklenen %q", got, c.arg)
			}
			if ok := clusterTableArgQualified(got); ok != c.want {
				t.Errorf("nitelenmiş=%v, beklenen %v (arg %q)", ok, c.want, got)
			}
		})
	}
	// Yorum satırı çağrı değildir (dosya başlıkları şekli anlatıyor).
	if n := len(findClusterAllReplicasSites("// clusterAllReplicas('c', spans_local) şeklini anlatan düzyazı")); n != 0 {
		t.Errorf("yorum satırı taranmamalı, %d saha bulundu", n)
	}
	// Sarmalayıcı adı çıkarımı.
	for line, want := range map[string]string{
		`		q := func(t string) string { return fmt.Sprintf("x", t) }`: "q",
		`		wrap = func(tbl string) string { return "x" + tbl + ")" }`: "wrap",
		`		return fmt.Sprintf("clusterAllReplicas('%s', %s)", c, t)`:  "",
	} {
		if got := caRWrapperAssign(line); got != want {
			t.Errorf("caRWrapperAssign(%q) = %q, beklenen %q", line, got, want)
		}
	}
}
