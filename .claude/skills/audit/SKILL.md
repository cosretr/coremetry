---
name: audit
description: 'Faz 0 audit: mevcut durumu ölçen, karar isteyen tek belge; kod değişmez. WHEN: bir alan ya da program için ölçüm + seçenekler + dilim planı istenir ("audit", "denetim", "Faz 0"). WHEN NOT: tek özelliğin tarifi (/spec), kök nedeni belli hata (/bugfix), tek yavaş yüzey (/perf-triage), tüm repo ölçek taraması (/scale-audit).'
---

# /audit — Faz 0: ölç, seçenek sun, karar iste, dur

**/spec ile farkı:** `/spec` tek sayfalık özellik tarifidir (ne yapılacak, hangi
dosyalar); onaydan sonra aynı oturum kodlar. `/audit` mevcut durumu ölçen ve
karar isteyen belgedir; kod yazmaz, belge yazılınca durur, uygulama sonraki
oturumlarda dilim dilim yapılır.

## Çağrı biçimi

```
/audit <konu>
Amaç: <tek cümle: bu belge hangi kararı verdirmek için yazılıyor>
Önce oku: <belge yolları; boş olabilir>
Çıktı: <yol, ör. docs/<alan>/<ad>-audit.md>   [Satır sınırı: N; yazılmazsa 300]
```

Amaç ya da çıktı yolu eksikse tek soruyla iste; tahminle başlama.

## Kurallar

1. **Yalnız belge.** Kod, test, migration, ayar ve yapılandırma değişmez. Yazılan
   tek dosya çıktı belgesidir.
2. **`architect` ajanında çalışır** (CLAUDE.md "Model kademeleri": Faz 0 audit =
   ileri kademe). Ana oturum çağrıyı dört alanıyla birlikte architect'e devreder.
   Ajan bu oturumda kayıtlı değilse aynı kurallar genel ajana verilir ve bu,
   son mesajda söylenir.
3. **Keşif `repo-explorer`'a devredilir.** Architect dosya taramaz; dar, tek
   konulu sorular sorar ve dönen `dosya:satır` özetleriyle yazar. Bağımsız
   sorular aynı anda gönderilir.
4. **"Önce oku" belgeleri önce okunur.** Orada cevaplanmış soru yeniden
   araştırılmaz; atıf verilir (`belge §bölüm`). Belgedeki bir satır bugünkü kodla
   çelişiyorsa **BAYAT** diye işaretlenir (`belge:satır` → `kod:satır`); kaynak
   belge düzeltilmez, bayat satırlar çıktının sonunda listelenir.
5. **≥1000 satırlık dosya bütün okunmaz** (liste `docs/claude/large-files.md`):
   sembol grep'i + ±80 satır. `docs/DECISIONS.md` ve `docs/INCIDENTS.md` için
   başlık grep'i.
6. **Her iddia `dosya:satır` ile.** Kanıtı olmayan cümle "[V] varsayım" ya da
   "ölçülmedi" diye işaretlenir. Sayım veren her ölçüm, nasıl sayıldığını
   (komut ya da regex) bir kez yazar; sonraki koruma testi aynı tanımı kullanır.
7. **Çıktı tek belge, en fazla 300 satır** (çağıran değiştirebilir). Sığmıyorsa
   kapsam daraltılır ve dışarıda kalan "Kapsam dışı"na yazılır; ikinci belge
   açılmaz.
8. **Belge yazılınca DUR.** Dilim başlatılmaz, commit atılmaz. Son mesaj:
   belge yolu, satır sayısı ve "Operatör kararları"ndaki soruların listesi.

## Zorunlu bölümler (bu sırayla)

| Bölüm | İçerik |
|---|---|
| **Özet** | Düz cümle, madde ve tablo yok: ne ölçüldü, en önemli bulgu, önerilen karar. |
| **Bugünkü durum** | Ölçümler ve davranış, `dosya:satır` ile; önce-oku belgelerine atıflar. |
| **Seçenekler ve önerilen karar** | Her seçenek için ne, maliyet, risk. Önerilen seçenek ilk sırada, gerekçesiyle. |
| **Kapsam dışı** | Bilerek bakılmayanlar ve neden. |
| **Dilim planı** | Tablo: # · kapsam · giriş ölçütü · çıkış ölçütü · koruma (test/ratchet) · risk. Her dilim tek sürüm ve bağımsız gönderilebilir; sıra bağımlılığı açık yazılır. |
| **Operatör kararları** | Numaralı sorular. Her soru: seçenekler, öneri, cevapsız kalırsa hangi dilimin beklediği. |
| **Bayat satırlar** | Yalnız varsa (kural 4). |

Operatör cevap verince "Kararlar (operatör, tarih)" bölümünü ana oturum ekler;
audit ajanı bu bölümü yazmaz ve cevap varsaymaz.

## Adımlar

1. Çağrıyı çöz: konu, amaç, önce-oku, çıktı yolu, satır sınırı.
2. Önce-oku belgelerini oku; cevaplanmış soruları ve atıf yerlerini not et.
3. Kalan soruları yaz ve `repo-explorer`'a dağıt (dar, tek konulu).
4. Ölç: sayımlar, akışlar, mevcut koruma testleri; sayım tanımını kaydet.
5. Seçenekleri ve dilimleri çıkar; her dilimi "tek sürüm, bağımsız" ölçütüyle sına.
6. Belgeyi yaz, sonra denetle: satır sınırı, zorunlu bölümler, her iddiada
   `dosya:satır`, bayat işaretleri, numaralı sorular.
7. DUR ve son mesajı ver.

## Yapma

- Kod ya da yapılandırma değiştirme; "küçük bir düzeltme" de dahil.
- Önce-oku belgesinde yazılı bir bulguyu yeniden keşfedip kendi bulgun gibi sunma.
- Ölçmeden sayı yazma; tahmini sayıyı ölçüm gibi gösterme.
- Bir dilime iki sürümlük iş koyma; "hepsi birlikte" dilimi yazma.
- Operatöre ait kararı (veri silme, prod davranışı, güvenlik, canlı bildirim,
  mevcut bir işlevin kaldırılması) öneri cümlesine gömme: soru olarak sor.
- Belgede gerçek şirket, cluster, servis ya da host adı kullanma.
