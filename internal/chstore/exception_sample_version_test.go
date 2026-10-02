package chstore

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/devops"
)

// exception_sample_version_test.go — v0.10.1048 (operatör: "Exception
// sayfasındaki dosya bağlantıları hâlâ daldan açılıyor; kod incelemesi artık
// sürümden okuyor. İkisi aynı yere baksın.").
//
// Exception sayfası frame linklerine stack'i GÖSTERİLEN örneğin kendi
// sürümünü (ExceptionSample.RunningVersion) yollar; "Kodu da incele" aynı
// olayın sürümünü exceptionStackVersion → SpanVersion → devops.RunningVersion
// ile seçer. İkisi aynı span için aynı cevabı vermeli: biri bir commit'e,
// öteki başka bir koda bakarsa sayfa kendisiyle çelişir.

// Örneğin sürümü, aynı resource attribute'lardan devops.RunningVersion'ın
// verdiği cevabın AYNISI; attribute yoksa boş.
func TestSampleRunningVersionMatchesDevops(t *testing.T) {
	cases := []struct {
		name string
		res  map[string]string
		want string
	}{
		{"image tag service.version'ı ezer", map[string]string{"container.image.tag": "release.20261002.1", "service.version": "1.4.2"}, "release.20261002.1"},
		{"k8s image tag ikinci", map[string]string{"k8s.container.image.tag": "release.20261002.2", "service.version": "1.4.2"}, "release.20261002.2"},
		{"yer tutucu tag atlanır", map[string]string{"container.image.tag": "latest", "service.version": "1.4.2"}, "1.4.2"},
		{"SNAPSHOT atlanır", map[string]string{"container.image.tag": "2.0.0-SNAPSHOT", "service.version": "1.4.2"}, "1.4.2"},
		{"boşluk kırpılır", map[string]string{"service.version": " 1.4.2 "}, "1.4.2"},
		{"zincir dışı anahtar sayılmaz", map[string]string{"helm.chart.version": "9.9.9", "k8s.pod.name": "card-service-7f9c"}, ""},
		{"yalnız yer tutucu", map[string]string{"service.version": "master"}, ""},
		{"attribute yok", nil, ""},
	}
	for _, c := range cases {
		var vals [len(exSampleVersionKeys)]string
		for i, k := range exSampleVersionKeys {
			vals[i] = c.res[k]
		}
		got := sampleRunningVersion(vals)
		if got != c.want {
			t.Errorf("%s: %q, beklenen %q", c.name, got, c.want)
		}
		if dv := devops.RunningVersion(c.res); got != dv {
			t.Errorf("%s: örnek %q ≠ devops.RunningVersion %q — sayfa ile kod incelemesi ayrışır", c.name, got, dv)
		}
	}
}

// Anahtar kümesi ve sırası devops zinciriyle birebir (kaynaktan). Zincire bir
// halka eklenip burada unutulursa örnek o halkayı hiç okumaz ve sayfa kod
// incelemesinden farklı bir sürüme bağlanır.
func TestSampleVersionKeysMatchDevopsChain(t *testing.T) {
	src := readSampleVersionSrc(t, "../devops/version_ref.go")
	m := regexp.MustCompile(`var runningVersionKeys = \[\]string\{([^}]*)\}`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("devops runningVersionKeys bulunamadı — kapı bayatlamış")
	}
	var chain []string
	for _, q := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(m[1], -1) {
		chain = append(chain, q[1])
	}
	if strings.Join(chain, ",") != strings.Join(exSampleVersionKeys[:], ",") {
		t.Errorf("örnek anahtarları devops zincirinden ayrıştı: chstore %v, devops %v", exSampleVersionKeys, chain)
	}
	want := "res_values[indexOf(res_keys, 'container.image.tag')], " +
		"res_values[indexOf(res_keys, 'k8s.container.image.tag')], " +
		"res_values[indexOf(res_keys, 'service.version')]"
	if got := exSampleVersionCols(); got != want {
		t.Errorf("SELECT ifadeleri:\n got %s\nwant %s", got, want)
	}
}

// Ulaşılabilirlik ([[feedback-tested-but-unreachable]]): sürüm AYNI sorgunun
// satırından okunur ve tel'e `runningVersion` adıyla gider; boşsa alan hiç
// yazılmaz (eski sunucu / sürümsüz span → yanıt bayt bayt eskisi).
func TestSampleVersionReachesTheWire(t *testing.T) {
	src := strings.Join(strings.Fields(readSampleVersionSrc(t, "exception_inbox.go")), " ")
	for _, want := range []string{
		"name, status_msg, ` + exSampleVersionCols() + ` FROM spans",
		"sm.RunningVersion = sampleRunningVersion(ver)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("exception_inbox.go: %q yok — örnek sürümü sorgudan yanıta ulaşmıyor", want)
		}
	}
	b, _ := json.Marshal(ExceptionSample{TraceID: "t1", RunningVersion: "1.4.2"})
	if !strings.Contains(string(b), `"runningVersion":"1.4.2"`) {
		t.Errorf("JSON alanı: %s", b)
	}
	b, _ = json.Marshal(ExceptionSample{TraceID: "t1"})
	if strings.Contains(string(b), "runningVersion") {
		t.Errorf("boş sürüm yazılmamalı: %s", b)
	}
}

// Parite pini: kod incelemesi (exceptionStackVersion) aynı olayın sürümünü
// SpanVersion → devops.RunningVersion ile seçer; örnek alanı da aynı
// yardımcıdan. Biri kendi listesine/sırasına dönerse kırmızı.
func TestSampleVersionSameHelperAsCodeReview(t *testing.T) {
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	sv := flat(readSampleVersionSrc(t, "../anomaly/stack_version.go"))
	if !strings.Contains(sv, "if sp.SpanID == spanID { return devops.RunningVersion(sp.ResourceAttributes) }") {
		t.Error("anomaly.SpanVersion artık devops.RunningVersion'ı çağırmıyor — exception sayfası ile kod incelemesi ayrışabilir")
	}
	ec := flat(readSampleVersionSrc(t, "../anomaly/exception_context.go"))
	if !strings.Contains(ec, "return SpanVersion(traceSpans, sm.SpanID)") {
		t.Error("exceptionStackVersion örneğin KENDİ span'inden okumuyor — parite pini bayatlamış")
	}
	inbox := flat(readSampleVersionSrc(t, "exception_inbox.go"))
	if !strings.Contains(inbox, "return devops.RunningVersion(res)") {
		t.Error("sampleRunningVersion devops.RunningVersion'dan geçmiyor")
	}
}

func readSampleVersionSrc(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
