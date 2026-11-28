# e2e — yerel ekran görüntüsü (yalnız localhost)

```
scripts/dev-shot.sh "/services?range=1h" services            # redhat + dark
scripts/dev-shot.sh /problems --themes dark --allow-console-errors
```

- Çıktı `e2e/shots/<ad>-<tema>.png` (git dışı); önceki görüntü `.prev.png`, rapor `.json`
  (konsol hataları, 4xx/5xx/iptal istekler, fark oranı, son URL, `data-theme`).
- Çıkış 1: konsol hatası > 0, giriş ekranına düşme ya da tema uygulanmaması.
- Tema `localStorage['coremetry-theme']` ile seçilir (`frontend/index.html` açılış betiği).
- İç kaydırıcılı sayfalarda görüntü yüksekliği taşma kadar uzatılır (en çok 8000 px).
- `playwright-core` çözümleme sırası ve tarayıcı: `scripts/dev-shot.sh` başlığı; indirme yapılmaz.
