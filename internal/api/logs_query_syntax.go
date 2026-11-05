package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logql"
	"github.com/cilcenk/coremetry/internal/logstore"
)

// logs_query_syntax.go — v0.10.280 (log-search audit Dilim 1b).
//
// v0.10.279 arama metnini ClickHouse'ta logql AST'sine derliyor ve
// ayrışmayan metinde eski alt-dize yoluna düşüyor. Düşüş derin katmanda
// sessizdir (yalnız log satırı); operatörün gördüğü tek şey "beklediğim
// sonuç değil" olurdu. Bu dosya hatayı yüzeye çıkarır: 400 + konumlu
// mesaj, /api/logs ve /api/logs/timeseries ikisinde de.
//
// v0.10.1819 (Log Search Faz 1 dilim 1.3) — ES de kapıda: ama YALNIZ yapısal
// kırıklar (kapanmamış tırnak/parantez, asılı operatör, boş parantez —
// Lucene'in de kesin reddettiği sınıflar; logql.Lint). ES'in kabul ettiği
// Lucene yazımları (fuzzy `~`, boost `^`, regex, alan grubu içinde alan)
// 400'lenmez, "Lucene" kipinde aynen gider. `lang=lucene` kapıyı tamamen
// atlar (operatör ham Lucene istedi; son söz ES'te, reddi v0.10.1818 400'ü).

// logQuerySyntaxReject — CH kararının saf hâli (tablo-testli); /api/search
// (search_query.go planLogSearch) da kullanır — orada ES kapısı YOK.
func logQuerySyntaxReject(backend, search string) (string, bool) {
	if backend != "clickhouse" || strings.TrimSpace(search) == "" {
		return "", false
	}
	if err := chstore.LogQuerySyntaxError(search); err != nil {
		return err.Error(), true
	}
	return "", false
}

// logsQuerySyntaxCheck — /api/logs* kapısının saf kararı: hangi arka uçta,
// hangi dilde hangi metin reddedilir. nil = geçer.
func logsQuerySyntaxCheck(backend, search, lang string) *logql.ParseError {
	if strings.TrimSpace(search) == "" {
		return nil
	}
	switch backend {
	case "clickhouse":
		return logql.Check(search)
	case "elasticsearch":
		_, pe := logql.ToES(search, lang == string(logql.LangLucene))
		return pe
	}
	return nil
}

// logsLangParam — URL `lang`: yalnız "lucene" anlamlı (zorla geçiş); diğer
// her değer "" = otomatik. Önbellek anahtarı kardinalitesi 2.
func logsLangParam(q url.Values) string {
	if strings.EqualFold(strings.TrimSpace(q.Get("lang")), string(logql.LangLucene)) {
		return string(logql.LangLucene)
	}
	return ""
}

// logsLangKey — önbellek anahtarının dil parçası; otomatikte "" (eski
// anahtarlar değişmez).
func logsLangKey(f logstore.Filter) string {
	if f.SearchLang == "" {
		return ""
	}
	return ":lang=" + f.SearchLang
}

// logsQueryLang — yanıttaki `queryLang`: aramanın ES'te HANGİ dilde koştuğu
// (arayüz rozeti). CH her zaman kql (Lucene çalıştıramaz). Boş arama "".
func logsQueryLang(backend string, f logstore.Filter) string {
	if strings.TrimSpace(f.Search) == "" {
		return ""
	}
	if backend != "elasticsearch" {
		return string(logql.LangKQL)
	}
	tr, pe := logql.ToES(f.Search, f.SearchLang == string(logql.LangLucene))
	if pe != nil {
		return string(logql.LangLucene)
	}
	return string(tr.Lang)
}

func (s *Server) rejectLogQuerySyntax(w http.ResponseWriter, search, lang string) bool {
	if s.logs == nil {
		return false
	}
	pe := logsQuerySyntaxCheck(s.logs.Backend(), search, lang)
	if pe == nil {
		return false
	}
	// v0.10.1818 gövdesi {error, pos, detail}: error = düz Türkçe cümle
	// (logql.Lint) ya da ayrıştırıcının mesajı; detail = konumlu teknik metin.
	b := parseErrorBody(pe, search)
	b.Error = pe.Msg
	b.Detail = pe.Error()
	writeLogQueryErrorBody(w, b)
	return true
}
