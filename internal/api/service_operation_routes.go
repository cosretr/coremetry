package api

// service_operation_routes.go — v0.10.1023. Operatör bildirimi: "Operation
// kısmında POST GET neden detail gözükmüyor, sonra trace'e girince
// çıkıyor." Servis sayfasının Operations sekmesi (Raw kip) çıplak "GET" /
// "POST" satırları gösteriyordu; Traces listesi aynı span'leri v0.10.756'dan
// beri "GET /metrics" diye basıyor. Bu uç Operations tablosuna, çıplak fiil
// satırlarını (fiil, http_route) başına bölmesi için ek satırları verir:
//
//	GET /api/services/{name}/operations/routes?from=&to=&since=&env=
//	→ { "rows": [OperationSummary + route], "covered": bool, "truncated"?: bool }
//
// Neden ayrı uç, bundle'ın operations dilimi DEĞİL: o satırları copilot,
// SpanDetail taban çizgisi, ProblemDetail, Overview OpsCard ve anomali
// incelemesi ham span adıyla birebir eşliyor; bölme yalnız tabloda,
// tarayıcıda yapılır (pages/service/operationRoutes.ts). Okuma
// chstore.GetBareVerbRouteOperations (spanmetrics_1m; pencere + sparkline
// ızgarası ops MV okumasıyla aynı).
//
// Pencere bundle'la AYNI çözülür (getServiceBundle): from yoksa now−since
// (varsayılan 24 sa), to yoksa now — bölünen ham satırlar bundle'dan geliyor.
// Bozuk from/to ya da to ≤ from 400 (bundle sessizce varsayılana düşer; bu
// uç düşmez, çünkü yanlış pencerenin satırları doğru pencerenin satırlarının
// yerine geçerdi).
//
// env doluysa SORGU KOŞMAZ, {rows: [], covered: false} döner: spanmetrics_1m'de
// deploy_env boyutu yok; bölmek env'e daralmış ham satırı tüm env'lerin rota
// satırlarıyla değiştirip ortamları karıştırırdı (GetEndpointsMV'nin env
// reddiyle aynı ilke). Tablo o durumda bugünkü gibi görünür.
//
// Rol kapısı YOK — /api/services/{name}/operations ile aynı duruş (salt-okunur,
// viewer görür). serveCached 30 sn; anahtar TÜM girdileri taşır: servis + env
// FNV ile (alan sınırı NUL), pencere cacheBucket (30 sn) + okuma ızgarası
// (chstore.OperationRoutesGridKey — aynı 30 sn hücresindeki iki `to` farklı
// ızgara üretebilir, v0.5.187 sınıfı).

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() {
	registerRoutesExtra("service-operation-routes", (*Server).registerServiceOperationRouteRoutes)
}

func (s *Server) registerServiceOperationRouteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services/{name}/operations/routes", s.getServiceOperationRoutes)
}

// opRoutesResponse — uç gövdesi. Truncated: satır sayısı tavana
// (chstore.BareVerbRouteLimit) ulaştı, kuyruk kesilmiş olabilir; tarayıcı o
// yanıtla bölme yapmaz (yarım bölme "All" toplamını sessizce eksiltirdi).
type opRoutesResponse struct {
	Rows      []chstore.OperationSummary `json:"rows"`
	Covered   bool                       `json:"covered"`
	Truncated bool                       `json:"truncated,omitempty"`
}

// opRoutesResult — SAF: store cevabından uç gövdesi. Kapsanmayan pencere
// (env kısa devresi dahil) satır TAŞIMAZ ve truncated sayılmaz; nil yerine []
// (FE .map()'liyor); satır tavanına ulaşan yanıt truncated.
func opRoutesResult(rows []chstore.OperationSummary, covered bool) opRoutesResponse {
	if !covered || rows == nil {
		rows = []chstore.OperationSummary{}
	}
	if !covered {
		return opRoutesResponse{Rows: rows}
	}
	return opRoutesResponse{Rows: rows, Covered: true, Truncated: len(rows) >= chstore.BareVerbRouteLimit}
}

// opRoutesKey — SAF: önbellek anahtarı (TÜM girdiler). service + env NUL
// ayırıcılı FNV (fnvStr), pencere 30 sn kovası + okuma ızgarası.
func opRoutesKey(service, env string, from, to time.Time) string {
	return fmt.Sprintf("svc-op-routes:v1:%s:w=%s:%s",
		fnvStr(service, env), cacheBucket(from, to), chstore.OperationRoutesGridKey(from, to))
}

// opRoutesWindow — SAF: bundle'ın pencere çözümü + girdi doğrulaması.
func opRoutesWindow(q url.Values, now time.Time) (time.Time, time.Time, error) {
	rawFrom, rawTo := strings.TrimSpace(q.Get("from")), strings.TrimSpace(q.Get("to"))
	for _, p := range []struct{ name, v string }{{"from", rawFrom}, {"to", rawTo}} {
		if p.v == "" {
			continue
		}
		if ns, err := strconv.ParseInt(p.v, 10, 64); err != nil || ns <= 0 {
			return time.Time{}, time.Time{}, fmt.Errorf("%s unix-ns tamsayı olmalı: %q", p.name, p.v)
		}
	}
	since := parseDuration(q.Get("since"), 24*time.Hour)
	from, to := parseTime(rawFrom), parseTime(rawTo)
	if from.IsZero() {
		from = now.Add(-since)
	}
	if to.IsZero() {
		to = now
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("to from'dan sonra olmalı")
	}
	return from, to, nil
}

func (s *Server) getServiceOperationRoutes(w http.ResponseWriter, r *http.Request) {
	svc := strings.TrimSpace(r.PathValue("name"))
	if svc == "" {
		writeJSONError(w, http.StatusBadRequest, "service name required")
		return
	}
	q := r.URL.Query()
	from, to, err := opRoutesWindow(q, time.Now())
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	env := strings.TrimSpace(q.Get("env"))
	if env != "" {
		// spanmetrics_1m env taşımaz — sorgusuz kısa devre (başlık yorumu).
		writeJSON(w, opRoutesResult(nil, false))
		return
	}
	key := opRoutesKey(svc, env, from, to)
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		// Kapalı gelen ctx (SWR arka plan tazelemesi, v0.8.319).
		rows, covered, err := s.store.GetBareVerbRouteOperations(ctx, svc, from, to)
		if err != nil {
			return nil, err
		}
		return opRoutesResult(rows, covered), nil
	})
}
