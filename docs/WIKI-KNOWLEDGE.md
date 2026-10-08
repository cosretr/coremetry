# Azure DevOps Wiki bilgisi (CoSRE)

CoSRE sohbeti, kurumun on-prem Azure DevOps wiki'lerindeki runbook, nasıl
yapılır, mimari ve sahiplik sayfalarından **kaynak bağlantılı** cevap verir.
Wiki içeriği Coremetry'nin ClickHouse'una indekslenir ve dışarı çıkmaz.

## Ön koşullar

1. **Ayarlar → Kod entegrasyonu** (Azure DevOps bağlantısı) yapılandırılmış
   olmalı: sunucu URL'i, koleksiyon, PAT, gerekirse "Skip TLS verify".
   Wiki için **ayrı kimlik bilgisi yok** — aynı bağlantı kullanılır.
2. PAT'in kapsamı: **Wiki (Read)** (Code: Read kod okuma içindir; ikisi
   birlikte olabilir). 401/403'te durum kartı "Wiki: Read" der.
3. **Opsiyonel (Yalnız canlı arama modunda ZORUNLU):** Azure DevOps Server'da
   **Search uzantısı** (wiki araması). Yoksa karma mod yalnız yerel indeksle
   çalışır; durum kartı "Azure DevOps Search: sunucuda yok" gösterir.
4. **Opsiyonel:** Bilgi (RAG) sekmesindeki embedding uç noktası (ör. bge-m3).
   Varsa wiki parçaları da embed edilir ve arama hibrit olur (anahtar sözcük +
   anlamsal); yoksa her şey anahtar sözcükle çalışır.

## Kurulum

**Ayarlar → Bilgi (RAG) → Azure DevOps Wiki**

| Alan | Varsayılan | Not |
|---|---|---|
| Wiki bilgisi aktif | kapalı | Açınca ilk senkron ≤15 sn içinde başlar |
| Projeler (izin listesi) | boş = tümü | Satır başına bir proje adı |
| Wiki'ler (izin listesi) | boş = tümü | `WikiAdı` ya da `Proje/WikiAdı` |
| Senkron aralığı (dk) | 60 | En az 15 |
| Sayfa tavanı | 5000 | Tavana takılırsa budama yapılmaz, kart uyarır |
| Mod | Karma | Aşağıdaki "Modlar" tablosu |

"Şimdi senkronize et" (yalnız admin, audit'li) isteği lider pod'a iletir;
senkron hangi pod'da koşarsa koşsun durum kartı aynı bilgiyi gösterir.

### Modlar (v0.10.1124)

| Mod | Ne yapar | Bedel |
|---|---|---|
| **Karma (önerilen)** | Senkronla yerel indeks + yerel sonuç zayıfsa / indeks boş-bayatsa Azure DevOps Search | — |
| **Yalnız canlı arama** | Senkron YOK. Her wiki sorusu Azure DevOps Search'e gider, en iyi ≤3 sayfa API'den okunur (eşzamanlı, 8 sn tavan); sayfalar yalnız bellekte (10 dk, ≤64 sayfa / 16 MB) — içerik yazılmaz (yalnız küçük durum blobları) | Soru başına 1–3 sn; **Search uzantısı şart** |
| **Yalnız senkron** | Yalnız yerel indeks; canlı arama yok (eski "Search'e sor" kapalı) | İçerik son senkron kadar güncel |

Canlı modda Search uzantısı yoksa (404 / hiçbir api-version kabul edilmiyor)
durum kartı, kayıt uyarısı, "Aramayı test et" ve sohbet cevabı açıkça
**"Azure DevOps Search bu sunucuda yok; senkron modunu kullanın"** der.
Canlı moda geçmeden önce indekslenmiş sayfalar **temizlenene dek kalır**
(kart bunu söyler); "İndeksi temizle" (yalnız oturumlu admin, onaylı,
audit `wiki.purge`) `wiki_pages` / `wiki_chunks` satırlarının tümünü siler.
Senkron sürerken mod canlıya alınırsa geçiş sayfa döngüsünde durur ve budama
yapmaz.
Canlı modda işaretsiz serbest sohbet sorusu wiki'ye gitmez (her soruya
gecikme eklenmesin); yalnız açık wiki sorusu ve `search_wiki` /
`read_wiki_page` gider.

### Aramayı test et (yönetici)

Bölümdeki "Aramayı test et" kutusu bir sorgunun yerel isabetlerini, canlı
Azure DevOps Search denemesini (sınıf: `ok` / `unavailable` / `bad_request` /
`auth` / `http` / `network`, http durumu, kullanılan api-version, sonuç ve
okunan sayfa sayısı, gönderilen AND/OR sorgusu) ve son listeyi gösterir; her
isabette RAG tabanını (0.5) ve açık wiki sorusu tabanını (0.3) geçip
geçmediği yazar. Geri çekilme / "unavailable" önbelleği bu denemede atlanır.
Sayfa içeriği dönmez (isabet başına ≤160 karakter kesit); audit satırı
(`wiki.test_search`) sorgu metnini değil yalnız uzunluğunu ve sayıları taşır.

### İndeksteki sayfalar

Bölümün altındaki tablo indekslenmiş sayfaları listeler (proje, wiki, Azure
DevOps sayfasına bağlantılı başlık, son güncelleme, parça sayısı); başlık/yol
süzgeci ve 100'lük sunucu sayfalaması var. Yönetici satırı açıp içeriğin ilk
~1000 karakterini önizleyebilir (`GET /api/wiki/pages`; önizleme sunucuda
yalnız admin rolüne seçilir).

## Nasıl çalışır

- **Senkron** yalnız lider pod'da (api/worker rolü, Redis kilidi) koşar, artımlıdır: sayfa
  ağacı alınır, yalnız **değişen** sayfaların içeriği okunur (git objectId ya
  da ETag ile tespit), kaynakta silinen sayfalar indeksten düşer. Geçici bir
  sunucu hatası indeksi boşaltmaz.
- **Sohbet — açık wiki sorusu** (v0.10.1124): bağlamsız CoSRE penceresinde
  (panel/çekmece/exception/trace/servis bağlamı YOKKEN) soru wiki'yi işaret
  ediyorsa wiki kademesi telemetri kademelerinden ÖNCE koşar. **Güçlü işaret**
  (`wiki`, `runbook`, `playbook`, `prosedür`, `procedure`, `kılavuz`,
  `howto`, `dokümantasyon`): sonuç yoksa "Wikide bulunamadı". **Zayıf işaret**
  ("how to", "how do I/we", "nasıl yapılır/yaparım…", `doküman`, `docs`):
  yalnız en iyi isabet ≥ 0.5 ise cevaplar, değilse soru normal akışa
  (guided/RAG) aynen gider. Wiki metni modele `<wiki_data>` çitinde verilir:
  yerel indeks + gerekirse canlı arama, bulunan sayfa metni modele verilir
  (tek sayfa baskınsa ~6000 karaktere dek) ve model **özetler / yorumlar**;
  cevabın altında **Wiki · sayfa başlığı** bağlantıları çıkar. Bulunamazsa
  canlı arama hatasında sohbet yalnız genel not görür ("Azure DevOps araması
  şu an yanıt vermedi"); ayrıntı "Aramayı test et"te.
- **Sohbet — diğer sorular**: RAG kademesi wiki parçalarını da dayanak
  yapar (taban 0.5; canlı arama yalnız indeks boş/bayatsa). Serbest sohbet
  döngüsünde model `search_wiki` / `read_wiki_page` araçlarını kullanır.
- **Canlı arama sorgusu**: soru cümlesi değil, soru sözcükleri (nasıl,
  nedir, mı, how, what…) atılmış terimler gönderilir; önce AND, sonuç yoksa
  OR. Uç koleksiyon adresindedir (`{koleksiyon}/_apis/search/wikisearchresults`);
  api-version sırası 7.0 → 6.0-preview.1 → 5.1-preview.1 → 5.0-preview.1 →
  4.1-preview.1 (önizleme eki istenirse aynı sürüm `-preview.1` ile). Yalnız
  404 ya da tüm sürümlerin **sürüm reddi** "Search yok" sayılır (6 sa sonra
  yeniden denenir); 401/403, 5xx geçicidir (15 dk ara). Sürümle ilgisiz 400
  yalnız o sorguya özgüdür: genel ara VERİLMEZ, AND sorgusu reddedildiyse bir
  kez düz OR denenir.
- **Canlı isabet skoru**: Azure DevOps'un sıralaması skora çevrilir (ilk
  isabet 0.66, sonra ×0.88) — sayfada sorgu teriminin kökü geçiyorsa ya da
  ADO vurgulamışsa. Türkçe ek farkı ("servisini" ↔ "servis") isabeti artık
  tabanın altına itmez.
- **Durum kartı**: "Azure DevOps Search" satırı son canlı denemeyi gösterir
  (hangi pod denediyse; `wiki_sync_status` yanında `wiki_search_status`
  blobu): sonuç sayısı, api-version ya da hata sınıfı + http durumu.
- **Erişim — ÖNEMLİ**: oturum açmış **her** Coremetry kullanıcısı (viewer
  dahil) kapsamdaki **tüm** wiki'leri sohbet üzerinden okuyabilir. Okuma
  PAT'in kimliğiyle yapılır; Azure DevOps'taki sayfa/wiki izinleri Coremetry
  kullanıcısı başına uygulanmaz. Hassas wiki'ler varsa **Projeler / Wiki'ler
  izin listesini mutlaka doldurun** (ya da PAT'i yalnız paylaşılabilir
  wiki'leri görebilen bir hesaptan üretin).
- **API token'ları (cmk_) ve dış MCP istemcileri** wiki içeriğine erişemez;
  ayarı değiştirmek ve senkron başlatmak yalnız oturum açmış admin'e açık.
- Sohbette harici MCP sunucusu yapılandırılmışsa model wiki **araçlarını**
  kullanmaz (wiki metni harici bir araca taşınamasın); wiki bilgisi yine
  araçsız cevap yolundan gelir.
- Arama Türkçe karakterden bağımsızdır (`şifre` = `sifre`) ve teknik
  tanımlayıcıları tam eşler (`svc-orders`, `ERR-1042`).

## Sorun giderme

| Belirti | Çare |
|---|---|
| "Azure DevOps bağlantısı yapılandırılmamış" | Kod entegrasyonu ayarını doldurun |
| `http 401/403 — … (Wiki: Read)` | PAT'e Wiki (Read) kapsamı ekleyin |
| "JSON yerine text/html … oturum açma sayfası" | URL bir SSO/form oturum açma arkasında; doğrudan sunucu URL'ini kullanın |
| "sayfa tavanına takıldı" | Sayfa tavanını artırın ya da izin listesini daraltın |
| Cevaplarda wiki çıkmıyor | "Aramayı test et" ile yerel/canlı yarıyı ayrı görün; soruda `wiki`/`runbook` geçirin (açık wiki kademesi) |
| "Azure DevOps Search: henüz denenmedi" | Hiçbir pod henüz canlı arama yapmadı — "Aramayı test et" dener ve sonucu karta yazar |
| `bad_request · http 400` | Sürümle ilgisiz 400 (ör. geçersiz proje süzgeci); uç var, ara verilmez — ayrıntı "Aramayı test et"te |
| Canlı modda "Search bu sunucuda yok" | Server'a Search uzantısını kurun ya da modu Karma / Yalnız senkron yapın |
