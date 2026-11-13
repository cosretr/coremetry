# Traces listesi — shadcn geçiş planı (Faz 0, 2026-11-13)

Durum: UYGULANDI (2026-11-13). Operatör mockup'ı onayladı ("mockup onay").
- S0 v0.10.2084 · S1 mockup (etiketsiz) · S2 v0.10.2088 · S3a v0.10.2090 · S3b v0.10.2093 · S4 v0.10.2095 · S6 v0.10.2096 · S7 bu kayıt.
- S5 önce DURDU (iki mevcut test değişmeden geçmiyordu); operatör iki sınırlı uyarlamaya izin verdi → S5 v0.10.2098 (DECISIONS 2026-11-13 "Traces shadcn S5").
- Kalan: Traces.tsx'te 3 satır içi stil testle çivili; EnterHint (filterBarSearch.pin) ve Chip kaldı; Traces MIGRATED olmadı.
- Şerit varsayılanı kodda p99: `frontend/src/components/traces/stripStat.ts:23` (513 p95 → 725 p50 → 1524 p99).
Operatör (2026-11-13): "Traces tablosunu taşıyabiliriz, ama çok iyi olsun."
Bağlam: DECISIONS:12046 (global shadcn), `docs/frontend/shadcn-phase1-plan.md`, tek tablo yığını kararı DECISIONS:13433-13445.
Hedef: Kullanıcı yalnız görsel tutarlılığı fark eder. Davranışta, verinin yerinde ve hızda hiçbir şey değişmez.

## 1. Bugün ne var (kanıt)
- Sayfa `frontend/src/pages/Traces.tsx`. 2250 satır, büyük dosya: grep ile okunur.
  - Liste `useDataTable<TraceRow>` (:1147, storageKey `traces-list`) + `VirtualTable` (:1748). İkisi de dondurulmuş `components/ui/DataTable/`'dan gelir. Sayfa kendi tablosunu kurmuyor.
  - Agg görünümü `traces-agg` (:959), `<table {...dt.tableProps}>` + `DataTableColgroup/Head` (:2028).
- Kolonlar `lib/traceColumns.ts:24` FIXED_COLS: Start time · Name · Service · Duration · Status · Spans.
  - Ek attr kolonları Service'ten sonra gelir (`traceColumnOrder` :1115). Varsayılanlar http.method, http.route, deployment.environment (:65), tavan 8.
  - Kullanıcı sırası: sürükle + `TracesColumnPicker` (v0.10.1516, DECISIONS:11333). Start time sabit ve stickyLeft (:1140, DECISIONS:11348).
- Sıralama SUNUCUDA (SERVER_SORTABLE :204). `persistSort:false`, varsayılan time/desc, URL anahtarı `s_traces-list`. Attr kolonlarında yalnız resize var.
- Satır: her hücre `<Link className="row-link" to={traceHref(..)} state={{from}}>` (:1775). Orta tık ve cmd-tık yerel çalışır.
  - Hover'da `api.trace(id)` prefetch yapılır (:1768).
  - Name hücresinde hover'da `row-kiosk` IconButton görünür ve `window.open` ile kiosk açar (:1781-1789).
  - Klavye: j/k/Enter → `openTrace` (:1059/1164). `/` bilerek bağlı değil (:1168).
- Sayfalama: sunucu `Pager` (offset/skip, 50 satır, `onEnd` sırayı ters çevirir, :1813-1838). "Load more" yok.
  - `VirtualTable height="auto"`, yani 50 satırda pencereleme fiilen kapalı.
  - >100 satır kuralı sunucu sayfalamasıyla karşılanıyor.
- Kaydırma: tek sayfa kaydırıcısı `#content`. Yatay kaydırma tablo kutusunda (:1716-1747). `scrollResetKey=page|sort|order`. Pager akışta.
- Şerit: `components/traces/VolumeChart` (uPlot) + `stripStat` (`rt` URL'de). volume|latency geçişi var. Daraltılabilir (LS anahtarı).
- Kayıtlı görünüm: `<SavedViewsBar page="traces" variant="compact">` (:1338).
- URL'nin sahiplendiği parametreler :2095'te listeli. Listede poll ve otomatik yenileme yok (:834). Yenilenirken opaklık .55 (:1668).
- Durumlar: `DataTableState` (:1265 liste, :1287 agg) → iskelet / hata + Retry / boş + `TracesEmptyDetail` (:2141).
- Kolon süzgeci: `pages/traces/ColumnFilter.tsx`.
  - `ColumnFilterShell` (:76) = dondurulmuş `ui/IconButton` + `ui/Popover` (300px, anchorRef) + `tcf-*` CSS (globals.css:6357).
  - Açılana kadar fetch yok, sunucu araması debounce'lu.
  - Exceptions ve Problems de bu bileşeni kullanıyor.
- İçe aktarmalar: shadcn yalnız `Button` (:36). Dondurulmuş ui'den IconButton, EnterHint, Chip ve DataTable ailesi geliyor. Spinner, Pager, ColumnManager, Combobox eski top-level bileşenler.
- Inline style: Traces.tsx'te 62, `components/traces/*`'ta 43 yer var.
  - `inlineStyleRatchet.test.ts:25` tavanı Traces grubu için 103 (`styles/pageFiles.ts`).
  - MIGRATED listesi yalnız `['RemoteCluster','Settings']` (:31).

### Koruyucu testler (bugün yeşil olanlar)
- `pages/` altında:
  - tracesRowLink, tracesScrollLayout, tracesPagerEnd, tracesReversePager, tracesSortDefault
  - Traces.tableStates, Traces.colOrder, Traces.noFit.pin, Traces.extrasService, Traces.savedViewsReach, Traces.k8sCells
  - tracesCellFont, tracesDurationBold, tracesNeutralPalette.pin, tracesStripGroup, tracesCountUniverse
- `pages/traces/*.test.ts` ve `ColumnFilter.contract.test.tsx`.
- Ratchet'ler: `styles/tableUnityRatchet` (:242 Link regex), `inlineStyleRatchet`, `buttonUnityRatchet:122`, `pagerAdoption:29`, `searchFieldAdoption`.
- Ortak testler: `lib/traceColumns.test`, `ui/DataTable/*.contract.test.tsx`.

## 2. Bu repoda "shadcn tablo" ne demek
- `components/shadcn/table.tsx` var. Settings'te yalnız statik ya da düzenlenebilir tablolarda kullanıldı (ExternalLinksTab:19, LdapTab, TeamRoutingTab).
- Kayıt listeleri `components/coremetry/DataTable` (R9, v0.10.1617) kullanır.
  - Bu sarmalayıcı `ui/DataTable` hook'larını ve `VirtualTable`'ı yeniden kullanır.
  - shadcn tablo primitive'i KULLANMAZ. Düz `<tr>/<td>` çizer ve token'larla giydirir.
  - Bilinen sınır (plan:366, types.ts:64-106): virtual modda rowActions, detail ve openRow çizilmiyor.
- Kural seti:
  - Taşınmış sayfada sıfır `style={{`. Hesaplanan değerler `lib/cssVars.ts vars()` ile verilir.
  - `components/ui` donduruldu. Ratchet yalnız aşağı iner, yeni ithalatçı eklenemez.
  - Radix yalnız sayfa chunk'ında olur. Giriş JS'i ≤347 KB gz (`scripts/bundle-budget.mjs:25`).
  - Ham button, input ve select yasak.
- Tablo standardı (DECISIONS:727-746): kayıt listesi = DataTable + useDataTable. Sunucu sayfalı tabloda yalnız resize olur. Satır yalnız tıklanabiliyorsa tıklanabilir görünür. Renk yalnız sapan değerde.
- Tek yığın kararı (DECISIONS:13433-13445, operatör B):
  - TanStack Table GİRMEZ. useDataTable + VirtualTable kalır.
  - VolumeChart ve uPlot sarmalayıcıları taşınmaz. Sorgular, URL ve kayıtlı görünümler kalır.
  - **Mevcut Traces testleri DEĞİŞMEDEN geçmeli. Geçmezse dur, operatöre sor.**

## 3. Değişmeyecekler (her madde bir testle sabitlenir; [Y] = dilim 0'da önce yazılacak yeni karakterizasyon testi)
- [ ] Kolon sırası Start time · Service · Name · attr… · Duration · Status · Spans. Zaman damgası en solda ve sabit.
  - Kaynak: v0.10.220, DECISIONS:496-506; memory `feedback-traces-start-time-leftmost`.
  - Test: `lib/traceColumns.test`, `Traces.colOrder.test.tsx`.
- [ ] Ek kolon değeri süzülen servisin giriş span'inden gelir; yoksa kökten, o da yoksa en erken span'den. any() asla.
  - Kaynak: DECISIONS:12187-12193 (1805-1808).
  - Test: `Traces.extrasService.test`. Değer sunucuda seçilir: Go testleri dokunulmaz kalır.
- [ ] "Veriye sığdır" düğmesi yok. Aday tavanı süzgeçten sonra uygulanmaz.
  - Kaynak: v0.10.812-813; memory `feedback-traces-no-fit-to-data`. DECISIONS'ta ayrı başlık yok, yalnız :5280.
  - Test: `Traces.noFit.pin`.
- [ ] Tek sayfa kaydırıcısı. İç dikey kaydırma yok. Pager akışta. Yapışkan thead, şerit ve pager yok.
  - Kaynak: 722 kutu-kaydırma reddi (`docs/audit/traces-scroll-audit-2026-09-13.md`); v0.9.1078 DECISIONS:488-495.
  - Test: `tracesScrollLayout`, `stickyHeaderPosition`, `Pager.contract`.
- [ ] Yatay taşmada kolon sığdırma çalışır.
  - INCIDENTS:258 (v0.10.357): `.vt-scroll` / `.table-wrap` seçicisi değişince sığdırma sessizce ölmüştü.
  - Test: [Y] sığdırma fonksiyonunun Traces'in kullandığı kaba gerçekten ulaştığını doğrulayan render testi.
- [ ] Şerit varsayılan istatistiği bugünkü kod değeri. Expand ve shrink yok.
  - Kaynak: 725; memory `feedback-traces-strip-p95-no-expand`.
  - Test: `stripStat.test` + [Y] `STRIP_STAT_DEFAULT` değer pini. DECISIONS:503 (p95), :11634 (p99) ve :11637 (p50) çelişiyor; kod değeri sabitlenir.
- [ ] Correlate ve alt Compare yok (347).
  - Test: [Y] Traces.tsx kaynak pini, ikisi de yok.
- [ ] Satır = `<Link row-link>` ve `state={{from}}`. Orta tık yerel çalışır. Hover prefetch ve kiosk düğmesi kalır. j/k/Enter çalışır, `/` bağlı değil.
  - Test: `tracesRowLink`, `tableUnityRatchet:242` + [Y] klavye/prefetch/kiosk render testi.
- [ ] Sunucu sıralaması: varsayılan time/desc, `persistSort:false`, `onEnd` sırayı ters çevirir. 50 satır/sayfa, ROW_H=36.
  - Test: `tracesSortDefault`, `tracesPagerEnd`, `tracesReversePager` + [Y] ROW_H ve pageSize pini.
- [ ] Arama deneyimi önce gelir: listede poll yok, URL parametre kümesi aynı kalır, kayıtlı görünüm aynı kalır.
  - Kaynak: 2026-09-19, memory `feedback-trace-search-experience-first`.
  - Test: `Traces.savedViewsReach` + [Y] :2095 sahip-parametre listesinin snapshot'ı + [Y] "listede setInterval/useAutoRefresh yok" pini.
- [ ] Kolon süzgeci açılmadan fetch etmez ve sunucu araması yapar. Picker hep sunucu taraflıdır.
  - Test: `ColumnFilter.contract`.
- [ ] Renk yalnız sapan değerde. Hücre fontu ve kalın duration aynı.
  - Test: `tracesNeutralPalette.pin`, `tracesCellFont`, `tracesDurationBold`.
- [ ] Liste ve agg görünümünde iskelet, hata + Retry ve boş + teşhis durumları aynı.
  - Test: `Traces.tableStates`.
- [ ] Tempo kaynak çipi kalır. Listede değil, tek trace sayfasında (Trace.tsx, TraceHonesty) yaşıyor. Bu plan oraya DOKUNMAZ.
- [ ] Emoji yok.
- [ ] [Y] DOM yapı snapshot'ı. Sabit fikstürle başlık + 3 satır çizilir ve şunlar kaydedilir:
  - kolon id sırası, href'ler, aria-sort, satır yüksekliği, hücre başına DOM düğüm sayısı.
  - Sınıf adları kayda girmez; kasıtlı token değişikliği testi kırmaz.

## 4. Değişecekler (kullanıcının göreceği)
1. Renk, tipografi ve boşluk token'ları taşınmış Settings sayfalarıyla aynı kaynaktan gelir (`styles/tokens.css` → shadcn değişkenleri).
   - Neden: sayfalar arası tutarlılık.
   - Satır yüksekliği, kolon genişliği ve font ailesi DEĞİŞMEZ.
2. Fokus halkası başlık düğmelerinde, süzgeç hunisinde, pager'da, satır linkinde ve kiosk düğmesinde shadcn `focus-visible` halkası olur.
   - Neden: klavye kullanıcısı için görünür ve tek tip.
3. Kolon süzgeci açılır paneli shadcn Popover yüzeyine geçer (radius, gölge, kenar).
   - Exceptions ve Problems'te de aynı değişir. Bkz. S5 ve soru 2.
4. Başka hiçbir şey değişmez: veri, sıra, metin, etkileşim ve yerleşim aynı kalır.

## 5. Yaklaşım seçenekleri

| | A: Yerinde yeniden giydir (ÖNERİ) | B: coremetry/DataTable sarmalayıcısına taşı | C: shadcn `table.tsx` ile yeniden kur |
|---|---|---|---|
| Ne | useDataTable + VirtualTable kalır. CSS token'a bağlanır. inline style → `vars()`/sınıf. Dondurulmuş atomlar → shadcn/coremetry. | Sayfa wrapper'a geçer. Wrapper'a renderColHead, onEnd pager, scrollResetKey ve colOrder picker eklenir. | Yeni tablo; virtual, resize ve reorder elle yazılır. |
| Virtualizasyon | Aynı | Aynı motor, ama virtual modda rowActions/openRow sınırı var | Yok, baştan yazılır |
| Süzgeç / resize / reorder | Aynı kod | Wrapper'ı genişletmek gerekir (paylaşılan risk) | Baştan yazılır |
| Bundle | ~0 (Popover zaten sayfa chunk'ında) | ~0 | + |
| Risk | Düşük; diff CSS + import | Orta; wrapper'ı kullanan Settings sayfaları da etkilenir | Yüksek; DECISIONS:13433 ve Tablo standardıyla çelişir |

Öneri A. Gerekçe:
- Davranışı taşıyan kod aynı kalır.
- DECISIONS:13433'teki "testler değişmeden geçsin" şartı yalnız A'da doğal olarak sağlanır.
- B'nin kazancı (tek sarmalayıcı) görünmez. Wrapper'ı Traces'in ihtiyaçlarına göre büyütmek ayrı bir program olur ve bu plana girmez.
- C reddedilir.

## 6. Dilimler (her biri tek commit, tek vX, tek `git revert` ile geri alınır)
- **S0 Karakterizasyon testleri.** Yalnız test eklenir, üretim kodu değişmez.
  - §3'teki tüm [Y] maddeleri buraya girer.
  - §7'deki benchmark taban değerleri dosyaya yazılır.
  - Mevcut testler yeşil kalmalı.
- **S1 Mockup.** `docs/frontend/mockups/traces-shadcn.html` (src dışında, memory `project-design-files-not-in-src`). Eski ve yeni yan yana gösterilir:
  - açık ve koyu tema;
  - 50 satırlık yoğun veri, 8 kolon (3 attr dahil);
  - çok uzun servis, operasyon ve route adları (kırpma ve title);
  - süzülmüş durum: başlık hunisi aktif, popover açık, Service filtresi var;
  - iskelet, hata + Retry, boş + teşhis;
  - 1024px ve 1366px dar genişlikte yatay taşma;
  - klavye fokusunda satır ve başlık;
  - agg görünümü.
  - Operatör onaylamadan S2'ye geçilmez.
- **S2 Token bağlama (yalnız CSS).**
  - Traces'e özel sınıflar (`row-link` görünümü, `tcf-*`, başlık, pager çevresi) shadcn ve tokens.css değişkenlerine bağlanır.
  - globals.css SONUNA kural eklenmez (CoSRE pin dersi). Kurallar mevcut bloklarda düzenlenir.
- **S3a / S3b Inline style temizliği.**
  - S3a: Traces.tsx'te liste ve başlık bölgesi. S3b: şerit kabı, agg görünümü ve kalanlar.
  - Görsel fark yok; DOM snapshot birebir kalmalı. `inlineStyleRatchet` tavanı her dilimde düşer.
- **S4 Dondurulmuş atomlar → shadcn/coremetry.**
  - Traces.tsx'te IconButton (kiosk dahil), Chip, EnterHint ve Spinner değişir.
  - Fokus halkası burada görünür olur. ui import ratchet'i düşer.
- **S5 ColumnFilterShell → shadcn Popover.**
  - Exceptions ve Problems de geçer.
  - `ColumnFilter.contract`'a dış tık, Esc, fokus geri dönüşü ve başlık sürükleme ile çakışma testleri eklenir.
- **S6 `components/traces/*` inline style.** Grafik sarmalayıcıları hariç (DECISIONS:13433).
  - Sonunda Traces MIGRATED listesine girer. Bkz. soru 1.
- **S7 Göç günlüğü ve kapanış.** migration-log girişi yazılır, memory kaydı kapatılır.

## 7. "Çok iyi"yi tarayıcısız kanıtlama
- Render benchmark'ı vitest + jsdom'da koşar ve dosyaya taban değeri yazar:
  - 50 satır × 11 kolon ilk render süresi;
  - sıralama ve sayfa değişiminde React Profiler commit sayısı;
  - satır başına DOM düğüm sayısı.
  - Kapı: süre ≤ taban × 1,10, commit sayısı ve düğüm sayısı ≤ taban.
- Stres: VirtualTable'a 5000 satır `virtual` verilir. Pencereleme hâlâ ≤ (görünür + 2×overscan 12) satır çizmeli.
- Bundle: `bundle:check` çalışır.
  - Giriş ≤347 KB gz ve S0 tabanından +0 KB.
  - Traces chunk'ı ≤ +3 KB gz.
  - Girişte Radix olmamalı (`bundleBudget.pin`).
- Her dilimde `scripts/gate.sh all` + `make audit` + golangci-lint (Go değişmese de tek kapı).
- Operatörün prod kontrol listesi, her dilim yayınından sonra beş dakika:
  1. Varsayılan açılış, 50 satır: süre gözle aynı.
  2. Start time en solda.
  3. Service filtresi + 3 attr kolonu: değerler eskisiyle aynı trace'lerde aynı.
  4. Başlık hunisi: değer arama, uygula, temizle.
  5. Duration sırala, son sayfa (sıra ters dönüyor).
  6. Satırı cmd-tık ile yeni sekmede aç; kiosk düğmesi.
  7. j/k/Enter.
  8. Dar pencerede yatay kaydırma; tek dikey kaydırma çubuğu.
  9. Koyu tema.
  10. Kayıtlı görünüm yükle.
  11. Exceptions ve Problems hunisi (S5 sonrası).

## 8. Riskler
- **En büyük risk:** CSS sınıfı ya da kap değişikliğiyle sessiz işlev kaybı (INCIDENTS:258 emsali). `.vt-scroll`, `.table-wrap`, `row-link`, `tcf-*` ve stickyLeft seçicilerine bağlı JS var. Görsel diff'te fark görünmez, kolon sığdırma veya sticky sessizce ölür.
  - Önlem: S0'daki erişilebilirlik testi. S2 ve S4'te sınıf adları yeniden adlandırılmaz, yalnız içerikleri değişir.
- Radix Popover'ın odak ve dış tık semantiği eski Popover'dan farklı. Başlık sürüklemesi ve huni tıkı çakışabilir; 3 sayfayı etkiler. S5 ayrı dilim ve ek testlerle gelir.
- Inline style → `vars()` geçişinde hesaplanan genişlik veya yükseklik kaybolabilir. DOM snapshot ve ROW_H pini bunu yakalar.
- Ratchet sayımı ile grep sayısı farklı (103 ve 105). S3 öncesi ratchet'in kendi sayımı taban alınır.

## 9. Doğrulanamayanlar
- `STRIP_STAT_DEFAULT`'un gerçek değeri okunmadı. DECISIONS kendi içinde çelişiyor; S0'da koddan sabitlenir.
- Ek kolonlarda giriş span'i seçimi backend'de. Sayfa yorumunda "root > service entry > earliest" yazıyor; 1806 ise süzgeçte servis span'ini önce alıyor. Sunucu kodu bu planda izlenmedi.
- Traces'te yoğunluk (density) kontrolünün yeri bulunamadı. CSS ikizleri var.
- Tarayıcıdaki gerçek piksel ve performans eşitliği CI'da kanıtlanamaz. Son söz §7'deki operatör listesinde.

## 10. Operatöre sorular (önerilen varsayılanla)
1. `components/traces` içindeki grafik sarmalayıcılarının inline style'ı (VolumeChart, LatencyScatter) "sıfır inline" kuralına dahil mi?
   - Öneri: HAYIR. DECISIONS:13433 bunları taşımıyor; `pageFiles.ts`'te ayrı grup açılır, tavanları kalır.
2. Süzgeç popover'ının görünümü Exceptions ve Problems'te de değişecek. Kabul mü?
   - Öneri: EVET, tek dilimde (S5). Çatallanmış iki süzgeç olmasın.
3. Şerit varsayılanı bugünkü koddaki değer olarak mı sabitlensin?
   - Öneri: EVET. Bu programda değişmez.
4. Agg (groupBy) görünümü bu programa dahil mi?
   - Öneri: EVET, S3b ve S4 içinde, aynı kurallarla.
