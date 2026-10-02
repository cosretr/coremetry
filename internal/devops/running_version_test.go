package devops

// running_version_test.go — v0.10.1044 (operatör: "Kod, dalın ucundan değil
// çalışan sürümden okunsun"). Çalışan sürüm ÜÇ yerde seçiliyor ve üçü aynı
// cevabı vermeli: tarayıcının frame linkleri (frontend/src/lib/
// runningVersion.ts), deploy tespiti (chstore/deploys.go
// effectiveVersionExpr + placeholderVersionList) ve kod incelemesi
// (RunningVersion). Ayrışırlarsa link bir commit'e, model başka bir koda
// bakar. Kapı KAYNAKTAN okur: sıra ya da liste bir yerde değişip ötekilerde
// kalırsa kırmızı.

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestRunningVersionChain(t *testing.T) {
	cases := []struct {
		name string
		res  map[string]string
		want string
	}{
		{"image tag service.version'ı ezer", map[string]string{"service.version": "1.0.0", "container.image.tag": "release.20260817.1"}, "release.20260817.1"},
		{"k8s image tag ikinci", map[string]string{"k8s.container.image.tag": "release.20260817.2", "service.version": "1.0.0"}, "release.20260817.2"},
		{"yer tutucu tag atlanır", map[string]string{"container.image.tag": "latest", "service.version": "2.3.4"}, "2.3.4"},
		{"SNAPSHOT tag atlanır", map[string]string{"container.image.tag": "1.0.0-SNAPSHOT", "service.version": "2.3.4"}, "2.3.4"},
		{"master yer tutucu", map[string]string{"service.version": "master"}, ""},
		{"yalnız yer tutucu", map[string]string{"service.version": "${project.version}"}, ""},
		{"boşluk kırpılır", map[string]string{"service.version": " 1.4.2 "}, "1.4.2"},
		{"nil", nil, ""},
	}
	for _, c := range cases {
		if got := RunningVersion(c.res); got != c.want {
			t.Errorf("%s: %q, beklenen %q", c.name, got, c.want)
		}
	}
}

func TestPickRunningVersion(t *testing.T) {
	sv := func(v string, at int64) VersionSample {
		return VersionSample{Resource: map[string]string{"service.version": v}, Start: at}
	}
	cases := []struct {
		name string
		in   []VersionSample
		want string
	}{
		{"boş", nil, ""},
		{"en sık kazanır", []VersionSample{sv("1.4.1", 900), sv("1.4.2", 100), sv("1.4.2", 200)}, "1.4.2"},
		{"eşitlikte en yeni span", []VersionSample{sv("1.4.1", 100), sv("1.4.2", 300), sv("1.4.1", 200), sv("1.4.2", 50)}, "1.4.2"},
		{"o da eşitse sözlük sırası", []VersionSample{sv("b-2", 100), sv("a-1", 100)}, "a-1"},
		{"yer tutucu ve boş sayılmaz", []VersionSample{sv("latest", 1), sv("latest", 2), sv("", 3), sv("1.4.2", 4)}, "1.4.2"},
		{"hiç çözülen yok", []VersionSample{sv("latest", 1), sv("", 2)}, ""},
		{"image tag zinciri span başına", []VersionSample{
			{Resource: map[string]string{"container.image.tag": "release.2", "service.version": "1.0.0"}, Start: 1},
			{Resource: map[string]string{"container.image.tag": "release.2", "service.version": "1.0.0"}, Start: 2},
			sv("1.0.0", 3),
		}, "release.2"},
	}
	for _, c := range cases {
		if got := PickRunningVersion(c.in); got != c.want {
			t.Errorf("%s: %q, beklenen %q", c.name, got, c.want)
		}
		// Deterministik: girdi sırası sonucu değiştirmez.
		rev := make([]VersionSample, len(c.in))
		for i := range c.in {
			rev[len(c.in)-1-i] = c.in[i]
		}
		if got := PickRunningVersion(rev); got != c.want {
			t.Errorf("%s (ters sıra): %q, beklenen %q", c.name, got, c.want)
		}
	}
}

func TestRunningVersionMatchesFrontendAndSQL(t *testing.T) {
	ts := readRepoFileDevops(t, "../../frontend/src/lib/runningVersion.ts")
	sql := readRepoFileDevops(t, "../chstore/deploys.go")
	quoted := regexp.MustCompile(`'((?:[^'\\]|\\.)*)'`)

	// ── anahtar sırası ──
	m := regexp.MustCompile(`const KEYS = \[([^\]]*)\]`).FindStringSubmatch(ts)
	if m == nil {
		t.Fatal("runningVersion.ts KEYS bulunamadı — kapı bayatlamış")
	}
	var tsKeys []string
	for _, q := range quoted.FindAllStringSubmatch(m[1], -1) {
		tsKeys = append(tsKeys, q[1])
	}
	if strings.Join(tsKeys, ",") != strings.Join(runningVersionKeys, ",") {
		t.Errorf("anahtar sırası ayrıştı: TS %v, Go %v", tsKeys, runningVersionKeys)
	}
	ev := strings.Index(sql, "const effectiveVersionExpr")
	if ev < 0 {
		t.Fatal("effectiveVersionExpr bulunamadı — kapı bayatlamış")
	}
	var sqlKeys []string
	for _, q := range regexp.MustCompile(`indexOf\(res_keys, '([^']+)'\)`).FindAllStringSubmatch(sql[ev:], -1) {
		if len(sqlKeys) == 0 || sqlKeys[len(sqlKeys)-1] != q[1] {
			sqlKeys = append(sqlKeys, q[1])
		}
		if len(sqlKeys) == len(runningVersionKeys) {
			break
		}
	}
	if strings.Join(sqlKeys, ",") != strings.Join(runningVersionKeys, ",") {
		t.Errorf("effectiveVersionExpr'in ilk halkaları ayrıştı: SQL %v, Go %v", sqlKeys, runningVersionKeys)
	}

	// ── yer tutucu listesi ──
	pm := regexp.MustCompile(`(?s)const PLACEHOLDERS = new Set\(\[(.*?)\]\)`).FindStringSubmatch(ts)
	if pm == nil {
		t.Fatal("runningVersion.ts PLACEHOLDERS bulunamadı — kapı bayatlamış")
	}
	tsSet := map[string]bool{}
	for _, q := range quoted.FindAllStringSubmatch(pm[1], -1) {
		tsSet[q[1]] = true
	}
	goSet := map[string]bool{}
	for k := range versionPlaceholders {
		goSet[k] = true
	}
	if a, b := sortedKeys(tsSet), sortedKeys(goSet); strings.Join(a, "|") != strings.Join(b, "|") {
		t.Errorf("yer tutucu listesi ayrıştı:\n TS=%q\n Go=%q", a, b)
	}
	lm := regexp.MustCompile("(?s)const placeholderVersionList = `\\((.*?)\\)`").FindStringSubmatch(sql)
	if lm == nil {
		t.Fatal("placeholderVersionList bulunamadı — kapı bayatlamış")
	}
	sqlSet := map[string]bool{}
	for _, q := range quoted.FindAllStringSubmatch(lm[1], -1) {
		sqlSet[q[1]] = true
	}
	// SQL'in her girdisi Go'da yer tutucu (açık liste ya da SNAPSHOT kuralı);
	// Go'nun açık listesinin her girdisi SQL'de.
	for v := range sqlSet {
		if !IsPlaceholderVersion(v) {
			t.Errorf("SQL yer tutucusu %q Go'da sürüm sayılıyor", v)
		}
	}
	for v := range goSet {
		if !sqlSet[v] {
			t.Errorf("Go yer tutucusu %q SQL listesinde yok", v)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
