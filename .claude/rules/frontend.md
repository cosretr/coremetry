---
paths:
  - "frontend/src/**"
---
# Önyüz kuralları (yalnız `frontend/src/` altında çalışırken yüklenir)

- `components/ui` DONUK (ratchet yalnız aşağı). Yeni parça `components/coremetry` ya da `components/shadcn`.
- `styles/globals.css` SONUNA CSS eklenmez; yeni kural ilgili bölüme ya da bileşenin kendi dosyasına.
- Sayı, süre, yüzde, bayt ve zaman yalnız `lib/format/` üzerinden (`formatUnityRatchet`); ham `toFixed`,
  `toLocale*`, `new Intl` yazılmaz.
- Yeni ürün metni TR + EN kataloğa (`i18nLeakRatchet`); yönetim ekranları (`/admin`, Settings, `/users`) çevrilmez.
- Açıklama paragrafı yerine boş durum ya da `HelpTip` (`helpParagraphRatchet`).
- Entity bağlantısı `EntityLink` ile (`entityLinkRatchet`); sağlık noktası tek `HealthDot`, yalnız açık Problem'den.
- Tablo ve sekme gövdesi sayfa grisi üstünde çıplak kalmaz: beyaz yüzey (kart). Her sekme ekran görüntüsüyle bakılır.
- Dört tablo türü ve seçici kuralları: kök `CLAUDE.md` "Hard constraints" + `/frontend-design-system`.
- Giriş paketi ve CSS bütçesi (`npm run bundle:check`) aşılırsa bütçe yükseltilmez; dur ve raporla.
