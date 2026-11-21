# Trace inceltme (kademeli trace saklama) — gölge mod

Sürüm: v0.10.2440 (2026-11-21). Durum: **yalnız gölge ölçüm**; gerçek silme
kapsam dışı (`mode=on` ayrılmış, API reddeder).

## Problem

Prod ClickHouse (2 shard × 2 replika) 30 günde ~70 milyar span / ~4 TB tutuyor
(~2.3 milyar span/gün, ~133 GB/gün). Upstream zaten tüm ERROR trace'lerini +
%5 olasılıksal örneği gönderiyor. 7 günden eski trace'lerin çoğu bir daha
açılmıyor; açılanlar ya hatalı, ya bir probleme/konuşmaya bağlı, ya da yavaş.

## Kural (operatör onaylı tasarım)

| Yaş | Kalan |
|---|---|
| 0 – `fullDays` (7) gün | her şey |
| `fullDays` – retention (30) | deterministik `keepPercent` (%10) + önemli trace'ler |

Bir trace şu sırayla İLK eşleşen sınıfa sayılır (toplam = kalanlar):

1. **hash** — `cityHash64(trace_id) % 100 < keepPercent` (deterministik:
   aynı trace her shard'da, her koşuda aynı kararı alır; span'ler parça parça
   düşmez).
2. **error** — en az bir span `status_code = 'error'`.
3. **ref** — referanslı: `root_cause_hypotheses.exemplar_trace_id`
   (problem / anomali / incident kanıtı), `trace_snapshots.trace_id`
   (kullanıcının paylaştığı/sakladığı trace), `cosre_interactions.trace_id`
   (CoSRE konuşmasında geçen trace).
4. **slow** — kök (servis, operasyon) için trace süresi > p99 (yalnız
   ≥ 100 örneklenmiş trace'i olan operasyonlar).

Kod: `ThinningClassify` (Go) ↔ `thinningClassSQL` (SQL ikizi; test eşler).

## Ayar ve durum

- `system_settings['retention.thinning']` = `{mode: off|shadow|on, keepPercent: 10, fullDays: 7}`.
  `on` ayrılmış — doğrulama reddeder. Yazım: `PUT /api/admin/trace-thinning/settings`
  (admin, audit `trace_thinning.settings`). İşçi her tik blob'u yeniden okur
  (çok pod'lu kurulumda tek kaynak).
- `system_settings['retention.thinning.shadow']` = gün başına tahmin listesi
  (band dışına düşen gün budanır). Yeni tablo yok.
- Okuma: `GET /api/admin/trace-thinning` → System → ClickHouse "Trace inceltme ·
  gölge tahmini" kartı (çevrilmez, silme düğmesi yok).

## Ölçüm (gün partition'ı başına)

Lider işçi (`coremetry:lock:trace-thinning`, worker kipi) 10 dakikada bir,
mod `shadow` ise EN ÇOK BİR günü ölçer. Sıra: ölçülmemiş gün (yeni → eski),
sonra `keepPercent`'i değişmiş ya da 7 günden bayat ölçüm (sonradan eklenen
referanslar önemli kümeyi büyütür). Başarısız gün 6 saat beklemeden yeniden
denenmez.

1. `system.parts` (küme kipinde `cluster()`, shard başına bir replika) —
   spans gün partition'ının satır + `bytes_on_disk`'i: **kesin** span ve byte.
2. Referans kaynakları — üç küçük tablo, her biri `LIMIT 2000`, `created_at/
   computed_at >= gün` (referans trace'ten sonra oluşur); toplam ≤ 2000 id.
3. `trace_id_ts` (ORDER BY trace_id) — hangi referans bu günde, hangi saatlerde:
   PK araması.
4. `trace_summary_5m` — günde bulunan referanslar, yalnız o saatlerin 5 dk
   kovaları: `time_bucket IN (küme) AND trace_id IN (…)`. İlk anahtar kolonu
   nokta kümesine bölününce ikinci kolon (trace_id) granül budar (lokal
   EXPLAIN: 2/50 granül). Kesin, ağırlık 1.
5. `trace_summary_5m` — örneklem: günün her saatinden BİR 5 dk kova (saat
   başına dönen ofset, :00 cron sivrilerine hep denk gelmesin) → partition'ın
   ~1/12'si; içinde `sipHash64(trace_id) % den = 0` (keep hash'inden bağımsız
   fonksiyon), `den` ≈ kovalardaki span / 20M. Referanslar örneklemden düşülür
   (çift sayım yok). p99 aynı örneklemden pencere fonksiyonuyla
   (`quantile(0.99) OVER (PARTITION BY rs, rn)`), kök operasyon başına.
   Çıktı ≤ 5 satır (sınıf başına trace + span).

Tahmin (`thinningEstimate`, saf): örneklem × `den` × 12 + referanslar →
tutulan span ORANI; kalan span = oran × kesin span; kazanılan byte =
(1 − oran) × kesin byte (span ağırlıklı; trace başı byte eşit varsayılmaz).

## Maliyet — prod ölçeği (koşu başına)

Varsayım: 2.3 milyar span/gün, ~12 span/trace → ~190M trace/gün →
trace_summary_5m ~200M satır/gün (trace × 5 dk kova).

| Adım | Okunan | Not |
|---|---|---|
| system.parts | metadata | ms |
| Referans kaynakları (3) | ≤ 3 × 2000 satır | PK/küçük tablo, 10 sn tavan |
| trace_id_ts | ≤ 2000 PK araması | granül 2048, 15 sn tavan |
| Referans kesin | granül budamalı | 30 sn tavan |
| Örneklem | ~16.7M satır (1/12), ~145 B/satır ≈ 2.4 GB sıkıştırmasız, ≈ 0.6–1 GB diskten, 2 shard'a bölünür | `den` ≈ 9 → GROUP BY ≈ 1.9M trace (~300 MB), pencere sıralaması aynı boy; 120 sn tavan, iç `LIMIT 5000000` |

Beklenen duvar saati 5–15 sn/koşu. Günlük: ilk dolum 23 koşu (~4 saat, 10 dk
arayla); kararlı hâlde ~1 yeni gün + 23/7 yeniden ölçüm ≈ 4–5 koşu/gün ≈
3–5 GB disk okuması/gün (günlük yazımın ~%3'ü). Mod `off` iken sıfır sorgu.

## Bilinen sınırlar

- Örneklem yalnız seçilen 5 dk kovalarındaki span'leri görür: birden çok
  kovaya yayılan uzun trace'in diğer kovadaki hata/kök bilgisi kaçabilir
  (trace'lerin çoğu tek kova; etkisi küçük, tahmin tarafında). Gerçek kural
  (`on`) trace düzeyinde çalışmalı.
- p99 günlük kök-operasyon düzeyi (saatlik değil): saat × operasyon başına
  örnek sayısı yetmiyor. ≥ 100 örnekli olmayan operasyonda "yavaş" hükmü yok
  (tutulanı biraz az sayar → kazancı biraz fazla gösterir).
- Referanslar 2000 id ile sınırlı (kart "sınırda" der); `problems` /
  `incidents` satırları trace id taşımıyor, kanıt yalnız hipotez exemplar'ı.
- `trace_id_ts` geriye dolmaz: MV'den önceki günlerde referans eşleşmesi 0.
- Gerçek silme (mode=on) ayrı tasarım ister: `ALTER … DELETE` / lightweight
  delete partition partition, mutasyon kısma (skipidx_materialize emsali),
  MV'lerin (trace_summary_5m, trace_id_ts) tutarlılığı.
