package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/logql"
	"github.com/cilcenk/coremetry/internal/logstore"
)

// logs_query_error.go — v0.10.1818 (Log Search Faz 1 dilim 1.2; audit §2
// yan bulgu, §3.3).
//
// Log sorgusu sözdizimi reddi 500 DEĞİL, 400'dür — ve konumludur:
//
//	{"error": "<operatöre Türkçe cümle>", "pos": <0 tabanlı karakter>, "detail": "<arka uç gerekçesi>"}
//
// Kaynaklar: (a) ES'in kendi reddi (logstore.BadQueryError — query_shard /
// parse_exception), serveCached'in hata yolunda yakalanır; (b) CH
// backend'inin Go ayrıştırıcı reddi (logql.ParseError, rejectLogQuerySyntax).
// `pos` yalnız güvenilirken yazılır (ES: yankılanan metin kullanıcının
// metniyle aynıysa). Ham ES gövdesi viewer/editor'a ASLA gitmez — yalnız
// gerekçe cümlesi; admin ek olarak `raw` (eski hata dizesi) görür.
// `error` ilk anahtar ve bir dize: hatayı düz okuyan eski istemciler
// (api.ts request → "HTTP 400: {…}") anlamlı bir metin görür.

// logQueryErrorBody — 400 gövdesi.
type logQueryErrorBody struct {
	Error  string `json:"error"`
	Pos    *int   `json:"pos,omitempty"`
	Detail string `json:"detail,omitempty"`
	Raw    string `json:"raw,omitempty"`
	// Stage — v0.10.1821: "precheck" = sorgu arka uca HİÇ gitmedi (Go
	// ayrıştırıcı/Lint reddi); "backend" = arka ucun kendi reddi.
	Stage string `json:"stage,omitempty"`
}

// logQueryErr — ErrBadQuery'yi kullanıcının sorgu metniyle sarar ki konum
// o metne göre hesaplanabilsin. Diğer hatalar aynen geçer.
type logQueryErr struct {
	err    error
	search string
}

func (e *logQueryErr) Error() string { return e.err.Error() }
func (e *logQueryErr) Unwrap() error { return e.err }

func logsQueryErr(err error, search string) error {
	if err == nil || !errors.Is(err, logstore.ErrBadQuery) {
		return err
	}
	return &logQueryErr{err: err, search: search}
}

// esQueryErrorBody — SAF: ES reddi → gövde (tablo testli).
func esQueryErrorBody(err error, admin bool) logQueryErrorBody {
	search := ""
	var lq *logQueryErr
	if errors.As(err, &lq) {
		search = lq.search
	}
	b := logQueryErrorBody{Detail: logstore.QueryErrorReason(err), Stage: "backend"}
	if pos, ok := logstore.QueryErrorPos(err, search); ok {
		b.Pos = &pos
		b.Error = fmt.Sprintf("Sorgu sözdizimi hatalı (%d. karakter). Elasticsearch sorguyu ayrıştıramadı.", pos+1)
	} else {
		b.Error = "Sorgu sözdizimi hatalı. Elasticsearch sorguyu ayrıştıramadı."
	}
	if admin {
		b.Raw = err.Error()
	}
	return b
}

// parseErrorBody — SAF: Go ayrıştırıcı reddi → gövde. ParseError.Pos bayt
// ofsetidir; istemci karakter indeksi bekler.
func parseErrorBody(err error, search string) logQueryErrorBody {
	b := logQueryErrorBody{Error: err.Error(), Stage: "precheck"}
	var pe *logql.ParseError
	if errors.As(err, &pe) {
		p := pe.Pos
		if p > len(search) {
			p = len(search)
		}
		if p < 0 {
			p = 0
		}
		c := utf8.RuneCountInString(search[:p])
		b.Pos = &c
		b.Detail = pe.Msg
	}
	return b
}

func writeLogQueryErrorBody(w http.ResponseWriter, b logQueryErrorBody) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(b)
}

// writeBadQueryErr — serveCached hata yolu: ES sözdizimi reddi ise 400 yazar
// ve true döner; değilse false (çağıran writeErr'e düşer).
func writeBadQueryErr(w http.ResponseWriter, r *http.Request, err error) bool {
	if !errors.Is(err, logstore.ErrBadQuery) {
		return false
	}
	admin := false
	if c := auth.FromContext(r.Context()); c != nil && c.Role == auth.RoleAdmin {
		admin = true
	}
	writeLogQueryErrorBody(w, esQueryErrorBody(err, admin))
	return true
}
