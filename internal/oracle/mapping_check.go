package oracle

import (
	"context"
	"strings"
	"time"
)

// mapping_check.go — v0.10.886 (operatör-bildirimli, prod 2026-09-23):
// "eşleşen operasyon kodu / hata kodu / servis yok, trace bulunamadı diyor
// ama aslında var — kolon adlarından mı?" Evet, olabilir: varsayılan eşleme
// ERR_TRACEID / ERR_CODE / ERR_SERVICE, kurum tablosu MCA_ERR_*. Eşlenmeyen
// kolon sessizce boş okunur; özet "0 trace id" der, arayüz "trace bulunamadı"
// yazar — cümle yanlış olmasa da sebebi gizler. Bu kontrol sebebi SÖYLER:
// hangi alanın kolonu tabloda yok ve (zaman/tip kutusundan türeyen önekle)
// tabloda hangi karşılığı var.

// MappingMiss — tabloda bulunmayan eşlenmiş kolon.
type MappingMiss struct {
	Field   string `json:"field"`
	Column  string `json:"column"`
	Suggest string `json:"suggest,omitempty"` // önekli karşılık tabloda varsa
}

// MappingCheck — sözlükten (ALL_TAB_COLUMNS) eşleme doğrulaması.
type MappingCheck struct {
	Checked bool          `json:"checked"`
	Error   string        `json:"error,omitempty"`
	Present int           `json:"present"`
	Missing []MappingMiss `json:"missing"`
	// Prefix — önerilerin türetildiği önek (zaman/tip kolonu varsayılanın
	// önekli hâliyse, ör. MCA_ERR_TIMESTAMP → "MCA_"). Boş = öneri yok.
	Prefix string `json:"prefix,omitempty"`
}

func tableColumnsSQL() string {
	return `SELECT COLUMN_NAME FROM ALL_TAB_COLUMNS WHERE OWNER = :1 AND TABLE_NAME = :2 ORDER BY COLUMN_ID`
}

// mappingPrefix — SAF: operatörün kendi kutularına yazdığı zaman/tip
// kolonu, varsayılanın (ERR_TIMESTAMP / ERR_TYPE) önekli hâliyse öneği verir.
func mappingPrefix(cols map[string]string) string {
	for _, pair := range [][2]string{{cols[FieldTimestamp], DefaultTimestampColumn}, {cols[FieldType], DefaultTypeColumn}} {
		have, def := strings.ToUpper(pair[0]), strings.ToUpper(pair[1])
		if have != def && strings.HasSuffix(have, def) {
			return strings.TrimSuffix(have, def)
		}
	}
	return ""
}

// mappingCheckFromRows — SAF. rows: ALL_TAB_COLUMNS satırları (COLUMN_NAME).
// Kapalı alan ("") atlanır; zaman/tip kolonları da sayılır ama öneri almaz
// (kendi kutuları var, önek zaten onlardan türer).
func mappingCheckFromRows(cfg SourceConfig, rows []map[string]any) MappingCheck {
	c := MappingCheck{Checked: true, Missing: []MappingMiss{}}
	cols, err := ResolveColumns(cfg)
	if err != nil {
		c.Error = err.Error()
		return c
	}
	table := map[string]bool{}
	for _, r := range rows {
		if name := strings.ToUpper(strings.TrimSpace(cellString(firstCell(r)))); name != "" {
			table[name] = true
		}
	}
	if len(table) == 0 {
		c.Checked = false
		c.Error = "sözlükte kolon yok (tablo adı / yetki?)"
		return c
	}
	prefix := mappingPrefix(cols)
	for _, f := range fieldOrder {
		col := strings.ToUpper(cols[f])
		if col == "" {
			continue
		}
		if table[col] {
			c.Present++
			continue
		}
		m := MappingMiss{Field: f, Column: col}
		if prefix != "" && f != FieldTimestamp && f != FieldType && table[prefix+col] {
			m.Suggest = prefix + col
			c.Prefix = prefix
		}
		c.Missing = append(c.Missing, m)
	}
	return c
}

func runMappingCheck(ctx context.Context, db sqlDB, cfg SourceConfig, budget time.Duration, secret string) *MappingCheck {
	owner, table := strings.ToUpper(cfg.Schema), strings.ToUpper(cfg.Table)
	_, rows, err := runRows(ctx, db, tableColumnsSQL(), []any{owner, table}, budget, secret)
	if err != nil {
		return &MappingCheck{Error: "sözlük (ALL_TAB_COLUMNS): " + err.Error(), Missing: []MappingMiss{}}
	}
	c := mappingCheckFromRows(cfg, rows)
	return &c
}
