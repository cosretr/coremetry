package anomaly

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1021 — "anomaly-cluster:" öneki İKİ üreticinin: servis topolojisi
// kümeleyicisi (clustering.go) ve dış seri kümeleri (external.go). Servis
// dedektörünün resolveStaleClusters'ı yalnız KENDİ kümelerini kapatmalı; aksi
// hâlde açık Oracle kümeleri her tikte kapanıp dış tarayıcıca yeniden açılır.

func TestOwnsServiceCluster(t *testing.T) {
	cases := map[string]bool{
		clusterRulePrefix + "payments-api":               true,  // servis kümesi
		chstore.RuleExtClusterPrefix + "extsrc/OP1":      false, // dış seri kümesi — dış tarayıcının
		"anomaly:payments-api:error_rate":                false, // bireysel anomali
		"anomaly:ext:extsrc/OP1/E1:external.q":           false,
		"exception-storm":                                false,
		"":                                               false,
		clusterRulePrefix + "extremely-normal-service":   true, // "ext" ile BAŞLAYAN servis adı dış küme değildir
		clusterRulePrefix + "ext-gateway":                true,
		chstore.RuleExtClusterPrefix + "src/with:colons": false,
	}
	for rule, want := range cases {
		if got := ownsServiceCluster(rule); got != want {
			t.Errorf("ownsServiceCluster(%q) = %v, beklenen %v", rule, got, want)
		}
	}
}

func TestResolveStaleClustersUsesOwnership(t *testing.T) {
	src, err := os.ReadFile("clustering.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	i := strings.Index(body, "func (d *Detector) resolveStaleClusters(")
	if i < 0 {
		t.Fatal("resolveStaleClusters bulunamadı")
	}
	fn := body[i:]
	if j := strings.Index(fn[1:], "\nfunc "); j > 0 {
		fn = fn[:j+1]
	}
	if !strings.Contains(fn, "if !ownsServiceCluster(p.RuleID) {") {
		t.Error("resolveStaleClusters sahiplik süzgecini kullanmalı (yalnız önek, dış kümeleri de kapatır)")
	}
	if strings.Contains(fn, "strings.HasPrefix(p.RuleID, clusterRulePrefix)") {
		t.Error("çıplak önek kontrolü geri gelmiş — dış (Oracle) kümeleri her tikte kapanır")
	}
	// Dış küme öneki gerçekten servis kümesi önekinin ALT kümesi olmalı; değilse
	// sahiplik ayrımı anlamsızlaşır.
	if !strings.HasPrefix(chstore.RuleExtClusterPrefix, clusterRulePrefix) {
		t.Errorf("RuleExtClusterPrefix %q, clusterRulePrefix %q ile başlamalı", chstore.RuleExtClusterPrefix, clusterRulePrefix)
	}
}
