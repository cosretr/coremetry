package api

// v0.10.1081 — SLO burn-rate Problem anahtarı (problem_priority.sloBurnProblems,
// *bool, nil = kapalı). Operatör: "SLO burn rate problem olmasın, çıkar. SLO
// ile ilgili beklentim yok." PUT gövdesi kayıtlı değerin üstüne çözülür: alanı
// göndermeyen istemci (eski UI, betik) operatörün açtığı anahtarı kapatmaz.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestPutProblemPrioritySLOBurnFlag(t *testing.T) {
	on, off := true, false
	cases := []struct {
		name   string
		stored *bool
		body   string
		want   bool
	}{
		{"kayıt yok, alan yok → kapalı (varsayılan)", nil, `{"bigBreachRatio":2,"staleCriticalHours":4}`, false},
		{"kayıtlı açık, alan yok → açık kalır", &on, `{"bigBreachRatio":2,"staleCriticalHours":4}`, true},
		{"kayıtlı açık, gövde false → kapanır", &on, `{"bigBreachRatio":2,"staleCriticalHours":4,"sloBurnProblems":false}`, false},
		{"kayıtlı kapalı, gövde true → açılır", &off, `{"bigBreachRatio":2,"staleCriticalHours":4,"sloBurnProblems":true}`, true},
		{"açık null → varsayılan (kapalı)", &on, `{"bigBreachRatio":2,"staleCriticalHours":4,"sloBurnProblems":null}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stored := chstore.ProblemPriorityConfig{BigBreachRatio: 2, StaleCriticalHours: 4, SLOBurnProblems: c.stored}
			got, err := decodeProblemPriorityPut(stored, strings.NewReader(c.body))
			if err != nil {
				t.Fatal(err)
			}
			if chstore.NormalizeProblemPriority(got).SLOBurnProblemsEnabled() != c.want {
				t.Fatalf("SLOBurnProblemsEnabled = %v, want %v", !c.want, c.want)
			}
		})
	}
	// Decode kayıtlı değerin bool'unu yerinde ezmez.
	if !on || off {
		t.Fatalf("kayıtlı işaretçiler yerinde değişti: on=%v off=%v", on, off)
	}
}
