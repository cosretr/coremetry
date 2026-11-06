package logstore

// sort_test.go — v0.10.1833 (Log Search Faz 1 dilim 1.10): çoklu alan
// sıralama — parametre ayrıştırma, imza, ES eşleme çözümü (text alanı
// keyword alt alanına, doc_values kapalı / text-yalnız → 400), zaman
// eşitlik bozucu, CH beyaz listesi, cursor imzası.

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestParseSortParam(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []SortKey
		bad  bool
	}{
		{"", nil, false},
		{"service", []SortKey{{Field: "service"}}, false},
		{"service:desc,level:asc", []SortKey{{Field: "service", Desc: true}, {Field: "level"}}, false},
		{"http.status_code:desc,@timestamp:asc,time:desc", []SortKey{{"http.status_code", true}, {"@timestamp", false}, {"time", true}}, false},
		{"a,b,c,d", nil, true},
		{"service:up", nil, true},
		{"service,service:desc", nil, true},
		{"bad field", nil, true},
		{`x"y`, nil, true},
	} {
		got, err := ParseSortParam(c.in)
		if (err != nil) != c.bad {
			t.Errorf("%q: err=%v", c.in, err)
			continue
		}
		if c.bad {
			if !errors.Is(err, ErrBadQuery) {
				t.Errorf("%q: 400 sınıfı olmalı (ErrBadQuery): %v", c.in, err)
			}
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: %+v; want %+v", c.in, got, c.want)
		}
	}
	if SortSig([]SortKey{{"a", true}, {"b", false}}) == SortSig([]SortKey{{"b", false}, {"a", true}}) {
		t.Error("imza sırayı taşımalı (birincil ≠ ikincil)")
	}
	if SortSig(nil) != "" {
		t.Error("boş imza")
	}
}

func TestWalkSortableAndResolve(t *testing.T) {
	var props map[string]any
	_ = json.Unmarshal([]byte(`{
		"message": {"type": "text"},
		"service": {"properties": {"name": {"type": "keyword"}}},
		"host": {"type": "text", "fields": {"keyword": {"type": "keyword", "ignore_above": 256}}},
		"log": {"properties": {"level": {"type": "keyword", "doc_values": false}}},
		"http": {"properties": {"status_code": {"type": "long"}}},
		"geo": {"type": "geo_point"}
	}`), &props)
	m := map[string]esSortable{}
	walkSortable("", props, m)
	for _, c := range []struct {
		id, field, unmapped string
		ok                  bool
	}{
		{"service.name", "service.name", "keyword", true},
		{"host", "host.keyword", "keyword", true},
		{"host.keyword", "host.keyword", "keyword", true},
		{"http.status_code", "http.status_code", "long", true},
		{"message", "", "", false},
		{"log.level", "", "", false},
		{"geo", "", "", false},
		{"missing.field", "", "", false},
	} {
		f, u, err := resolveESSort(c.id, []string{c.id}, m)
		if (err == nil) != c.ok || f != c.field || u != c.unmapped {
			t.Errorf("%s: (%q,%q,%v)", c.id, f, u, err)
		}
		if err != nil {
			se, ok := AsSortError(err)
			if !ok || se.Field != c.id || se.Reason == "" || !errors.Is(err, ErrBadQuery) {
				t.Errorf("%s: SortError bekleniyor: %v", c.id, err)
			}
		}
	}
	// Aday zinciri: ilk sıralanabilir aday kazanır.
	if f, _, err := resolveESSort("pod", []string{"message", "service.name"}, m); err != nil || f != "service.name" {
		t.Errorf("aday zinciri: %q %v", f, err)
	}
	// Eşleme bilinmiyor → aynen + keyword.
	if f, u, err := resolveESSort("x", []string{"x"}, nil); err != nil || f != "x" || u != "keyword" {
		t.Errorf("eşlemesiz: %q %q %v", f, u, err)
	}
}

func TestESSortClausesTimeTiebreak(t *testing.T) {
	res := func(id string) (string, string, error) {
		if id == SortFieldTime {
			return "@timestamp", "date", nil
		}
		return id + ".kw", "keyword", nil
	}
	got, err := esSortClauses([]SortKey{{"service", false}, {"level", true}}, res, "@timestamp", "desc")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got)
	want := `[{"service.kw":{"order":"asc","unmapped_type":"keyword"}},{"level.kw":{"order":"desc","unmapped_type":"keyword"}},{"@timestamp":{"order":"desc","unmapped_type":"date"}}]`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
	// Zaman listedeyse ikinci kez eklenmez.
	got, _ = esSortClauses([]SortKey{{"time", false}, {"service", true}}, res, "@timestamp", "desc")
	if len(got) != 2 {
		t.Errorf("zaman tekrar eklenmemeli: %d", len(got))
	}
	// Sıralanamaz alan sorgu kurulmadan döner.
	if _, err := esSortClauses([]SortKey{{"message", false}}, func(id string) (string, string, error) {
		return "", "", unsortable(id, "Metin")
	}, "@timestamp", "desc"); err == nil {
		t.Error("hata bekleniyor")
	}
}

func TestChSortColsWhitelist(t *testing.T) {
	cols, err := chSortCols([]SortKey{{"service", true}, {"level", false}, {"time", false}})
	if err != nil || len(cols) != 3 || cols[0].Col != "service_name" || !cols[0].Desc || cols[1].Col != "severity_num" || cols[2].Col != "time" {
		t.Fatalf("%+v %v", cols, err)
	}
	if _, err := chSortCols([]SortKey{{"http.status_code", false}}); err == nil || !errors.Is(err, ErrBadQuery) {
		t.Errorf("öznitelik CH'de 400 olmalı: %v", err)
	}
}

func TestESCursorCarriesSortSig(t *testing.T) {
	tok := encodeESCursor("pit1", []any{"orders-api", 1.0}, false, "service:asc")
	c, ok := decodeESCursor(tok)
	if !ok || c.Sig != "service:asc" || c.Pit != "pit1" {
		t.Fatalf("%+v %v", c, ok)
	}
	// Eski (imzasız) token zaman sırası imzası "" olarak çözülür.
	old := encodeESCursor("", []any{1.0}, false, "")
	if c, ok := decodeESCursor(old); !ok || c.Sig != "" {
		t.Errorf("eski token: %+v", c)
	}
}
