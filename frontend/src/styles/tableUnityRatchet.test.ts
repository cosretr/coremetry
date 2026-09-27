import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { stripTsComments } from './zLayers.test';
import { jsxOpenTags } from './jsxTags';

// tableUnityRatchet — v0.10.932 (tablo standardı, dilim 0).
//
// Operatör kararı 2026-09-26 ("önerini yapalım mockup gördüm uygundur"):
// tek tablo standardı T1–T12 (docs/DECISIONS.md "Tablo standardı").
// Envanter ~195 tablonun iskeletinin ortak, GÜRÜLTÜNÜN detayda olduğunu
// gösterdi: satır içi hücre stilleri yoğunluk ayarını hücrelerin yarısından
// saklıyor, monospace sayılar satıra ikinci yazı tipi getiriyor, sabit
// satır yüksekliği tahminleri kaydırma çubuğunu zıplatıyor.
//
// KAPI: her sayı yalnız AŞAĞI iner — buttonUnityRatchet ile aynı dil. Bir
// göç dilimi sayıyı düşürdüğünde tavan AYNI commit'te düşer; artış = yeni
// kod standardın dışına çıktı. Tabanlar v0.10.932 ölçümü (yorumlar
// ayıklanır; ui/DataTable primitifinin kendisi sayılmaz).
//
// v0.10.977 — dilim 7 (SON dilim): göç bitti. Sıfır olmayan her sayacın
// kalanı aşağıda tek tek gerekçeli — ya standardın kendi muafı (T1 lejant /
// sohbet / iskelet), ya tarifin §2b'si (dinamik değer, sınıf karşılığı
// olmayan yerleşim), ya tarif §2 bilinçli kendini gizleme, ya da canvas.
// Bundan sonra bir tavanın altına inmek yeni bir gerekçeyi (ör. KeyValue'ya
// sıkı kip, canvas için çözümlenmiş mono belirteci) ister; artış ise
// gerekçesiz yeni kullanımdır ve bu dosyada değil, o kodda düzeltilir.
const SRC = resolve(__dirname, '..');
const PRIMITIVE = join('components', 'ui', 'DataTable') + '/';

const CEILINGS = {
  /** T1 — `<table>` sayısı eksi `<DataTableHead>` sayısı (dosya başına).
   *  Tür ayırmaz: T1'in gerekçeli statik tablosu da sayılır.
   *  v0.10.977 — dilim 7 (son): kalan 47'nin hepsi muaf ya da gerekçeli;
   *  sayaç tasarım gereği düşmez. MUAF 9 — sohbet markdown / kanıt tablosu 5
   *  (ai/ChatBubble 3, ai/ChatTraceList, ai/EvidenceCard), grafik lejantı 3
   *  (chart/PanelLegend, chart/StatsLegend, viz/TimeSeriesPanel), yer tutucu
   *  1 (Skeleton `TableSkeleton` — gerçek tablonun iskeleti, veri satırı
   *  yok). GEREKÇELİ STATİK (T1; dosya içi `statik tablo` yorumu, durumu
   *  olanlar `<DataTableState colSpan>` taşır) 38 — ≤10 sabit / sunucu
   *  sıralı özet 19 (ExternalPaths, RolloutDrawer başlıksız önizleme,
   *  RootCausePanel 4, anomalies/ExceptionPodsPanel, anomalies/ProblemDetail
   *  2 [top 5 + başlıksız ≤14 örnek], AdminK8sCoverage alan listesi,
   *  AIObservability 2 [3 kova kalibrasyon, biçimli dizge kartı],
   *  alerts/NoisyRulesPanel top-10, EntityDetail ömürler, pod/PodContextTables
   *  2, Rollouts 2 topN, SpanDetail hotspots top-10); `.ps-kv` öznitelik
   *  listesi 4 (SpanDetail 3, trace/KioskSpanPanel — Tempo yoğunluğu
   *  KeyValue'nun sabit etiketinde yok, sıkı kip gelince birlikte göçer);
   *  settings/ düzenlenebilir eşleme / seçici / ≤10 önizleme 9
   *  (ExternalLinksTab, LdapTab 3, LdapUserPicker, OracleTab ≤3 örnek,
   *  SpanClusterValuesPanel, TeamRoutingTab, ZoomChannelPicker);
   *  AdminClickhouse 6 (DDL günlüğü yürütme sırasında, onarım + artık
   *  birleşik satır modeli, replika kararı çok satırlı hücre, dar kartta
   *  ingest pod özeti, düzenlenebilir gün seçici, ön kontrolde küme başına
   *  kapsama). */
  rawTable: 47, // v0.10.973 — dilim 6: 48 → 47
  /** T5 — `<td style={…}>`: hücre görünümü sınıfa/sütun tanımına taşınır.
   *  v0.10.977 — dilim 7 (son): 69 → 60 — settings/ 7 (LdapTab 3,
   *  LdapUserPicker 4) taban `tbody td` ritmine indi, ProblemDetail 2
   *  gerekçelendi. Kalan 60'ın hepsi tarif §2b (dinamik değer ya da sınıf
   *  karşılığı olmayan yerleşim): lejant (T1 muafı) 17 (chart/StatsLegend 12,
   *  viz/TimeSeriesPanel 5); hesaplanmış değer 6 (yapışkan sol ofset —
   *  Endpoints 2, Traces, DependenciesTable; Metrics bayat satır opaklığı;
   *  AdminAudit açılır hücrenin whiteSpace'i); sınıfsız renk 3 (`--accent2`
   *  kimlik / bağlantı tonu, sapma tonu değil — traces/ShapesView, Runbook,
   *  explore/GroupTable imleç değeri); `maxWidth` 6 (AdminClickhouse 2,
   *  SlowQueries, Alerts, explore/TracesResult, endpoints/detailSections
   *  `maxWidth: 0` otomatik düzen kırpması); ortalanmış açma / onay hücresi 4
   *  (Endpoints, DependenciesTable, anomalies/AnomaliesPage, Inbox);
   *  `row-detail` üçlüsü olmayan özel dolgu / zemin / kenar / satır aralığı
   *  20 (ai/insightRow dolgusuz kart, DBQueriesPanel bg0, DependenciesTable
   *  bg1, LogTable 10px 20px, AnomaliesPage bg1, Endpoints bg0,
   *  service/ServicePodsTable dolgusuz JMX, service/OperationsTable bg2 +
   *  accent sol kenar, AdminCatalog italik AI ipucu, AdminClickhouse 6
   *  [dipnot + gün seçicinin 5 sıkı hücresi], ProblemDetail başlıksız
   *  listenin 14px kenarı 2, SpanDetail hotspots 2, RootCausePanel çok
   *  satırlı gerekçe); 700 ağırlık 3 (anomalies/streams, explore/RepeatsResult,
   *  service/OperationsTable — `.cell-strong` 600'dür, 700 sınıfı yok);
   *  saat damgasında sağa yaslama 1 (AdminClickhouse ingest pod hhmm —
   *  `num` mono'yu düşürür, S2 damgayı muaf tutar, sağa yaslama sınıfı yok). */
  tdStyle: 60, // v0.10.977 — dilim 7: 69 → 60
  /** T5 — satır içi hücre yazı boyu: yoğunluk ayarı ulaşamıyor. */
  tdFontSize: 0, // v0.10.973 — dilim 6: 1 → 0
  /** T4 — sayı hücresinde monospace (`num mono` / `mono num`). */
  numMono: 0, // v0.10.973 — dilim 6: 8 → 0
  /** T2 — satır içi `<tr … cursor:` (imleç yalnız tıklanabilir satırda, CSS'ten).
   *  v0.10.933 (tablo standardı T2) — sayım artık süslü parantez farkında
   *  etiket yürüyücüsüyle (styles/jsxTags.ts): eski `<tr\b[^>]*cursor:`
   *  regex'i `{...rowActivation(() => …)}` gibi önceki bir yayılımın ok
   *  fonksiyonunda duruyor, SONRAKİ `style={{ cursor: … }}`'u görmüyordu —
   *  "9 → 0" ölçümü 3 koşullu imleci (Databases, databases/detailSections,
   *  traces/ShapesView) kaçırmıştı; onlar da silindi (rowActivation yalnız
   *  açılan satırda). Yürüyücüyle ölçülen gerçek sayım: 0. */
  trCursor: 0,
  /** T6 — elle `containIntrinsicSize` (tek `--row-h` ritmi).
   *  v0.10.977 — dilim 7 (son): kalan 3'ün hiçbiri tablo satırı değil, T6
   *  kapsamı dışı (tarif §3.6): ai/ChatBubble (muaf sohbet tablosu, >100
   *  satırda içerik boylu 26px), LogFieldsPanel (alan rayı — DisclosureButton
   *  listesi, tablo yok), TraceWaterfall (`.wf-row` div, sanallaştırma
   *  kapalıyken). `.cv-row`a göç tablo dışı olduğu için yok. */
  containIntrinsicSize: 3, // v0.10.973 — dilim 6: 4 → 3
  /** T10 — ölü `.is-fit` (v0.9.1078'den beri masaüstü kuralı yok).
   *  v0.10.933 (dilim 1): 68 → 66 — LogPatternsPanel'in iki iç kaydırmalı kabı `is-scroll`. */
  isFit: 0, // v0.10.947 — dilim 3 dalga 4 (AI gözlem, metrik/dashboard, grafik, servis/topoloji, uyarılar): 3 → 0
  /** T10 — satır içi `tableLayout` (tek tablo sınıfı / primitif). */
  tableLayout: 0, // v0.10.973 — dilim 6: 1 → 0
  /** T3 — sahte sıralanabilir sütun (`sortValue: () => 0`). */
  fakeSortable: 0, // v0.10.945 — dilim 3 dalga 3 (trace/log/problem/ops sayfaları): 10 → 0
  /** T7 — talimat ipuçlu satır (`<tr title=…>`).
   *  v0.10.977 — dilim 7 (son): kalan 2 lejant satırı, T1 muafı
   *  (chart/StatsLegend, viz/TimeSeriesPanel — "tıkla, seriyi yalnız
   *  bırak" ipucu lejantın kendi etkileşimi, tablo satırı sözleşmesi değil). */
  trTitle: 2, // v0.10.947 — dilim 3 dalga 4 (AI gözlem, metrik/dashboard, grafik, servis/topoloji, uyarılar): 5 → 2
  /** T5 — satır içi monospace yığını (`fontFamily: '…monospace…'` /
   *  `font: '…monospace…'` dizgisi). v0.10.933 (tablo standardı T5) — TEK
   *  yığın `--font-mono` (globals.css); ikinci yazım yığını çoğaltır, tema /
   *  yoğunluk ayarı ona ulaşamaz. Taban v0.10.933 ölçümü (263); göçü dilim 3.
   *  v0.10.977 — dilim 7 (son): 9 → 4 (Trace.tsx 5 `--font-mono`ya göçtü;
   *  çivi pages/Trace.monoStack.pin.test.ts). Kalan 4 DOM değil canvas:
   *  charts/TimeChart 2 + service/charts/OverviewChart 2 — uPlot eksen
   *  `font`u bir canvas dizgisi, `var(--font-mono)`yu çözmez; göçü
   *  çözümlenmiş bir belirteç (resolveVar benzeri, useThemeTick ile) ister. */
  inlineMonoStack: 4, // v0.10.977 — dilim 7: 9 → 4
  /** T12 / S6 — durumu tablonun İÇİNDE olmayan DataTable tablosu. Dosya
   *  başına `max(0, <DataTableHead> − dt'li <DataTableState>)` + `state=`
   *  almayan `<VirtualTable>` (`dtNoStateOf`). Her DataTable tablosu tam bir
   *  DataTableHead basar (dt'yi alt bileşene geçiren sayfada da başlık ve
   *  durum AYNI dosyada); VirtualTable başlığını ve durum satırını kendi
   *  basar, benimseme `state=`.
   *  v0.10.954 — dilim 4 tabanı 139; iki pilot (ServiceBacktrace,
   *  PartitionLagTable) ile 137; dilim 4 göçü 40.
   *  v0.10.967 — dilim 5 (P-1 `detail`, P-2 `colSpan`): ölçüm 17.
   *  explore/GroupTable göçü artık gerçek (Explore `state={groupState}`
   *  veriyor); v0.10.954'ün "gerçek değil" notu ve +1'i silindi.
   *  v0.10.967 — P-2'nin statik `<DataTableState colSpan>`'ı dt'li durum
   *  SAYILMAZ: aynı dosyadaki durumsuz bir DataTableHead'i örtmesin
   *  (Rollouts, PodContextTables statik + dt'li tabloyu bir arada tutuyor).
   *  Ölçüm iki sayımla da 17 — bugün örtülen tablo yok.
   *  v0.10.973 — dilim 6: service/ServicePodsTable göçü artık gerçek —
   *  ServicePodsTab `state={podsState}` veriyor, tablo her durumda bağlı
   *  (çivi: service/ServicePodsTab.tableStates.test.tsx). v0.10.967'nin
   *  "gerçek değil" notu ve onu zorlayan dürüstlük çivisi silindi; sayım
   *  bundan değişmedi. OverviewTables OpsCard Service → Overview → OpsCard
   *  `state` zinciriyle göçtü: 17 → 16. Ölçüm iki sayımla da 16.
   *  v0.10.977 — dilim 7 (son): kalan 16'nın hepsi bilinçli kendini gizleme
   *  / boş olamayan tablo (tarif §2; her birinde dosya içi `dtNoState:`
   *  yorumu). Yükleniyor / hata her birinde bölümün tek okumasına ait ve
   *  dışarıda çizilir; boş = "gösterilecek şey yok" ürün kararı, tablo
   *  çizilmez. AdminClickhouse 8 (bekleyen DDL host + kuyruk başı, async
   *  insert tamponu, mutasyon kuyruğu, replika gecikmesi, düğüm listesi,
   *  shard politikası, bulgu listesi — sağlıklı küme = boş bölüm, sayı
   *  özette); anomalies/AnomalyWindowTable (Overview yalnız pencerede
   *  anomali varken çizer); dependencies/DetailDrawer top ops;
   *  dependencies/panels/PostgresPanel veritabanları; AdminCardinality
   *  FinOps katkısı; adminstats/panels sıcak anahtarlar; EntityDetail pod ×
   *  servis; service/TopEndpointsCard (boşsa Overview OpsCard'a düşer);
   *  settings/AiProfilesPanel (varsayılan profil silinemez, liste boşalamaz). */
  dtNoState: 16, // v0.10.973 — dilim 6: 17 → 16
} as const;

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (p.endsWith('.tsx') && !p.endsWith('.test.tsx')) out.push(p);
  }
  return out;
}
const files = walk(SRC).filter(p => !p.includes(PRIMITIVE));
const code = files.map(p => stripTsComments(readFileSync(p, 'utf8')));
const count = (re: RegExp) => code.reduce((a, s) => a + (s.match(re)?.length ?? 0), 0);

// v0.10.954 (tablo standardı T12) — `<VirtualTable<Row>` genel parametresi
// jsxOpenTags'in ad sınırını (`[\s>/]`) kaçırır: önce düz ada indirilir.
const VT_GENERIC = /<VirtualTable<(?:[^<>]|<[^<>]*>)*>/g;
/** Etiketin KENDİ özniteliği mi? `renderRow` içindeki `<Link state={…}>`
 *  gibi iç JSX derinlik > 0'da kalır ve sayılmaz. */
function hasTopLevelAttr(tag: string, name: string): boolean {
  let depth = 0; let q: string | null = null;
  for (let i = 0; i < tag.length; i++) {
    const c = tag[i];
    if (q) { if (c === q) q = null; continue; }
    if (depth === 0 && (c === '"' || c === "'")) { q = c; continue; }
    if (c === '{') depth++;
    else if (c === '}') depth--;
    else if (depth === 0 && /\s/.test(c) && tag.startsWith(`${name}=`, i + 1)) return true;
  }
  return false;
}

function tableCounts(): Record<keyof typeof CEILINGS, number> {
  return {
    rawTable: code.reduce((a, s) =>
      a + Math.max(0, (s.match(/<table\b/g)?.length ?? 0) - (s.match(/<DataTableHead\b/g)?.length ?? 0)), 0),
    // v0.10.933 — td/tr sayaçları da süslü-parantez farkında yürüyücüyle: düz
    // `<td\b[^>]*` bir önceki ok fonksiyonundaki `>`'de durup eksik sayıyordu.
    tdStyle: code.reduce((a, s) => a + jsxOpenTags(s, 'td').filter(t => /\sstyle=\{/.test(t.tag)).length, 0),
    tdFontSize: code.reduce((a, s) => a + jsxOpenTags(s, 'td').filter(t => /\sstyle=\{\{[^}]*fontSize/.test(t.tag)).length, 0),
    numMono: count(/\b(num mono|mono num)\b/g),
    trCursor: code.reduce((a, s) => a + jsxOpenTags(s, 'tr').filter(t => /\bcursor\s*:/.test(t.tag)).length, 0),
    containIntrinsicSize: count(/containIntrinsicSize/g),
    isFit: count(/\bis-fit\b/g),
    tableLayout: count(/tableLayout:/g),
    fakeSortable: count(/sortValue:\s*\(\)\s*=>\s*0\b/g),
    trTitle: code.reduce((a, s) => a + jsxOpenTags(s, 'tr').filter(t => /\stitle=/.test(t.tag)).length, 0),
    inlineMonoStack: count(/\bfont(?:Family)?:\s*(['"`])[^'"`\n]*monospace[^'"`\n]*\1/g),
    dtNoState: code.reduce((a, s) => a + dtNoStateOf(s), 0),
  };
}

/** Dosya başına dtNoState (yorumları ayıklanmış kaynak).
 *  v0.10.967 (dilim 5, P-2) — `<DataTableState colSpan={N}>` statik tablonun
 *  (dt yok) durum satırıdır; dt'li durum sayılmaz. Düz `<DataTableState\b`
 *  sayımı onu da sayıyor, aynı dosyadaki durumsuz bir DataTableHead'i
 *  örtüyordu. `colSpan` yalnız etiketin KENDİ özniteliğiyse: `detail`
 *  içindeki `<td colSpan>` derinlik > 0'da kalır. */
function dtNoStateOf(s: string): number {
  const heads = s.match(/<DataTableHead\b/g)?.length ?? 0;
  const dtStates = jsxOpenTags(s, 'DataTableState').filter(t => !hasTopLevelAttr(t.tag, 'colSpan')).length;
  const vtBare = jsxOpenTags(s.replace(VT_GENERIC, '<VirtualTable '), 'VirtualTable')
    .filter(t => !hasTopLevelAttr(t.tag, 'state')).length;
  return Math.max(0, heads - dtStates) + vtBare;
}

describe('tablo standardı mandalı (v0.10.932)', () => {
  const now = tableCounts();
  for (const k of Object.keys(CEILINGS) as (keyof typeof CEILINGS)[]) {
    it(`${k} tavanı aşmaz (${CEILINGS[k]})`, () => {
      expect(now[k], `${k}: standart dışı yeni kullanım — docs/DECISIONS.md "Tablo standardı"`)
        .toBeLessThanOrEqual(CEILINGS[k]);
    });
  }
});

// v0.10.954 (tablo standardı T12) — dtNoState'in VirtualTable ayrımı çivili:
// Traces'in `renderRow`'undaki `<Link state={{ from }}>` düz regex'le
// "benimsendi" sayılıyordu (ilk ölçümde yakalandı).
describe('dtNoState — VirtualTable `state=` yalnız kendi özniteliğiyse sayılır', () => {
  const bare = (src: string) => jsxOpenTags(src.replace(VT_GENERIC, '<VirtualTable '), 'VirtualTable')
    .filter(t => !hasTopLevelAttr(t.tag, 'state')).length;
  it('iç JSX\'teki state= benimseme değil; kendi state= benimseme', () => {
    expect(bare(`<VirtualTable<Row> dt={dt} renderRow={t => <Link to={h} state={{ from: x }}>{t.id}</Link>} />`)).toBe(1);
    expect(bare(`<VirtualTable<Map<string, number>> dt={dt}\n  state={{ kind: 'loading' }} renderRow={r => <td>{r.a}</td>} />`)).toBe(0);
    expect(bare(`<VirtualTable dt={dt} title="a state=b" />`)).toBe(1);
  });
});

// v0.10.967 (tablo standardı, dilim 5) — P-2 statik durumu dt'li durum
// sayılmaz (dtNoStateOf); aksi hâlde aynı dosyadaki durumsuz DataTable
// tablosu görünmez olurdu.
describe('dtNoState — statik `<DataTableState colSpan>` dt\'li tabloyu örtmez', () => {
  const HEAD = '<table {...dt.tableProps}><DataTableColgroup dt={dt} /><DataTableHead dt={dt} /><tbody>{rows}</tbody></table>';
  it('statik durum + durumsuz DataTable başlığı = 1', () => {
    expect(dtNoStateOf(`<table><tbody>{n ? r : <DataTableState colSpan={3} kind="empty" />}</tbody></table>${HEAD}`)).toBe(1);
    // colSpan'dan önce ok fonksiyonu / JSX olsa da etiketin kendi özniteliği
    expect(dtNoStateOf(`<DataTableState kind="no-match" onClearFilters={() => setQ('')} colSpan={3} />${HEAD}`)).toBe(1);
    expect(dtNoStateOf(`<DataTableState kind="empty" detail={<Link to="/x">x</Link>} colSpan={3} />${HEAD}`)).toBe(1);
  });
  it('dt\'li durum başlığı karşılar; detail içindeki colSpan etiketin değil', () => {
    expect(dtNoStateOf(`${HEAD}<DataTableState dt={dt} {...tableState} />`)).toBe(0);
    expect(dtNoStateOf(`${HEAD}<DataTableState dt={dt} kind="empty" detail={<td colSpan={2} />} />`)).toBe(0);
  });
});
