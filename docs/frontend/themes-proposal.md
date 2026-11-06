# Yeni temalar: öneri ve maliyet (2026-11-06)

Durum: ONAYLI (operatör kararı 2026-11-06, son bölüm). Uygulama v0.10.1847-1851 (§7 kayıt spec'i). Mevcut üç temanın token değerleri değişmez. Yan yana önizleme: `docs/frontend/mockups/themes-preview.html` (8 çerçeve: 3 mevcut + 5 aday).

## 1. Özet ve öneri

- Beş aday hazır; hepsi shadcn modelinde (nötr taban ölçeği + tek birincil renk + anlamsal durum renkleri + seri paleti), bu ürünün token adlarıyla. shadcn takma adları (`--background`, `--primary`, `--ring`…) `var()` ile türediği için tema başına shadcn işi yoktur (§2).
- Önerilen sıra: **(1) Arduvaz/Slate → (2) Yüksek Kontrast → (3) Kağıt/Stone**; operatör beğenirse **(4) Mor/Violet**; **Yüksek Kontrast Açık** ertelenir.
  - Slate ilk: yapısı `dark` ile aynı (mavi vurgu, bilgi = vurgu), en az risk; "daha yumuşak koyu" boşluğunu kapatır. İlk yeni KOYU tema olduğu için §3'teki koyu-tema tuzaklarını bir kez çözer.
  - Yüksek Kontrast ikinci: işlevsel değeri en yüksek (NOC duvarı, erişilebilirlik). Vurgu üstü yazı ilk kez siyah; üç sabit beyazı temizletir.
  - Stone üçüncü: tek sıcak açık tema, ama iki tasarım riski taşır (turuncu vurgu durum ailesine yakın; ısı haritası rampası). Önizlemede görülüp karar verilmeli.
  - Violet: en ucuz aday (durum değerleri `dark` ile aynı), işlevsel katkısı en düşük; ayrışan bir marka vurgusu isteniyorsa.
- 5. aday gerekçesi: "Mist/gri-mavi açık" zaten bugünkü `light` (#f4f6f8 + mavi). "Forest/zümrüt" birincil rengi K5 ile çakışır (yeşil yalnız "düzeldi", docs/DECISIONS.md:686). Gerçek boşluk açık kutuplu erişilebilirlik (aydınlık oda, projeksiyon, ekran paylaşımı): **Yüksek Kontrast Açık**.
- Seçicide ad (id → etiket): `slate` → Arduvaz, `stone` → Kağıt, `violet` → Mor, `contrast` → Yüksek Kontrast, `contrast-light` → Yüksek Kontrast Açık. Mevcut etiketler `Dark` / `Light` / `Kurumsal` (lib/theme.ts:14-16). Liste "Açık" ve "Koyu" başlıklarıyla gruplanır.

## 2. Mevcut tema sistemi

Yollar `frontend/src/` altındadır (`index.html`: `frontend/`).

| Parça | Dosya:satır | Adet / not |
|---|---|---|
| Tema blokları | styles/globals.css:7-8 dark (`[data-theme="dark"], :root`), :258 light, :335 redhat | dark 87 özellik: 44 renk/gölge + 4 radius + 20 geometri/font + 19 z-index |
| Bir temanın elle yazdığı | light :258-319; redhat :335-399 | light 42 token + `color-scheme`; redhat +4 radius = 46. `--on-accent`, `--backdrop`, radius, sp/fs/font/z dark'tan miras |
| Türetilenler | globals.css:29,105 | `--field-bg` (`var`), dark `--accent-soft` (`color-mix`); `--bg` = `--bg1` (testle) |
| Kapsamlı remap | globals.css:405-432 `[data-theme="redhat"] #sidebar` | 18 token + `color-scheme: dark`; üründe başka remap yok |
| Seri paleti (CSS) | styles/tokens.css:62-69 `--series-1..8` | 2 palet: dark; light = redhat |
| Seri paleti (JS aynası) | lib/chartFmt.ts:152,160 `SERIES_PALETTES` | canvas `var()` okuyamaz; eşitlik tokens.pin.test.ts:84-92; eksik anahtar = `undefined` |
| shadcn eşlemesi | tokens.css:25-60 `:root, [data-theme]` | 16 takma ad + durum takma adları, hepsi `var()`. `--chart-1..5`, `--sidebar-*`, shadcn `--accent` bilinçli tanımsız (:98) |
| Tailwind köprüsü | tokens.css:79-157 `@theme inline reference`; :23 `@custom-variant dark` | dark varyantı yalnız `[data-theme="dark"]` tanır |
| Isı haritası | lib/chart/heatmapRamp.ts:29; LatencyHeatmap.tsx:33 | 6 durak: saydam → `--accent` α .18/.40/.65 → `--warn-solid` .80 → `--err` .90. Tema başına sabit yok |
| Canvas renkleri | components/charts/TimeChart.tsx:196-198; lib/chart/resolveVar.ts:10; lib/useThemeTick.ts:24 | eksen = `--text3`, ızgara = `--border`; önbellek yok, `data-theme` değişince yeniden çizer |
| Tema kaydı | lib/themeDefault.ts:7,10; lib/theme.ts:12-27,41-43,52,65 | `Theme` birliği, NEXT, GLYPH, LABEL, THEME_COLOR, `readTheme`, THEMES: 7 sabit üçlü + `isLightTheme`; varsayılan `redhat` |
| Boya-öncesi betik | index.html:2,41,44,52 | renk haritası + üçlü ad kontrolü elle; `renderDefaultTheme` yalnız işaretçiyi değiştirir (themeDefault.ts:21) |
| Seçiciler | components/ThemeToggle.tsx:22 (döngü düğmesi, 5 yerde); Sidebar.tsx:543-555; lib/palette/localActions.ts:47-49 | son ikisi THEMES'ten türer |
| Saklama | localStorage `coremetry-theme` (theme.ts:10) | tarayıcı başına. Sunucu/kullanıcı ayarı yok; "sistem" ve `prefers-color-scheme` yok (0 eşleşme) |
| Yoğunluk | lib/density.ts:12,15 (`data-density`) | temadan bağımsız; yalnız pages/design/cells.ts:12'de çarpılır |
| Grafana köprüsü | lib/chart/dataFrame.ts:54-55; components/coremetry/Toaster.tsx:66 | `isLightTheme` → `createTheme` modu ve sonner; URL'de `theme=` parametresi yok |

## 3. Bir tema eklemenin maliyeti

**Bugün (kayıt refaktörü olmadan) bir tema = 6 kaynak dosya + en az 14 test dosyası:**
1. `globals.css`: yeni `[data-theme="x"]` bloğu, 44 renk tokenı + `color-scheme` (§4'teki bloklar).
2. `tokens.css`: `--series-1..8` için seçici (hex yalnız burada serbest, tokens.pin.test.ts:78-82).
3. `themeDefault.ts` + `theme.ts`: birlik, NEXT, GLYPH, LABEL, THEME_COLOR (= `--bg0`), `readTheme`, THEMES.
4. `index.html`: renk haritası (:44) ve ad kontrolü (:52).
5. `chartFmt.ts`: `ChartTheme` + `SERIES_PALETTES` anahtarı.
6. Sabit üçlü taşıyan testler: theme.test.ts:17, themeColor.test.ts:46,56, tokens.pin.test.ts:21-36, contrastTokens.test.ts:31-39,55, designAxe.test.tsx:53-58, seriesPalette.test.ts:14-59, corePanelContracts.test.ts:46-67, methodBadge.pin.test.ts:69-84, fieldBg.test.ts:26-66, surfaceTokens.test.ts:188, bgZeroIsPageOnly.test.ts:94-95, radiusTokens.test.ts:40-64, chartThemeMode.test.ts:24-30, colorLeaks.test.ts:188-226.
7. Elle metin: `.design-sync/conventions.md:12`, `NOTES.md:11`; design-sync yeniden derleme.

**Tuzaklar (ilk yeni koyu / yüksek kontrast temada bir kez çözülür):**
- `isLightTheme = t !== 'dark'` (theme.ts:52): yeni koyu tema "açık" sayılır; Grafana modu ve toast açık temayla çizilir.
- `@custom-variant dark` (tokens.css:23, tam metni tokens.pin.test.ts:66'da çivili) yalnız `dark` adını tanır: 26 dosyadaki 35 `dark:` sınıfı (19'u `components/shadcn`) yeni koyu temalarda sessizce devre dışı kalır.
- Çıplak `:root` = dark: blokta unutulan token sessizce dark değerini alır; contrastTokens `tok()` da `:root`'a düşer (:36-39), yani eksik token testte görünmez.
- colorLeaks dilimleri blok SIRASINA bağlıdır (`indexOf('[data-theme="redhat"]')`, :188-190, :219-220); araya eklenen blok dilimi bozar.
- Vurgu üstü sabit beyaz: Sidebar.tsx:481 (`color: '#fff'`), shadcn/button.tsx:17 ve shadcn/badge.tsx:15 (`bg-destructive text-white`). Yüksek Kontrast'ta `--on-accent` siyah olduğu için bu üçü okunmaz olur.
- Isı haritası düşük ucu `--accent`'tir: Stone'da turuncu → amber → kızıl tek renk ailesine düşer. Öneri: `--heat` tokenı (varsayılan `var(--accent)`), Stone'da `var(--info)`; önizleme böyle çizildi.

**Axe matrisi ve CI:** matris `THEMES × 3 yoğunluk` (cells.ts:12); bugün 9 hücre gece işi (design-axe-nightly.yml:19), CI'da tema başına 1 hücre (ci.yml:44-46). Tema başına gece +3, CI +1 hücre; beş temayla 24 ve 8. Tek süre verisi ölçülmemiş bir tahmindir (shadcn-phase1-plan.md:451: 3 hücre ≈ 40-90 s); doğrusal varsayımla CI'a tema başına ≈ +13-30 s. Axe `color-contrast` kuralını koşmaz, temaya özgü risk `contrastTokens` kapısındadır (ms). Öneri: CI hücreleri sabit dört temsilci olsun (varsayılan, bir açık, bir koyu, yüksek kontrast), gerisi gece işinde kalsın; CI süresi tema sayısıyla büyümez.

**N+1. temayı ucuzlatan (tek kayıt dosyası, `lib/themeRegistry.ts`):**
- Kayıt girdisi: `{ id, label, glyph, scheme: 'light'|'dark', contrast: 'aa'|'aaa', themeColor, series }`. Birlik, THEMES, LABEL, THEME_COLOR, `SERIES_PALETTES` ve `isLightTheme` (artık `scheme`) buradan türer.
- `<html>` üstünde `data-theme` yanında `data-scheme`; `@custom-variant dark` şemaya bağlanır. Grafana modu ve sonner şemayı okur.
- `index.html` haritası ve ad kontrolü, zaten var olan `renderDefaultTheme` adımında kayıttan üretilir.
- Testler kayıt üzerinde döner (`describe.each`); bloklar ada göre genel regex ile ayıklanır; yeni "tamlık" kapısı her bloğun 44 tokenı açıkça yazmasını ister. `contrastTokens` eşikleri `contrast` alanına göre seçilir.
- Seri paleti kutba bağlanabilir: bugün light = redhat zaten çivili. Ölçüm: `dark` paleti Slate'te en az 3.42, Violet'te 3.85 (eşik 3) verir, yani bu ikisi yeni palet olmadan da çıkabilir; Stone'da `light` paleti 3.01 ile sınırda, yüksek kontrast temalar kendi paletini ister (4.30 ve 3.11 < 4.5).
- Sonuç: refaktörden sonra tema = 1 CSS bloğu (44 token + 8 seri) + 1 kayıt girdisi; test dosyası düzenlenmez.

## 4. Aday temalar

Ortak: durum renkleri her temada aynı anlamı taşır (ok yeşil, uyarı amber, hata kırmızı, bilgi mavi) ve her yerde ikon + metinle gelir. Marka kırmızısı yalnız logoda. Radius ve geometri dark'tan miras (4/6/8/12).

- **Arduvaz (`slate`, koyu):** shadcn slate tabanı, mavi vurgu. Zemin #0f172a; zinc-siyahtan (#09090b) belirgin biçimde açık ve soğuk, uzun oturum için.
- **Kağıt (`stone`, açık):** sıcak kağıt nötrü, kiremit-turuncu vurgu (ton 17°). Uyarı altın (43°), hata kızıl (347°): vurgudan 26° ve 30° ayrık. Bilgi ayrı bir mavi (#1e5aa8); bağlantı rengi koyu kiremit.
- **Mor (`violet`, koyu):** hafif mor alt tonlu koyu nötr, mor vurgu (shadcn violet). Durum renkleri `dark` ile aynı; bilgi mavi kalır.
- **Yüksek Kontrast (`contrast`, koyu):** siyah zemin, beyaz metin, tüm metin ve durum renkleri AAA (≥ 7), `--border` 3:1, form kenarı 4.5:1, beyaz odak halkası, vurgu üstünde siyah yazı.
- **Yüksek Kontrast Açık (`contrast-light`, açık):** beyaz kart, siyah metin, aynı AAA eşikleri; koyu kenar çubuğu yok.

```css
/* slate · koyu · THEME_COLOR #0f172a */
--bg0:#0f172a; --bg1:#172135; --bg2:#1e293b; --bg3:#273449; --bg:#172135; --field-bg:var(--bg2); --border:#334155; --divider:#263247; --border-control:#475569; --border-strong:#64748b;
--text:#f1f5f9; --text2:#cbd5e1; --text3:#9ba9bd; --text-faint:#6b7b93; --brand:#E30613; --brand2:#ff2333; --accent:#2563eb; --accent2:#60a5fa; --accent-hover:#1d4ed8; --focus:#60a5fa; --on-accent:#ffffff; --accent-soft:color-mix(in srgb, var(--accent) 16%, transparent); --accent-bg:#1c2f52; --accent-border:#2a4a85;
--ok:#4ac98a; --warn:#f5b83d; --warn-solid:#f5b83d; --err:#fa8080; --err-solid:#dc2626; --fatal:#fa8080; --info:#60a5fa; --ok-bg:#17352e; --warn-bg:#3a321f; --err-bg:#3d2630; --info-bg:#1c2f52;
--purple:#b197fc; --indigo:#818cf8; --orange:#fb923c; --teal:#2dd4bf; --shadow-sm:0 1px 2px rgba(2,6,23,.35); --shadow-md:0 6px 20px rgba(2,6,23,.5); --shadow-pop:0 8px 24px rgba(2,6,23,.55); --shadow-modal:0 20px 60px rgba(2,6,23,.6); --backdrop:rgba(2,6,23,.6); color-scheme:dark;
--series-1:#4f9cf0; --series-2:#2ec4c6; --series-3:#e06a94; --series-4:#c2bd3a; --series-5:#9db4ff; --series-6:#e58a3c; --series-7:#b18ae0; --series-8:#f0a3dd;
/* stone · açık · THEME_COLOR #f4f2ee · ısı haritası düşük ucu --info */
--bg0:#f4f2ee; --bg1:#fdfcfa; --bg2:#f1eee9; --bg3:#e6e2db; --bg:#fdfcfa; --field-bg:var(--bg1); --border:#d9d5cd; --divider:#e3e0db; --border-control:#b0a9a1; --border-strong:#8c857d;
--text:#1c1917; --text2:#44403c; --text3:#645e58; --text-faint:#8a837c; --brand:#E30613; --brand2:#ff2333; --accent:#c2410c; --accent2:#9a3412; --accent-hover:#9a3412; --focus:#c2410c; --on-accent:#ffffff; --accent-soft:color-mix(in srgb, var(--accent) 10%, transparent); --accent-bg:#fbeee4; --accent-border:#f0c8a8;
--ok:#166534; --warn:#7c5800; --warn-solid:#b07d00; --err:#b4173a; --err-solid:#be123c; --fatal:#8f1239; --info:#1e5aa8; --ok-bg:#e9f2ea; --warn-bg:#f7f0dd; --err-bg:#f9ebec; --info-bg:#e8f0f9;
--purple:#7c4fd0; --indigo:#5a5fd0; --orange:#c2570a; --teal:#0f766e; --shadow-sm:0 1px 2px rgba(41,37,36,.06); --shadow-md:0 4px 16px rgba(41,37,36,.09); --shadow-pop:0 8px 24px rgba(41,37,36,.14); --shadow-modal:0 20px 60px rgba(41,37,36,.18); --backdrop:rgba(41,37,36,.4); color-scheme:light;
--series-1:#1f6fa8; --series-2:#0d8f8a; --series-3:#a3245a; --series-4:#7d7a08; --series-5:#5a6fd0; --series-6:#b85c0a; --series-7:#7a4aa6; --series-8:#b85aa3;
/* violet · koyu · THEME_COLOR #0e0d13 */
--bg0:#0e0d13; --bg1:#17161e; --bg2:#1f1e28; --bg3:#282733; --bg:#17161e; --field-bg:var(--bg2); --border:#3b3a49; --divider:#2a2935; --border-control:#514f63; --border-strong:#6f6d85;
--text:#f5f4fa; --text2:#d2d0dc; --text3:#a5a3b5; --text-faint:#72708a; --brand:#E30613; --brand2:#ff2333; --accent:#7c3aed; --accent2:#a78bfa; --accent-hover:#6d28d9; --focus:#a78bfa; --on-accent:#ffffff; --accent-soft:color-mix(in srgb, var(--accent) 16%, transparent); --accent-bg:#2a2147; --accent-border:#47358a;
--ok:#4db96b; --warn:#f3b94c; --warn-solid:#f3b94c; --err:#fe7f73; --err-solid:#cc4740; --fatal:#fe7f73; --info:#6ba6fb; --ok-bg:#22362c; --warn-bg:#3a3526; --err-bg:#3d2a2c; --info-bg:#212d42;
--purple:#c084fc; --indigo:#818cf8; --orange:#e8853a; --teal:#39c5cf; --shadow-sm:0 1px 2px rgba(0,0,0,.30); --shadow-md:0 6px 20px rgba(0,0,0,.45); --shadow-pop:0 8px 24px rgba(0,0,0,.50); --shadow-modal:0 20px 60px rgba(0,0,0,.55); --backdrop:rgba(0,0,0,.55); color-scheme:dark;
--series-1:#4a9fe0; --series-2:#22c3c5; --series-3:#e2678f; --series-4:#bcb832; --series-5:#9bb5ff; --series-6:#e08535; --series-7:#c493f5; --series-8:#f5a8e2;
/* contrast · koyu · THEME_COLOR #000000 */
--bg0:#000000; --bg1:#0a0a0a; --bg2:#141414; --bg3:#1f1f1f; --bg:#0a0a0a; --field-bg:var(--bg2); --border:#666666; --divider:#202020; --border-control:#7d7d7d; --border-strong:#9e9e9e;
--text:#ffffff; --text2:#e6e6e6; --text3:#c4c4c4; --text-faint:#8f8f8f; --brand:#E30613; --brand2:#ff2333; --accent:#6cb6ff; --accent2:#7cc0ff; --accent-hover:#9ccfff; --focus:#ffffff; --on-accent:#000000; --accent-soft:color-mix(in srgb, var(--accent) 20%, transparent); --accent-bg:#0b2440; --accent-border:#3d7fc0;
--ok:#5ee08a; --warn:#ffcf40; --warn-solid:#ffcf40; --err:#ff9489; --err-solid:#ff6b6b; --fatal:#ff9489; --info:#7cc0ff; --ok-bg:#0c2a18; --warn-bg:#2e2500; --err-bg:#330f0c; --info-bg:#0b2440;
--purple:#c9a6ff; --indigo:#a9b4ff; --orange:#ffa257; --teal:#4fe0d0; --shadow-sm:0 1px 2px rgba(0,0,0,.60); --shadow-md:0 6px 20px rgba(0,0,0,.75); --shadow-pop:0 8px 24px rgba(0,0,0,.80); --shadow-modal:0 20px 60px rgba(0,0,0,.85); --backdrop:rgba(0,0,0,.75); color-scheme:dark;
--series-1:#6cb6ff; --series-2:#4fe0d0; --series-3:#ff8fb3; --series-4:#e6e04a; --series-5:#b3c4ff; --series-6:#ffa257; --series-7:#cfa0ff; --series-8:#ffb3ec;
/* contrast-light · açık · THEME_COLOR #f2f2f2 */
--bg0:#f2f2f2; --bg1:#ffffff; --bg2:#f5f5f5; --bg3:#e6e6e6; --bg:#ffffff; --field-bg:var(--bg1); --border:#858585; --divider:#e4e4e4; --border-control:#707070; --border-strong:#545454;
--text:#000000; --text2:#1f1f1f; --text3:#424242; --text-faint:#666666; --brand:#E30613; --brand2:#ff2333; --accent:#0b4bb5; --accent2:#0a3f99; --accent-hover:#083378; --focus:#0b4bb5; --on-accent:#ffffff; --accent-soft:color-mix(in srgb, var(--accent) 10%, transparent); --accent-bg:#e3edfb; --accent-border:#7da2e0;
--ok:#0f5323; --warn:#5c4300; --warn-solid:#a87900; --err:#8f1111; --err-solid:#8f1111; --fatal:#6b0d2a; --info:#0a3f99; --ok-bg:#e3f3e6; --warn-bg:#f8efd2; --err-bg:#fbe6e4; --info-bg:#e3edfb;
--purple:#5b2fb0; --indigo:#3f3fb5; --orange:#8a3d00; --teal:#0b5c58; --shadow-sm:0 1px 2px rgba(0,0,0,.10); --shadow-md:0 4px 16px rgba(0,0,0,.16); --shadow-pop:0 8px 24px rgba(0,0,0,.22); --shadow-modal:0 20px 60px rgba(0,0,0,.28); --backdrop:rgba(0,0,0,.5); color-scheme:light;
--series-1:#0b5a94; --series-2:#0a6e6a; --series-3:#8c1848; --series-4:#5e5c00; --series-5:#3f51b5; --series-6:#8a4500; --series-7:#63378f; --series-8:#96307f;
```

**Kontrast (WCAG 2.1 göreli ışıklık; betik çıktısı).** Eşik sütunu: `contrastTokens.test.ts` eşiği · yüksek kontrast (YK) temalar için bu öneride uygulanan sıkı eşik.

| Çift | Eşik (repo · YK) | Slate | Stone | Violet | YK | YK Açık |
|---|---|---|---|---|---|---|
| `text` / bg0 · bg1 · bg2 · bg3 | ≥ 7 · ≥ 15 | 16.30 · 14.68 · 13.35 · 11.45 | 15.64 · 17.06 · 15.11 · 13.55 | 17.69 · 16.41 · 15.07 · 13.45 | 21.00 · 19.80 · 18.42 · 16.48 | 18.76 · 21.00 · 19.26 · 16.83 |
| `text2` en düşük (bg0-3) | ≥ 4.5 · ≥ 7 | 8.45 | 7.96 | 9.66 | 13.21 | 13.21 |
| `text3` en düşük (bg0-3) | ≥ 4.5 · ≥ 7 | 5.26 | 4.95 | 5.96 | 9.45 | 8.05 |
| `text3` en düşük (durum/vurgu -bg) | ≥ 4.5 | 5.32 | 5.52 | 4.95 | 8.72 | 8.39 |
| `text-faint` / bg1 (en düşük bg0-2) | ≥ 3 · ≥ 4.5 | 3.74 (3.40) | 3.65 (3.23) | 3.76 (3.45) | 6.12 (5.70) | 5.74 (5.13) |
| `accent2` en düşük (bg0-3) | ≥ 4.5 · ≥ 7 | 4.94 | 5.66 | 5.40 | 8.51 | 7.70 |
| `on-accent` / accent · hover · err-solid | ≥ 4.5 · ≥ 7 | 5.17 · 6.70 · 4.83 | 5.18 · 7.31 · 6.29 | 5.70 · 7.10 · 4.62 | 9.77 · 12.78 · 7.57 | 7.81 · 11.97 · 9.31 |
| `ok`: en düşük bg0-3 · kendi -bg | ≥ 4.5 · ≥ 7 | 5.98 · 6.32 | 5.52 · 6.23 | 5.93 · 5.19 | 9.82 · 9.19 | 7.37 · 7.99 |
| `warn`: en düşük bg0-3 · kendi -bg | ≥ 4.5 · ≥ 7 | 7.05 · 7.13 | 5.00 · 5.68 | 8.30 · 6.90 | 11.19 · 10.32 | 7.45 · 8.09 |
| `err`: en düşük bg0-3 · kendi -bg | ≥ 4.5 · ≥ 7 | 5.06 · 5.57 | 5.21 · 5.80 | 5.95 · 5.43 | 7.73 · 8.11 | 7.46 · 7.77 |
| `info`: en düşük bg0-3 · kendi -bg | ≥ 4.5 · ≥ 7 | 4.94 · 5.23 | 5.28 · 5.92 | 5.93 · 5.58 | 8.51 · 8.09 | 7.70 · 8.14 |
| `text` en düşük (durum -bg) | ≥ 7 | 11.58 | 15.09 | 11.18 | 15.21 | 17.54 |
| `divider` / bg1 · bg2 | 1.15-1.30 · ≥ 1.07 | 1.25 · 1.14 | 1.28 · 1.14 | 1.25 · 1.15 | 1.22 · 1.13 | 1.27 · 1.17 |
| `border` / bg1 | divider'dan güçlü · YK ≥ 3 | 1.55 | 1.43 | 1.61 | 3.45 | 3.69 |
| `border-control` / bg1 · bg2 | ≥ 2 · ≥ 1.75 (YK bg1 ≥ 4.5) | 2.12 · 1.93 | 2.27 · 2.01 | 2.26 · 2.08 | 4.81 · 4.48 | 4.95 · 4.54 |
| `border-strong` / bg1 | ≥ 3 · ≥ 7 | 3.38 | 3.55 | 3.59 | 7.39 | 7.57 |
| `focus` en düşük (bg0-3) | ≥ 3 | 4.94 | 4.01 | 5.40 | 16.48 | 6.26 |
| `warn-solid` en düşük (bg0-2) | ≥ 3 | 8.22 | 3.14 | 9.30 | 12.50 | 3.47 |
| Seri paleti en düşük (bg0-2) | ≥ 3 · ≥ 4.5 | 4.64 | 3.42 | 5.15 | 8.57 | 5.44 |

- Sonuç: beş adayda da hem repo eşiklerinde hem kendi katmanında 0 hata. Betik yalnız üç `--divider` değerini oynattı (Stone #e7e4df → #e3e0db, YK #1e1e1e → #202020, YK Açık #e3e3e3 → #e4e4e4); bloklar son değerleri taşır.
- Sağlama: aynı betik mevcut üç temada repo çiftlerinin hepsini geçirdi (dark / light / redhat: 0 hata), yani çift listesi bugünkü yeşil kapıyla tutarlı.
- En dar paylar: Violet `on-accent`/err-solid 4.62 (dark ile aynı değer), Stone `warn-solid` 3.14 ve seri 3.42, Slate `accent2` ve `info` 4.94.

## 5. Uygulama planı

Her dilim tek sürüm; etiket işi kimin yapacağını söyler.

1. **[tasarım]** Operatör önizlemeden temaları seçer, §6 sorularını kapatır. Kod yok.
2. **[tasarım]** Tema kaydı için kısa spec: girdi şekli, `data-scheme`, testlerin kayıt üzerinde dönmesi, blokların dosya yeri, CI temsilci hücreleri.
3. **[mekanik → implementer]** Kayıt refaktörü, yeni tema YOK. Kabul: üç temada token değerleri birebir aynı, görsel fark sıfır, mevcut pin'ler yeşil, tamlık kapısı eklendi.
4. **[mekanik → implementer]** Vurgu üstü sabit beyazların `--on-accent` / `destructive-foreground`'a çevrilmesi (3 yer). Mevcut temalarda görsel fark yok.
5. **[mekanik → implementer]** Slate.
6. **[mekanik → implementer]** Yüksek Kontrast (`contrast: 'aaa'` katmanıyla).
7. **[tasarım → mekanik]** Stone, `--heat` kararıyla birlikte.
8. **[mekanik → implementer]** Violet (seçilirse).
9. **[tasarım → mekanik]** Yüksek Kontrast Açık ve isteğe bağlı `prefers-contrast` (seçilirse).

Tema sürümü kabul ölçütleri:
- Blok 44 tokenı + 8 seriyi açıkça yazar; `contrastTokens` temanın katmanında yeşil; seri CSS = JS; `meta theme-color` = `--bg0`.
- Seçicide etiket ve grup doğru; koyu temada `dark:` sınıfları, Grafana modu ve toast koyu çalışır.
- /design kataloğu temanın hücrelerinde hatasız; Services, servis detayı, Traces, trace şelalesi ve Settings üç yoğunlukta gözle geçilir.
- Mevcut üç temanın pin'leri değişmeden geçer.

## 6. Açık sorular (önerilen varsayılanla)

1. **"Sistem" seçeneği olsun mu, hangi çiftle?** Bugün yok. Varsayılan: şimdilik eklenmez, varsayılan tema `redhat` kalır. Eklenecekse çift Kurumsal ↔ Koyu (ikisi de prod onaylı); davranış değişikliği olduğu için ayrı onay.
2. **Kullanıcı başına mı, genel varsayılan mı?** Bugün tarayıcı başına localStorage + derleme sabiti. Varsayılan: aynen kalır; sunucu ayarı istenirse ayrı iş (`system_settings` anahtarı + API).
3. **Redhat'in koyu kenar çubuğu açık adaylara uygulansın mı?** Varsayılan: hayır. Koyu ray redhat kimliğidir (K4); her remap ikinci bir token bloğu ve kontrast kapısında ayrı bölüm demektir.
4. **Üst şeritteki döngü düğmesi.** 6-8 temada döngü kullanışsız. Varsayılan: düğme, kenar çubuğundaki listeyi açar. Davranış değişikliği: onay gerekir.
5. **Stone'un birincil rengi.** Varsayılan: brief'teki turuncu + `--heat` = bilgi mavisi. Alternatif: sıcak zemin + mavi birincil (durumla karışma riski sıfır, ama `light`'a yaklaşır).
6. **Yüksek Kontrast'ta `--divider`.** Varsayılan: repo bandı (1.15-1.30) korunur, güç `--border` (3:1) ve form kenarından gelir. Alternatif: bu katmanda bant gevşetilir, satır çizgileri güçlenir.
7. **Slate ve Violet'in paleti.** Varsayılan: `dark` paletini paylaşırlar (ölçüldü, §3); önizlemedeki kendi paletleri seçenek olarak durur.
8. **`prefers-contrast: more` Yüksek Kontrast'ı kendiliğinden seçsin mi?** Varsayılan: hayır, yalnız elle seçim.
9. **Yüksek Kontrast odak halkası.** Varsayılan beyaz; alternatif sarı (uyarı rengiyle karışabilir).

## 7. Kayıt spec'i (v0.10.1847)

Amaç: tema = 1 kayıt girdisi + 1 CSS bloğu + seri seçicisinde 1 kimlik; test dosyası düzenlenmez.

**Girdi şekli** (`frontend/src/lib/themeRegistry.ts`, bağımlılıksız; vite.config.ts `themeDefault.ts` üzerinden yükler):

| Alan | Tip | Kullanan |
|---|---|---|
| `id` | `'dark' \| …` (birlik `Theme` buradan) | `data-theme`, localStorage `coremetry-theme` |
| `label` | metin | seçici, ⌘K, kullanıcı menüsü |
| `glyph` | tek karakter | üst şerit düğmesi |
| `scheme` | `'light' \| 'dark'` | `data-scheme`, `isLightTheme`, Grafana `createTheme` modu, sonner |
| `contrast` | `'aa' \| 'aaa'` | `contrastTokens` eşik katmanı |
| `series` | `SERIES_PALETTE_SETS` anahtarı | `SERIES_PALETTES` (canvas), tokens.css aynası |
| `themeColor` | hex = bloğun `--bg0` | `meta theme-color`, açılış betiği |
| `ciCell` | boolean | /design axe CI hücresi (gece işi hepsini koşar) |

Kayıt sırası = döngü sırası = grup içi liste sırası. `THEMES`, `THEME_LABEL`, `THEME_GLYPH`, `THEME_COLOR`, `THEME_NEXT`, `readTheme`, `SERIES_PALETTES`, /design hücreleri ve ⌘K tema eylemleri kayıttan türer.

**`data-scheme`:** `<html>` üstünde `data-theme` ile birlikte yazılır (açılış betiği, `applyTheme`, `cycleTheme`; /design hücrelerinde `themeScopeAttrs`). `@custom-variant dark` tema adına değil `[data-scheme="dark"]`'a bağlanır; öznitelik yoksa `:root` = koyu (eski kural). `<html>` öğesinde statik `data-scheme` yok: betik ilk boyamadan önce yazar (singleSource pin'i `<html>` etiketini çiviliyor).

**Açılış betiği:** `index.html` renk ve şema haritalarını yer tutucuyla taşır (`__COREMETRY_THEME_COLORS__`, `__COREMETRY_THEME_SCHEMES__`); `renderDefaultTheme` (Vite `transformIndexHtml`) kayıttan doldurur, eksik yer tutucuda fırlatır. Ad kontrolü harita üyeliğidir (`hasOwnProperty`). Statik `<meta name="theme-color">` varsayılan temanın rengi olarak kalır (themeColor.test çiviliyor).

**Blokların yeri:** hepsi `styles/globals.css`'te. Koyu blok `[data-theme="dark"],\n:root` (öznitelik yoksa geri dönüş); diğerleri `[data-theme="<id>"] {` kök seviyede, redhat `#sidebar` remap'inden SONRA, kayıt sırasıyla (colorLeaks/radiusTokens dilimleri light→redhat sırasına bağlı; araya blok girmez). Seri paleti `styles/tokens.css`'te tek kural; tema kimliği o paletin seçici listesine eklenir (Arduvaz `dark` paletinin seçicisine).

**Testler kayıt üzerinde döner:** `styles/themeBlocks.ts` (saf kapı yardımcısı) bloğu kimlikle bulur (`themeBlockBody`), tamlık token listesini (`THEME_TOKENS`) taşır. Kayıt üzerinde dönenler: contrastTokens, tokens.pin, themeColor, theme, theme.singleSource.pin, seriesPalette, chartThemeMode, fieldBg, methodBadge.pin, k5HealthNeutral.pin, corePanelContracts, localActions, designAxe. Değer pin'leri (redhatPalette, zincDark, bgZeroIsPageOnly, surfaceTokens D7 tablosu, radiusTokens) mevcut temalarda aynen kalır. Kontrast eşikleri `contrast` alanından seçilir ('aaa' katmanı v0.10.1850 ile gelir).

**Tamlık kapısı** (`styles/themeRegistry.pin.test.ts`): kayıttaki her temanın tam bir bloğu var, kayıtsız blok yok, `:root` yalnız `dark` ile ortak; blok `THEME_TOKENS`'in hepsini açıkça yazar (44 + v0.10.1844 `--sig-*` = 47; light/redhat artık `--on-accent` ve `--backdrop`'u da yazar, değer `:root` ile aynı); `color-scheme` = `scheme`; `themeColor` = `--bg0`; tema tam bir tokens.css seri kuralında, 8 yuva = `SERIES_PALETTES[id]` = `SERIES_PALETTE_SETS[series]`.

**CI temsilci hücreleri:** `ciCell: true` = dark, light, redhat (bugünkü 3 hücre aynen); Yüksek Kontrast eklendiğinde dördüncü temsilci olur, Arduvaz ve Kağıt yalnız gece işinde.

## Doğrulanmayanlar

- Aday bloklar gerçek `contrastTokens.test.ts` ile koşulmadı (repoda blok yok). Çiftler testin okunmasından yeniden kuruldu; betik repoda değil, oturum scratchpad'inde.
- Stone'da düzeltme döngüsü pay hedefinde salındı ve yakınsamadı; son değerler katı eşiklerin hepsini geçiyor.
- `--purple/--indigo/--orange/--teal`, gölgeler ve `--backdrop` ölçülmedi. Renk körlüğü simülasyonu, axe ve gerçek sayfalarda görsel tur yapılmadı; önizleme tarayıcıda açılmadı.
- Okunmayan pin'ler: k5HealthNeutral, paletteStep2, linkColorGate, publicBackground, tracesNeutralPalette, platformCss, fonts.pin:60 sonrası. CI süresi tahmindir.

## Operatör kararı (2026-11-06)
Operatör önizlemeyi gördü: "Ok temalar." Önerilen plan onaylı: önce tema kaydı düzenlemesi (görsel değişiklik sıfır) ve dolgu üstü sabit beyazların temizliği, sonra sırayla **Arduvaz (Slate) → Yüksek Kontrast → Kağıt (Stone)**. Mor ve Yüksek Kontrast Açık hazır tasarım olarak bekler; istenirse birer sürümle eklenir. Açık sorular önerilen varsayılanlarla: "sistem" seçeneği eklenmez, varsayılan tema değişmez; tercih tarayıcı başına kalır; açık adaylarda koyu kenar çubuğu yok; tema düğmesi liste açar; Kağıt vurgusu turuncu + `--heat` = bilgi mavisi; Arduvaz `dark` seri paletini paylaşır; `prefers-contrast` otomatik seçim yapmaz; Yüksek Kontrast odak halkası beyaz.
