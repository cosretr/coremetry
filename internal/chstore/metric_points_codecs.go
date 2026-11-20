package chstore

// metric_points_codecs.go — metric_points sayısal kolon kodekleri
// (depolama sıkıştırması). store.go CREATE ile birebir; mevcut kurulumlarda
// boot her açılışta MODIFY eder (kodek-yalnız MODIFY metadata işlemidir:
// mutasyon yok, yalnız YENİ part'lar ve merge'ler yeni kodekle yazılır —
// CH 26.2'de doğrulandı). DEFAULT korunur: MODIFY COLUMN tipi + default'u
// birlikte yeniden yazar. ORDER BY'a dokunulmaz (tablo takası kapsam dışı).
//
// Ölçüm (CH 26.2, 21M satır / 2 gün, OPTIMIZE FINAL): bu altı kolon
// 44.1 → 16.5 MiB; tablo 111.2 → 83.6 MiB (-%25). En büyük kalem
// series_fingerprint (kodeksiz LZ4 33.3 → ZSTD(3) 9.8 MiB). Delta/ZSTD(1)
// alternatifleri ölçüldü, her kolonda bu seçimden kötü ya da eşit.
var metricPointsCodecAlters = []string{
	`ALTER TABLE metric_points MODIFY COLUMN count UInt64 DEFAULT 0 CODEC(T64, ZSTD(3))`,
	`ALTER TABLE metric_points MODIFY COLUMN min_value Float64 DEFAULT 0 CODEC(Gorilla, ZSTD(3))`,
	`ALTER TABLE metric_points MODIFY COLUMN max_value Float64 DEFAULT 0 CODEC(Gorilla, ZSTD(3))`,
	`ALTER TABLE metric_points MODIFY COLUMN bucket_bounds Array(Float64) DEFAULT [] CODEC(ZSTD(3))`,
	`ALTER TABLE metric_points MODIFY COLUMN bucket_counts Array(UInt64) DEFAULT [] CODEC(T64, ZSTD(3))`,
	`ALTER TABLE metric_points MODIFY COLUMN series_fingerprint UInt64 DEFAULT 0 CODEC(ZSTD(3))`,
}
