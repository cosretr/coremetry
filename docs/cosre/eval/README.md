# CoSRE akıcılık değerlendirmesi (H1)

Bu dizin CoSRE konuşma kalitesi programının ölçüm tabanını tutar
(`docs/cosre/conversation-audit.md` §6.1, dilim H1). Buradaki
`fluency-<tarih>.md` dosyalarını operatör kendisi kaydeder ve puanlar; kod bu
dizine hiçbir şey yazmaz.

## Set ne ölçüyor

- 22 çok turlu Türkçe konuşma (`internal/copilot/evalset/conversation/fluency.json`,
  toplam 55 tur; ikisi v0.10.2328'in doğrulayıcı yoklamaları). Yalnız nötr
  adlar: checkout, orders, payments, inventory.
- Kapsam: tek alanı değiştiren takip soruları ("peki dün?", "ya ödeme tarafı?"),
  durum cevabından sonra "deploy mu vardı", asistan bir sonraki adımı
  önerdikten sonra çıplak "bak", kısa olması beklenen durum soruları, hata
  avı, açıklama, karşılaştırma, bilgi sorusu, sohbet, bulgu yok durumu ve
  kullanıcının varlığı düzelttiği tur ("o değil, …").
- Turlar gerçek CoSRE v2 sohbetinden, üretimdeki kademe zinciriyle geçer.
  Konuşma kimliği, düz metin geçmiş ve sunucu sohbet bağlamı turdan tura
  taşınır (bağlam koşu boyunca bellekte tutulur; kimsenin Redis bağlamına
  yazılmaz).
- Veri tarafı sabittir: araç ve guided sonuçları vakanın sahte verisinden
  gelir. ClickHouse/Elasticsearch'e sorgu gitmez (istisna aşağıda).
- Model çağrıları gerçektir: seçtiğin iki profilin modelleri. Tüm çağrılar
  mevcut profil/sağlayıcı ayarından, kendi barındırdığın uç noktaya gider.
- Her turun beklentisi yapısaldır, kelime eşleşmesi yoktur: hangi veri
  okunabilir/okunmalı, tur hangi servise ve pencereye çözülmeli, durum
  turunda en çok kaç cümle, cevapta sahte veride olmayan sayı var mı.

## Nasıl koşulur (iki profil)

1. Ayarlar › CoSRE › Değerlendirme ekranını aç (`/settings/ai?tab=eval`).
   Ekran yalnız admin rolüne açıktır.
2. "Akıcılık seti (konuşma)" satırında iki profili seç: Profil A ve Profil B
   (ör. varsayılan yerel model ile denemek istediğin ikinci aile).
3. "İki profille koş" düğmesine bas. Önce A profilinin 22 konuşması, sonra B
   profilininki koşar; koşu arka planda sürer, sayfadan ayrılmak durdurmaz.
4. Koşu bitince koşu geçmişinde seç ve "Akıcılık raporunu indir (.md)"
   düğmesine bas. İnen dosyayı `docs/cosre/eval/fluency-<YYYY-MM-DD>.md`
   adıyla kaydet.

Uç nokta karşılıkları (aynı yetki, admin):

```
POST /api/ai/evalset/runs
     {"surfaces":["ChatConversation"],"profiles":["<profil-A>","<profil-B>"]}
GET  /api/ai/evalset/runs/<koşu-id>/fluency-report      → text/markdown
```

Tek profil de verilebilir (`"profiles":["<profil>"]`); profil verilmezse
üretimin sohbet profili kullanılır. En çok iki profil kabul edilir.

## Beklenen model yükü

- Profil başına 49 sohbet turu, iki profilde yaklaşık 100 tur. Her tur gerçek
  sohbet gibi bir ya da birkaç model çağrısı yapar (niyet sınıflandırıcı,
  guided anlatım ya da araç döngüsü turları).
- Koşu sıralıdır (aynı anda tek tur), üretim sohbetiyle aynı model uç
  noktasını paylaşır; yoğun saatlerde koşma. Koşu tavanı 180 dakikadır.
- Set "Tümü" koşusuna ve günlük otomatik koşuya GİRMEZ; yalnız bu düğmeyle
  (ya da `ChatConversation` yüzeyi açıkça seçilerek) koşar.
- Çağrılar /ai sayfasında "Kaynak: Değerlendirme" altında `evalset-` önekiyle
  görünür; üretim sayılarına karışmaz.

## Rapor nasıl okunur

- Her vaka ve tur için iki profilin cevabı yan yana; altında kademe, okunan
  veri, tur/araç/token ve yapısal ihlaller.
- Sonda profil başına ölçüt tablosu: kalıp açılış/kapanış oranı (audit §4
  S2 sorgusunun kalıp listesi, kodda tek yer:
  `internal/ai/evalrubric/fluency.go`), ortalama cümle ve karakter, madde
  listeli cevap payı, soruyla biten cevap payı, cevaba kadar model turu ve
  araç çağrısı, tur başına giriş/çıkış token'ı, ilk token süresi, ve
  "doğrulayıcıdan geçen pay": gölge cevap doğrulayıcısının (E1) sıfır
  uyuşmazlık saydığı turların payı; payda yalnız model anlatımı olan turlar.
  Türetilen değer (kanıttaki iki değerin toplamı / farkı / oranı) geçmeyi
  bozmaz, ayrı satırda sayılır. Tur başına sayaçlar ve uyuşmayan değerler
  "Doğrulayıcı" satırında. Koşu bu sayımı ayardan bağımsız yapar (yalnız
  sayaç; cevap değişmez).
- Puanlamayı sen yaparsın; tablo yalnız otomatik sayımdır.

## Sınırlar

- Rota turu, operasyon ön-getirmesi, kaynak keşfi ve wiki/RAG gibi LLM'siz
  kademeler canlı katalogdan ad okumaya devam eder (mevcut sınırlı okumalar;
  araç ve guided verisi sahte olduğu için Elasticsearch'e sorgu gitmez).
  Nötr adlar canlı katalogda yoksa bu kademeler boş döner ve zincir sürer;
  canlıda aynı adla bir servis ya da operasyon varsa o turun kademesi farklı
  olabilir — raporda her turun kademesi yazılıdır. Uç nokta seçici kartının
  satırları sahteden gelir (vakada yoksa kart çıkmaz).
- Koşu isteği saat dilimini sabitler (`Europe/Istanbul`); takvim
  beklentileri çözücünün kuralıyla bu dilimdedir: "dün" dünün yerel takvim
  günü, "bu sabah" 06:00–12:00 (şimdiye kırpılır; 06:00'dan önce dünün
  sabahı), sabah penceresinden sonra çıplak "dün" dün sabah. Bağlam araçları
  (`set_context` vb.) v2'de durum açıkken katalogda yoktur; hiçbir beklenti
  onları anmaz.
- Doğrulayıcı yoklamaları (`verifier: pass | flag`): "pass" turunda gölge
  doğrulayıcı uyuşmazlık saymamalı; "flag" turu sahtede olmayan bir değeri
  sorar — model bir sayı uydurursa doğrulayıcı onu yakalamalı.
