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
3. **Opsiyonel:** Azure DevOps Server'da **Search uzantısı** (wiki araması).
   Yoksa özellik yalnız yerel indeksle çalışır; durum kartı
   "Azure DevOps Search: sunucuda yok" gösterir.
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
| Azure DevOps Search'e sor | açık | Yerel sonuç zayıfsa / indeks bayatsa canlı arama |

"Şimdi senkronize et" (yalnız admin, audit'li) isteği lider pod'a iletir;
senkron hangi pod'da koşarsa koşsun durum kartı aynı bilgiyi gösterir.

## Nasıl çalışır

- **Senkron** yalnız lider pod'da (api/worker rolü, Redis kilidi) koşar, artımlıdır: sayfa
  ağacı alınır, yalnız **değişen** sayfaların içeriği okunur (git objectId ya
  da ETag ile tespit), kaynakta silinen sayfalar indeksten düşer. Geçici bir
  sunucu hatası indeksi boşaltmaz.
- **Sohbet**: wiki'ye benzeyen sorularda (runbook, prosedür, "kim sorumlu")
  cevap ilgili wiki bölümlerinden kurulur; cevabın altında **Wiki · sayfa
  başlığı** bağlantıları çıkar. Gelişmiş sohbet döngüsünde model
  `search_wiki` / `read_wiki_page` araçlarını kullanır.
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
| Cevaplarda wiki çıkmıyor | Durum kartında indeks sayısını kontrol edin; soruda sayfadaki terimleri kullanın |
