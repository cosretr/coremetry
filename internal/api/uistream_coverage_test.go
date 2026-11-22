package api

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/cosre/uistream"
)

// uistream_coverage_test.go — v0.10.1591 (CoSRE v2 Faz 1, R3): sohbet
// yazıcısına ulaşabilen her v1 olay türü uistream'in bildiği kümede. Yeni bir
// `emit("yeni-tur", …)` eklenip çevirici güncellenmezse v2 istemcisi onu
// data-unknown olarak görür — bu test önce kırmızıya döner.
//
// Küme: internal/api'deki `emit("<tür>"` literalleri ∪ {codeLockEvent},
// eksi withBlockSeq'in `block`a katladığı türler (yazıcıya hiç ulaşmaz).

// blockSeqFolded — withBlockSeq'in (chat_step_ids.go) block'a katladığı türler.
var blockSeqFolded = []string{"evidence", "trace_list", "ev_block", "service_report", "topology", "charts_signals", "log_spike", "metric_panel"}

func TestUIStreamCoversEmittedKinds(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`\bemit\("([a-z_-]+)"`)
	kinds := map[string]bool{codeLockEvent: true}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			kinds[m[1]] = true
		}
	}
	// Katlanan türler gerçekten withBlockSeq'te katlanıyor mu (liste bayatlarsa kırmızı).
	src, err := os.ReadFile("chat_step_ids.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	i := strings.Index(body, "func withBlockSeq(")
	if i < 0 {
		t.Fatal("withBlockSeq bulunamadı")
	}
	fn := body[i:]
	if j := strings.Index(fn[1:], "\nfunc "); j > 0 {
		fn = fn[:j+1]
	}
	folds := map[string]string{
		"evidence": `kind == "evidence"`, "trace_list": `kind == "trace_list"`, "ev_block": `kind == "ev_block"`,
		"service_report": "blocks.TypeServiceReport", "topology": "blocks.TypeTopology",
		"charts_signals": "blocks.TypeChartsSignals", "log_spike": "blocks.TypeLogSpike", "metric_panel": "blocks.TypeMetricPanel",
	}
	for _, k := range blockSeqFolded {
		if !strings.Contains(fn, folds[k]) {
			t.Errorf("withBlockSeq %s türünü artık katlamıyor (%s yok)", k, folds[k])
		}
		delete(kinds, k)
	}
	var missing []string
	for k := range kinds {
		if !uistream.KnownKinds[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("uistream.KnownKinds eksik: %v — translator.go'ya eşleme ekle", missing)
	}
	if len(kinds) < 8 {
		t.Fatalf("tarama yalnız %d tür buldu — regex bozulmuş olabilir: %v", len(kinds), kinds)
	}
}
