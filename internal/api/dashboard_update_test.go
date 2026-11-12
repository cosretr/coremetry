package api

// dashboard_update_test.go — v0.10.2000 (Dashboards Faz 3 dilim 3.10):
// iki sekmede kaydetme. İkinci sekme ilkinin yazdığı sürümü görmeden
// kaydederse 409; alan yoksa (eski istemci / sunucu içi yol) denetim yok.

import (
	"encoding/json"
	"testing"
)

func TestDashboardConflict(t *testing.T) {
	cases := []struct {
		name        string
		ifUpdatedAt int64
		current     int64
		want        bool
	}{
		{"alan yok: denetim yok", 0, 100, false},
		{"aynı sürüm", 100, 100, false},
		{"başkası kaydetti", 100, 200, true},
	}
	for _, c := range cases {
		if got := dashboardConflict(c.ifUpdatedAt, c.current); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestDashboardUpdateInputDecodes(t *testing.T) {
	var in dashboardUpdateInput
	body := `{"name":"A","panels":[{"id":"p"}],"tags":["x"],"ifUpdatedAt":1700000000123456789}`
	if err := json.Unmarshal([]byte(body), &in); err != nil {
		t.Fatal(err)
	}
	if in.Name != "A" || in.IfUpdatedAt != 1700000000123456789 || string(in.Tags) != `["x"]` || len(in.Panels) == 0 {
		t.Fatalf("gömülü alanlar çözülmedi: %+v", in)
	}
	var old dashboardUpdateInput
	_ = json.Unmarshal([]byte(`{"name":"B"}`), &old)
	if old.IfUpdatedAt != 0 || old.Variables != nil {
		t.Fatalf("eski gövde: %+v", old)
	}
}
