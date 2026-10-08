package api

// inbox_env_scope_test.go — v0.10.1131 (operatör-bildirimli): env seçiliyken
// Problems (/inbox) çip/rozet sayısı ile liste dış kaynak problemlerinde
// ayrışıyordu. Liste env'i Go'da (EnvScopeKeepsRow) uyguluyor, sayım
// (CountProblemsBySubject) env'i hiç almıyordu. Burada iki taraf AYNI üye
// kümesinden beslenir; SQL ⇔ Go eşitliği chstore'da
// (TestProblemCountScopeMatchesInboxList, TestEnvScopeSQLAndGoAgree).

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestInboxEnvScopeItemsExternal(t *testing.T) {
	mk := func() []InboxItem {
		return []InboxItem{
			problemToInbox(chstore.Problem{ID: "p1", Service: "svc-orders", Kind: chstore.ProblemKindService, Status: "open"}),
			problemToInbox(chstore.Problem{ID: "p2", Service: "svc-batch", Kind: chstore.ProblemKindService, Status: "open"}),
			problemToInbox(chstore.Problem{ID: "p3", Service: "ext:oracle-src/OP_ORDERS", Kind: chstore.ProblemKindExternal, Status: "open"}),
			// Dış kaynak, gerçek servise çözülmüş (Kind service) — servisin env'i.
			problemToInbox(chstore.Problem{ID: "p4", Service: "svc-orders", Kind: chstore.ProblemKindService, RuleID: "ext-series:x", Status: "open"}),
			problemToInbox(chstore.Problem{ID: "p5", Service: "", Status: "open"}),
		}
	}
	ids := func(items []InboxItem) []string {
		out := []string{}
		for _, it := range items {
			out = append(out, it.ID)
		}
		return out
	}

	// Env seçili değil: hepsi.
	if got := ids(inboxEnvScopeItems(mk(), nil)); len(got) != 5 {
		t.Fatalf("env yokken satır düşmemeli: %v", got)
	}
	// Env seçili: env'siz ext: öznesi gizli, çözülmüş olan + global kalır.
	got := ids(inboxEnvScopeItems(mk(), []string{"svc-orders"}))
	want := ids([]InboxItem{
		problemToInbox(chstore.Problem{ID: "p1"}),
		problemToInbox(chstore.Problem{ID: "p4"}),
		problemToInbox(chstore.Problem{ID: "p5"}),
	})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("env=svc-orders: got %v want %v", got, want)
	}
	// Boş çözüm: yalnız global.
	if got := ids(inboxEnvScopeItems(mk(), []string{})); len(got) != 1 {
		t.Fatalf("boş env yalnız global satırı tutmalı: %v", got)
	}
}

// Sayım kapsamı listenin env üyelerini taşımalı — env'siz kapsam hatanın
// birebir şekli.
func TestInboxProblemCountScopeCarriesEnv(t *testing.T) {
	sc := inboxProblemCountScope("open", []string{"svc-orders"}, []string{"svc-orders", "svc-payments"})
	if !reflect.DeepEqual(sc.Env, []string{"svc-orders", "svc-payments"}) {
		t.Fatalf("env üyeleri sayıma inmiyor: %+v", sc)
	}
	if !reflect.DeepEqual(sc.Exclude, pickExcludedStatuses("open")) || !reflect.DeepEqual(sc.Team, []string{"svc-orders"}) {
		t.Fatalf("statü/takım ekseni listeden ayrıştı: %+v", sc)
	}
	if sc := inboxProblemCountScope("all", nil, nil); sc.Env != nil || sc.Exclude != nil {
		t.Fatalf("kısıtsız kapsam nil kalmalı: %+v", sc)
	}
}

// Kaynak pin: inboxView env'i BİR KEZ çözer ve hem liste daraltmasına hem
// çip COUNT'una aynı değişkeni verir; rozet de aynı çözücüyü kullanır.
func TestInboxViewSharesEnvMembersWithCounts(t *testing.T) {
	b, err := os.ReadFile("inbox.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"envMembers := s.resolveInboxEnvMembers(ctx, env)",
		"items = inboxEnvScopeItems(items, envMembers)",
		"inboxProblemCountScope(statusFilter, teamServices, envMembers)",
		"envServices := s.resolveInboxEnvMembers(ctx, env)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("inbox.go'da %q yok — liste ile sayım env kümesini paylaşmıyor", want)
		}
	}
	if strings.Count(src, "s.store.EnvMemberServices(") != 0 {
		t.Error("inbox.go env üyelerini resolveInboxEnvMembers dışında çözüyor — ikinci çözüm ayrışma kapısı")
	}
}
