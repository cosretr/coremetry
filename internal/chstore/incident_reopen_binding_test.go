package chstore

// v0.10.1580 — yeniden açılan sabit-kimlikli problem ÇÖZÜLMÜŞ incident'a
// yeniden bağlanıyordu (kural 1 durum bakmıyordu). Davranışsal kanıt gerçek
// CH'ye karşı: incident_reopen_chsmoke_test.go. Bu test kararın her kolunu
// çiviler.

import "testing"

func TestReuseIncidentBindingOnlyWhenNotResolved(t *testing.T) {
	cases := []struct {
		name string
		inc  *Incident
		want bool
	}{
		{name: "incident satırı yok", inc: nil, want: false},
		{name: "open", inc: &Incident{Status: "open"}, want: true},
		{name: "acknowledged", inc: &Incident{Status: "acknowledged"}, want: true},
		{name: "resolved", inc: &Incident{Status: "resolved"}, want: false},
	}
	for _, tc := range cases {
		if got := reuseIncidentBinding(tc.inc); got != tc.want {
			t.Errorf("%s: reuseIncidentBinding = %v, istenen %v", tc.name, got, tc.want)
		}
	}
}
