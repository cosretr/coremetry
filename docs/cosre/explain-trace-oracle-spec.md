# Explain trace + Oracle satır detayı (request/response) — spec

Durum: **Kademe A gemide (v0.10.921, operatör onayı 2026-09-25).** Kademe B (request/response) operatör kararıyla şimdilik bırakıldı.
Tarih: 2026-09-25 · `main` @ v0.10.919.
Tablo, kolon ve host adları yer tutucudur (`<schema>.<table>`, `<TRACE_COL>`); gerçek adlar repoya yazılmaz.

**Operatör isteği:** "Explain trace Oracle data source'una gidip SELECT atarak oradaki detaylara erişse; belki request/response da alabilir."

**Operatör kararı (2026-09-25):** model **lokal**. Bu yüzden payload modele gidebilir; veri kurum dışına çıkmaz.

## 1. Bugün ne var (kod okuması)

| Konu | Durum |
|---|---|
| Trace ↔ Oracle bağı | Coremetry Oracle'a **trace için hiç sorgu atmaz**. Poller hata satırlarını ClickHouse'a kopyalar (`oracle_error_log`, 30 gün, `trace_id` bloom index; `internal/chstore/oracle_error_log.go:93-121`). Trace › Logs sekmesi bunları `oracle` rozetiyle gösterir (`/api/oracle/errors`, rol kapısı yok). |
| Explain trace | Kanıt olarak span'lar, loglar (≤15, 6 sn) ve isteğe bağlı kod kullanır (`internal/api/explain_trace_input.go:103-313`). **Oracle satırlarını hiç kullanmaz.** |
| Request/response | Hiçbir eşleme taşımıyor. Özel SQL kipinde satırlar dakika×operasyon×hata grubuna **toplanıyor**, payload orada kayboluyor. `SelectMappedOnly` kipi eşlenmeyen kolonları zaten düşürüyor. |
| Canlı Oracle sorgusu | Yalnız üç yerde var: poller, ayar testi ve admin SQL konsolu. **Bind'li (parametreli) trace sorgusu yok.** Konsolda bind yok, satır sınırı 10k, hücre başına kırpma yok. |
| CLOB | Sürücü (go-ora, INLINE) LOB'un **tamamını** belleğe çeker (~1 GiB'a kadar). Kırpma ancak indirmeden sonra olur. |
| Oturum | Her çağrı yeni bir TNS oturumu açar. Havuz, semafor ve oran sınırı yok. |
| Rol | Explain her rolde açık. Oracle konsolu ve ayarları yalnız admin. |

## 2. Tasarım: iki kademe

### Kademe A — kayıtlı Oracle satırları Explain'e (sıfır risk, varsayılan AÇIK)

- `buildTraceExplainInput`, logların yanında ve paralel olarak `OracleErrorsByTrace` okur (ClickHouse, span penceresi ±60 sn, 4 sn zaman aşımı, sessiz düşüş).
- **Modele giden:** en çok 10 satır, yalnız tipli alanlar. Alanlar: zaman, operation, error code, external code, error type, channel, host/instance, request.id. `attr` alanlarından değer başına en çok 200 rün.
- **Özel SQL kipindeki satırlar grup satırıdır.** Prompt bunu açıkça söyler: "Bu satır aynı dakika/operasyon/hata kodu için N adet; tek olayın detayı değil."
- Bu veri bugün de her role açık olduğu için yeni bir erişim açılmaz.
- Önbellek anahtarı kendiliğinden değişir (prompt'un tamamı hash'lenir).
- `systemTrace` prompt'una tek satır eklenir ("ORACLE hata satırları varsa…"); prompt sürümü artar.

### Kademe B — canlı detay: request/response (varsayılan KAPALI, tıklama başına)

**Kaynak başına yeni ayar bloğu `traceLookup`** (Settings › Oracle, admin):

```
traceLookup: {
  enabled:        false,
  table:          "<schema>.<table>",       // identRe ile doğrulanır
  traceColumn:    "<TRACE_COL>",
  timeColumn:     "<TS_COL>",               // bölüm budama için pencere bind'i
  columns:        ["<OP_COL>", "<ERR_COL>", "<CHANNEL_COL>", ...],   // açık liste, * yok
  payloadColumns: ["<REQUEST_COL>", "<RESPONSE_COL>"],               // CLOB olabilir
  payloadMaxBytes: 16384,                   // hücre başına, SQL tarafında kesilir
  roles:          "admin" | "admin+editor", // viewer asla
  payloadToModel: true                      // yalnız lokal/iç AI ucu doğrulanınca etkin
}
```

**Sorguyu kod kurar (saf, tablo-testli fonksiyon):**

```sql
SELECT <columns>,
       DBMS_LOB.SUBSTR(<REQUEST_COL>,  :maxBytes, 1) AS REQUEST,
       DBMS_LOB.SUBSTR(<RESPONSE_COL>, :maxBytes, 1) AS RESPONSE
FROM   <schema>.<table>
WHERE  <TRACE_COL> = :traceId
  AND  <TS_COL> BETWEEN <bindTime :fromTs> AND <bindTime :toTs>   -- trace penceresi ±5 dk
FETCH FIRST 20 ROWS ONLY
```

- **Trace ID ve zaman bind edilir, metne gömülmez.** Kimlik doğrulaması ingest ile aynıdır (32 hex, `normalizeTraceID`). Saat dilimi `bindTime` ile çözülür.
- **`DBMS_LOB.SUBSTR` ile CLOB sunucuda kesilir;** 1 GiB indirme riski kalmaz. VARCHAR kolonlarda `SUBSTR` kullanılır. LONG kolonlar reddedilir (ORA-00997).
- **Satır ve boyut sınırları:** 20 satır; yanıt bütçesi 256 KB, Go tarafında ikinci bir kesme ile.
- **Kaynaklar:**
  - kaynak başına semafor: 2 eşzamanlı;
  - (kaynak, trace) başına singleflight;
  - zaman aşımı min(`queryTimeoutSec`, 8 sn);
  - sonuç önbelleği 5 dk, yalnız bellekte. SWR kullanılmaz, çünkü arka plan yenilemesi kullanıcısız ve denetimsiz sorgu atardı.
- **Ayar testi:**
  - üretilen SQL'i gösterir;
  - `<TRACE_COL>` üzerinde indeks olup olmadığını denetler; indeks yoksa "tam tarama riski" uyarısı verir;
  - örnek 1 trace ile çalışır;
  - kolon tiplerini (CLOB/VARCHAR/LONG) raporlar.
- **Önerilen yapılandırma:** salt-okunur bir DB kullanıcısı. Sınır GRANT'tır; bunu ayarda da yazıyoruz.

**Explain'e bağlanma:**

- **Tıklama başına opt-in:** gövdede `includeOracle` bayrağı, `includeCode`'un ikizi. Yalnız `copilotExplainTrace`'te kullanılır. `buildTraceExplainInput`'a GİRMEZ; böylece sohbet ve çekmece yolları payload'u kendiliğinden çekmez.
- **Rol kapısı:** saf `oracleExplainAllowed(claims, src)`, tablo-testli (`explainAllowed` deseni). Yetkisi olmayan kullanıcı normal Explain'i alır, yalnız düğmeyi görmez; Explain'in tamamı 403 vermez.
- **Modele giden blok:**
  - en çok 5 satır, hata satırları önce;
  - payload başına en çok 1500 rün, blok toplamı en çok 4000 rün;
  - kırpıldığında açık not ("kırpıldı: N bayt");
  - bağlam taşarsa blok yarıya indirilip bir kez yeniden denenir (kod bloğu deseni).
- **Blok veri olarak çerçevelenir** (`DataNotInstruction`, `FenceSafe`). Müşteri tarafından yazılmış metin yeni bir enjeksiyon yüzeyidir; "gizle/maskele" dili kullanılmaz (test yeşil kalır).
- **Lokal uç koruması:** `payloadToModel` yalnız etkin AI profilinin uç noktası loopback, özel IP ya da operatör tarafından iç ağ ilan edilmiş bir host ise etkindir. Aksi hâlde payload yalnız UI'da gösterilir ve modele "payload gönderilmedi" notu gider.
- **Önbellek:** blok, Explain önbellek anahtarının `extra` kısmına girer; yetkisiz kullanıcının isteğiyle aynı anahtarı paylaşmaz.
- **Denetim:** her canlı sorgu (önbellek isabeti dahil) kaydedilir:
  - `s.audit(r, "oracle.trace_lookup", "oracle_source", srcID, …)`
  - ayrıntılar: `{traceId, rows, capped, tookMs, cols (yalnız adlar), payloadBytes, sentToModel, aiHost, exchangeId, cached}`
  - payload ve DSN asla kaydedilmez.

## 3. Mockup

**Trace detay › "Explain this trace" çekmecesi** (Kademe B yetkisi olan kullanıcı):

```
┌─ CoSRE · Trace 9f3c…e21a ─────────────────────────────────────────────┐
│ Karar: POST /v1/address/save — upstream ADDR servisi ERR-1001 ile     │
│ düştü; adres doğrulaması reddetti (response: "INVALID_POSTCODE").     │
│                                                                       │
│ İşlem Akışı ve Veri Özeti                                             │
│ …                                                                     │
│ Kök Neden ve Sonraki Adım                                             │
│ …                                                                     │
│                                                                       │
│ Kanıt: 19 span · 1 trace · 2 Oracle satırı (kayıtlı) · 1 canlı satır  │
│                                                                       │
│ [ Kodu da incele ]  [ Oracle detayını da incele ]  ← yalnız admin     │
│                                                                       │
│ ── Oracle · <kaynak adı> · canlı · 1 satır · 312 ms ───────────────── │
│  20:58:10  OP=ADDRESS_SAVE  ERR=ERR-1001  CH=MOB                      │
│  ▸ REQUEST   (2.1 KB, JSON)                        [Kopyala]          │
│  ▾ RESPONSE  (0.6 KB, JSON)                        [Kopyala]          │
│    {                                                                  │
│      "status": "ERROR",                                               │
│      "code": "INVALID_POSTCODE",                                      │
│      "message": "…"                                                   │
│    }                                                                  │
│  Modele gönderildi: evet (lokal uç) · kırpılmadı                      │
└───────────────────────────────────────────────────────────────────────┘
```

**Rozet satırı** (kanıt satırının altında, küçük ve soluk): `📄 Oracle: <kaynak> · SQL sha 3f9a… · denetim kaydı`

**Settings › Oracle › kaynak › "Trace detay sorgusu"** (admin):

```
┌─ Trace detay sorgusu (Explain için) ────────────── [ Kapalı ▾ ] ─┐
│ Tablo           [ <schema>.<table>                ]              │
│ Trace kolonu    [ <TRACE_COL> ]   ✓ indeksli                     │
│ Zaman kolonu    [ <TS_COL>    ]   pencere: trace ±5 dk            │
│ Kolonlar        [ OP, ERR, CHANNEL, HOST, … ]                    │
│ Payload         [ <REQUEST_COL> (CLOB) ] [ <RESPONSE_COL> (CLOB) ]│
│ Hücre sınırı    [ 16 KB ]   Kim kullanabilir  (•) admin ( ) +editor│
│ Payload modele  [✓]  AI ucu: <iç-host> — lokal ✓                  │
│                                                                  │
│ Üretilen SQL (salt okunur, bind'li):                              │
│   SELECT … WHERE <TRACE_COL> = :traceId AND <TS_COL> BETWEEN …    │
│                                                                  │
│ [ Örnek trace ile dene: ____________ ]  → 1 satır · 280 ms · CLOB │
└──────────────────────────────────────────────────────────────────┘
```

## 4. Kararlar (operatör)

| # | Karar | Öneri |
|---|---|---|
| K1 | Kademe A (kayıtlı Oracle satırları) her Explain'e girsin mi? | **Evet.** Yeni erişim açmaz, maliyeti sıfır |
| K2 | Kademe B sorgusunu kim yazar: kod mu kurar, yoksa `:traceId` bind'li özel SQL mi? | **Kod kursun** (tablo + kolon listesi). CLOB güvenliği ve indeks denetimi garanti olur. Özel SQL gerekirse sonra eklenir |
| K3 | Canlı detayı kim tetikleyebilir? | **admin** varsayılan; kaynak başına "+editor" seçeneği; viewer asla |
| K4 | Payload modele: yalnız lokal/iç uç doğrulanınca mı? | **Evet** (lokal model kararı + güvenli varsayılan) |
| K5 | `ai_calls` örneğinde (90 gün, admin görür) payload kalsın mı? | **Olduğu gibi kalsın** (tam sadakat duruşu). Yalnız bu yüzey KB aday listesine girmesin |
| K6 | Request/response nerede görünsün? | **Önce Explain çekmecesinde.** Trace › Logs'ta "canlı detay" düğmesi ikinci adım |

**Operatör bilgisi (2026-09-25):** önce "request/response nerede, ben de bilmiyorum", sonra "O database ama tablo yok". Oracle hata veritabanında request/response'u tutan bir tablo **yok**. Sonuç:
- **Kademe B (Oracle'dan canlı request/response) askıda.** Veri kaynağı yok. Operatör 2026-09-25: "Şimdilik request response bırakalım"; Kademe B ve request/response keşfi bekletiliyor.
- **Kademe A etkilenmez** ve beklemeden gidebilir.
- Request/response'un gerçek yeri ayrıca bulunmalı. Adaylar: trace sayfasındaki dış "Log İzleme (requestId)" sistemi, ya da Coremetry'nin zaten okuduğu uygulama logları. Aynı requestId ile atılan loglarda istek/yanıt gövdesi varsa Explain onu log yolundan alabilir (`trace_link_identity` requestId'yi zaten çözüyor). Bu yol ayrı bir spec konusudur.

## 5. Sürüm planı

1. **v0.10.920 — Kademe A:** kayıtlı Oracle satırları Explain'e. Test önce: saf blok kurucu, bütçe ve sıra; prompt pin testleri.
2. **v0.10.921 — Kademe B arka uç:**
   - `traceLookup` ayarı, saf SQL kurucu, bind'ler, `DBMS_LOB.SUBSTR`, semafor, singleflight, bellek önbelleği, denetim;
   - ayar formu ve "örnek trace ile dene" düğmesi (indeks ve kolon tipi raporu).
   - Explain'e henüz bağlanmaz.
3. **v0.10.922 — Kademe B Explain:**
   - `includeOracle`, rol kapısı, lokal uç koruması, model bloğu (`DataNotInstruction`), önbellek `extra`'sı;
   - çekmece alt bilgisi ve REQUEST/RESPONSE görüntüleyici (`LogTable` JSON görünümü yeniden kullanılır);
   - prompt sürümü artar.

## 6. Keşif: request/response nerede?

Oracle veri sözlüğü (`ALL_TAB_COLUMNS`), Coremetry'nin bağlandığı kullanıcının **okuyabildiği** tablo ve kolonları listeler. Canlı sorgunun erişebileceği küme de tam olarak budur. Aşağıdaki sorgular Settings › SQL (Oracle) konsolunda çalışır: admin yetkisiyle, salt-okunur ve denetim kaydıyla.

**1. Trace kolonu taşıyan tablolar:**

```sql
SELECT owner, table_name, column_name, data_type, data_length
FROM   all_tab_columns
WHERE  UPPER(column_name) LIKE '%TRACE%'
  AND  owner NOT IN ('SYS','SYSTEM','XDB','MDSYS','CTXSYS','ORDSYS','WMSYS','OLAPSYS','DBSNMP','OUTLN','APEX_PUBLIC_USER')
ORDER  BY owner, table_name
```

**2. Bu tablolardan payload'a benzeyen kolonları olanlar** (CLOB/LOB/XML, uzun VARCHAR2 ya da REQ/RESP/PAYLOAD/BODY/XML/MESSAGE adlı kolonlar):

```sql
WITH t AS (
  SELECT DISTINCT owner, table_name
  FROM   all_tab_columns
  WHERE  UPPER(column_name) LIKE '%TRACE%'
    AND  owner NOT IN ('SYS','SYSTEM','XDB','MDSYS','CTXSYS','ORDSYS','WMSYS','OLAPSYS','DBSNMP','OUTLN','APEX_PUBLIC_USER')
)
SELECT c.owner, c.table_name, c.column_name, c.data_type, c.data_length
FROM   all_tab_columns c
JOIN   t ON t.owner = c.owner AND t.table_name = c.table_name
WHERE  c.data_type IN ('CLOB','NCLOB','BLOB','LONG','XMLTYPE')
   OR (c.data_type IN ('VARCHAR2','NVARCHAR2') AND c.data_length >= 2000)
   OR REGEXP_LIKE(UPPER(c.column_name), 'REQ|RESP|PAYLOAD|BODY|XML|MESSAGE|MSG')
ORDER  BY c.owner, c.table_name, c.column_id
```

**3. Payload tablosunda trace kolonu yoksa** (payload bir istek kimliğiyle ya da satır kimliğiyle bağlanıyorsa): trace kolonu yerine `%REQUEST%ID%` ile aynı iki sorgu. Bu durumda canlı sorgu iki adımlı olur. Önce trace ile hata tablosundan istek kimliği alınır, sonra istek kimliği ile payload tablosu okunur. Kademe B kurucusu bunu `joinColumn` ile destekler.

**4. Hata tablosunun kendi kolonları** (payload aynı tabloda olabilir):

```sql
SELECT column_name, data_type, data_length
FROM   all_tab_columns
WHERE  owner = '<SCHEMA>' AND table_name = '<TABLE>'
ORDER  BY column_id
```

**Ürüne girecek hâli (v0.10.921):** Settings › Oracle › "Trace detay sorgusu" formuna **"Tablo bul"** düğmesi. Bu düğme:
- sorgu 1+2'yi (ve gerekirse 3'ü) aynı veri sözlüğü okumasıyla çalıştırır (mevcut sözlük yoklamaları `internal/oracle/client.go:401-455` deseni, admin, denetimli);
- aday tabloları "trace kolonu · payload kolonları (tip, uzunluk) · trace kolonunda indeks var mı" olarak listeler;
- tıklanan aday formu doldurur;
- operatörün tablo ya da kolon adı bilmesine gerek bırakmaz.
