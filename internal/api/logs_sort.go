package api

// logs_sort.go — v0.10.1833 (Log Search Faz 1 dilim 1.10): /api/logs
// `sort=field:dir,…` (≤ logstore.MaxSortKeys). Bozuk parametre ve
// sıralanamaz alan 400 {error, detail, field, stage} — 500 değil; FE aynı
// satır-içi hata kutusunu (logQueryError) gösterir ve "sıralamayı kaldır"
// eylemini `field`e bağlar. Önbellek anahtarı sıralama imzasını taşır
// (sıra anlamlı: birincil ≠ ikincil; liste özeti, küme değil).

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/cilcenk/coremetry/internal/logstore"
)

// applyLogsSortParam — `sort` → f.Sort; hata yazıldıysa false.
func applyLogsSortParam(w http.ResponseWriter, q url.Values, f *logstore.Filter) bool {
	keys, err := logstore.ParseSortParam(q.Get("sort"))
	if err != nil {
		if se, ok := logstore.AsSortError(err); ok {
			writeLogQueryErrorBody(w, sortErrorBody(se))
			return false
		}
		writeLogQueryErrorBody(w, logQueryErrorBody{Error: err.Error(), Stage: "precheck"})
		return false
	}
	f.Sort = keys
	return true
}

// sortErrorBody — SAF: SortError → 400 gövdesi.
func sortErrorBody(se *logstore.SortError) logQueryErrorBody {
	return logQueryErrorBody{Error: se.Msg, Detail: se.Reason, Field: se.Field, Stage: "precheck"}
}

// logsSortKey — önbellek anahtarı parçası; sıralama yoksa "" (eski anahtar).
func logsSortKey(f logstore.Filter) string {
	sig := logstore.SortSig(f.Sort)
	if sig == "" {
		return ""
	}
	return fmt.Sprintf(":sort=%s", fnvStr(sig))
}
