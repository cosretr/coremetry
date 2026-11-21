package infraroutes

import (
	"regexp"
	"testing"
)

func TestMatchAndRegexAgree(t *testing.T) {
	re := regexp.MustCompile("^(?:" + RouteRegex() + ")$") // VM `=~` / `!~` tam çapalar
	cases := map[string]bool{
		"/APP/actuator/prometheus": true,
		"/shop/Actuator/Health":    true,
		"/healthz":                 true,
		"/.well-known/openid":      true,
		"/svc/-/ready":             true,
		"/api/v1/orders/{id}":      false,
		"/api/v1/users":            false,
		"":                         false, // rota etiketi yok → korunur
		"/Xwell-known/x":           false, // nokta kaçışlı: "." herhangi karakter DEĞİL
	}
	for in, want := range cases {
		if Match(in) != want {
			t.Errorf("Match(%q) = %v", in, !want)
		}
		if re.MatchString(in) != want {
			t.Errorf("RouteRegex(%q) = %v", in, !want)
		}
	}
	if RegexBody()[:4] != "(?i:" {
		t.Errorf("harf duyarsız gövde: %s", RegexBody())
	}
}
