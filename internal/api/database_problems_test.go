package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1027 — /databases listesinde satır başına açık problem işareti:
// (özne, biçim) gruplama, önem (öncelik değil), "problem değil" dışlaması.

const capRule = chstore.RuleDBCapacityPrefix + "postgres-connections"

func TestSummarizeDBProblems(t *testing.T) {
	probs := []chstore.Problem{
		// Aynı özne dizgisi İKİ biçimde: "postgres" hem bir instance (kapasite)
		// hem bir veritabanı adı (yavaş ifade). Biçimler AYRI sayılır.
		{ID: "a", RuleID: capRule, Service: "db:postgresql@postgres", Severity: "warning"},
		{ID: "b", RuleID: "db-slow-stmt", Service: "db:postgresql@postgres", Severity: "critical"},
		{ID: "c", RuleID: "db-slow-stmt", Service: "db:postgresql@postgres", Severity: "warning"},
		{ID: "d", RuleID: "rule-target-1", Service: "db:oracle@CORE", Severity: ""}, // boş önem → info
		{ID: "e", RuleID: capRule, Service: "db:oracle@core-db", Severity: "info"},
		{ID: "f", RuleID: capRule, Service: "db:oracle@core-db", Severity: "critical"},
		{ID: "g", RuleID: "db-slow-stmt", Service: "", Severity: "critical"},             // boş özne atlanır
		{ID: "h", RuleID: "db-slow-stmt", Service: "db:mysql@orders", Severity: "fatal"}, // bilinmeyen → info
	}
	got, truncated := summarizeDBProblems(probs, nil, 100)
	type want struct {
		inst, name *dbProblemCount
	}
	cases := map[string]want{
		"db:postgresql@postgres": {inst: &dbProblemCount{1, "warning"}, name: &dbProblemCount{2, "critical"}},
		"db:oracle@CORE":         {name: &dbProblemCount{1, "info"}},
		"db:oracle@core-db":      {inst: &dbProblemCount{2, "critical"}},
		"db:mysql@orders":        {name: &dbProblemCount{1, "info"}},
	}
	if len(got) != len(cases) {
		t.Fatalf("özne sayısı %d, beklenen %d: %v", len(got), len(cases), got)
	}
	eq := func(a, b *dbProblemCount) bool {
		if a == nil || b == nil {
			return a == b
		}
		return *a == *b
	}
	for id, w := range cases {
		g := got[id]
		if !eq(g.Instance, w.inst) || !eq(g.DBName, w.name) {
			t.Errorf("%s: instance=%+v dbName=%+v, beklenen instance=%+v dbName=%+v", id, g.Instance, g.DBName, w.inst, w.name)
		}
	}
	if _, ok := got[""]; ok {
		t.Error("boş özne haritaya girmemeli")
	}
	if truncated {
		t.Error("8 satır / tavan 100: truncated false olmalı")
	}

	t.Run("problem değil işaretlileri sayılmaz; nil küme = açık kapı (hepsi sayılır)", func(t *testing.T) {
		in := []chstore.Problem{
			{RuleID: "db-slow-stmt", Service: "db:oracle@CORE", Severity: "critical"},
			{RuleID: capRule, Service: "db:oracle@CORE", Severity: "warning"},
		}
		noise := chstore.NoiseVerdictSignatures([]chstore.ProblemVerdict{
			{Signature: chstore.RuleProblemVerdictSignature("db-slow-stmt", "db:oracle@CORE"), Verdict: chstore.ProblemVerdictNoise},
			{Signature: chstore.RuleProblemVerdictSignature(capRule, "db:oracle@CORE"), Verdict: chstore.ProblemVerdictReal},
		})
		got, _ := summarizeDBProblems(in, noise, 100)
		g := got["db:oracle@CORE"]
		if g.DBName != nil {
			t.Errorf("noise imzalı yavaş ifade sayıldı: %+v", g.DBName)
		}
		if g.Instance == nil || *g.Instance != (dbProblemCount{1, "warning"}) {
			t.Errorf("\"gerçek\" kararlı kapasite problemi sayılmalı: %+v", g.Instance)
		}
		// Karar listesi okunamadı (nil): ikisi de sayılır.
		open, _ := summarizeDBProblems(in, nil, 100)
		if o := open["db:oracle@CORE"]; o.DBName == nil || o.Instance == nil {
			t.Errorf("açık kapı: her şey sayılmalı, gelen %+v", o)
		}
		// Hepsi noise ise özne hiç yazılmaz.
		all := chstore.NoiseVerdictSignatures([]chstore.ProblemVerdict{
			{Signature: "p:db-slow-stmt|db:oracle@CORE", Verdict: chstore.ProblemVerdictNoise},
			{Signature: "p:" + capRule + "|db:oracle@CORE", Verdict: chstore.ProblemVerdictNoise},
		})
		if none, _ := summarizeDBProblems(in, all, 100); len(none) != 0 {
			t.Errorf("tümü noise: özne yazılmamalı, gelen %v", none)
		}
	})

	t.Run("tavan", func(t *testing.T) {
		for _, c := range []struct {
			n, limit int
			want     bool
		}{
			{n: 2, limit: 3, want: false},
			{n: 3, limit: 3, want: true}, // tavana dayandı: eksik olabilir
			{n: 4, limit: 3, want: true},
			{n: 0, limit: 0, want: false}, // tavansız çağrı kesme iddia etmez
		} {
			rows := make([]chstore.Problem, c.n)
			for i := range rows {
				rows[i] = chstore.Problem{RuleID: "db-slow-stmt", Service: "db:oracle@x", Severity: "warning"}
			}
			if _, tr := summarizeDBProblems(rows, nil, c.limit); tr != c.want {
				t.Errorf("n=%d limit=%d: truncated %v, beklenen %v", c.n, c.limit, tr, c.want)
			}
		}
		// Kesme OKUMAYA bakar: süzülen satırlar da tavanı doldurmuş sayılır.
		rows := []chstore.Problem{
			{RuleID: "db-slow-stmt", Service: "db:oracle@x"},
			{RuleID: "db-slow-stmt", Service: "db:oracle@x"},
		}
		noise := map[string]struct{}{"p:db-slow-stmt|db:oracle@x": {}}
		if got, tr := summarizeDBProblems(rows, noise, 2); !tr || len(got) != 0 {
			t.Errorf("süzme sonrası boş ama okuma tavanda: truncated %v, got %v", tr, got)
		}
	})

	t.Run("boş girdi boş harita (null değil)", func(t *testing.T) {
		got, tr := summarizeDBProblems(nil, nil, dbProblemsScanLimit)
		if got == nil || len(got) != 0 || tr {
			t.Errorf("got %v truncated %v", got, tr)
		}
	})
}

func TestDatabaseProblemsRoute(t *testing.T) {
	s := &Server{}
	mux := http.NewServeMux()
	s.registerDatabaseProblemRoutes(mux)
	// Handler'ı ÇALIŞTIRMADAN eşleşen kalıbı sorar (store yok).
	_, pattern := mux.Handler(httptest.NewRequest("GET", "/api/databases/problems", nil))
	if pattern != "GET /api/databases/problems" {
		t.Errorf("kalıp %q", pattern)
	}
	if _, ok := extraRouteRegistrars["database-problems"]; !ok {
		t.Error("defterde kayıt yok — rota HTTP 200 + boş SPA sayfası döner")
	}

	src, err := os.ReadFile("database_problems.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	for _, want := range []string{
		`registerRoutesExtra("database-problems"`,
		`s.serveCached(w, r, dbProblemsCacheKey, 15*time.Second`,
		`SubjectKind: chstore.ProblemKindDB`,
		// Problems sekmesiyle AYNI "bitti" tanımı (inbox.go inboxDoneStatuses).
		`NotStatuses: pickExcludedStatuses("open")`,
		`Limit:       dbProblemsScanLimit`,
		`summarizeDBProblems(probs, s.dbProblemNoiseSet(ctx), dbProblemsScanLimit)`,
		`s.store.ListProblemVerdicts(ctx)`,
		`chstore.NoiseVerdictSignatures(list)`,
		`chstore.RuleProblemVerdictSignature(p.RuleID, p.Service)`,
		`chstore.DBProblemSubjectForm(p.RuleID)`,
	} {
		if !strings.Contains(code, want) {
			t.Errorf("database_problems.go %q taşımalı", want)
		}
	}
	// Önem saklanan kolon: öncelik zenginleştirmesi (ve deploy okuması) yok.
	for _, bad := range []string{"enrichProblemsForRead", "EnrichProblemsWithPriority", "r.Context()"} {
		if strings.Contains(code, bad) {
			t.Errorf("database_problems.go %q içermemeli", bad)
		}
	}
	if dbProblemsScanLimit != chstore.ProblemScanCeiling {
		t.Errorf("tarama tavanı %d, ProblemScanCeiling %d", dbProblemsScanLimit, chstore.ProblemScanCeiling)
	}
}
