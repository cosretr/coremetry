package oracle

import (
	"strings"
	"testing"
)

// v0.10.886 — eşlenen kolon tabloda yoksa söylenir; zaman/tip kutusundaki
// önek (APP_ERR_TIMESTAMP → APP_) tablodaki karşılığı önerir; kapalı alan
// sayılmaz; sözlük boşsa hüküm yok.
func TestMappingCheckFromRows(t *testing.T) {
	cfg := cfgFor(t)
	cfg.TimestampColumn, cfg.TypeColumn = "APP_ERR_TIMESTAMP", "APP_ERR_TYPE"
	cfg.Columns = map[string]string{FieldLocation: ""} // kapalı
	rows := []map[string]any{}
	for _, n := range []string{"APP_ERR_TIMESTAMP", "APP_ERR_TYPE", "APP_ERR_TRACEID", "APP_ERR_CODE", "APP_ERR_SERVICE", "ERR_MESSAGE", "APP_ERR_ERRORDUMP"} {
		rows = append(rows, map[string]any{"COLUMN_NAME": n})
	}
	c := mappingCheckFromRows(cfg, rows)
	if !c.Checked || c.Error != "" || c.Prefix != "APP_" {
		t.Fatalf("hüküm: %+v", c)
	}
	if c.Present != 3 { // timestamp, type, message
		t.Errorf("present=%d", c.Present)
	}
	got := map[string]string{}
	for _, m := range c.Missing {
		got[m.Field] = m.Suggest
		if m.Field == FieldLocation {
			t.Error("kapalı alan eksik sayılmamalı")
		}
	}
	if got[FieldTraceID] != "APP_ERR_TRACEID" || got[FieldCode] != "APP_ERR_CODE" || got[FieldService] != "APP_ERR_SERVICE" {
		t.Errorf("öneriler: %v", got)
	}
	if s, ok := got[FieldHost]; !ok || s != "" {
		t.Errorf("host eksik ama karşılığı yok → öneri boş: %q/%v", s, ok)
	}
	// Önek yok (varsayılan zaman kolonu) → öneri yok, eksikler yine listelenir.
	plain := cfgFor(t)
	pc := mappingCheckFromRows(plain, rows)
	if pc.Prefix != "" || len(pc.Missing) == 0 {
		t.Errorf("öneksiz: %+v", pc)
	}
	for _, m := range pc.Missing {
		if m.Suggest != "" {
			t.Errorf("öneksiz öneri: %+v", m)
		}
	}
	if e := mappingCheckFromRows(cfg, nil); e.Checked || e.Error == "" {
		t.Errorf("boş sözlük hüküm vermemeli: %+v", e)
	}
	if !strings.Contains(tableColumnsSQL(), "ALL_TAB_COLUMNS") {
		t.Error("sözlük sorgusu")
	}
}
