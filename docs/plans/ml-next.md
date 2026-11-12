# ML programı — sonraki oturum (operatör, 01.11.2026)

Prod gözlemi (v0.10.1762) sonrası kodlanmayan maddeler; sıra operatörün. Gemide: 1763 backfill, 1764 VM bütçesi, 1765 ES kısmi sonuç, 1766 A3 eski dedektör anahtarları.

## Önceki listeden kalanlar (4, 5, 7 ve 6-kanıt v0.10.1767 ile GEMİDE; 6-tekilleştirme B3'te)

**4) field_caps önbelleği.** `logstore-es` çözümlemesi (service.name → .keyword, log.level, channel) her tikte tekrarlanıyor. `termsAggField` 10 dk TTL'li ve yalnız `len(fields)==1` sonucu önbellekliyor; `MLResolveField` adayları sırayla deniyor, boş dönen aday önbelleğe girmiyor → her tik field_caps. Çözüm: boş sonucu da (negatif) 1 sa TTL ile tut; `groupFieldTTL` ML yolunda 1 sa.

**5) Tik gözlemlenebilirliği** (tik 25–38 sn). Tik log satırına adım süreleri (ES, VM, CH okuma, CH yazma, backfill durumu). `MLTickStatus.Steps` ve `Signals` (ml_status.go'da alanlar hazır, 1767 doldurur): sinyal başına çalıştı mı, servis sayısı, kaynak süresi, aday / bastırılan / yazılan, kısmi sonuç, backoff'taki aileler, backfill ilerlemesi. Karnede "0 olay" ile "çalışmıyor" ayrımı.

**6) pod_outlier tekrar olay** (bsa-openbanking-accountaggregator 12:10 ve 12:30 aynı değerler, iki satır). Kimlik `signal+detector+service+part`; RMT aynı id'yi ezer — iki satır ya farklı part (ölçü err_ratio/p95) ya dağıtık CH'de farklı shard (FINAL shard'lar arası tekilleştirmez; ml_events hv listesinde mi kontrol). Kanıta sorgu zamanı + VM serisinin son örnek damgası (bayat seri teşhisi). B3 ile birleşir: epizot modeli.

**7) Varsayılanlar.** BaselineDays 7 → 21 (7 günde tek cumartesi; baselineDates tüm günlere düşüyor, cumartesi sahte düşüş). MaxServices (500) Settings › ML › Eşikler'de (10–5000); karnede "izlenen N / toplam M servis".

## B2) ML op_latency kronik desen — GEMİDE v0.10.1769
Prod örneği: bsa-investment-securities-fund-prod, mobil5.prod publish — p99 ~20 ms, birkaç dakikada bir tek istek Kafka timeout'u ile 4–6 sn; eski dedektör 102 kez "normalin 164×" açtı. Yeni ML: kaynak VM örnekleme öncesi histogram; bucket başına asgari istek; p99 yerine yavaş istek oranı + mutlak etkilenen istek eşiği; dwell; son 7 günde ≥ K epizot → olay yerine tek "kronik" kaydı. Test: sentetik seri ile olay yok / kronik var.

## B3) Epizot modeli — GEMİDE v0.10.1768
Açık → kapandı (art arda 3 normal bucket), `ended_at`; 30 dk içinde tekrar → aynı epizot. `MLEventID` zamansız, tekrar eden olaylar üzerine yazıyor; kimlik epizot başlangıcını içersin (migrate). Madde 6 bununla birleşir: bölüm farklıysa hata değil.

## C1) Tam sayfa detay — GEMİDE v0.10.1770
Listede tıklayınca drawer yerine `/anomalies/:id` ve `/anomalies/ml/:id`; gerçek `<a href>`; geri dönüşte filtre/scroll korunur; eski `?event=`, `?replay=`, `/inbox?anomaly=` linkleri yönlenir.

## C2) Replay — GEMİDE v0.10.1771
Pencere başlangıç − 1 sa → bitiş + 1 sa; kayıt epizot kapanınca tamamlanır; bucket başına mevsimsel beklenen bant; gölgeli anomali bölgesi; ilk sapma/tespit/tepe/bitiş işaretleri; rollout/takvim; diğer ML sinyalleri.

## C3) Minimalist anlatım — ML sayfası GEMİDE v0.10.1771; eski anomali sayfasında yalnız başlık+özet, tam düzen geçişi AÇIK
İnsan diliyle başlık ("Kafka'ya yazma yavaşladı"), durum + süre; tek satır özet "normal → şimdi" birimli + etkilenen sayı/pay ("normalin N×" yok); tek grafik; "Ne değişti" en fazla 3 kanıtlı madde; eylemler tek satır + "Gerçek mi? Evet/Hayır"; kronik uyarısı tek satır; güven/kaynak/skor/eşik/baseline/bağlam/sonraki adım → katlanır "Teknik detay"; jargon yok; listede Durum ve Süre sütunları. Başlık/özet üretimi saf fonksiyon + test. Eski anomali sayfası da aynı düzene geçsin.

## Açık not
"Heap bandı" için anomaly_sensitivity'de ayrı anahtar bulunamadı (grep HeapBand/heapBand boş); runtime_pods (runtime_vm GC) yerleşik anahtarı altında olabilir — A3'te yalnız op_latency + log_template_new eklendi.

## Operatör notu 01.11.2026 — JVM / heap / GC kaynağı Thanos
Heap, JVM ve GC metrikleri VictoriaMetrics'te DEĞİL; uzak cluster'ların Thanos'unda (`internal/chstore/thanos.go`, clusters katmanı). Sonuçlar:
- `metric_jvm` ailesi (M1) VM'de boş döner → bugünkü hâliyle prod'da sessiz kalır; kaynak seçimi gerekir: aile başına kaynak (VM | Thanos cluster(ları)), Thanos için cluster × servis gezme (multicluster-first).
- Eski heap bandı dedektörü (`anomaly/heap_source.go`, metricsrc CH|VM) da aynı kaynak sorununu taşır; A3'te ayrı anahtar bulunamadı — Thanos kaynağıyla birlikte ele alınır.
- Backoff / bütçe aynı mantıkla Thanos sorgularına uygulanır (Thanos query_range maliyeti VM'den yüksek).
- v0.10.2019 GEMİDE (heap bandı hariç): aile başına kaynak saatte bir otomatik (VM varsa VM, yoksa metriği taşıyan Thanos cluster'ları; ayar yok, Settings › ML'de salt-okunur); Thanos'ta ifade pod düzeyinde, pod → servis `thanos.ServicePodKeys` ile; cluster × tik ≤ 12 sorgu, ayrı backoff, query_range yok. Kod: `internal/ml/thanos_source.go`, `metric_thanos.go`, `thanos_adapter.go`.
Karar operatörde; kodlanmadı.
