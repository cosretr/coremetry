package chstore

// problem_graph_merge_setting_test.go — v0.10.2473: görünüm kipi ayarı
// (varsayılan on, kayıtlı değer kazanır) ve manşet kökeni kuralları.

import (
	"errors"
	"testing"
)

func TestProblemGraphMergeSettingDefaultsAndStoredWins(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
		err  bool
	}{
		{"satır yok → varsayılan on", "", GraphMergeOn, false},
		{"kayıtlı off kazanır", `{"mode":"off"}`, GraphMergeOff, false},
		{"kayıtlı shadow kazanır", `{"mode":"shadow"}`, GraphMergeShadow, false},
		{"kayıtlı on", `{"mode":"on","updatedBy":"admin"}`, GraphMergeOn, false},
		{"tanınmayan kip → varsayılan", `{"mode":"enforce"}`, GraphMergeOn, false},
		{"kip alanı yok → varsayılan", `{}`, GraphMergeOn, false},
		{"bozuk JSON → hata (ekran kaydı ezmesin)", `{"mode":`, "", true},
	}
	for _, c := range cases {
		got, err := decodeProblemGraphMerge([]byte(c.raw))
		if (err != nil) != c.err {
			t.Fatalf("%s: err = %v", c.name, err)
		}
		if !c.err && got.Mode != c.want {
			t.Fatalf("%s: mode = %q, want %q", c.name, got.Mode, c.want)
		}
	}
	if DefaultProblemGraphMerge().Mode != GraphMergeOn {
		t.Fatal("varsayılan kip on olmalı")
	}
}

func TestProblemGraphMergeModeSwitch(t *testing.T) {
	for _, c := range []struct {
		mode string
		on   bool
	}{{GraphMergeOff, false}, {GraphMergeShadow, false}, {GraphMergeOn, true}} {
		restore := SetProblemGraphMergeModeForTest(c.mode)
		if ProblemGraphMergeMode() != c.mode || ProblemGraphMergeOn() != c.on {
			t.Fatalf("%s: mode=%q on=%v", c.mode, ProblemGraphMergeMode(), ProblemGraphMergeOn())
		}
		// Görünüm kipi bildirim tutması anahtarını ASLA açmaz.
		if ProblemGraphMergeEnforced() || ProblemGraphFrequentEnforced() {
			t.Fatalf("%s: bildirim tutması sabitleri kapalı kalmalı", c.mode)
		}
		restore()
	}
}

func TestLoadProblemGraphMergeKeepsLastGoodOnError(t *testing.T) {
	defer SetProblemGraphMergeModeForTest(GraphMergeOn)()
	LoadProblemGraphMergeWith(func() (ProblemGraphMergeSetting, error) {
		return ProblemGraphMergeSetting{Mode: GraphMergeShadow}, nil
	})
	if ProblemGraphMergeMode() != GraphMergeShadow {
		t.Fatalf("okunan değer yayınlanmalı: %q", ProblemGraphMergeMode())
	}
	LoadProblemGraphMergeWith(func() (ProblemGraphMergeSetting, error) {
		return ProblemGraphMergeSetting{}, errors.New("clickhouse yok")
	})
	if ProblemGraphMergeMode() != GraphMergeShadow {
		t.Fatalf("okuma hatasında son iyi değer korunmalı: %q", ProblemGraphMergeMode())
	}
}

func originGroup(o *ProblemGraphOrigin) ProblemGraphGroup {
	return ProblemGraphGroup{OwnerID: "p-pay", Entities: 3, Origin: o, Members: []ProblemGraphMember{
		{ProblemID: "p-pay", Entity: "svc:payments", Type: "service", Name: "payments", Owner: true},
		{ProblemID: "p-co", Entity: "svc:checkout", Type: "service", Name: "checkout", Link: GraphRuleCall},
		{ProblemID: "p-gw", Entity: "svc:api-gateway", Type: "service", Name: "api-gateway", Link: GraphRuleCall},
	}}
}

// Köken etiketi kuralları: yalnız computeOrigin bir köken SEÇTİYSE; belirsiz /
// eksik / tanınmayan her durumda etiket yok (tahmin yok).
func TestStdOriginOfRules(t *testing.T) {
	picked := func(rule, conf string) *ProblemGraphOrigin {
		return &ProblemGraphOrigin{Entity: "svc:payments", Name: "payments", Type: "service", Rule: rule, Confidence: conf}
	}
	cases := []struct {
		name string
		g    ProblemGraphGroup
		id   string
		want string // beklenen kural; "" = etiket yok
		self bool
	}{
		{"deploy (K-ROLLOUT)", originGroup(picked(OriginRuleRollout, OriginStrong)), "p-co", OriginRuleRollout, false},
		{"ortak bağımlılık (K-SHARED)", originGroup(picked(OriginRuleShared, OriginStrong)), "p-co", OriginRuleShared, false},
		{"en derin halka (K-DEEP), tek kural", originGroup(picked(OriginRuleDeep, OriginWeak)), "p-gw", OriginRuleDeep, false},
		{"ilk bozulan (K-FIRST)", originGroup(picked(OriginRuleFirst, OriginWeak)), "p-co", OriginRuleFirst, false},
		{"kökenin kendi Problem'i", originGroup(picked(OriginRuleDeep, OriginStrong)), "p-pay", OriginRuleDeep, true},
		{"köken hesaplanmadı", originGroup(nil), "p-co", "", false},
		{"köken belirsiz (hiçbir kural ayırmadı)", originGroup(&ProblemGraphOrigin{Confidence: OriginUnclear, Earliest: "payments"}), "p-co", "", false},
		{"güven alanı tanınmıyor", originGroup(picked(OriginRuleDeep, "maybe")), "p-co", "", false},
		{"kural tanınmıyor", originGroup(picked("K-GUESS", OriginStrong)), "p-co", "", false},
		{"varlık kimliği yok", originGroup(&ProblemGraphOrigin{Name: "payments", Rule: OriginRuleDeep, Confidence: OriginStrong}), "p-co", "", false},
		{"ad yok", originGroup(&ProblemGraphOrigin{Entity: "svc:payments", Rule: OriginRuleDeep, Confidence: OriginStrong}), "p-co", "", false},
	}
	for _, c := range cases {
		got := StdOriginOf(c.g, c.id)
		if c.want == "" {
			if got != nil {
				t.Fatalf("%s: etiket olmamalı: %+v", c.name, got)
			}
			continue
		}
		if got == nil || got.Rule != c.want || got.Entity != "svc:payments" || got.Name != "payments" || got.Entities != 3 || got.Self != c.self {
			t.Fatalf("%s: %+v", c.name, got)
		}
	}
	// Tek varlıklı küme (yalnız bağlanmayanları olan Problem): etiket yok.
	single := originGroup(picked(OriginRuleDeep, OriginStrong))
	single.Entities = 1
	if StdOriginOf(single, "p-pay") != nil {
		t.Fatal("tek varlıklı kümede köken etiketi olmamalı")
	}
	// Kendi Problem'i olmayan ortak bağımlılık köken olabilir: kimse Self değil.
	dep := originGroup(&ProblemGraphOrigin{Entity: "db:postgresql@orders-db", Name: "orders-db", Type: "database", Rule: OriginRuleShared, Confidence: OriginStrong})
	if o := StdOriginOf(dep, "p-co"); o == nil || o.Type != "database" || o.Self {
		t.Fatalf("ortak bağımlılık kökeni: %+v", o)
	}
}

// Standardize* köken alanını DOLDURMAZ: bildirim / politika / özet yolu
// (StandardizeProblem'i çağıranlar) v0.10.2472 ile aynı standardı görür.
func TestStandardizeProblemNeverSetsOrigin(t *testing.T) {
	for _, mode := range []string{GraphMergeOff, GraphMergeShadow, GraphMergeOn} {
		restore := SetProblemGraphMergeModeForTest(mode)
		std := StandardizeProblem(Problem{ID: "p-co", RuleID: "anomaly:checkout:error_rate", Service: "checkout", Status: "open",
			Severity: "critical", Metric: "error_rate", Value: 12, Threshold: 1}, StdFacts{})
		restore()
		if std.Origin != nil {
			t.Fatalf("%s: StandardizeProblem köken yazmamalı: %+v", mode, std.Origin)
		}
	}
}
