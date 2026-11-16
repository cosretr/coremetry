# Problem birleştirme — entity grafı ile (tasarım, kod yok)

Tarih 2026-11-16 · Taban `d98fa3d42` (v0.10.2268) · Durum: ONAY BEKLİYOR · Taslak ekran: `problem-birlestirme-taslagi.html` (oturum scratchpad'i).
Önceki adım: `docs/plans/spec-davis-style-problem-merge.md` (Faz 1, servis içi birleşme, gemide). Bu belge onun "Faz 2 — varlıklar arası" başlığının tasarımıdır.

Değişmeyen kurallar: karar deterministik ve denetlenebilir sistem kuralıdır; LLM ve kullanıcı oyu/kararı hiçbir yeni davranışı sürmez; ayar/vida yok (sayılar kod sabiti); önce gölgede ölç, operatör sayıları görünce uygula.

## 1. Bugün ne oluyor (koddan)

**İki anomali ne zaman aynı Problem'e düşer**
- *Aynı servis, ML olayları:* aynı servisin olayları, pencereleri arasındaki boşluk teyit penceresini (varsayılan 10 dk, ayar 1–120) aşmıyorsa tek gruptur; zincirleme geçerlidir, kronik kayıt girmez (`internal/ml/group.go:9-14`, `:93-132`, `:171-173`; `internal/chstore/ml_settings.go:141`, `:157`).
- *Aynı servis, her aile:* servis başına TEK açık Problem. Açık bir sahip varken gelen her servis sinyali yeni satır açmaz, üye olur (`internal/ml/group_live.go:194-383`; aile tablosu `internal/chstore/problem_owner.go:10-18`, `:72-93`; üye olayı `internal/anomaly/problem_member.go:263-279`). Önceden açılmış çift satırlar "merged into" ile kapanır (`internal/evaluator/problem_merge.go:75-98`, `problem_owner.go:294-331`). Burada zaman penceresi bile yok: sahibin o an açık olması yeter.
- *Servisler arası:* yalnız `anomaly-cluster:`. Aday yalnız `anomaly:<svc>:<metrik>` satırlarıdır (`internal/anomaly/clustering.go:57-75`), en az 3 servis gerekir (`:23`), aday son 30 dk içinde açılmış olmalıdır (`:50`), komşuluk `topology_edges_5m`'in hata ağırlıklı kenarlarından gelir (`internal/anomaly/anomaly.go:781-783`, `internal/chstore/service_adjacency.go:174-200`), kaynak en yüksek suçlama puanıdır (hop başına 0,5 çarpan, en çok 3 hop: `internal/correlator/propagation.go:43-49`). Üye satırları kapatılır (`clustering.go:355-414`). ML ve yaygın yavaşlama Problem'leri servisler arası hiç birleşmez.
- Birleşme yollarının hiçbiri entity grafını okumaz. `internal/entity` bugün yalnız ML kaydının özne kimliğinde kullanılıyor (`internal/ml/record.go:52-59`).

**Sahibi ne seçer:** en eski açık, sahip olabilen satır (başlangıç, eşitse kimlik) (`problem_owner.go:167-195`). Kimlik, başlık ve başlangıç sahibin kalır; öncelik yalnız yükselir (`:235-252`). Yani sahip "ilk açan"dır, köken iddiası değildir. Köken tahmini ayrı bir yerde, puanla üretilir (`root_cause_hypotheses`, "Olası neden" kutusu: `frontend/src/features/anomalies/ProblemDetail.tsx:1026-1030`).

**Emme süresine sınır var mı:** yok. Açık Problem, açık kaldığı sürece üye alır; canlı üyesi olan sahip kapanmaz (`problem_member.go:375-385`, `group_live.go:465-478`); ML zinciri uzunluk sınırı taşımaz (`group.go:117-121`). Var olan iki sınır başka şeydir: üye olayı 20 dk tazelenmezse düşer (`internal/chstore/anomaly_feed.go:50-58`), küme adayı 30 dk'dan eski açılışsa aday olmaz (Problem'in yaşına değil adayın yaşına bakar).

**Yalnız zamana dayanan yerler:** (a) ML grubu: aynı servis + ≤ 10 dk boşluk; (b) servis başına tek Problem: aynı servis + aynı anda açık; (c) birleşik anomali akışı aynı zincir kuralını kullanır (`group.go:87-92`); (d) kümenin 30 dk penceresi; (e) satırdaki "deploy" çipi (servis + yakın zaman). Servis içinde bu kabul edilebilir; sorun, servisler arasında ilişkiyi doğrulayan bir kural olmaması.

**Bildirim:** açılışta bir kez, P1'e geçişte bir kez (`group_live.go:22-26`, `problem_member.go:104-132`). Tekrar tabanı 15 dk / 1 sa (`internal/notify/problem_dedup.go:84-112`, filo geneli yarısı `problem_dedup_shared.go`). Politika kararı `NotifiableAt`: 12 sa tekrar penceresi, baypas = öncelik yükseldi / etki ≥ 2× / yüksek güvenli P1 (`internal/chstore/notify_policy.go:494-542`, `:409-420`); varsayılan kip gölge (`:5-7`). Tekrar defteri süreç içi ve imzası `p:<kural>|<servis>` (`internal/notify/enforce.go:36-40`, `:76-79`).

**"Problem değil" okumaları (bugün var, bu tasarım kaldırmaz):** birleşme kararında `group_live.go:526-546`, `problem_member.go:159-170`, `problem_merge.go:88-93`, `problem_owner.go:114-117`; bildirimde `internal/notify/verdict_silence.go`, `policy_shadow.go`.

## 2. Entity grafı birleşme kuralı

**Model: bağla, kapatma.** Faz 1'in birimi korunur: servis başına tek Problem. Yeni katman bu servis Problem'lerini birbirine BAĞLAR. Birleşik Problem = bağlı servis Problem'leri kümesi. Kümenin *sahibi* en erken açılan üyedir (bugünkü "önce açan sahip" kuralının servisler arası hali; kimlik sabit kalır). *Olası köken* ayrı hesaplanır (§3) ve sahipten farklı olabilir. Hiçbir satır kapatılmaz: servis sayfası kendi Problem'ini göstermeye devam eder. Uygulama diliminde değişen iki şey olur: liste bağlı satırları tek satırda katlar, sonradan bağlanan üyenin açılış bildirimi tutulur.

**Kimler girer:** servis özneli, sahip olabilen ailelerin açık Problem'leri (Faz 1 tablosu) ve bağımlılık özneli Problem'ler (`db-health:`, `anomaly:ext…`). Bağımlılık Problem'i Faz 1'de "ayrı satır"dı çünkü servis anahtarına sığmıyordu; bağlama modelinde kimse kapanmadığı için kümeye girebilir. Alarm kuralları, `exception-storm`, `self-*` hiçbir zaman bağlanmaz (operatör sözleşmesi; Faz 1 kararı).

**Kurallar** (kimlik kalıcıdır; arayüzdeki "birleşme gerekçesi" budur)

| Kimlik | İlişki (graf) | Ek koşul | Arayüz cümlesi |
|---|---|---|---|
| `M-SVC` | aynı servis | Faz 1 kuralı, değişmez | "aynı servis" |
| `M-CALL` | A `calls` B, tek adım (B servis, db, queue ya da ext) | ikisinde de açık Problem; gölgede iki sürüm ölçülür: (A) yalnız ilişki + zaman, (B) ek olarak çağıranın o hedefe ait bağımlılık olayı (`dependency_latency` / `dependency_error_rate`) | "checkout-api, orders-db'yi çağırıyor" |
| `M-DEP` | A ve B aynı db / queue / ext / servisi çağırıyor, ortak noktanın kendi Problem'i YOK | her iki üyede de o hedefe ait bağımlılık olayı var (ortak noktanın Problem'i varsa bağ zaten `M-CALL` ile kurulur) | "ikisi de orders-db'ye bağlı" |
| `M-WL` | aynı iş yükü iki servisi sunuyor (`serves`) | yok (aynı deployment, aynı rollout) | "aynı iş yükü: checkout" |
| `M-NODE` | etkilenen pod'lar aynı node'da (canlı dizin, pod → node) | üyelerin sapması o node'daki pod'larla sınırlı, YA DA node'un kendi sinyali var | "etkilenen pod'lar node-a-01 üzerinde" |
| `M-HOST` | aynı Kubernetes dışı host iki servisi çalıştırıyor (`runs`) | host'un kendi sinyali var ya da servis yalnız o host'ta | "ikisi de host-a-07 üzerinde" |

**Sınırlar (kod sabiti):**
- Bağ başına en çok bir graf adımı (ortak nokta kuralları ortak varlık üzerinden iki kenar). Sağlıklı bir servisin üzerinden atlanmaz: zincir yalnız her halkası sapmış ise uzar.
- Sahipten uzaklık en çok 3 bağ (bugünkü `propagationMaxHops` ile aynı). Birleşik Problem en çok 25 servis; fazlası bağlanmaz ve sayılır (`N-SIZE`).
- Zaman: üyenin başlangıcı, bağlandığı komşunun başlangıcına en çok 30 dk uzak (bugünkü küme penceresi). Ayrıca §7'deki emme sınırı.
- Geniş ortak nokta: bir bağımlılığı ya da node'u 20'den fazla servis kullanıyorsa, kendi sinyali olmadan birleştirmez (`N-HUB`). Herkesin çağırdığı veritabanı her şeyi tek Problem'e çekmesin diye.
- `parent` ilişkisi yalnız pod → iş yükü tırmanışında kullanılır. Aynı namespace ya da aynı cluster birleşme gerekçesi DEĞİLDİR.

**Birleşmeyenler ve nedeni (arayüzde de görünür):** `N-REL` graf ilişkisi yok (aynı anda başlamış olmaları yetmez) · `N-TIME` 30 dk pencere dışı · `N-CAP` emme sınırı geçti · `N-HUB` geniş ortak nokta, kendi sinyali yok · `N-KIND` bağlanmayan aile · `N-SIZE` üye tavanı · `N-GRAPH` graf verisi yok ya da eski.

**Graf yoksa ya da eskiyse:** karar bugünkü davranışa düşer (yalnız `M-SVC`) ve bunu söyler.
- Varlık çözülemedi (servisin `svc:` satırı ya da hiç kenarı yok; db/ext özne kimliği eşlenemedi) → o üye için `N-GRAPH`, arayüzde "graf verisi yok — yalnız aynı servis kuralı uygulandı".
- Kaynak eski: `calls` geçişi 15 dk'da bir koşar (`internal/entity/calls.go:30-41`); son başarılı geçiş 60 dk'dan eskiyse `M-CALL` ve `M-DEP` o tik kapalı. Canlı dizin 3 tik yaşar (`internal/entity/live.go:32`); bayatsa `M-NODE` kapalı. Okuma hatası = o kural ailesi o tik kapalı. Hiçbir durumda tahminle bağ kurulmaz.
- Graf sonradan gelirse bağ sonraki tikte kurulur; kurulmuş bağ, graf eskidi diye koparılmaz (çırpınma olmasın), "graf verisi eski" notu düşülür.

## 3. Olası köken sıralaması

Adaylar: üyeler ve onları bağlayan, kendi sinyali olan ortak varlıklar. Puan yok; sıralı kurallar var, ilk ayıran kazanır. Böylece sonuç tek cümleyle açıklanır.

1. `K-ROLLOUT` — adayın iş yükünde, başlangıcından önceki 30 dk içinde rollout var ve aday en erken başlayanlar arasında (en erkene ≤ 5 dk). Kaynak: mevcut Problem–rollout okuması (`internal/chstore/rollout_problem.go:1-17`).
2. `K-SHARED` — en az iki üyenin bağlı olduğu ortak bağımlılık / node / host / iş yükü (kendi Problem'i ya da üyelerdeki bağımlılık olaylarıyla kanıtlı), ona bağlı üyelerin önündedir.
3. `K-DEEP` — çağrı zincirinde en derindeki üye (üyeler arasında sapmış bir çağrılanı olmayan).
4. `K-FIRST` — en erken başlangıç. 5 dk içindeki farklar eşit sayılır (eski dedektörlerin kovası 5 dk).
5. Hâlâ eşitse varlık kimliği sırası; bu durumda köken "belirsiz" yazılır.

Güven iki kademe: **güçlü** = kazanan en az iki kuralda birden önde (ör. `K-SHARED` + `K-DEEP`); **zayıf** = tek kural. Belirsizde manşet köken iddia etmez: "Köken belirsiz; en erken başlayan cart-api (14:02)".
Cümle kalıbı: "Olası köken orders-db: cart-api ve checkout-api'nin ortak bağımlılığı ve çağrı zincirinin en derin halkası; ilk sapma 14:02." Altında kanıt listesi (kural kimliği, ölçülen değer, kaynak, bağlantı).
Mevcut "Olası neden" (hipotez) kutusu kalır. İkisi farklı varlık gösterirse ikisi de etiketiyle görünür; uyuşma oranı gölgede ölçülür (§9 S2). LLM kararda yok; CoSRE yalnız bu sonucu okuyup anlatabilir.

## 4. "Neden birleşti" — arayüz

**Detay sayfası** (bugünkü yerleşim korunur: başlık, künye, triyaj, solda kısa kutular, altta kapalı "Teknik kanıt"):
- Manşetin altında tek cümlelik köken satırı + güven kademesi.
- "Ne oluyor" kutusunun altında yeni bölüm **Neden tek Problem**: küçük bağımlılık yolu çizimi (köken vurgulu, etkilenen üyeler, sağlıklı çağıranlar soluk) ve üyeler tablosu: sıra (başlangıca göre) · varlık · ne bozuk · başlangıç · gerekçe çipi (cümle + kimlik) · yol. Tablo ≤ 25 satır, sabit, sıralanmaz (tablo standardı: "≤ 10 sabit satır ya da gerekçeli statik tablo"; gerekçe: sıra başlangıç sırasıdır).
- Graf verisi olmayan üye aynı tabloda, gerekçe sütununda nötr "graf verisi yok" ve açıklaması ile.
- Aynı pencerede açılmış ama bağlanmamış Problem varsa tek satır: "cart-search aynı dakikalarda saptı, bu Problem'e bağlanmadı: graf ilişkisi yok (`N-REL`)". En çok 3 satır.
- "Teknik kanıt" içinde: kural kimlikleri, kullanılan graf kenarları (kaynak ve son görülme), ölçülen iki sürüm (A/B), köken kanıt listesi.

**Liste satırı:** tek satır. "Ne bozuk" = sahibin manşeti; alt satırda "köken orders-db · 4 varlık · 5 olay". Servis sütununda sahip + "+3". Çipler bugünkü yerde: nötr "sık tekrar eden" (§6). Yeni sütun yok.

## 5. Etki alanı

Kökenin çağıranları `called_by` üzerinden (`internal/entity/calls.go:285-327`; tek okuma, ≤ 100, fazlası "kesildi" işaretli). İkinci adım yalnız ilk adımın sonucu için tek toplu okuma (≤ 100 uç, LIMIT 500); üçüncü adım yok.
Gösterim iki grup: **Etkilenen (ölçüldü)** = kendi sapması olan üyeler; **Risk altında** = kökeni ya da etkilenenleri çağırıyor ama sapması yok. İlk 8 ad yazılır, kalanı "+N". Sayılar yalnız grafın verdiği kadar: kullanıcı sayısı gibi elimizde olmayan bir etki uydurulmaz. Çağrı hacmi mevcut "Kimi etkiliyor" verisinden (doğrulanmış çağıranlar, `frontend/src/features/anomalies/alertDetailBoxes.ts:37-51`) gelir; yoksa yazılmaz.

## 6. Sık tekrar eden (önce gölge)

Amaç: aynı şey tekrar tekrar açılıyorsa her seferinde bildirmemek, bunu oy/karar yerine sistem kuralıyla yapmak.

- **Aynı tür** = (köken varlık kimliği, aile, yön). Aile Problem standardının ailesidir (hata / gecikme / trafik / erişilebilirlik …; `internal/chstore/problem_standard.go:70-81`); yön yalnız trafikte ayırır (düşüş / artış). Kural kimliği imzaya girmez: sahip "kim önce açtı"ya göre değiştiği için aynı olay farklı kural kimliğiyle gelebilir.
- **Gözlem pencereleri:** son 24 saatte en az 3 önceki bölüm, YA DA son 7 günde en az 4 ayrı günde görülme → "sık tekrar eden". Bölüm = aynı imzayla açılıp kapanmış Problem. Sayılar kod sabiti; gölge raporu 2 / 3 / 5 eşiklerinde de sayar ki karar veriyle verilsin.
- **Şiddet arttı** (o zaman yine bildirilir) = öncelik, penceredeki en yüksek önceliğin üstüne çıktı; ya da büyüklük penceredeki en büyüğün en az 2 katı; ya da yüksek güvenli P1. Bugünkü baypasla aynı ölçü (`notify_policy.go:409-420`). Yaş eskalasyonu (yeni kanıt yokken önceliği yükselten kural) şiddet artışı sayılmaz; gölgede ayrı sayılır.
- **Davranış (uygulamada):** işaretli Problem açılır, listede görünür, "sık tekrar eden" çipini taşır; açılış bildirimi gitmez. Şiddet artarsa bir kez gider. Çözülme bildirimi, açılışı gitmediyse gitmez (bugünkü kural).
- **Mevcut katmanlarla ilişki:** 15 dk / 1 sa tekrar tabanı altta aynen kalır. `NotifiableAt` içinde yeni tutma kodu `frequent`, sıra: etki tabanından sonra, "problem değil" ve 12 sa tekrar penceresinden önce. 12 sa penceresi kısa, süreç içi ve kural imzalı; bu kural kalıcı kayıttan okur ve varlık imzalıdır. P1'e geçiş bildirimi (D5) her zaman geçer.
- **Gölgede ölçülen:** bildirim hunisindeki her açılış için bugünkü gölge satırına (`notification_log`, `channel_kind='policy'`) `hold:frequent` ya da geçiş nedeni yazılır; gönderim değişmez. Rapor (mevcut replay ucuna eklenen sütunlar): aile başına bugün giden / bu kuralla tutulacak sayı; tutulacaklardan sonradan P1'e çıkan, atanan ya da onaylı vakaya dönen liste (kaçırılacak gerçek olaylar; hedef sıfır); 2 / 3 / 5 eşik karşılaştırması; ve yalnız rapor için: bugün "problem değil" ile susan bildirimlerin kaçını bu kural da tutardı.
- **"Problem değil" okumalarını gereksiz kılacak olan:** bu son sayı. Susturulan imzaların tamamına yakını `frequent`, yayın kapıları ya da etki tabanı tarafından zaten tutuluyorsa, birleşme ve bildirimdeki karar okumaları ayrı bir onayla kaldırılabilir. O güne kadar yerinde durur. Yeni kod yolları karar listesini çalışma anında hiç okumaz.

## 7. Emme süresi sınırı

**Önce ölçüm.** İki kaynak:
1. Operatörün koşacağı salt okunur sorgu (taslak; kolon tipleri `/clickhouse-schema` ile doğrulanacak; bağ ifadesi `internal/chstore/problem_events.go:134-138` ile aynı olmalı):
```sql
SELECT quantiles(0.5, 0.9, 0.99)(span_min), max(span_min), countIf(span_min > 90), count()
FROM (SELECT problem_ref, dateDiff('minute', min(started_at), max(started_at)) AS span_min
      FROM (SELECT started_at, <mlEventProblemIDExpr> AS problem_ref FROM ml_events FINAL
            WHERE last_seen >= now() - INTERVAL 14 DAY
              AND (position(evidence, 'problem_id') > 0 OR position(evidence, 'problemId') > 0))
      WHERE problem_ref != '' GROUP BY problem_ref LIMIT 100000)
SETTINGS max_execution_time = 20
```
   Anlamı: bir Problem'e bağlanan ilk ve son olayın başlangıçları arasındaki süre. Faz 1 iki gündür prod'da; veri az, bu yüzden:
2. Gölge sayacı (S1): her yeni üye katılışında sahibin o andaki yaşı kovalanır (0–30, 30–60, 60–90, > 90 dk), kural kimliğiyle birlikte tik günlüğüne ve gölge raporuna.

**Önerilen sınır:** 90 dk (Dynatrace ile aynı), sayılar aykırı değilse.
- Servisler arası: Problem'in en erken başlangıcından 90 dk sonra yeni servis bağlanmaz (`N-CAP`). Geç gelen kendi Problem'ini açar ve kendi bildirimini gönderir; iki sayfada karşılıklı "ilişkili, süre sınırı nedeniyle ayrı" satırı görünür.
- Aynı servis: "servis başına tek açık Problem" kuralı bozulmaz; geç gelen olay aynı Problem'e eklenir ama "geç katıldı" işaretlenir ve şiddeti artırıyorsa bildirim hakkı vardır (§6 ölçüsü). Alternatif (ikinci Problem açmak) Faz 1 kararını geri alır; önerilmez.

## 8. Maliyet

| Ne zaman | Okuma / yazma | Sınır |
|---|---|---|
| Tik (evaluator, lider, 60 sn), aday < 2 | ek okuma yok | açık anlık görüntü zaten okunuyor |
| Tik, aday ≥ 2 | `entity_relations` en çok 3 okuma (`calls`, `serves`, `runs`) + canlı dizin (Redis, 30 sn yerel kopya) | uç ≤ 500, LIMIT 5000, 10 sn (`internal/chstore/entity_relations.go:18`, `:38-69`); sonuç aday kümesinin tam özetiyle 5 dk hatırlanır |
| Tik, B sürümü kanıtı | `ml_events` tek okuma (aday servisler, son 1 sa) | LIMIT 2000, Faz 1 üye okumasıyla aynı kalıp |
| Bağ değişince / 5 dk'da bir tazeleme | bağ olayı yazımı (`ml_events`, yeni dedektör adı `member_graph`, kanıt JSON ≤ 2 KB) | ≤ 25 × açık birleşik Problem; yalnız lider; async insert |
| Köken sıralaması | rollout okuması yalnız küme kurulunca / değişince | mevcut okuma: ≤ 50 iş yükü, ≤ 200 satır |
| Problem açılışı (§6) | `problems` tek okuma (o tikte açılan servisler, son 7 gün) | LIMIT 2000, 5 sn; imza başına 1 sa hatırlanır |
| Detay sayfası | bağ olayları (15 sn önbellek) + etki alanı en çok 2 kenar okuması (60 sn önbellek) | ≤ 100 uç / adım |

Yeni ClickHouse tablosu, MV ya da kolon YOK. Mevcut kolonlara yeni değerler gelir: `ml_events.detector = member_graph`, `notification_log.error = hold:merged:<kural>` ve `hold:frequent`. Bağ olayı ayrı bir kanıt anahtarı taşır (`graph_parent`), Faz 1 okuyucuları (`problem_id`) onu üye saymaz. Ayrı onay gerektirebilecek tek nokta: `problems` tablosu `ORDER BY id` (`internal/chstore/store.go:2766-2767`); 7 günlük servis okumasının maliyeti prod'da ölçülmeden uygulama dilimine geçilmez. Pahalıysa seçenek Redis'te imza başına sınırlı sayaçtır (şema yok); kolon eklemek bu tasarımda önerilmiyor.

## 9. Dilimler

Her dilim tek sürüm, tek başına gemilenir, tek commit ile geri alınır. S1–S3 hiçbir Problem'i, bildirimi ya da liste satırını değiştirmez.

| Dilim | Ne yapar | Operatör ne görür | Geri alma |
|---|---|---|---|
| **S1** gölge bağ + "neden" (salt okunur) | saf plan işlevi + evaluator son pası; bağ olayları gölge işaretli yazılır; `GET /api/problems/{id}/graph`; detayda "Neden tek Problem (gölge)" bölümü ve birleşmeyenler satırı | detayda gerekçe, yol, başlangıç sırası; admin raporu: kaç küme kurulurdu, boyut dağılımı, kural başına sayı, A ve B sürümü farkı, `N-*` sayıları, graf verisi eksik oranı, tutulacak bildirim sayısı, katılış yaşı kovaları (§7), mevcut `anomaly-cluster` ile örtüşme | revert; gölge satırları okunmaz |
| **S2** köken + etki alanı (salt okunur) | sıralama, köken cümlesi, etki alanı bloğu | manşet altında köken satırı; rapor: köken güven dağılımı, hipotez kutusuyla uyuşma oranı | revert |
| **S3** sık tekrar eden gölgesi | imza, pencereler, gölge satırı, replay sütunları; satırda nötr çip ("bildirim değişmedi" ipucu) | §6'daki rapor | revert; çip kaybolur |
| **S4** birleşmeyi uygula | liste katlaması + sonradan bağlanan üyenin bildirimi tutulur (`hold:merged`); emme sınırı devrede | Problems listesi kısalır; menü sayacı küme sayar | tek sabit kapatılır: katlama ve tutma durur, hiçbir satır kapatılmadığı için veri onarımı yok |
| **S5** sık tekrar edeni uygula | `frequent` tutma kodu enforce yolunda | tekrar bildirimleri kesilir | revert; kod yolu bugünkü hale döner |
| **S6** temizlik (ayrı onaylar) | `anomaly-cluster` emekliliği; "problem değil" okumalarının kaldırılması | — | her biri kendi revert'i |

Geçiş ölçütü S3 → S4/S5: en az 7 gün gölge; tutulacak bildirimler arasında sonradan P1'e çıkan / onaylı vakaya dönen SIFIR; yanlış bağ örnekleri operatörce gözden geçirilmiş (rapor en büyük 20 kümeyi listeler).

## 10. Operatör kararları

1. **Model "bağla, kapatma" mı?** (üye servis Problem'leri açık kalır, liste katlar) — Öneri: evet. Kapatmak, etkilenen servisin sayfasını "sorunsuz" gösterir ve geri alması zordur.
2. **`M-CALL` için ek kanıt şart mı?** (B sürümü) — Öneri: S1 raporundaki A/B farkını gördükten sonra karar; varsayılan beklenti B.
3. **Emme sınırı 90 dk ve yalnız servisler arası mı?** — Öneri: evet; aynı serviste "geç katıldı" işareti, ikinci Problem yok.
4. **Sık tekrar eşiği: 24 saatte 3 bölüm ya da 7 günde 4 ayrı gün mü?** — Öneri: bu sayılarla gölgeye gir, raporla kesinleştir.
5. **"sık tekrar eden" çipi, Problem satırındaki mevcut "yinelenen" çipinin yerini alsın mı?** — Öneri: S5'te evet (tek kelime, tek anlam); S3'te ikisi yan yana durmaz, yalnız yenisi gölge ipucuyla.
6. **Geniş ortak nokta eşiği 20 servis, üye tavanı 25 servis mi?** — Öneri: evet; S1 raporu `N-HUB` ve `N-SIZE` sayılarını verir, gerekirse sabit değişir.

## Koddan belirlenemeyenler

- Görevde adı geçen `internal/ml/publish_gate.go` bu taban commit'te yok; yayın durumu `internal/ml/record.go:30-39` içinde.
- db / ext özneli Problem'in (`db-health:`, `anomaly:ext…`) entity kimliğine (`db:…`, `ext:…`) eşlenmesi doğrulanmadı; S1 eşlenemeyeni `N-GRAPH` sayıp raporlar.
- ML bağımlılık olayındaki hedef adı (`server.address`, `db.namespace`) ile graf düğümü kimliğinin birebir eşleştiği doğrulanmadı (B sürümünün dayanağı).
- Node düzeyi kendi sinyali olan bir Problem ailesi olup olmadığı incelenmedi; yoksa `M-NODE` yalnız "pod'larla sınırlı" koşuluyla çalışır.
- Entity eşitleyicisinin geçiş başına "son başarılı" zamanının okunabilir olduğu varsayıldı (`entity_runs` / `Syncer.Observability`); S1'de doğrulanır.
- `problems` tablosunun boyutu ve 7 günlük servis okumasının süresi; prod'da kümelerin gerçek sıklığı; bildirim politikasının prod'daki kipi (gölge mi, uygulama mı).
- Tarayıcıda görünüm doğrulanmadı (taslak statik HTML).
