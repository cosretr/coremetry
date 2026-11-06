package api

// logs_business_fields_test.go — v0.10.1853 (Log Search Faz 2 dilim 2.2):
// `logs_search.businessFields` (alan kenar çubuğu "İş alanları"). Varsayılan
// boş; PUT doğrulaması her ihlal türünü 400'e çevirir; okuma yolu bozuk
// tanımı sessizce düşürür; tam blob gidiş-dönüşü maxSample'ı korur; JSON
// her zaman dizi (null değil).

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestLogsBusinessFieldsDefaultEmpty(t *testing.T) {
	c := decodeLogsSearchSettings(nil)
	if c.BusinessFields == nil || len(c.BusinessFields) != 0 {
		t.Fatalf("varsayılan boş dizi olmalı: %#v", c.BusinessFields)
	}
	raw, _ := json.Marshal(newLogsSearchSettingsResponse(c))
	if !strings.Contains(string(raw), `"businessFields":[]`) {
		t.Fatalf("JSON dizi olmalı: %s", raw)
	}
	// Eski blob (yalnız maxSample) aynen okunur.
	if got := decodeLogsSearchSettings([]byte(`{"maxSample":750}`)); got.MaxSample != 750 || len(got.BusinessFields) != 0 {
		t.Fatalf("eski blob: %+v", got)
	}
}

func TestLogsBusinessFieldsValidate(t *testing.T) {
	ok := []logsBusinessField{{Label: " Kanal ", Fields: []string{" CHANNEL_CODE", "channel_code", "CHANNEL_CODE", ""}}}
	got, err := validateLogsSearchSettings(logsSearchSettings{MaxSample: 500, BusinessFields: ok})
	if err != nil {
		t.Fatal(err)
	}
	want := []logsBusinessField{{Label: "Kanal", Fields: []string{"CHANNEL_CODE", "channel_code"}}}
	if fmt.Sprint(got.BusinessFields) != fmt.Sprint(want) || got.MaxSample != 500 {
		t.Fatalf("kırp + tekilleştir: %+v", got)
	}

	many := make([]logsBusinessField, 21)
	for i := range many {
		many[i] = logsBusinessField{Label: fmt.Sprintf("L%d", i), Fields: []string{"f"}}
	}
	nine := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	bad := map[string][]logsBusinessField{
		"boş etiket":       {{Label: " ", Fields: []string{"x"}}},
		"uzun etiket":      {{Label: strings.Repeat("ş", 65), Fields: []string{"x"}}},
		"tekrar etiket":    {{Label: "Kanal", Fields: []string{"a"}}, {Label: "kanal", Fields: []string{"b"}}},
		"aday yok":         {{Label: "A", Fields: []string{" ", ""}}},
		"9 aday":           {{Label: "A", Fields: nine}},
		"boşluklu alan":    {{Label: "A", Fields: []string{"a b"}}},
		"tırnak":           {{Label: "A", Fields: []string{`a"b`}}},
		"joker":            {{Label: "A", Fields: []string{"CHANNEL_*"}}},
		"soru joker":       {{Label: "A", Fields: []string{"a?"}}},
		"ters bölü":        {{Label: "A", Fields: []string{`a\b`}}},
		"uzun alan":        {{Label: "A", Fields: []string{strings.Repeat("x", 257)}}},
		"21 tanım":         many,
		"kontrol karakter": {{Label: "A", Fields: []string{"a\tb"}}},
	}
	for name, in := range bad {
		if _, err := validateLogsSearchSettings(logsSearchSettings{BusinessFields: in}); err == nil {
			t.Errorf("%s: 400 bekleniyordu", name)
		}
	}
	// Sınırda olanlar geçer: 20 tanım, 8 aday, 64 rune, 256 bayt alan.
	edge := many[:20]
	edge[0] = logsBusinessField{Label: strings.Repeat("ş", 64), Fields: append([]string{strings.Repeat("x", 256)}, nine[:7]...)}
	if _, err := validateLogsSearchSettings(logsSearchSettings{BusinessFields: edge}); err != nil {
		t.Fatalf("sınır değerleri geçmeli: %v", err)
	}
}

// Okuma yolu: bozuk tanım düşer, iyi tanımlar ve maxSample kalır.
func TestLogsBusinessFieldsReadDropsInvalid(t *testing.T) {
	c := decodeLogsSearchSettings([]byte(`{"maxSample":900,"businessFields":[{"label":"Kanal","fields":["CHANNEL_CODE"]},{"label":"","fields":["x"]},{"label":"Op","fields":["a b"]},{"label":"İşlem","fields":["FUNCTION_CODE"]}]}`))
	if c.MaxSample != 900 || len(c.BusinessFields) != 2 || c.BusinessFields[1].Label != "İşlem" {
		t.Fatalf("okuma kelepçesi: %+v", c)
	}
}
