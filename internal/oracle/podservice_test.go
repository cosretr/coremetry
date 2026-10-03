package oracle

import (
	"reflect"
	"testing"
)

// v0.10.908 — pod adından servis: aday sırası + yalnız canlı ad kabul.
func TestPodServiceCandidates(t *testing.T) {
	cases := []struct {
		pod  string
		want []string
	}{
		{"acme-mobile-login-prod-7b9949bb74-l4bg5", []string{"acme-mobile-login-prod", "acme-mobile-login"}},
		{"acme-digital-mobile-pushconfirm-prod-oneagent-55675dfc9-ab12c", []string{"acme-digital-mobile-pushconfirm-prod-oneagent", "acme-digital-mobile-pushconfirm-prod", "acme-digital-mobile-pushconfirm"}},
		{"ACME-Cards-SwitchIntegration-NonTx-Prod-B4c5c97cb-CGQXZ", []string{"acme-cards-switchintegration-nontx-prod", "acme-cards-switchintegration-nontx"}},
		{"kafka-broker-2", []string{"kafka-broker"}},
		{"WMOBAPPP84", nil},
		{"", nil},
		{"shop-7f9c", nil},
	}
	for _, c := range cases {
		if got := podServiceCandidates(c.pod); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%q → %v, beklenen %v", c.pod, got, c.want)
		}
	}
	alive := map[string]bool{"acme-mobile-login": true}
	if s := podService("acme-mobile-login-prod-7b9949bb74-l4bg5", alive); s != "acme-mobile-login" {
		t.Fatalf("canlı aday: %q", s)
	}
	if s := podService("acme-mobile-login-prod-7b9949bb74-l4bg5", map[string]bool{"acme-mobile-login-prod": true, "acme-mobile-login": true}); s != "acme-mobile-login-prod" {
		t.Fatalf("öncelik tam ad: %q", s)
	}
	if podService("acme-x-prod-7b9949bb74-l4bg5", alive) != "" || podService("acme-mobile-login-prod-7b9949bb74-l4bg5", nil) != "" {
		t.Fatal("canlı olmayan / doğrulanamayan ad kabul edilmemeli")
	}
}
