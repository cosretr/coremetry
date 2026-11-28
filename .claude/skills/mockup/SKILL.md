---
name: mockup
description: 'Tek dosyalık tıklanabilir HTML mockup: docs/frontend/mockups/<ad>.html. WHEN: operatör bir sayfa ya da akış için "mockup" ister; bir audit dilimi "önce mockup" şartı koyar. WHEN NOT: ürün koduna dokunan prototip, tek bileşen ince ayarı, ekran görüntüsü isteği.'
---

# /mockup — tek dosya, ürünün belirteçleriyle, kod değişmeden

Mockup karar almak içindir: operatör bakar, düzeltir, onaylar. Ürün kodu
(`internal/`, `frontend/src/`, `migrations/`) değişmez; yazılan tek dosya
mockup'ın kendisidir.

## Dosya

- Yol: `docs/frontend/mockups/<ad>.html`. Emsaller aynı klasörde.
- **Tek dosya.** CSS ve JS dosyanın içinde; ayrı varlık dosyası yok.
- **Harici istek yok:** CDN, web yazı tipi, uzak görsel, analitik yok. Dosya ağ
  olmadan, çift tıklamayla açılır.
- **JS yalnız** sekme, tema, panel (aç/kapat) ve gezinme (mockup içi sayfa
  geçişi) içindir. Veri üretimi, zamanlayıcı, sahte istek yok.

## Görünüm

- **Belirteçler kopyalanır, uydurulmaz.** Renk, boşluk, yarıçap, gölge ve yazı
  boyutu değerleri `frontend/src/styles/tokens.css` ve
  `frontend/src/styles/globals.css` içinden aynen alınır. Orada olmayan bir
  değer gerekiyorsa mockup'a ekleme; açık karar olarak yaz.
- **İki tema, geçişli:** `redhat` ve `dark` (`data-theme`). Her ekran iki temada
  da okunur olmalı.
- **Logo kırmızı-beyaz.**
- **Kabuk** (yan menü, üst çubuk, sayfa başlığı) `docs/screenshots/` içindeki
  düzeni izler; mockup kendi kabuğunu icat etmez.
- **En küçük yazı 12px.**

## İçerik

- **Köken bilgisi varsayılan görünümde yok:** veri kaynağı, eşleşme kuralı,
  örnekleme, "capped" gibi notlar ilk bakışta görünmez; bir bilgi panelinde
  durur.
- **İç jargon ve dedektör kimliği görünür metinde yok.**
- Örnek veride gerçek şirket, cluster, servis ya da host adı kullanılmaz.

## Bitiş

Mockup yazılınca DUR. Son mesaj: dosya yolu, nasıl açılacağı, mockup'ta
verilen açık kararlar. Uygulama operatör onayından sonra, ayrı işte başlar.
