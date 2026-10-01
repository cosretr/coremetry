package evaluator

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1027 — /databases işareti ve /database problem kartı özne BİÇİMİNİ
// kuraldan türetir: kapasite kuralı instance, gerisi dbName. Üreticiler o
// sözleşmeye uyuyor mu — burada çivili.

func TestDBProblemFormMatchesProducers(t *testing.T) {
	for _, c := range capacityChecks {
		rid := capacityRuleID(c.id)
		if got := chstore.DBProblemSubjectForm(rid); got != chstore.DBSubjectFormInstance {
			t.Errorf("kapasite kuralı %q biçimi %q, beklenen instance", rid, got)
		}
		if pid := capacityProblemID(c.id, "core-db", ""); !strings.HasPrefix(pid, chstore.RuleDBCapacityPrefix) {
			t.Errorf("kapasite problem kimliği öneksiz: %q", pid)
		}
	}
	if got := chstore.DBProblemSubjectForm(dbSlowStmtRuleID); got != chstore.DBSubjectFormDBName {
		t.Errorf("yavaş ifade kuralı biçimi %q, beklenen dbName", got)
	}
}

// Bugünkü BİLİNEN açık: PostgreSQL kapasite denetimi motoru "POSTGRES" yazar,
// özne `db:postgres@<instance>` olur; span ve receiver satırları ise
// "postgresql" taşır (chstore/dependencies.go receiver önekleri). Liste ve
// detay bu problemi AYNI biçimde kaçırır. Düzeltme üreticide (özne kimliği
// kanonikleştirme) ayrı bir değişiklik — o gün bu pin ve FE ikizi
// (pages/databases/databaseProblems.test.ts "Postgres takma adı") birlikte
// güncellenir; tek taraflı bir düzeltme ikisinden birini kırar.
func TestPostgresCapacitySubjectAliasPin(t *testing.T) {
	var found bool
	for _, c := range capacityChecks {
		if c.id != "postgres-connections" {
			continue
		}
		found = true
		subj, kind := capacitySubject(c.dbsys, "pg-1")
		if subj != "db:postgres@pg-1" || kind != chstore.ProblemKindDB {
			t.Errorf("bugünkü özne %q/%q, pin db:postgres@pg-1/db", subj, kind)
		}
	}
	if !found {
		t.Fatal("postgres-connections denetimi bulunamadı")
	}
}
