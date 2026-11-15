package mcp

// arg_coerce.go — v0.10.2201 (operator-reported, /ai araç tablosu:
// get_topology %29 başarı): yerel modeller tam sayı argümanını sık sık
// metin olarak yazar (`"range_s":"3600"`). Şema doğrulaması (enforce) bunu
// bad_args diye reddediyor, kip kapalıyken handler'ın json.Unmarshal'ı
// "decode args" hatası veriyordu. Şema integer/number diyen ÜST DÜZEY
// alanda sayıya ayrıştırılabilen metin, doğrulamadan ÖNCE sayıya çevrilir.
// Diğer her şey bayt bayt aynen kalır.

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// CoerceNumericArgs — SAF. Değişiklik yoksa (raw, false) — çağıran baytları
// aynen kullanır. Yalnız şemada tipi integer|number olan ve değeri metin
// olan alanlar; integer'da yalnız tam sayı metni ("12", "12.0" değil).
func CoerceNumericArgs(schema map[string]any, raw json.RawMessage) (json.RawMessage, bool) {
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 || len(bytes.TrimSpace(raw)) == 0 {
		return raw, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil || obj == nil {
		return raw, false
	}
	changed := false
	for k, v := range obj {
		str, ok := v.(string)
		if !ok {
			continue
		}
		ps, _ := props[k].(map[string]any)
		if n, ok := numericFromString(schemaTypes(ps["type"]), str); ok {
			obj[k] = n
			changed = true
		}
	}
	if !changed {
		return raw, false
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return raw, false
	}
	return out, true
}

// numericFromString — SAF: şema tipi string KABUL ediyorsa dokunulmaz.
func numericFromString(types []string, s string) (json.Number, bool) {
	wantInt, wantNum := false, false
	for _, t := range types {
		switch t {
		case "string":
			return "", false
		case "integer":
			wantInt = true
		case "number":
			wantNum = true
		}
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	if wantInt {
		if _, err := strconv.ParseInt(s, 10, 64); err == nil {
			return json.Number(s), true
		}
	}
	if wantNum {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return json.Number(strconv.FormatFloat(f, 'f', -1, 64)), true
		}
	}
	return "", false
}
