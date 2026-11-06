package api

// pages_incidents_test.go — v0.10.1828 (Incidents emekliliği E3): Settings
// → Roles ızgarası emekliyken /incidents önermez; doğrulama tam listeyle
// kalır (kayıtlı rol değeri reddedilmez).

import "testing"

func TestAvailablePagesForIncidentsSwitch(t *testing.T) {
	has := func(ps []AvailablePage, id string) bool {
		for _, p := range ps {
			if p.ID == id {
				return true
			}
		}
		return false
	}
	if !has(availablePagesFor(true), "/incidents") {
		t.Error("açık: /incidents listede olmalı")
	}
	off := availablePagesFor(false)
	if has(off, "/incidents") || !has(off, "/anomalies") || len(off) != len(availablePages)-1 {
		t.Errorf("emekli: /incidents yok, /anomalies var, diğerleri aynen olmalı (%d/%d)", len(off), len(availablePages))
	}
	if _, ok := availablePageIDs()["/incidents"]; !ok {
		t.Error("doğrulama tam listeyle kalmalı (kayıtlı rol değeri reddedilmez)")
	}
}
