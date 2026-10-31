package api

// v0.10.1723 — Operatör: CoSRE v2'de composer bağlamı trace + servis +
// env + pencere taşırken takım bilinmiyordu (no_team); model "Hangi
// takımdasınız?" diye durup takım listesi döktü. Çapalı soru takım
// kapsamı istemez; takım biçimli soru / brifing bugünkü gibi ister.

import (
	"strings"
	"testing"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	"github.com/cilcenk/coremetry/internal/copilot"
)

func TestNeedsTeamScope(t *testing.T) {
	const tid = "4bf92f3577b34da6a3ce929d0e0e4736"
	cases := []struct {
		name string
		ctx  teamScopeCtx
		q    string
		want bool
	}{
		{"trace çapası", teamScopeCtx{Trace: tid}, "bu trace neden yavaş?", false},
		{"servis çapası", teamScopeCtx{Service: "orders-api"}, "hata oranı ne?", false},
		{"açıklama çapası", teamScopeCtx{Explain: true}, "logda ne yazıyor?", false},
		{"problem çapası", teamScopeCtx{Problem: "p-1"}, "kök neden ne?", false},
		{"trace + servis + soru düz", teamScopeCtx{Trace: tid, Service: "orders-api"}, "açıkla", false},
		{"çapalı ama takım biçimli: servislerim", teamScopeCtx{Service: "orders-api"}, "Servislerim nasıl?", true},
		{"çapalı ama takım biçimli: ekibimde", teamScopeCtx{Trace: tid}, "ekibimde başka hata var mı", true},
		{"takımım (büyük İ/I katlanır)", teamScopeCtx{}, "TAKIMIMIN durumu", true},
		{"my services", teamScopeCtx{Service: "orders-api"}, "how are my services", true},
		{"brifing satırı", teamScopeCtx{BriefingID: "b-1", Service: "orders-api"}, "bunu açıkla", true},
		{"çapasız düz soru", teamScopeCtx{}, "bugün ne bozuldu?", true},
		{"boşluk çapa sayılmaz", teamScopeCtx{Trace: "  ", Service: " "}, "neden yavaş", true},
	}
	for _, c := range cases {
		if got := needsTeamScope(c.ctx, c.q); got != c.want {
			t.Errorf("%s: needsTeamScope = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTeamScopeCtxFromPages(t *testing.T) {
	c := teamScopeCtxFrom("", "", false, "",
		nil,
		&agentctx.PageContext{Page: "trace", TraceID: "abc"},
		&agentctx.PageContext{Page: "service", Service: "orders-api", ExceptionID: "fp1"})
	if c.Trace != "abc" || c.Service != "orders-api" || c.Problem != "fp1" {
		t.Fatalf("got %+v", c)
	}
	// Açık bağlam alanı sayfanınkini ezer.
	if c := teamScopeCtxFrom("x", "svc", false, "", &agentctx.PageContext{TraceID: "y", Service: "z"}); c.Trace != "x" || c.Service != "svc" {
		t.Fatalf("explicit lost: %+v", c)
	}
}

func TestScopeAddendumFor(t *testing.T) {
	noTeam := userScope{Unscoped: true, Reason: scopeReasonNoTeam}
	anch := copilot.ChatUserScopeAnchoredAddendum()
	cases := []struct {
		name string
		u    userScope
		need bool
		want string
	}{
		{"no_team + çapalı → takım sorma", noTeam, false, anch},
		{"no_services + çapalı → takım sorma", userScope{Unscoped: true, Reason: scopeReasonNoServices}, false, anch},
		{"no_team + takım gerekli → bugünkü sor eki", noTeam, true, copilot.ChatUserScopeAskTeamAddendum()},
		{"kaçış → ek yok", userScope{Unscoped: true, Reason: scopeReasonEscape}, false, ""},
		{"kapalı → ek yok", userScope{Unscoped: true, Reason: scopeReasonDisabled}, false, ""},
		{"kapsamlı → kapsam eki aynen", userScope{Reason: scopeReasonTeam, Team: "payments", Services: []string{"a"}}, false, copilot.ChatUserScopeAddendum("payments", 1)},
	}
	for _, c := range cases {
		if got := scopeAddendumFor(c.u, nil, c.need, false); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	// Çapalı ek modeli takım sormaya / list_teams'e İTMEZ: "bir kez sor" işaretini taşımaz.
	if strings.Contains(strings.ToLower(anch), copilot.ChatUserScopeAskMarker) {
		t.Fatalf("anchored addendum carries ask marker %q", copilot.ChatUserScopeAskMarker)
	}
}
