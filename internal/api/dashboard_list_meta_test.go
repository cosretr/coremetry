package api

// dashboard_list_meta_test.go — v0.10.1996 (Dashboards Faz 3 dilim 3.2):
// "Son görüntülemeniz" blobu pano başına tek giriş tutar, en yeni önde,
// 50'ye kırpılır.

import (
	"fmt"
	"testing"
)

func TestDashboardRecentPush(t *testing.T) {
	e := func(id string, at int64) dashboardRecentEntry { return dashboardRecentEntry{ID: id, At: at} }
	ids := func(xs []dashboardRecentEntry) string {
		s := ""
		for _, x := range xs {
			s += x.ID + ","
		}
		return s
	}
	cases := []struct {
		name string
		cur  []dashboardRecentEntry
		in   dashboardRecentEntry
		max  int
		want string
	}{
		{"boş", nil, e("a", 1), 50, "a,"},
		{"en yeni önde", []dashboardRecentEntry{e("a", 1)}, e("b", 2), 50, "b,a,"},
		{"tekrar açılan öne taşınır", []dashboardRecentEntry{e("b", 2), e("a", 1), e("c", 0)}, e("a", 3), 50, "a,b,c,"},
		{"tavan", []dashboardRecentEntry{e("b", 2), e("a", 1)}, e("c", 3), 2, "c,b,"},
	}
	for _, tc := range cases {
		cur := append([]dashboardRecentEntry(nil), tc.cur...)
		got := dashboardRecentPush(tc.cur, tc.in, tc.max)
		if ids(got) != tc.want {
			t.Errorf("%s: got %s want %s", tc.name, ids(got), tc.want)
		}
		if fmt.Sprint(cur) != fmt.Sprint(tc.cur) {
			t.Errorf("%s: girdi dilimi değişti", tc.name)
		}
	}
	if got := dashboardRecentPush(nil, e("x", 9), 50); got[0].At != 9 {
		t.Errorf("zaman korunmalı: %+v", got)
	}
}
