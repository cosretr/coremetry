package chstore

import (
	"strings"
	"testing"
)

// attr_index_test.go — v0.10.299: DDL şekli, idempotency, ifade/ayırıcı pinleri.

func TestAttrIndexV2DDLShapeAndOrder(t *testing.T) {
	c, legacy := attrIndexV2Cols[0], attrIndexCols[0]
	stmts := attrIndexV2DDL(c, legacy, false, false, false, "MATERIALIZED")
	want := []string{
		"ALTER TABLE spans ADD COLUMN IF NOT EXISTS attr_kvh2 Array(UInt64) ALIAS arrayMap((k, v) -> cityHash64(k, v), attr_keys, attr_values)",
		"ALTER TABLE spans ADD INDEX IF NOT EXISTS idx_attr_kvh2 attr_kvh2 TYPE bloom_filter(0.01) GRANULARITY 4",
		"ALTER TABLE spans ADD INDEX IF NOT EXISTS idx_attr_keys attr_keys TYPE bloom_filter(0.01) GRANULARITY 4",
		// Eski kolon: indeks -> TEK ALTER'da DROP + ADD ALIAS (aynı ifade).
		// MODIFY ... ALIAS DEĞİL: eski part'larda veri kalır ve sonraki her
		// mutasyon kod 47 ile düşer (CH 26.2, ölçüldü).
		"ALTER TABLE spans DROP INDEX IF EXISTS idx_attr_kvh",
		"ALTER TABLE spans DROP COLUMN IF EXISTS attr_kvh, ADD COLUMN attr_kvh Array(UInt64) ALIAS arrayMap((k, v) -> cityHash64(concat(k, '\\x1F', v)), attr_keys, attr_values)",
	}
	if strings.Join(stmts, "\n") != strings.Join(want, "\n") {
		t.Fatalf("DDL:\n%s\nwant\n%s", strings.Join(stmts, "\n"), strings.Join(want, "\n"))
	}
	// v2 ifadesi SABİT taşımaz — analyzer'ın bloom eşleşmesi buna bağlı.
	if strings.Contains(attrIndexV2Expr(c), "'") {
		t.Errorf("v2 ifadesi sabit taşımamalı: %s", attrIndexV2Expr(c))
	}
	// Idempotent: hepsi varken ve eski kolon zaten ALIAS / yokken ifade yok.
	for _, kind := range []string{"ALIAS", ""} {
		if n := len(attrIndexV2DDL(c, legacy, true, true, true, kind)); n != 0 {
			t.Errorf("kind=%q: %d ifade", kind, n)
		}
	}
	r := attrIndexV2DDL(attrIndexV2Cols[1], attrIndexCols[1], false, true, true, "")
	if len(r) != 1 || !strings.Contains(r[0], "res_kvh2 Array(UInt64) ALIAS arrayMap((k, v) -> cityHash64(k, v), res_keys, res_values)") {
		t.Errorf("res_kvh2 DDL'i: %v", r)
	}
}

func TestAttrKVSeparatorAndHashSQL(t *testing.T) {
	if AttrKVSep != "\x1f" || len(AttrKVSep) != 1 {
		t.Errorf("ayırıcı 0x1F olmalı: %q", AttrKVSep)
	}
	// SQL literal'i CH'nin çözeceği kaçış: ters bölü + x1F (ham baytı DEĞİL —
	// kaynağa kontrol karakteri sızmasın: feedback-binary-poisoned-source).
	if attrKVSepSQL != `'\x1F'` || strings.ContainsRune(attrKVSepSQL, 0x1f) {
		t.Errorf("SQL ayırıcısı kaçışlı olmalı: %q", attrKVSepSQL)
	}
	if strings.Count(AttrKVHashSQL, "?") != 2 || !strings.HasPrefix(AttrKVHashSQL, "cityHash64(concat(?, '\\x1F', ?))") {
		t.Errorf("hash SQL'i iki bağlama taşımalı: %s", AttrKVHashSQL)
	}
	// Kaynak dosyada ham 0x1F baytı YOK.
	if strings.ContainsRune(attrIndexExpr(attrIndexCols[0]), 0x1f) {
		t.Error("ifade ham kontrol karakteri taşıyor")
	}
}

func TestAttrIndexRegistry(t *testing.T) {
	prev := attrIndexMode.Load()
	t.Cleanup(func() { registerAttrIndex(prev) })
	registerAttrIndex(attrIndexNone)
	if AttrIndexAvailable() {
		t.Error("none kaydı")
	}
	registerAttrIndex(attrIndexLegacy)
	if !AttrIndexAvailable() || attrKVHCol("attr") != "attr_kvh" || attrKVHashExpr() != AttrKVHashSQL {
		t.Error("legacy kaydı")
	}
	registerAttrIndex(attrIndexV2)
	if !AttrIndexAvailable() || attrKVHCol("res") != "res_kvh2" || attrKVHashExpr() != AttrKVHash2SQL {
		t.Error("v2 kaydı")
	}
}
