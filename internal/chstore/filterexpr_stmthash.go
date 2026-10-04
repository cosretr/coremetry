package chstore

// filterexpr_stmthash.go — v0.10.1093 — ifade kimliği süzgeci
// (`db_stmt_hash`): /traces'te "bu ifade sınıfının span'ini taşıyan trace'ler".
//
// Operator-reported (prod, Databases › Top statements › statement detail):
// "Bu sayfada traces alanı yok, ilgili statement'ın trace'lerine gidemiyorum."
// Exemplar linkleri TEK trace'e gidiyordu; sınıfın tüm trace'lerini listeleyen
// bir pivot yoktu.
//
// Kimlik, exemplar okumasıyla AYNI: spans.db_stmt_hash (dbStmtExemplarWhere).
// Normalize SQL METNİ süzgece girmez — span'deki db.statement ham metindir,
// tam eşleşme boş döner, LIKE öneki ise başka sınıfları da toplar
// (repeatsExploreHref / statementTracesHref başlıkları). Kimlik bir UInt64:
//   - yalnız =, !=, IN, NOT IN (LIKE / regex / aralık / EXISTS anlamsız → 400);
//   - değerler ondalık uint64 metni (FE'nin taşıdığı biçim, stmtParam.ts) ve
//     Go'da çözülüp UInt64 olarak bağlanır — sütun tipine metin karşılaştırması
//     yaptırılmaz.
//
// Kolon yoksa (dış Distributed, cluster_name boş — store.go D1 probu) kolon
// adı code 47 verirdi; bu durumda AYNI MATERIALIZED ifade (dbStmtHashExpr)
// satır başına hesaplanır. Sonuç birebir aynı (ifade kolonun tanımı), yalnız
// pahalı — o kurulumda /slow-queries de ham yolda. Süzgeç ASLA sessizce
// düşmez (v0.9.269 sınıfı: düşen süzgeç daha GENİŞ, inandırıcı bir sonuç).
//
// Yalnız SPANS yolları (SQL / SQLWithPromoted / SQLAliased) bu anahtarı
// tanır; metric_points'te kolon yok, SQLForMetricPoints eski davranışta
// (dizi araması) kalır.

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
)

// StmtHashFilterKey — süzgeç anahtarı; FE lib/filterQuery.ts STMT_HASH_FILTER_KEY.
const StmtHashFilterKey = "db_stmt_hash"

// stmtHashColReady — spans.db_stmt_hash kolonu bu kurulumda var mı (migrate,
// hasDBStmtHashCol probu). FilterExpr.SQL() saf ve Store'u görmez; bayrak
// attrIndexReady emsali paket düzeyinde.
var stmtHashColReady atomic.Bool

// stmtHashLHS — SAF: süzgecin sol tarafı. Kolon varsa kolon; yoksa kolonun
// MATERIALIZED ifadesi (alias'lı self-join'de db_statement nitelenir).
func stmtHashLHS(alias string, colReady bool) string {
	if colReady {
		return qualCol(alias, "db_stmt_hash")
	}
	if alias == "" {
		return dbStmtHashExpr
	}
	return strings.ReplaceAll(dbStmtHashExpr, "db_statement", alias+".db_statement")
}

// stmtHashValues — değerleri uint64'e çözer; bozuk değer hata.
func stmtHashValues(vs []string) ([]any, error) {
	out := make([]any, 0, len(vs))
	for _, v := range vs {
		n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("key %q needs a decimal statement id, got %q", StmtHashFilterKey, v)
		}
		out = append(out, n)
	}
	return out, nil
}

// validateStmtHash — sınır doğrulaması (FilterExpr.Validate'ten): op ve
// değer biçimi. op zaten büyük harfe çevrilmiş ve allowedOps'tan geçmiş gelir.
func validateStmtHash(op string, vs []string) error {
	switch op {
	case "=", "!=":
		if len(vs) != 1 {
			return fmt.Errorf("op %s on %q needs exactly one value", op, StmtHashFilterKey)
		}
	case "IN", "NOT IN":
		if len(vs) == 0 {
			return fmt.Errorf("op %s on %q needs at least one value", op, StmtHashFilterKey)
		}
	default:
		return fmt.Errorf("key %q supports only =, !=, IN, NOT IN (got %q)", StmtHashFilterKey, op)
	}
	_, err := stmtHashValues(vs)
	return err
}

// stmtHashFilterSQL — SAF: `db_stmt_hash` yan tümcesi.
func stmtHashFilterSQL(alias, op string, vs []string, colReady bool) (string, []any, error) {
	if err := validateStmtHash(op, vs); err != nil {
		return "", nil, err
	}
	args, _ := stmtHashValues(vs)
	lhs := stmtHashLHS(alias, colReady)
	switch op {
	case "IN", "NOT IN":
		return lhs + " " + op + " (" + strings.TrimRight(strings.Repeat("?,", len(args)), ",") + ")", args, nil
	default:
		return lhs + " " + op + " ?", args, nil
	}
}

// spansSQL — spans yollarının ortak girişi: ifade kimliği anahtarı burada
// ayrılır, gerisi genel derleyiciye.
func (f FilterExpr) spansSQL(alias string, promoted map[string]string) (string, []any, error) {
	if f.Key == StmtHashFilterKey {
		return stmtHashFilterSQL(alias, normOp(f.Op), f.Values, stmtHashColReady.Load())
	}
	return f.sql(alias, wellKnown, wellKnownResource, promoted, AttrIndexAvailable())
}

// normOp — boş op "=" (sql() ve Validate ile aynı kural).
func normOp(op string) string {
	o := strings.ToUpper(strings.TrimSpace(op))
	if o == "" {
		return "="
	}
	return o
}
