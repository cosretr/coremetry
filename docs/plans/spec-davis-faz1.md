# Spec — Davis paritesi FAZ 1: varlık başına baseline + yönlü anomaly_event (2026-11-05)

Kaynak: `docs/audit/davis-parity-audit.md` §FAZ 1 (satır 456-472) + KARARLAR 1-12 (2026-10-29).
FAZ 0.5 (v0.10.1585-1588) gemide. Bu spec onay bekler; onaysız kod yok.

## Hedef
Anomali tabanı servis×metrik'ten **özne×metrik**'e iner (servis, endpoint, db instance, kuyruk) ve her
`anomaly_events` satırı **yönünü ve tabanını** taşır. "Taban yok"u `ratio=0` ile kodlama tuzağı
(1587) kapanır: açık `baseline_kind` + `direction`.

## Dilimler (5 sürüm, sırayla; her biri kendi tag'i; eşikler ayardan)

| # | Sürüm | İş | Dosyalar | Kapı |
|---|---|---|---|---|
| F1.1 | 1789 | **Şema**: `anomaly_events` + `direction LowCardinality(String) DEFAULT ''` (up/down), `baseline_value Float64 DEFAULT 0`, `baseline_kind LowCardinality(String) DEFAULT ''` (seasonal/consecutive/none), `subject_kind LowCardinality(String) DEFAULT 'service'`, `subject_id String DEFAULT ''`. İki-boot probe (`ddl_defer.go` reprobe deseni); Go satır struct + okuyucular DEFAULT'a tolerant; RMT whole-row replace tüm alanları taşır | `chstore/store.go`, `chstore/ddl_defer.go`, `chstore/anomaly_events*.go`, `migrations/` (dağıtık not) | `/clickhouse-schema` C2-C3 (LC yalnız düşük distinct; `subject_id` String), distributed-column-safety testi, `go test ./internal/chstore/...` |
| F1.2 | 1790 | **Ayar**: `anomaly_sensitivity.subjects{endpoint,db,queue: enabled(false), topN(20), maxSeriesPerTick}` + `seasonalTimezone` (varsayılan `Europe/Istanbul`, karar 3; geçişte taban bir kez kayar → sürüm notu). LoadPersisted/SavePersisted, admin PUT audit, Settings › Anomaly › "Özneler" alt bölümü | `chstore/anomaly_sensitivity.go`, `api/anomaly_sensitivity.go`, `pages/settings/AnomalyTab.tsx` + `SubjectsSubsection.tsx`, `lib/types.ts`, `lib/api.ts` | ayar varsayılanı testi; snapshot+restore disiplini (memory) |
| F1.3 | 1791 | **Özne tabanı**: `fetchAllSeasonal` anahtarı `(subject_kind, subject_id, metric)`; endpoint = VM `http_route` histogramı `sum by (svc, route)` (karar 2), servis başına üst-N rota (karar 1), etiket yoksa servis düzeyine düşer; db/queue mevcut okuyuculardan. VM bütçesi: tik başına ≤24 sorgu (gruplu), aşımda özne türü atlanır + self-obs sayaç | `anomaly/anomaly.go` (fetchAllSeasonal/buildAllSeasonalQuery), `anomaly/red_vm_query.go`, `anomaly/subjects.go` (yeni, saf anahtar + bütçe planlayıcı) | tablolu test: anahtar üretimi, top-N, düşüş, bütçe kesimi; `TestREDBaselineEquivalence` yeşil |
| F1.4 | 1792 | **Yönlü olay**: `evaluateAnomaly` → `direction`, `baseline_value`, `baseline_kind` (verdict); yazıcı yeni kolonları doldurur; Problem önceliği / RCA / standardizasyon `ratio=0` yerine `baseline_kind='none'` okur; trace_op/log_pattern/svc-slowdown aynı mevsimsel taban (c sınıfı). `-batch` kuralı ve P1 yapışkanlığı korunur | `anomaly/verdict.go`, `anomaly/anomaly.go`, `anomaly/behavior.go` (minBaselineRate okuyucu), `sre/`, `api/problems*.go` okuyucular, FE anomali detayı "yön + taban" satırı | `TestEvaluateAnomaly` genişler; `/aiops` kontrol listesi; her kolon yazan yol testle pinli ("ikinci yarı") |
| F1.5 | 1793 | **Eval kapısı**: karar 11 eval seti (aile başına dengeli ≥50 satır; gerçek/değil mevcut düğmeler) → `make eval-anomaly`: `anomaly_explain` çekirdeğiyle replay, FP ve recall tablosu, FAZ 0.5 ile kıyas. Kapı: FP ≤ FAZ 0.5, recall ≥ mevcut, endpoint düzeyinde ≥1 onaylı vaka. Özneler eval geçmeden varsayılan AÇILMAZ | `cmd/anomaly-eval/` ya da `internal/anomaly/eval_test.go` (`-tags evalset`), `Makefile` | kapı sonucu `docs/audit/davis-parity-audit.md`'ye |

Sıra gerekçesi: şema ve ayar önce (dağıtık iki-boot, prod'da kolon yerleşir), sonra okuma, sonra yazma;
eval en son çünkü eval seti ve Sorgu A operatörde.

## Operatörden gereken girdiler
1. **Sorgu A** (`docs/audit/davis-sorgu-a.sql`, 102 satır, placeholder yok; sabitler gömülü: 30 gün,
   bigBreach 2.0, flap 15 dk, ±30 dk) — prod CH'de koş, sonucu yapıştır. F1.3'te top-N ve hangi
   özne türlerinin ilk açılacağını belirler; F1.5'te sınıf başına FP tablosunun paydası.
2. **Eval seti** (≥50 satır, Sorgu B'den `noise/not_anomaly/cosre_correct=0` + onaylı gerçek vakalar) —
   F1.5 kapısı bunsuz ölçülemez. F1.1-F1.4 onunla başlayabilir.

## Riskler
- Endpoint kardinalitesi (1000s servis × 100s rota): yalnız top-N + bütçe kesimi; Sorgu A olmadan N=20 tahmin.
- `operation_summary_5m` span adı ≠ rota: endpoint tabanı metrikten (VM), MV'den değil (karar 2).
- Saat dilimi geçişi (karar 3): tüm mevsimsel tabanlar bir kez kayar; sürüm notu + 1 gün gözlem.
- Okuyucu uyumluluğu: eski satırlarda `direction=''` → "yön bilinmiyor", `ratio=0` yorumu değişmez (geri uyum).
- GROUP BY itmesi yasak, boş küme → 0 değil yok (memory dersleri) — F1.3 sorgu şekli testle pinli.

## Kapsam dışı
vmalert (karar 1 Go), problem_groups (karar 5 incident), gölge/canlı eval (FAZ 3), 0011 K8s katmanı
genişlemesi (karar 4; ayrı spec).

## Tahmin
F1.1 ~2 saat · F1.2 ~2 saat · F1.3 ~yarım gün · F1.4 ~yarım gün · F1.5 ~2 saat (eval seti hazırsa).
