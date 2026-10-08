package chstore

// problem_count_scope_test.go — v0.10.1131 (operatör-bildirimli): env
// seçiliyken /inbox şerit çipi (dış kaynak) listenin gizlediği satırları
// sayıyordu. Sayım kapsamı artık env eksenini listenin kuralıyla
// (envScopeConjunct ⇔ EnvScopeKeepsRow) taşıyor; burada sayım == liste
// matris olarak çivilenir: env seçili/değil × iç/dış kaynak × env'i
// çözülebilir/çözülemez.

import (
	"reflect"
	"strings"
	"testing"
)

func TestProblemCountScopeWhere(t *testing.T) {
	s := &Store{hasProblemKindCol: true}

	// Env yok → eski gövdeyle birebir (takım ekseni dokunulmadı).
	for _, team := range [][]string{nil, {}, {"svc-orders"}} {
		gotSQL, gotArgs := s.problemCountScopeWhere(ProblemCountScope{Exclude: []string{"resolved"}, Team: team})
		wantSQL, wantArgs := s.problemCountWhere([]string{"resolved"}, team)
		if gotSQL != wantSQL || !reflect.DeepEqual(gotArgs, wantArgs) {
			t.Fatalf("env'siz kapsam eski gövdeden ayrıştı: %q %v / %q %v", gotSQL, gotArgs, wantSQL, wantArgs)
		}
	}

	// Env var → listenin env yazımı AYNEN eklenir; argüman sırası
	// statü, takım, env.
	sql, args := s.problemCountScopeWhere(ProblemCountScope{
		Exclude: []string{"resolved"},
		Team:    []string{"svc-orders"},
		Env:     []string{"svc-payments", "svc-orders"},
	})
	want := "status NOT IN (?) AND " + envScopeConjunct(1, true) + " AND " + envScopeConjunct(2, true)
	if sql != want {
		t.Fatalf("kapsam WHERE'i:\n got %q\nwant %q", sql, want)
	}
	if !reflect.DeepEqual(args, []any{"resolved", "svc-orders", "svc-payments", "svc-orders"}) {
		t.Fatalf("arg sırası: %v", args)
	}

	// Boş env (hiçbir servise çözüldü) YİNE kısıttır.
	if sql, _ := s.problemCountScopeWhere(ProblemCountScope{Env: []string{}}); sql != "1 AND (service = '' OR kind = 'db')" {
		t.Fatalf("boş env kısıt olmalı: %q", sql)
	}
}

// TestProblemCountScopeMatchesInboxList — rozet/çip sayısı == liste satırı.
//
// "Liste": /inbox varsayılan şeridi (servis + dış kaynak) satırlarını Go'da
// EnvScopeKeepsRow ile daraltır. "Sayım": problemCountScopeWhere'in ürettiği
// env disjunktları (evalEnvConjunct, SQL metninden okur) ve kind kovaları
// (servis + dış kaynak toplamı, inboxLaneProblemCount'un okuduğu).
func TestProblemCountScopeMatchesInboxList(t *testing.T) {
	type row struct {
		name, service, kind string
	}
	rows := []row{
		{"iç servis, env üyesi", "svc-orders", ProblemKindService},
		{"iç servis, env dışı", "svc-batch", ProblemKindService},
		{"dış kaynak, servise çözülmemiş (env yok)", "ext:oracle-src/OP_ORDERS", ProblemKindExternal},
		{"dış kaynak, üye servise çözülmüş", "svc-orders", ProblemKindService},
		{"dış kaynak, env dışı servise çözülmüş", "svc-batch", ProblemKindService},
		{"servissiz global satır", "", ProblemKindService},
		{"db öznesi (başka şerit)", "db:oracle@crm.example.test", ProblemKindDB},
	}
	inLane := func(kind string) bool {
		k := ProblemSubjectKind(kind)
		return k == ProblemKindService || k == ProblemKindExternal
	}
	s := &Store{hasProblemKindCol: true}

	cases := []struct {
		name         string
		members      []string // nil = env seçili değil
		wantList     int
		wantExternal int
	}{
		// Env yok: şeridin 6 satırı da görünür, ext: dahil.
		{"env seçili değil", nil, 6, 1},
		// Env seçili: env'i olmayan ext: öznesi GİZLİ; çözülmüş dış kaynak
		// servisinin üyeliğiyle eşleşir; global satır görünür.
		{"env seçili", []string{"svc-orders"}, 3, 0},
		// Env hiçbir servise çözüldü: yalnız global satır.
		{"env boş çözüldü", []string{}, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set := map[string]bool{}
			for _, m := range tc.members {
				set[m] = true
			}
			sql, _ := s.problemCountScopeWhere(ProblemCountScope{Env: tc.members})
			conj := strings.TrimPrefix(sql, "1 AND ")

			list, ext := 0, 0
			buckets := map[string]int{}
			for _, r := range rows {
				// Liste yolu.
				if inLane(r.kind) && (tc.members == nil || EnvScopeKeepsRow(r.service, r.kind, set)) {
					list++
				}
				// Sayım yolu.
				keep := true
				if tc.members != nil {
					keep = evalEnvConjunct(t, conj, tc.members, r.service, r.kind)
				}
				if keep {
					buckets[ProblemSubjectKind(r.kind)]++
					if r.kind == ProblemKindExternal {
						ext++
					}
				}
			}
			count := buckets[ProblemKindService] + buckets[ProblemKindExternal]
			if count != list {
				t.Fatalf("rozet/çip (%d) ≠ liste (%d) — env=%v", count, list, tc.members)
			}
			if list != tc.wantList {
				t.Fatalf("liste satırı: got %d want %d", list, tc.wantList)
			}
			if buckets[ProblemKindExternal] != tc.wantExternal || ext != tc.wantExternal {
				t.Fatalf("dış kaynak kovası: got %d want %d", buckets[ProblemKindExternal], tc.wantExternal)
			}
		})
	}
}
