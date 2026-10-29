package anomaly

// v0.10.1583 — kök-neden rollout adayı yalnız v1 workload_rollouts'u
// okuyordu; rollouts.source="v2" (varsayılan) iken aday hiç çıkmıyordu.
// Davranışsal kanıt gerçek CH'ye karşı: rollout_causes_v2_chsmoke_test.go.
// Bu test kaynak kararının her kolunu ve main.go kablolamasını çiviler
// (saf karar yeşilken kaynak hiç bağlanmazsa düzeltme kendini iptal eder).

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/rollout"
)

func TestRolloutReadsV2FollowsSource(t *testing.T) {
	cases := []struct {
		name string
		src  func() string
		want bool
	}{
		{name: "kurulmamış", src: nil, want: false},
		{name: "v1", src: func() string { return rollout.SourceV1 }, want: false},
		{name: "v2", src: func() string { return rollout.SourceV2 }, want: true},
		{name: "boş", src: func() string { return "" }, want: false},
		{name: "sözlük dışı", src: func() string { return "v3" }, want: false},
	}
	for _, tc := range cases {
		if got := rolloutReadsV2(tc.src); got != tc.want {
			t.Errorf("%s: rolloutReadsV2 = %v, istenen %v", tc.name, got, tc.want)
		}
	}
}

func TestMainWiresRolloutSource(t *testing.T) {
	b, err := os.ReadFile("../../main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "rcSynth.SetRolloutSource(func() string { return rolloutSettings.ResolvedV2().Source })") {
		t.Fatal("main.go sentezleyiciye rollout kaynağını bağlamıyor")
	}
}
