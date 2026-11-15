package mcptools

// operation_search.go — v0.10.1297 (operatör 2026-10-17: ACM operasyon adı
// linki yanlış — coremetry.io + search=). Kullanıcı CoSRE'ye bir Oracle/ACM
// OPERASYON ADI yazdı ("EXAMPLE_PAYMENTSYSTEMCAMPAIGN_WALLET_LIST_OF_GAINED_
// CAMPAIGNS"); ön-getirme adı doğru çözdü (kod GXWQD01) ama model build_link'i
// search=<ad> ile çağırdı ve "Coremetry'de aç" /traces?search=<ad> açtı. Ad
// span'lerde YOK (köprü fonksiyon kodu, oracle_operation.go), yani serbest
// metin araması sıfır trace bulur — operatör: "fonksiyon kodu ile filtreleyerek
// araması gerekir".
//
// Kök neden: build_link ve search_traces `search` alanını olduğu gibi Traces'in
// serbest metin süzgecine yazıyordu; operasyon adı → fonksiyon kodu çevirisi
// yalnız guided yolda (trace_nl_search.go) ve ön-getirme kartında vardı.
// Çözüm araçların KENDİSİNDE, deterministik: `search` Oracle operasyon adı
// ŞEKLİNDEYSE ve resolve_oracle_operation'ın çözümleyicisi TAM eşleşmeyle kod
// veriyorsa, search düşer ve yerine FUNCTION_CODE süzgeci (span anahtarının
// kayıtlı yazımıyla) girer. Eşleşme yoksa / Oracle kaynağı yoksa hiçbir şey
// değişmez (bayt bayt eski davranış) — yakın adlarla KENDİLİĞİNDEN arama yok.

import (
	"context"
	"regexp"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// oracleOpShapeRe — Oracle operasyon adı ŞEKLİ (büyük harf, rakam, alt çizgi;
// ≥6). Yalnız şekil kapısı: çözümleyiciye gitmeye değer mi.
var oracleOpShapeRe = regexp.MustCompile(`^[A-Z0-9_]{6,}$`)

// LooksLikeOracleOperation — SAF: en az bir harf ve bir alt çizgi şart (salt
// rakam bir kimlik ya da süre, "SAMP01" gibi tek kelime bir kod; operasyon adı
// değil). api/trace_nl_search.go guided kapısı da bunu kullanır (tek kural).
func LooksLikeOracleOperation(s string) bool {
	s = strings.TrimSpace(s)
	return oracleOpShapeRe.MatchString(s) && strings.Contains(s, "_") && strings.ContainsAny(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
}

// OracleCodeFilter — SAF: fonksiyon kod(lar)ının tek Traces süzgeci; tek kod
// "=", birden çok kod "IN" (FE functionCodeTracesHref ile aynı şekil).
func OracleCodeFilter(key string, codes []string) chstore.FilterExpr {
	if len(codes) == 1 {
		return chstore.FilterExpr{Key: key, Op: "=", Values: []string{codes[0]}}
	}
	return chstore.FilterExpr{Key: key, Op: "IN", Values: append([]string(nil), codes...)}
}

// OperationSearchTranslation — `search` alanındaki operasyon adının çevirisi
// (araç sonucunda `operation_translated` olarak döner; model çeviriyi söyler).
type OperationSearchTranslation struct {
	Operation string   `json:"operation"`
	SpanKey   string   `json:"span_key"`
	Codes     []string `json:"function_codes"`
	Note      string   `json:"note"`
}

// Filter — çevirinin Traces süzgeci.
func (t OperationSearchTranslation) Filter() chstore.FilterExpr {
	return OracleCodeFilter(t.SpanKey, t.Codes)
}

const operationTranslatedNote = "Operasyon adı span'lerde yok; arama serbest metin yerine fonksiyon kodu süzgeciyle yapıldı — bunu cevapta söyle."

// translateOperationSearch — `search` Oracle operasyon adı şeklindeyse ve
// çözümleyici TAM eşleşmeyle kod veriyorsa çeviri; aksi hâlde ok=false
// (çağıran search'ü aynen kullanır). Çözümleyici hatası da ok=false: link /
// arama eski hâline düşer, araç hata vermez.
func translateOperationSearch(ctx context.Context, src OracleOpSource, search string) (OperationSearchTranslation, bool) {
	name := strings.TrimSpace(search)
	if src == nil || !LooksLikeOracleOperation(name) || len(name) > oracleOpNameMax || !src.Enabled() {
		return OperationSearchTranslation{}, false
	}
	res, err := src.ResolveOracleOperation(ctx, name)
	if err != nil || strings.TrimSpace(res.SpanKey) == "" {
		return OperationSearchTranslation{}, false
	}
	var codes []string
	seen := map[string]bool{}
	for _, m := range res.Matches {
		c := strings.TrimSpace(m.Code)
		if c == "" || seen[c] || len(codes) >= OracleOpMaxCodes {
			continue
		}
		seen[c] = true
		codes = append(codes, c)
	}
	if len(codes) == 0 {
		return OperationSearchTranslation{}, false
	}
	return OperationSearchTranslation{Operation: name, SpanKey: res.SpanKey, Codes: codes, Note: operationTranslatedNote}, true
}

// isBusinessCodeScopeFilter — SAF: fonksiyon/kanal kodu eşitlik süzgeci terfi
// etmiş tek kolona (attr_function_code / attr_channel_code) iner — guided yol
// ve ön-getirme bunu kapsamsız koşar. search_traces kapısı bu süzgeci "kapsam
// çipi" sayar: resolve_oracle_operation'ın "servis gerekmez" sözleşmesi
// (search_traces'i kod başına filters ile çağır) kapıda reddediliyordu ve
// model serbest metin aramasına düşüyordu (v0.10.1297).
func isBusinessCodeScopeFilter(f chstore.FilterExpr) bool {
	if f.Op != "=" && f.Op != "IN" {
		return false
	}
	norm := strings.ToUpper(strings.NewReplacer("_", "", ".", "").Replace(strings.TrimSpace(f.Key)))
	if norm != "FUNCTIONCODE" && norm != "CHANNELCODE" {
		return false
	}
	_, _, ok := chstore.AttrKeyColumn(strings.TrimSpace(f.Key))
	return ok
}
