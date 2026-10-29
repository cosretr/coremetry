package chstore

// v0.10.1582 — Problem RecentDeploy pipeline deploy event'lerini görmüyordu
// (fetchDeploysByService yalnız span çıkarımı). Davranışsal kanıt gerçek
// CH'ye karşı: problem_deploy_events_chsmoke_test.go. Bu test birleşimin
// her kolunu çiviler: event kazanır, yeni sürüm eklenir, liste ASC kalır,
// dokunulmayan servis aynı, boş girdi.

import (
	"reflect"
	"testing"
)

func TestMergeDeployEventsByService(t *testing.T) {
	cases := []struct {
		name   string
		in     map[string][]spanDeploy
		events []RecentDeployEntry
		want   map[string][]spanDeploy
	}{
		{
			name: "event yok → aynı",
			in:   map[string][]spanDeploy{"a": {{"v1", 10}}},
			want: map[string][]spanDeploy{"a": {{"v1", 10}}},
		},
		{
			name:   "aynı sürüm: event zamanı kazanır, sıra ASC",
			in:     map[string][]spanDeploy{"a": {{"v1", 10}, {"v2", 30}}},
			events: []RecentDeployEntry{{Service: "a", Version: "v2", FirstSeenNs: 5}},
			want:   map[string][]spanDeploy{"a": {{"v2", 5}, {"v1", 10}}},
		},
		{
			name:   "yalnız event'te olan sürüm eklenir",
			in:     map[string][]spanDeploy{"a": {{"v1", 10}}},
			events: []RecentDeployEntry{{Service: "a", Version: "v3", FirstSeenNs: 20}},
			want:   map[string][]spanDeploy{"a": {{"v1", 10}, {"v3", 20}}},
		},
		{
			name:   "span çıkarımı boş servis",
			in:     nil,
			events: []RecentDeployEntry{{Service: "b", Version: "v9", FirstSeenNs: 7}},
			want:   map[string][]spanDeploy{"b": {{"v9", 7}}},
		},
		{
			name:   "başka servise dokunulmaz",
			in:     map[string][]spanDeploy{"a": {{"v1", 10}}, "c": {{"x", 1}}},
			events: []RecentDeployEntry{{Service: "a", Version: "v2", FirstSeenNs: 40}},
			want:   map[string][]spanDeploy{"a": {{"v1", 10}, {"v2", 40}}, "c": {{"x", 1}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeDeployEventsByService(tc.in, tc.events)
			if tc.want == nil {
				tc.want = map[string][]spanDeploy{}
			}
			if got == nil {
				got = map[string][]spanDeploy{}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, istenen %+v", got, tc.want)
			}
		})
	}
}
