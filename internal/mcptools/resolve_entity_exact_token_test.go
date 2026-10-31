package mcptools

// v0.10.1739 (operator-reported) — CoSRE v2'de "<servis> endpointleri" (servis
// adı katalogdaki bir adla BİREBİR aynı) "birden çok varlığa oturuyor (12 aday)"
// netleştirmesine düştü: tüm metin tek sorgu sayıldı, "endpointleri" jetonu
// hiçbir ada uymadığından tam ad ve önek kardeşleri aynı kısmi jeton skorunu
// aldı. Metindeki bütün bir jeton bir servis adına birebir eşitse çapa odur.
// Adlar SENTETİK.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/entity"
)

func TestResolveEntitiesExactServiceToken(t *testing.T) {
	sibs := []string{"orders-api", "orders-api-prod", "orders-apps-prod", "orders-api-test", "orders-api-uat",
		"orders-card-bff", "orders-card-bff-prod", "orders-card-bff-test", "orders-worker-prod", "orders-batch-prod",
		"orders-gw-prod", "orders-sync-prod", "orders-web-prod", "orders-ui-prod"}
	idx := EntityCatalogIndex{EntityLayer: true, Services: sibs,
		Namespaces: []EntityCandidate{{Kind: entity.TypeNamespace, Cluster: "eu-west", ClusterID: "c-1", Name: "orders"}}}
	cases := []struct {
		name string
		text string
		want []string // nil → yalnız aday sayısı ≥2 (gerçekten belirsiz)
	}{
		{"tam ad + endpointleri", "orders-api-prod endpointleri", []string{"orders-api-prod"}},
		{"tam ad + ekli kelime", "orders-card-bff-prod endpoint leri", []string{"orders-card-bff-prod"}},
		{"büyük harf", "ORDERS-API-PROD endpointleri", []string{"orders-api-prod"}},
		{"kesme ekli", "orders-api'nin endpointleri", []string{"orders-api"}},
		{"noktalama", "orders-apps-prod? endpointleri", []string{"orders-apps-prod"}},
		{"iki tam ad", "orders-api orders-api-prod kıyasla", []string{"orders-api", "orders-api-prod"}},
		{"tek jeton tam ad", "orders-api-prod", []string{"orders-api-prod"}},
		{"belirsiz önek + endpointleri", "orders-card endpointleri", nil},
		{"belirsiz önek tek jeton", "orders-card", nil},
	}
	for _, c := range cases {
		got := ResolveEntities(c.text, idx, 12)
		if c.want == nil {
			if len(got) < 2 {
				t.Errorf("%s: belirsiz metin tek adaya indi: %+v", c.name, got)
			}
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: %q → %d aday, want %v: %+v", c.name, c.text, len(got), c.want, got)
			continue
		}
		for i, w := range c.want {
			if got[i].Kind != entity.TypeService || got[i].Name != w || got[i].Match != "exact" {
				t.Errorf("%s: aday[%d]=%+v, want exact service %q", c.name, i, got[i], w)
			}
		}
	}
	if one := ResolvedOne(ResolveEntities("orders-card-bff-prod endpointleri", idx, 12)); one == nil || one.Name != "orders-card-bff-prod" {
		t.Errorf("tek güçlü aday çözülmeli: %+v", one)
	}
}
