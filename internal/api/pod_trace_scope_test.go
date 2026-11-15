package api

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/entity"
)

// v0.10.2195 — pod → trace kapsamı entity yolundan: pod kenarı > iş yükü
// servisi (servis geneli) > none; cluster süzgeci kaydın eşlemesinden.
func TestPodTraceScopeFrom(t *testing.T) {
	podEdge := entity.Path{Service: "svc:cards", ClusterValue: "OCPA"}
	wlSvc := entity.Path{Service: "svc:bsa-cards-dispatcher-prod"}
	cases := []struct {
		name    string
		pod, wl entity.Path
		values  []string
		want    podTraceScope
	}{
		{"pod kenarı: span'in gerçek cluster değeri", podEdge, wlSvc, []string{"ocpa"}, podTraceScope{Mode: "pod", ClusterValue: "OCPA"}},
		{"pod kenarı, değer yok → kaydın tek değeri", entity.Path{Service: "svc:x"}, entity.Path{}, []string{"ocpa"}, podTraceScope{Mode: "pod", ClusterValue: "ocpa"}},
		{"pod span'i yok → iş yükü servisi", entity.Path{}, wlSvc, []string{"ocpa"}, podTraceScope{Mode: "service", ClusterValue: "ocpa", Service: "bsa-cards-dispatcher-prod"}},
		{"çok değerli kayıt → cluster süzgeci yok", entity.Path{}, wlSvc, []string{"a", "b"}, podTraceScope{Mode: "service", Service: "bsa-cards-dispatcher-prod"}},
		{"hiçbiri", entity.Path{}, entity.Path{}, []string{"ocpa"}, podTraceScope{Mode: "none", ClusterValue: "ocpa"}},
	}
	for _, c := range cases {
		if got := podTraceScopeFrom(c.pod, c.wl, c.values); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}
