package mcptools

import (
	"context"
	"testing"
)

func TestAliasPhraseIn(t *testing.T) {
	cases := []struct {
		text, phrase string
		want         bool
	}{
		{"kart tarafı nasıl", "kart tarafı", true},
		{"Kart tarafındaki hatalar", "kart tarafı", true},
		{"kart tarafında hata var mı", "kart tarafı", true},
		{"peki KART TARAFI?", "kart tarafı", true},
		{"tarafı kart", "kart tarafı", false},
		{"kartlar nasıl", "kart tarafı", false},
		{"payments'ı soruyorum", "payments", true},
		{"", "kart tarafı", false},
		{"kart tarafı", "", false},
	}
	for _, c := range cases {
		if got := AliasPhraseIn(c.text, c.phrase); got != c.want {
			t.Errorf("AliasPhraseIn(%q, %q) = %v", c.text, c.phrase, got)
		}
	}
}

func TestApplyEntityAliases(t *testing.T) {
	idx := EntityCatalogIndex{Services: []string{"checkout", "orders", "payments", "inventory"},
		Namespaces: []EntityCandidate{{Kind: "namespace", Name: "shop", ID: "ns:c1:shop"}}}
	orig := []EntityCandidate{{Kind: "service", Name: "orders", ID: "svc:orders", Score: 50}}
	al := []EntityAlias{
		{Phrase: "eski taraf", ID: "svc:gone", Not: "svc:orders"},
		{Phrase: "kart tarafı", ID: "svc:payments", Not: "svc:orders"},
		{Phrase: "vitrin", ID: "ns:c1:shop"},
	}
	got, a := ApplyEntityAliases("kart tarafı nasıl", idx, al, orig)
	if a == nil || len(got) != 1 || got[0].Name != "payments" || got[0].Match != "alias" || got[0].ID != "svc:payments" {
		t.Fatalf("eş ad uygulanmadı: %+v %+v", got, a)
	}
	if got, a := ApplyEntityAliases("eski taraf nasıl", idx, al, orig); a != nil || len(got) != 1 || got[0].Name != "orders" {
		t.Fatalf("bayat hedef yok sayılmalıydı: %+v %+v", got, a)
	}
	if got, a := ApplyEntityAliases("vitrin durumu", idx, al, nil); a == nil || got[0].Kind != "namespace" {
		t.Fatalf("kimlikle (namespace) eşleme: %+v", got)
	}
	if got, a := ApplyEntityAliases("kart tarafı", idx, nil, orig); a != nil || len(got) != 1 {
		t.Fatalf("eş adsız çağrı adayları değiştirdi: %+v", got)
	}
}

// TestEntityAliasesScopedToContext — eş adlar yalnız kullanıcının turunun
// bağlamında; bağlamsız çağrı (başka kullanıcı, gece işi) hiç görmez.
func TestEntityAliasesScopedToContext(t *testing.T) {
	ctx := WithEntityAliases(context.Background(), []EntityAlias{{Phrase: "kart tarafı", ID: "svc:payments"}})
	if len(EntityAliasesFrom(ctx)) != 1 || EntityAliasesFrom(context.Background()) != nil {
		t.Fatal("bağlam sızdı")
	}
	if WithEntityAliases(context.Background(), nil) != context.Background() {
		t.Fatal("boş liste bağlamı değiştirdi")
	}
}
