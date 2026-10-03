package api

// logs_pattern_param.go — v0.10.1071 (operatör, prod ES: anomali detayının
// "Logları aç" pivotu, "Desen sayısı" grafiğinin saydığından başka satırlar
// gösterdi — grafik ~24 bin, /logs ~80 bin, satırlar ilgisiz bir iş yükünün
// DEBUG/INFO'su).
//
// Kök: pivot deseni /logs'un ARAMA DİLİNE çeviriyordu (token'ların tırnaklı
// OR'u, `q=`). O metin dedektörün yüklemi değildir: CH'de token'ların harf-
// duyarsız alt-dize eşleşmesi (dedektör ayrıca büyük-küçük duyarlı regex
// ister), ES'te expandShorthand + default_operator AND + must bağlamı; ve
// operatör kutuyu düzenleyince bağ tamamen kopar. Çeviri yerine yüklemin
// KENDİSİ: `/logs?pattern=<küratörlü ad>` → mevcut okumaların (`/api/logs`,
// `/api/logs/search`, `/api/logs/timeseries`, `/api/logs/fieldstats`,
// `/api/logs/stream`) `pattern` parametresi → anomaly.LogPatternSpecByName →
// logstore.Filter.Pattern. Arka uç dedektörün yan tümcesini uygular (ES:
// patternFilterClause, CH: chPatternConjunct) — serbest metinle AND.
//
// Yeni rota YOK (yalnız var olan okumalara parametre); api.go büyümez.

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/logstore"
)

// logsPatternFromQuery — SAF: `pattern` değeri → dedektörün spec'i.
// Boş → (nil, true): desen süzgeci yok. Bilinmeyen ad → (nil, false): çağıran
// reddeder — sessizce desensiz liste dönmek "desene uyan satırlar bunlar"
// yalanı olurdu (eski satır / yeniden adlandırılmış desen).
func logsPatternFromQuery(raw string) (*logstore.PatternSpec, bool) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return nil, true
	}
	spec, ok := anomaly.LogPatternSpecByName(name)
	if !ok {
		return nil, false
	}
	return &spec, true
}

// applyLogsPatternParam — `pattern` parametresini Filter'a işler. Bilinmeyen
// ad → 400 JSON (mesaj operatöre) ve false; çağıran hemen döner. Önbellek
// anahtarından ÖNCE çağrılır: anahtar Filter.Pattern'i taşır.
func applyLogsPatternParam(w http.ResponseWriter, q url.Values, f *logstore.Filter) bool {
	spec, ok := logsPatternFromQuery(q.Get("pattern"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "bilinmeyen log deseni")
		return false
	}
	f.Pattern = spec
	return true
}
