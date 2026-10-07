package api

import (
	"fmt"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// appendValidatedDSL — v0.10.1117 (inceleme bulgusu). parseFiltersAndDSL,
// `filters=` JSON'unu parseFilters'ta ValidateFilters'tan geçiriyordu (v0.10.118)
// ama `dsl=` ile gelen yaprakları doğrulamadan ekliyordu. Op kısıtlı anahtarlar
// (http.route_shape / name_shape, op_group, db_stmt_hash) için `dsl=… ~ x`
// derleme anında hata verir; ApplyFilters yan tümceyi LOGLAYIP ATLAR → sorgu
// SÜZGEÇSİZ koşar (v0.9.269 sınıfı: daha geniş, inandırıcı bir sonuç). Artık
// DSL yaprakları da sınırda reddedilir: errBadRequest → çağıranlar 400.
func appendValidatedDSL(out, parsed []chstore.FilterExpr) ([]chstore.FilterExpr, error) {
	if err := chstore.ValidateFilters(parsed); err != nil {
		return nil, fmt.Errorf("%w: dsl: %v", errBadRequest, err)
	}
	return append(out, parsed...), nil
}
