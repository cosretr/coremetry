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
		{"bsa-mobile-login-prod-7b9949bb74-l4bg5", []string{"bsa-mobile-login-prod", "bsa-mobile-login"}},
		{"bsa-digital-mobile-pushconfirm-prod-oneagent-55675dfc9-ab12c", []string{"bsa-digital-mobile-pushconfirm-prod-oneagent", "bsa-digital-mobile-pushconfirm-prod", "bsa-digital-mobile-pushconfirm"}},
		{"BSA-Cards-SwitchIntegration-NonTx-Prod-B4c5c97cb-CGQXZ", []string{"bsa-cards-switchintegration-nontx-prod", "bsa-cards-switchintegration-nontx"}},
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
	alive := map[string]bool{"bsa-mobile-login": true}
	if s := podService("bsa-mobile-login-prod-7b9949bb74-l4bg5", alive); s != "bsa-mobile-login" {
		t.Fatalf("canlı aday: %q", s)
	}
	if s := podService("bsa-mobile-login-prod-7b9949bb74-l4bg5", map[string]bool{"bsa-mobile-login-prod": true, "bsa-mobile-login": true}); s != "bsa-mobile-login-prod" {
		t.Fatalf("öncelik tam ad: %q", s)
	}
	if podService("bsa-x-prod-7b9949bb74-l4bg5", alive) != "" || podService("bsa-mobile-login-prod-7b9949bb74-l4bg5", nil) != "" {
		t.Fatal("canlı olmayan / doğrulanamayan ad kabul edilmemeli")
	}
}
