package api

// database_problems.go — v0.10.1027 — /databases LİSTESİNDE satır başına açık
// problem işareti (Databases × Dynatrace, dilim 4). Dynatrace'in veritabanı
// listesi her satırda "bu varlıkta açık problem var" der; bizde aynı bilgi
// yalnız detay sayfasının problem kartında (v0.10.1019) ve Problems
// sekmesinin Veritabanları şeridindeydi.
//
//	GET /api/databases/problems
//
// PARAMETRESİZ, bilerek: açık problem "ŞİMDİ"dir — seçili pencereye bağlı
// değil; ve db özneleri env'e bağlı değil (problems satırı env boyutu
// taşımaz, db öznesi env şeridinden zaten kaçar — env_members.go). Pencere
// ya da env anahtara girseydi cevabı DEĞİŞTİRMEDEN aynı yükün N kopyasını
// önbelleğe basardı.
//
// Neden ayrı uç, /api/problems?service= DEĞİL: liste satır başına özne
// soramaz — N satır = N istek. Tek okuma: kind=db + bitmemiş (Problems
// sekmesiyle AYNI tanım: pickExcludedStatuses), ikisi de SQL'de LIMIT'ten
// ÖNCE (v0.9.322 sınıfı); sonra (özne, BİÇİM) başına sayım + en ağır önem.
//
// BİÇİM: `db:<system>@<X>` X'in instance mı veritabanı adı mı olduğunu
// söylemez; kuraldan türer (chstore.DBProblemSubjectForm — kapasite kuralı
// instance, gerisi dbName). Cevap ikisini AYRI taşır, istemci instance
// biçimini yalnız satırın instance'ıyla, dbName biçimini yalnız satırın
// db.name'iyle eşleştirir (databaseProblems.ts rowProblemSummary). Ayırmadan
// "postgres" adlı bir veritabanının yavaş ifadesi, instance'ı "postgres"
// olan HER span satırını işaretlerdi.
//
// ÖNEM, öncelik DEĞİL: /inbox exception dışı her satırı P3'e çiviliyor
// (forceNonExceptionP3, operatör kararı v0.9.487); listede kırmızı "P1"
// Problems sekmesinde P3 görünürdü. Önem saklanan kolon — zenginleştirme
// (deploy okuması dahil) gerekmez.
//
// "Problem değil" işaretli imzalar (p:<kural>|<özne>) sayılmaz — Problems
// sekmesi onları varsayılan listede gizliyor. Karar listesi okunamazsa her
// şey sayılır (açık kapı): bir süsleme için işaretleri karartmak, gürültülü
// bir işaretten kötü.
//
// Rol kapısı yok (/api/databases ile aynı duruş — viewer görür). 15 sn
// önbellek; anahtar statik çünkü ucun girdisi yok.

import (
	"context"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("database-problems", (*Server).registerDatabaseProblemRoutes) }

func (s *Server) registerDatabaseProblemRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/databases/problems", s.getDatabaseProblems)
}

// dbProblemsScanLimit — tarama tavanı (chstore.ProblemScanCeiling emsali).
// Tavana dayanınca cevap truncated=true der ve liste bunu bir notla söyler:
// sessizce eksik bir işaret kümesi "bu veritabanında problem yok" diye okunur.
const dbProblemsScanLimit = chstore.ProblemScanCeiling

// dbProblemsCacheKey — girdisiz uç, tek anahtar. Yük şekli değişirse v2.
const dbProblemsCacheKey = "db-problems:v1"

// dbProblemCount — bir (özne, biçim) çiftinin açık problem özeti.
type dbProblemCount struct {
	Open        int    `json:"open"`
	TopSeverity string `json:"topSeverity"` // critical | warning | info
}

// dbProblemForms — bir özne kimliğinin iki biçimdeki özeti; yalnız açık
// problemi olan biçim yazılır.
type dbProblemForms struct {
	Instance *dbProblemCount `json:"instance,omitempty"`
	DBName   *dbProblemCount `json:"dbName,omitempty"`
}

type dbProblemsResponse struct {
	// Subjects — özne kimliği (Problem.Service, DBSubjectID biçimi) → biçim özetleri.
	Subjects  map[string]dbProblemForms `json:"subjects"`
	Truncated bool                      `json:"truncated"`
}

// dbSeverity — saklanan önemin kanonik hâli: critical / warning; gerisi info.
func dbSeverity(s string) string {
	switch s {
	case "critical", "warning":
		return s
	default:
		return "info"
	}
}

func dbSeverityRank(s string) int {
	switch s {
	case "critical":
		return 3
	case "warning":
		return 2
	default:
		return 1
	}
}

// summarizeDBProblems — SAF: (özne, biçim) başına açık sayı + en ağır önem.
// noise: "problem değil" imza kümesi; nil = karar listesi okunamadı → her şey
// sayılır (açık kapı). Boş özneli satır atlanır. truncated: OKUMA tavana
// dayandı (len >= limit, süzmeden önceki sayı) — küme eksik olabilir.
func summarizeDBProblems(probs []chstore.Problem, noise map[string]struct{}, limit int) (map[string]dbProblemForms, bool) {
	out := make(map[string]dbProblemForms)
	for _, p := range probs {
		if p.Service == "" {
			continue
		}
		if _, isNoise := noise[chstore.RuleProblemVerdictSignature(p.RuleID, p.Service)]; isNoise {
			continue
		}
		f := out[p.Service]
		slot := &f.DBName
		if chstore.DBProblemSubjectForm(p.RuleID) == chstore.DBSubjectFormInstance {
			slot = &f.Instance
		}
		if *slot == nil {
			*slot = &dbProblemCount{}
		}
		c := *slot
		c.Open++
		if sev := dbSeverity(p.Severity); c.TopSeverity == "" || dbSeverityRank(sev) > dbSeverityRank(c.TopSeverity) {
			c.TopSeverity = sev
		}
		out[p.Service] = f
	}
	return out, limit > 0 && len(probs) >= limit
}

// dbProblemsVerdictErrLogged — karar okuması düşerken log seli olmasın: hata
// serisinin İLKİ loglanır, ilk başarılı okuma bayrağı sıfırlar.
var dbProblemsVerdictErrLogged atomic.Bool

// dbProblemNoiseSet — "problem değil" imzaları; okuma hatasında nil (açık kapı).
func (s *Server) dbProblemNoiseSet(ctx context.Context) map[string]struct{} {
	list, err := s.store.ListProblemVerdicts(ctx)
	if err != nil {
		if dbProblemsVerdictErrLogged.CompareAndSwap(false, true) {
			log.Printf("[databases] problem kararları okunamadı, işaretler süzülmeden sayılıyor: %v", err)
		}
		return nil
	}
	dbProblemsVerdictErrLogged.Store(false)
	return chstore.NoiseVerdictSignatures(list)
}

func (s *Server) getDatabaseProblems(w http.ResponseWriter, r *http.Request) {
	s.serveCached(w, r, dbProblemsCacheKey, 15*time.Second, func(ctx context.Context) (any, error) {
		probs, err := s.store.ListProblems(ctx, chstore.ProblemFilter{
			SubjectKind: chstore.ProblemKindDB,
			NotStatuses: pickExcludedStatuses("open"),
			Limit:       dbProblemsScanLimit,
		})
		if err != nil {
			return nil, err
		}
		subjects, truncated := summarizeDBProblems(probs, s.dbProblemNoiseSet(ctx), dbProblemsScanLimit)
		return dbProblemsResponse{Subjects: subjects, Truncated: truncated}, nil
	})
}
