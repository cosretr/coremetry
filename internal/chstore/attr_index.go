package chstore

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
)

// attr_index.go — v0.10.299 (docs/audit/trace-attribute-search.md Dilim 1a,
// operatör onayı 2026-09-03): HER attribute için tek, attribute sayısından
// bağımsız eşitlik indeksi.
//
// Ölçülen kusur (audit G1): terfi etmemiş bir attribute filtresi
// `attr_values[indexOf(attr_keys,'k')] = v` hiçbir skip index kullanmıyor —
// pencerenin TAMAMI okunuyor (868k/868k satır, değer nadir de olsa yok da
// olsa). Çare: her span için `cityHash64(k <US> v)` dizisi (MATERIALIZED —
// INSERT anında sunucu hesaplar, Go yoluna dokunmaz, Distributed forward
// düşürür: dağıtık-güvenli) + `bloom_filter(0.01)` skip index. Ölçüldü
// (scratch, 24 s): nadir değerde 868k → 65k satır, 155 MiB → 4.9 MiB;
// kolon 10 B/satır, indeks 0.3 B/satır. Yaygın değerde budamaz (Datadog
// facet davranışı) — o yol Dilim 2 facet kaydı.
//
// Ayırıcı 0x1F (ASCII unit separator): anahtar/değer içinde `=` olabilir,
// 0x1F pratikte olmaz; SQL'de `'\x1F'` kaçışı (CH string literal), Go
// tarafında AttrKVSep. Derleyici (Dilim 1b) hash'i SUNUCUDA üretir
// (`cityHash64(concat(?, '\x1F', ?))`, iki bağlama) — Go'da CityHash
// uygulaması gerekmez, Go↔CH hash uyumu sorusu doğmaz.
//
// Eski part'lar: MATERIALIZED kolon okuma anında hesaplanır, indeks ise
// yalnız YENİ part'larda — kazanç retention boyunca dolar (operatör kararı:
// kademeli; `MATERIALIZE INDEX` opsiyonel, migrations/0014 ADIM 6).
// Prod dış Distributed: boot koşmaz (0013 sözleşmesi) — migrations/
// 0014_attr_kvh.sql elle; ifade bu dosyayla BİREBİR (attr_index_migration_test).

// AttrKVSep — anahtar ile değer arasındaki ayırıcı (0x1F).
const AttrKVSep = "\x1F"

// attrKVSepSQL — aynı ayırıcının CH string literal'i (raw string: `\x1F`
// metni CH'ye gider, CH kaçışı çözer).
const attrKVSepSQL = `'\x1F'`

// AttrKVHashSQL — derleyicinin bir (k, v) çifti için hash ifadesi; iki
// bağlama argümanı (anahtar, değer) — kontrol karakteri asla bağlanmaz.
const AttrKVHashSQL = "cityHash64(concat(?, " + attrKVSepSQL + ", ?))"

type attrIndexCol struct {
	col, keysCol, valsCol, idx, keysIdx string
}

// attrIndexCols — span ve resource kapsamı; ikisi de aynı şekil.
var attrIndexCols = []attrIndexCol{
	{col: "attr_kvh", keysCol: "attr_keys", valsCol: "attr_values", idx: "idx_attr_kvh", keysIdx: "idx_attr_keys"},
	{col: "res_kvh", keysCol: "res_keys", valsCol: "res_values", idx: "idx_res_kvh", keysIdx: "idx_res_keys"},
}

// attrIndexExpr — MATERIALIZED ifadesi (0014 ile birebir).
func attrIndexExpr(c attrIndexCol) string {
	return fmt.Sprintf("arrayMap((k, v) -> cityHash64(concat(k, %s, v)), %s, %s)", attrKVSepSQL, c.keysCol, c.valsCol)
}

// ── v2: ALIAS hash kolonu (depolama sıkıştırması) ──────────────────────────
//
// attr_kvh MATERIALIZED olarak spans'ın %15.5'i (92 MiB / 10M span; bloom
// indeksi yalnız 2.98 MiB). Kolonun TEK işi indeksi beslemek → ALIAS yeter:
// indeks INSERT anında ifadeden hesaplanır, kolon diske yazılmaz.
//
// NEDEN AYNI İFADE DEĞİL (ölçüldü, CH 26.2): analyzer açıkken (24.3+
// varsayılan) ALIAS ifadesi bir SABİT taşıyorsa ('\x1F' ayırıcı) bloom_filter
// indeksi sorguyla EŞLEŞMEZ — has(attr_kvh, h) tüm granülleri okur (392/392);
// sabitsiz ifade eşleşir (8/392). Bu yüzden v2 hash'i ayırıcısız iki
// argümanlı cityHash64(k, v) (LowCardinality anahtar = String anahtar hash'i,
// doğrulandı) ve AYRI ad taşır: eski pod'lar attr_kvh + concat hash'iyle
// sorgulamaya devam eder; attr_kvh de ALIAS'a çevrilir (aynı değer, disk yok)
// → yuvarlanan dağıtımda hiçbir pod yanlış sonuç görmez.
var attrIndexV2Cols = []attrIndexCol{
	{col: "attr_kvh2", keysCol: "attr_keys", valsCol: "attr_values", idx: "idx_attr_kvh2", keysIdx: "idx_attr_keys"},
	{col: "res_kvh2", keysCol: "res_keys", valsCol: "res_values", idx: "idx_res_kvh2", keysIdx: "idx_res_keys"},
}

// AttrKVHash2SQL — v2 hash ifadesi; iki bağlama (anahtar, değer).
const AttrKVHash2SQL = "cityHash64(?, ?)"

// attrIndexV2Expr — ALIAS ifadesi (migrations/0017 ile birebir).
func attrIndexV2Expr(c attrIndexCol) string {
	return fmt.Sprintf("arrayMap((k, v) -> cityHash64(k, v), %s, %s)", c.keysCol, c.valsCol)
}

// attrIndexV2DDL — v2 kolon + indeks (eksik olanı) ve eski kolonun
// sıkıştırması: legacyKind == "MATERIALIZED" ise eski indeks düşer, sonra
// kolon TEK ALTER'da DROP + ADD ... ALIAS (aynı ifade) olur.
//
// Neden MODIFY COLUMN … ALIAS DEĞİL (ölçüldü, CH 26.2): MODIFY eski
// part'larda fiziksel veriyi bırakır ve tabloda başka MATERIALIZED kolon
// varsa (spans'ta onlarca) o part'lara dokunan HER mutasyon kod 47 ile
// düşer ("Missing columns: 'attr_kvh'") — takılı mutasyon part'ı merge'den
// de alıkoyar. DROP COLUMN mutasyonu dosyaları sert-bağlantıyla kopyalayıp
// siler (ucuz) ve disk HEMEN boşalır; aynı ALTER'daki ADD sayesinde kolon
// adı hiçbir an çözümsüz kalmaz (eski pod'lar doğru okumaya devam eder).
func attrIndexV2DDL(c, legacy attrIndexCol, colExists, idxExists, keysIdxExists bool, legacyKind string) []string {
	var out []string
	if !colExists {
		out = append(out, fmt.Sprintf("ALTER TABLE spans ADD COLUMN IF NOT EXISTS %s Array(UInt64) ALIAS %s", c.col, attrIndexV2Expr(c)))
	}
	if !idxExists {
		out = append(out, fmt.Sprintf("ALTER TABLE spans ADD INDEX IF NOT EXISTS %s %s TYPE bloom_filter(0.01) GRANULARITY 4", c.idx, c.col))
	}
	if !keysIdxExists {
		out = append(out, fmt.Sprintf("ALTER TABLE spans ADD INDEX IF NOT EXISTS %s %s TYPE bloom_filter(0.01) GRANULARITY 4", c.keysIdx, c.keysCol))
	}
	if legacyKind == "MATERIALIZED" {
		out = append(out,
			fmt.Sprintf("ALTER TABLE spans DROP INDEX IF EXISTS %s", legacy.idx),
			fmt.Sprintf("ALTER TABLE spans DROP COLUMN IF EXISTS %s, ADD COLUMN %s Array(UInt64) ALIAS %s", legacy.col, legacy.col, attrIndexExpr(legacy)))
	}
	return out
}

// Derleyici kipi: hangi hash kolonu + ifadesi.
const (
	attrIndexNone   int32 = 0
	attrIndexLegacy int32 = 1 // attr_kvh + concat hash (0014; dış Distributed, 0017 öncesi)
	attrIndexV2     int32 = 2 // attr_kvh2 + cityHash64(k, v)
)

var attrIndexMode atomic.Int32

// attrKVHCol — kapsamın hash kolonu (scope "attr" | "res").
func attrKVHCol(scope string) string {
	if attrIndexMode.Load() == attrIndexV2 {
		return scope + "_kvh2"
	}
	return scope + "_kvh"
}

// attrKVHashExpr — kipin (k, v) hash ifadesi.
func attrKVHashExpr() string {
	if attrIndexMode.Load() == attrIndexV2 {
		return AttrKVHash2SQL
	}
	return AttrKVHashSQL
}

// repairAttrIndexCols — boot: uygulama yönetimli kurulumda v2 ALIAS kolon +
// indeksleri ekler ve eski MATERIALIZED attr_kvh'yi ALIAS'a çevirir (küme
// kipinde execDDL erteler → iki-boot sözleşmesi). Dış Distributed prod'da
// atlanır (0014 + 0017 elle).
func (s *Store) repairAttrIndexCols(ctx context.Context) map[string]bool {
	ensured := map[string]bool{}
	if s.spansIsExternalDistributed(ctx) {
		log.Printf("[chstore] dış Distributed `spans` — attribute hash indeksi (attr_kvh2) ATLANDI; migrations/0017_attr_kvh_alias.sql elle")
		return ensured
	}
	for i, c := range attrIndexV2Cols {
		legacy := attrIndexCols[i]
		stmts := attrIndexV2DDL(c, legacy, s.spansColumnKind(ctx, c.col) != "",
			s.spansIndexExists(ctx, c.idx), s.spansIndexExists(ctx, c.keysIdx), s.spansColumnKind(ctx, legacy.col))
		ensured[c.col] = true
		for _, q := range stmts {
			if err := s.execDDL(ctx, q); err != nil {
				ensured[c.col] = false
				log.Printf("[chstore] attribute hash indeksi DDL'i başarısız (yumuşak): %v", err)
				break
			}
		}
	}
	return ensured
}

// probeAttrIndex — derleyici kipi: v2 kolonları varsa v2, yoksa eski iki
// kolon (MATERIALIZED ya da ALIAS — ikisi de her part'ta doğru değeri
// verir) varsa legacy, aksi none. "var" = "kullanılabilir": ifade eski
// part'larda okuma anında hesaplanır. İndeksin varlığı doğruluğu değil
// yalnız hızı etkiler.
func (s *Store) probeAttrIndex(ctx context.Context) int32 {
	has := func(cols []attrIndexCol) bool {
		for _, c := range cols {
			if s.spansColumnKind(ctx, c.col) == "" {
				return false
			}
		}
		return true
	}
	switch {
	case has(attrIndexV2Cols):
		return attrIndexV2
	case has(attrIndexCols):
		return attrIndexLegacy
	}
	return attrIndexNone
}

var (
	// attrIndexUsed — derleyicinin bloom yolunu seçtiği yüklem sayısı
	// (/api/health `attr_index_used`; self-observability kuralı).
	attrIndexUsed atomic.Uint64
)

// AttrIndexStats — (bloom yüklemi sayısı, indeks hazır mı).
func AttrIndexStats() (uint64, bool) { return attrIndexUsed.Load(), AttrIndexAvailable() }

// AttrIndexUsed — /api/health sayacı.
func AttrIndexUsed() uint64 { return attrIndexUsed.Load() }

// registerAttrIndex — boot / ertelenmiş DDL inince yayınlanır.
func registerAttrIndex(mode int32) {
	attrIndexMode.Store(mode)
	if mode != attrIndexNone {
		log.Printf("[chstore] attribute hash indeksi hazır: %s/%s — eşitlik/IN filtreleri bloom yolunda", attrKVHCol("attr"), attrKVHCol("res"))
	}
}

// AttrIndexAvailable — derleyici kapısı (Dilim 1b): false iken dizi yolu aynen.
func AttrIndexAvailable() bool { return attrIndexMode.Load() != attrIndexNone }
