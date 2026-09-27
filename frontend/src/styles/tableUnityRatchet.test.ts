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
const SRC = resolve(__dirname, '..');
const PRIMITIVE = join('components', 'ui', 'DataTable') + '/';

const CEILINGS = {
  /** T1 — `<table>` sayısı eksi `<DataTableHead>` sayısı (dosya başına).
   *  Tür ayırmaz: T1'in gerekçeli statik tablosu da sayılır.
   *  v0.10.973 — kalan 47: 16'sı başka iş akışlarının dosyalarında
   *  (AdminClickhouse 6, settings/ 9, trace/KioskSpanPanel 1); muaf sohbet
   *  (ChatBubble 3, ChatTraceList, EvidenceCard) ve lejant (PanelLegend,
   *  StatsLegend, TimeSeriesPanel) tabloları 8. */
  rawTable: 47, // v0.10.973 — dilim 6: 48 → 47
  /** T5 — `<td style={…}>`: hücre görünümü sınıfa/sütun tanımına taşınır.
   *  v0.10.973 — kalan 69: 16'sı başka iş akışlarının dosyalarında
   *  (AdminClickhouse 9, settings/LdapUserPicker 4, settings/LdapTab 3);
   *  lejant tabloları 17 (StatsLegend 12, TimeSeriesPanel 5); dilim 6'da
   *  gerekçeyle kalan 7 (Endpoints 4, Metrics 1 dinamik opaklık,
   *  SlowQueries 1 maxWidth, ServicePodsTable 1 JMX düz hücresi). */
  tdStyle: 69, // v0.10.973 — dilim 6: 82 → 69
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
   *  v0.10.973 — kalan 3: ai/ChatBubble (muaf sohbet tablosu),
   *  LogFieldsPanel, TraceWaterfall. */
  containIntrinsicSize: 3, // v0.10.973 — dilim 6: 4 → 3
  /** T10 — ölü `.is-fit` (v0.9.1078'den beri masaüstü kuralı yok).
   *  v0.10.933 (dilim 1): 68 → 66 — LogPatternsPanel'in iki iç kaydırmalı kabı `is-scroll`. */
  isFit: 0, // v0.10.947 — dilim 3 dalga 4 (AI gözlem, metrik/dashboard, grafik, servis/topoloji, uyarılar): 3 → 0
  /** T10 — satır içi `tableLayout` (tek tablo sınıfı / primitif). */
  tableLayout: 0, // v0.10.973 — dilim 6: 1 → 0
  /** T3 — sahte sıralanabilir sütun (`sortValue: () => 0`). */
  fakeSortable: 0, // v0.10.945 — dilim 3 dalga 3 (trace/log/problem/ops sayfaları): 10 → 0
  /** T7 — talimat ipuçlu satır (`<tr title=…>`).
   *  v0.10.973 — kalan 2: lejant satırları (chart/StatsLegend,
   *  viz/TimeSeriesPanel); dilim 6'da değişmedi. */
  trTitle: 2, // v0.10.947 — dilim 3 dalga 4 (AI gözlem, metrik/dashboard, grafik, servis/topoloji, uyarılar): 5 → 2
  /** T5 — satır içi monospace yığını (`fontFamily: '…monospace…'` /
   *  `font: '…monospace…'` dizgisi). v0.10.933 (tablo standardı T5) — TEK
   *  yığın `--font-mono` (globals.css); ikinci yazım yığını çoğaltır, tema /
   *  yoğunluk ayarı ona ulaşamaz. Taban v0.10.933 ölçümü (263); göçü dilim 3.
   *  v0.10.973 — kalan 9: Trace.tsx 5 (başka iş akışının dosyası);
   *  charts/TimeChart 2 + service/charts/OverviewChart 2 — uPlot eksen
   *  `font`u bir canvas dizgisi, `var(--font-mono)`yu çözmez; göçü
   *  çözümlenmiş bir belirteç ister. */
  inlineMonoStack: 9, // v0.10.973 — dilim 6: 19 → 9
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
   *  Kalan 16: AdminClickhouse 8 (başka iş akışının dosyası); kendini
   *  gizleyen / boş olamayan 8 (AnomalyWindowTable, DetailDrawer top ops,
   *  PostgresPanel, AdminCardinality FinOps, adminstats top keys,
   *  EntityDetail pods × services, TopEndpointsCard, settings/
   *  AiProfilesPanel — sonuncusu da başka iş akışının dosyası). */
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
