package chstore

// filterexpr_routeshape.go — v0.10.1117 — /endpoints "Group by shape" pivot
// süzgeçleri (`http.route_shape`, `name_shape`).
//
// v0.10.1115'in bıraktığı kardeş hata (filterexpr_opgroup.go emsali):
// /endpoints "Group by shape" açıkken satırlar ŞEKİLDİR — okuma anında
// opSigWrap(http_route) (RPC & Messaging sekmesinde opSigWrap(name)) ile
// kimlikler `:id`e katlanır (`/users/8421` → `/users/:id`). Satırın ve endpoint
// detay sayfasının Traces / Explore pivotu ise `http.route = <şekil>` çipi
// taşıyordu: kesin kolon eşitliği; span'ler ham id taşıdığında (`/users/8421`)
// hiçbir satır şekle eşit değil → BOŞ liste, boş şerit.
//
// Çözüm: şekil bir süzgeç ANAHTARI ve endpoints sayfasının gruplamada
// kullandığı İFADENİN KENDİSİNE derlenir — yeni bir normalleştirici değil,
// aynı opSigWrap + opSigArgs (endpointRoutePred, endpoint detay çekmecesinin
// v0.8.360'tan beri kullandığı yüklem). Satır hangi ifadeyle gruplandıysa pivot
// aynı ifadeyle eşler; ingest tarafı op_group'un (NormalizeOperation) daha
// geniş kimlik katlaması burada KULLANILMAZ (base64url / ünsüz-yalnız
// kimlikleri opSig katlamaz → op_group ile eşleşme kayardı).
//   - http.route_shape → opSigWrap(http_route)  (HTTP sekmesi, detay sayfası)
//   - name_shape       → opSigWrap(name)        (RPC & Messaging sekmesi)
//   - yalnız =, !=, IN, NOT IN (şekil bir KİMLİK; LIKE / regex / aralık /
//     EXISTS anlamsız → sınırda 400, op_group / db_stmt_hash emsali);
//   - değerler `?` ile bağlanır, metne gömülmez.
//
// v0.8.356 clickhouse-go PARAMETRE TUZAĞI: sürücü sorgu metni `{.+:.+}`
// desenine uyarsa ifadeyi SUNUCU TARAFI parametre kipine alır ve her
// konumsal argüman "unsupported query parameter type" ile düşer. opSigWrap'in
// metninde `':id'` / `'/:id'` sabitleri DURUR (iki nokta üst üste); bu yüzden
// süslü parantez taşıyan regex desenleri ASLA metne gömülmez — opSigArgs ile
// bağlanır (endpoints.go opSigWrap başlığı). Bu dosya o kalıbı olduğu gibi
// yeniden kullanır: yeni metin üretmez, desenler + değer bağlı argümandır.
// Kullanıcının yazdığı değer (`/users/{id}` gibi süslü bir rota dahil) de
// bağlıdır — sürücünün deseni ham sorgu metnine bakar, bağlanmış değere değil.
// Sürücünün GERÇEK bağlama yolu (bindQueryOrAppendParameters) üzerinden
// round-trip testi: ch_bind_roundtrip_test.go.
//
// Kolon fallback'i YOK (op_group'tan farkı, bilinçli): http_route ve name
// spans'in CREATE TABLE kolonlarıdır (store.go; eski kurulumlar ALTER ile) ve
// `http.route` / `name` anahtarları bugün de koşulsuz bu kolonlara derlenir.
// Probe edilecek bir eksik-kolon hâli yok.
//
// Okuma yolları — hepsi spansSQL'den geçer (op_group ile aynı):
//   - /traces liste ham WHERE'i (buildGetTracesWhere) ve arama + çip
//     birlikteyken trace düzeyi HAVING (traceLevelFilterHaving);
//   - Errors şeridi / iki basamaklı hata (traceErrBothWhere, v0.10.1082);
//   - trace_summary_5m hızlı yolu: herhangi bir çip MV'yi diskalifiye eder
//     (tracesMVEligible) → ham yol, span düzeyi çip;
//   - hacim şeridi (QuerySpanMetricMulti): op-MV kapısı (operationMVGate), dar
//     rollup (narrowFilterCol) ve spanmetrics kademeleri (tierDimColumn) şekil
//     anahtarını boyut olarak tanımaz → ham spans (zaman sınırı + LIMIT +
//     max_execution_time). Explore'un metrik sonucu da aynı ham yol.
// metric_points yolu (SQLForMetricPoints) değişmedi: orada kolon yok.

import (
	"fmt"
	"strings"
)

const (
	// RouteShapeFilterKey — HTTP rotası şekli; FE lib/filterQuery.ts
	// ROUTE_SHAPE_FILTER_KEY.
	RouteShapeFilterKey = "http.route_shape"
	// NameShapeFilterKey — span adı şekli (RPC & Messaging sekmesi); FE
	// lib/filterQuery.ts NAME_SHAPE_FILTER_KEY.
	NameShapeFilterKey = "name_shape"
)

// shapeFilterCols — şekil anahtarı → endpoints sayfasının opSigWrap ile
// sardığı HAM kolon (GetEndpointsMV dimCol, endpointRoutePred).
var shapeFilterCols = map[string]string{
	RouteShapeFilterKey: "http_route",
	NameShapeFilterKey:  "name",
}

// isShapeFilterKey — anahtar bir şekil süzgeci mi.
func isShapeFilterKey(key string) bool {
	_, ok := shapeFilterCols[key]
	return ok
}

// validateShapeFilter — SAF sınır doğrulaması: op ve değer sayısı. op zaten
// büyük harfe çevrilmiş ve allowedOps'tan geçmiş gelir.
func validateShapeFilter(key, op string, vs []string) error {
	switch op {
	case "=", "!=":
		if len(vs) != 1 {
			return fmt.Errorf("op %s on %q needs exactly one value", op, key)
		}
	case "IN", "NOT IN":
		if len(vs) == 0 {
			return fmt.Errorf("op %s on %q needs at least one value", op, key)
		}
	default:
		return fmt.Errorf("key %q supports only =, !=, IN, NOT IN (got %q)", key, op)
	}
	return nil
}

// shapeFilterSQL — SAF: şekil yan tümcesi (alias'lı self-join dahil). Sol taraf
// opSigWrap(<kolon>); argümanlar metindeki yer tutucu sırasıyla: önce
// opSigArgs (UUID, hex, sayı — opSigWrap sözleşmesi), sonra değer(ler).
func shapeFilterSQL(key, alias, op string, vs []string) (string, []any, error) {
	col, ok := shapeFilterCols[key]
	if !ok {
		return "", nil, fmt.Errorf("unknown shape filter key %q", key)
	}
	if err := validateShapeFilter(key, op, vs); err != nil {
		return "", nil, err
	}
	lhs := opSigWrap(qualCol(alias, col))
	args := append(make([]any, 0, len(opSigArgs())+len(vs)), opSigArgs()...)
	for _, v := range vs {
		args = append(args, v)
	}
	if op == "IN" || op == "NOT IN" {
		return lhs + " " + op + " (" + strings.TrimRight(strings.Repeat("?,", len(vs)), ",") + ")", args, nil
	}
	return lhs + " " + op + " ?", args, nil
}
