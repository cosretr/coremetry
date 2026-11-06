// LogPatternsPanel.tsx — v0.10.298 (log-search audit Dilim 2b): /logs
// "Desenler" — pencere içindeki mesajlar NormalizeSignature imzasıyla
// gruplanmış (sunucu, ÖRNEKLEMELİ ≤cap satır). Sayımlar örneğe göredir;
// altbilgi bunu SÖYLER. Fetch yalnız panel açıkken (v0.8.270 disiplini).
// Satır / "Ara" → şablondan türetilen tırnaklı AND sorgusu serbest metne
// yazılır (iki backend de anlar; yaklaşıktır, öyle etiketlenir).
//
// v0.10.310 (Dilim 2c) — ikinci sekme "Şablonlar": Drain templater'ın
// KALICI şablonları (/api/logs/templates). Fark açıkça yazılır: Desenler
// pencerenin örneği, Şablonlar 5 dk'da ≤1000 satırlık örneklemeden
// biriken kalıcı liste; totalCount pencere sayımı DEĞİL. Sekme yerel
// tercih (panelin açık/kapalı durumu gibi); fetch yalnız aktif sekme için.
import { useMemo, useState, type ReactNode } from 'react';
import { TabStrip } from '@/components/ui/TabStrip'; // v0.10.456 (D5)
import { rowActivation } from '@/lib/a11y'; // v0.10.455 (dış denetim D3 dilim 3)
import { useLogsPatterns, useLogsTemplates } from '@/lib/queries';
import type { LogsParams } from '@/lib/api';
import type { LogPatternGroup, LogTemplate } from '@/lib/types';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState,
  type ColumnDef, type DataTableStateProps,
} from '@/components/ui/DataTable';
import { Button } from '@/components/ui/Button';
import { trendCell, trendLabel, firstSeenBadge, insightsNotes } from '@/lib/logPatternsTrend'; // v0.10.508 (C6); v0.10.1896 (4.2)
import { Sparkline } from '@/components/Sparkline'; // v0.10.1896 — tablo içi mini grafik emsali (SVG)
import { PatternCompare } from '@/components/coremetry/logs/PatternCompare'; // v0.10.1897 (4.4)
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { PATTERN_COMPARE_LABEL, PATTERN_COMPARE_MODES, type PatternCompareMode } from '@/lib/logsPatternCompare';
import { getItem, setItem } from '@/lib/storage';
import { IconButton } from '@/components/ui/IconButton'; // v0.10.502 (B6)
import { sevClass, sevName, tsLong, tsShort } from '@/lib/utils';
import type { LogPatternsResult } from '@/lib/types';
import { getRaw, setRaw } from '@/lib/storage';

export function agoLabel(ns: number, nowMs = Date.now()): string {
  const s = Math.max(0, Math.round((nowMs - ns / 1e6) / 1000));
  if (s < 60) return `${s} sn önce`;
  if (s < 3600) return `${Math.round(s / 60)} dk önce`;
  if (s < 86400) return `${Math.round(s / 3600)} sa önce`;
  return `${Math.round(s / 86400)} g önce`;
}

// coveredLabel — v0.10.441 (log arama denetimi C4): tavan dolduysa
// örneklemenin GERÇEKTEN kapsadığı alt pencere. Kapı sampled >= cap
// (truncated değil — Total güvenilmez olabilir, sunucu clamp'i truncated'ı
// false bırakır). Tavan dolmadıysa null: aralık yalnız veri yayılımıdır,
// "daraltılmış tarama" diye göstermek yeni bir yalan olurdu. Eski
// önbellek gövdesinde alanlar yok → null.
export function coveredLabel(d: Pick<LogPatternsResult, 'sampled' | 'cap' | 'coveredFromNs' | 'coveredToNs'> | null | undefined): string | null {
  if (!d || !d.coveredFromNs || !d.coveredToNs || d.cap <= 0 || d.sampled < d.cap) return null;
  const spanS = Math.max(0, Math.round((d.coveredToNs - d.coveredFromNs) / 1e9));
  const span = spanS < 60 ? `${spanS} sn` : spanS < 3600 ? `${Math.round(spanS / 60)} dk` : `${(spanS / 3600).toFixed(1)} sa`;
  return `kapsanan: ${tsShort(d.coveredFromNs).slice(0, 8)}–${tsShort(d.coveredToNs).slice(0, 8)} (${span}, en yeni uç)`;
}

// templatesSinceRung — v0.10.310: /api/logs/templates `since` sunucu cache
// anahtarına girer → pencere başlangıcı "şimdi"ye göre rung'lanır
// (1h/6h/24h/168h/720h). Kalıcı tablo last_seen ile süzülür; geçmiş bir
// pencere için de "şimdi − from" doğru üst sınırdır. from yoksa 24h.
export function templatesSinceRung(fromNs?: number, nowMs = Date.now()): string {
  if (!fromNs) return '24h';
  const h = (nowMs - fromNs / 1e6) / 3.6e6;
  if (h <= 1) return '1h';
  if (h <= 6) return '6h';
  if (h <= 24) return '24h';
  if (h <= 168) return '168h';
  return '720h';
}

export type PanelTab = 'patterns' | 'templates';
const TAB_KEY = 'logs.patterns.tab';
const TEMPLATES_LIMIT = 200;

// COLS — v0.10.508 (C6): Δ sütunu (önceki pencere örneklemesine göre oran /
// YENİ). Taban istenmediyse hücre "—"; sıralama değeri satır başına
// trendSortValue ile (taban satır verisinde değil sonuç zarfında olduğu
// için sortValue'ya kapanışla giriyor — aşağıda colsFor).
// v0.10.945 (tablo standardı dilim 3) — hücre görünümü kolon bayraklarında:
// 11/11.5px hücreler tablo boyunda, ikincil olanlar renkle (S3); eylem
// kolonu `kind: 'actions'` (sahte sortValue yok, T3/T8).
const COLS: ColumnDef<LogPatternGroup>[] = [
  { id: 'template', label: 'Desen', sortValue: r => r.template, naturalDir: 'asc', flex: true, mono: true },
  { id: 'count', label: 'Sayı', sortValue: r => r.count, numeric: true, width: 110 },
  // v0.10.1896 (4.2) — "Δ" → "Önceki pencere": oran ifadesi + "önceki pencerede yok" / "ilk kez görüldü".
  { id: 'trend', label: 'Önceki pencere', sortValue: r => r.new ? Number.MAX_SAFE_INTEGER : (r.ratio ?? 0), numeric: true, width: 190, minWidth: 120 },
  { id: 'severity', label: 'Seviye', sortValue: r => r.severity, numeric: true, width: 84 },
  { id: 'services', label: 'Servisler', sortValue: r => r.services.join(','), naturalDir: 'asc', width: 170, mono: true, tone: () => 'muted' },
  { id: 'lastSeen', label: 'Son', sortValue: r => r.lastSeen, numeric: true, width: 96, tone: () => 'muted' },
  // v0.10.945 — eylem kolonu boyutlanmaz (T8): genişlik Ara + ⊕ + ⊖ içeriğine göre; 64px'te ⊖ kırpılıyordu.
  { id: 'act', label: 'Eylemler', kind: 'actions', width: 108, minWidth: 108 },
];

// v0.10.1896 (4.2) — eğilim sütunu yalnız `logs_search.patternInsights` açıkken
// (ayrı storageKey: kapalı kipin kayıtlı genişlikleri bozulmaz).
const COLS_INSIGHTS: ColumnDef<LogPatternGroup>[] = [
  ...COLS.slice(0, 2),
  { id: 'spark', label: 'Eğilim', width: 110, minWidth: 90, priority: 3 },
  ...COLS.slice(2),
];

const TCOLS: ColumnDef<LogTemplate>[] = [
  { id: 'template', label: 'Şablon', sortValue: r => r.template, naturalDir: 'asc', flex: true, mono: true },
  { id: 'totalCount', label: 'Toplam', sortValue: r => r.totalCount, numeric: true, width: 110 },
  { id: 'services', label: 'Servisler', sortValue: r => r.services.join(','), naturalDir: 'asc', width: 170, mono: true, tone: () => 'muted' },
  { id: 'firstSeen', label: 'İlk', sortValue: r => r.firstSeen, numeric: true, width: 96, tone: () => 'muted' },
  { id: 'lastSeen', label: 'Son', sortValue: r => r.lastSeen, numeric: true, width: 96, tone: () => 'muted' },
  { id: 'act', label: 'Eylemler', kind: 'actions', width: 64 },
];

export function LogPatternsPanel({ params, open, onSearch, tab: tabProp, onTab, embedded = false, toolbar, aside, insights = false, compare = null, onCompare }: {
  params: LogsParams;
  /** v0.10.1897 (Faz 4 dilim 4.4) — iki aralık karşılaştırması (URL `cmp`). Seçiliyken
   *  desen tablosu yerine Yeni / Artan / Kaybolan çizilir ve tek-aralık desen isteği
   *  GİTMEZ (ES işi: iki tek-sayfa örnekleme + bir _msearch). onCompare verilmezse
   *  seçici çizilmez. */
  compare?: PatternCompareMode | null;
  onCompare?: (m: PatternCompareMode | null) => void;
  /** v0.10.1896 (Faz 4 dilim 4.2) — `logs_search.patternInsights`: eğilim çizgisi +
   *  "ilk kez görüldü" (ek ES sorgusu; yalnız bu sekme açıkken istenir). */
  insights?: boolean;
  /** v0.10.1499 (/logs Discover dilim 2) — sayfanın görünüm anahtarı
   *  (Logs | Desenler | Şablonlar) sekmeyi sürer: kendi TabStrip'i çizilmez,
   *  panel tablonun YERİNE oturur (kart boşluğu yok). Varsayılan false =
   *  bağımsız panel, bayt bayt eski hâl. */
  embedded?: boolean;
  /** Başlık satırının sağ ucu (embedded: ✨ Desenleri anlat). */
  toolbar?: ReactNode;
  /** Başlığın altı, tablonun üstü (embedded: AI desen anlatımı kutusu). */
  aside?: ReactNode;
  /** v0.10.448 (C8) — sekme dışarıdan (URL) sürülebilir; verilmezse localStorage. */
  tab?: PanelTab;
  onTab?: (t: PanelTab) => void;
  open: boolean;
  // v0.10.502 (B6) — mode: 'replace' (Ara/satır, varsayılan) | 'and' (⊕) | 'not' (⊖).
  onSearch: (query: string, mode?: 'replace' | 'and' | 'not') => void;
}) {
  const [localTab, setLocalTab] = useState<PanelTab>(() => (getRaw(TAB_KEY) === 'templates' ? 'templates' : 'patterns'));
  const tab = tabProp ?? localTab;
  const switchTab = (t: PanelTab) => { setRaw(TAB_KEY, t); setLocalTab(t); onTab?.(t); };
  const since = useMemo(() => templatesSinceRung(params.from), [params.from]);

  // v0.10.508 (C6) — Trend: önceki eşit pencerenin örneklemesiyle Δ/YENİ.
  // Kalıcı (localStorage); yalnız açıkken ek bir ES sayfası (500) çekilir.
  const [trend, setTrend] = useState<boolean>(() => { try { return getItem<string>('logs.patternsTrend', '0') === '1'; } catch { return false; } });
  const toggleTrend = () => setTrend(v => { const n = !v; try { setItem('logs.patternsTrend', n ? '1' : '0'); } catch { /* storage yok */ } return n; });
  const q = useLogsPatterns({ ...params, limit: 50, ...(trend ? { baseline: 1 as const } : {}), ...(insights ? { insights: 1 as const } : {}) }, open && tab === 'patterns' && !compare);
  const tq = useLogsTemplates(
    { since, limit: TEMPLATES_LIMIT, sort: 'last_seen', service: params.service || undefined },
    open && tab === 'templates',
  );
  const rows = useMemo(() => q.data?.groups ?? [], [q.data]);
  const trows = useMemo(() => tq.data ?? [], [tq.data]);
  const dt = useDataTable<LogPatternGroup>({
    storageKey: insights ? 'logs-patterns-insights' : 'logs-patterns',
    columns: insights ? COLS_INSIGHTS : COLS,
    rows,
    initialSort: { id: 'count', dir: 'desc' },
    onOpen: r => { if (r.query) onSearch(r.query); },
  });
  const tdt = useDataTable<LogTemplate>({
    storageKey: 'logs-templates',
    columns: TCOLS,
    rows: trows,
    initialSort: { id: 'lastSeen', dir: 'desc' },
    onOpen: r => { if (r.query) onSearch(r.query); },
  });
  if (!open) return null;
  const d = q.data;
  const maxCount = rows.reduce((m, r) => Math.max(m, r.count), 0);
  const maxTotal = trows.reduce((m, r) => Math.max(m, r.totalCount), 0);
  // v0.10.954 — tablo standardı T12: iki sekmenin durumları kendi tablosunun
  // İÇİNDE, başlık durur. Sıra eskisiyle aynı (bekliyor → hata → boş); hata
  // satırı sunucunun metnini eskisi gibi taşır. Panelin kendi süzgeci ve
  // temizleme eylemi yok — boş liste eskisi gibi "boş". Hata, eldeki (bayat)
  // satırların yerine geçer — Hosts/Events'teki `q.isError ? null` ile aynı
  // (eski Empty dolu tabloda da görünüyordu; yenileme hatası saklanmaz).
  // Mesajsız hatada özne ("Desenler alınamadı") yine görünür.
  const errMsg = (subject: string, e: unknown) => (e instanceof Error && e.message ? `${subject}: ${e.message}` : subject);
  const patternsState: Omit<DataTableStateProps<LogPatternGroup>, 'dt'> =
    q.isPending ? { kind: 'loading', message: 'Desenler çıkarılıyor' }
    : q.isError ? { kind: 'error', message: errMsg('Desenler alınamadı', q.error) }
    : { kind: 'empty', message: 'Bu pencerede desen yok' };
  const templatesState: Omit<DataTableStateProps<LogTemplate>, 'dt'> =
    tq.isPending ? { kind: 'loading', message: 'Şablonlar alınıyor' }
    : tq.isError ? { kind: 'error', message: errMsg('Şablonlar alınamadı', tq.error) }
    : {
      kind: 'empty',
      message: 'Bu aralıkta kalıcı şablon yok — Templater 5 dakikada bir örnekler; yeni kurulumda ilk şablonlar birkaç dakika sonra görünür.',
    };
  /* v0.10.933 (tablo standardı T2) — koşullu cursor kalktı: el imleci + hover
     globals.css'ten role=button işaretiyle gelir, işaret yalnız sorgusu olan
     (açılan) satırda. */
  return (
    <div className={embedded ? 'card lp-panel lp-embedded' : 'card lp-panel'} style={embedded ? undefined : { padding: '10px 12px', marginBottom: 10 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 6 }}>
        {!embedded && (
          <TabStrip ariaLabel="Log desenleri paneli" value={tab} onChange={switchTab} tabs={[
            { key: 'patterns', label: 'Desenler', title: 'Penceredeki mesajlar imzaya göre gruplanır (örneklemeli)' },
            { key: 'templates', label: 'Şablonlar', title: "Drain templater'ın kalıcı şablonları (5 dk'da ≤1000 satır örneklenir)" },
          ]} />
        )}
        {tab === 'patterns' && onCompare && <span className="lp-cmp-label">Karşılaştır:</span>}
        {tab === 'patterns' && onCompare && (
          <SegmentedControl<'off' | PatternCompareMode> size="sm" aria-label="Karşılaştır" value={compare ?? 'off'}
            onChange={v => onCompare(v === 'off' ? null : v)}
            options={[{ value: 'off', label: 'Tek aralık' }, ...PATTERN_COMPARE_MODES.map(m => ({ value: m, label: PATTERN_COMPARE_LABEL[m] }))]} />
        )}
        {tab === 'patterns' && !compare && (
          <Button variant="secondary" active={trend} size="xs" onClick={toggleTrend} /* v0.10.1342 — açık/kapalı durum: active, birincil değil */
            title="Hemen önceki eşit pencerenin örneklemesiyle (tek ek sayfa, 500 satır) karşılaştırır — ipucu, kanıt değil">
            Önceki pencere
          </Button>
        )}
        <span style={{ fontSize: 11, color: 'var(--text3)' }}>
          {/* v0.10.954 — hata anında bayat örnek/toplam sayıları güncel gibi sunulmaz. */}
          {tab === 'patterns' && compare ? null : tab === 'patterns' ? (d && !q.isError ? (
            <>
              {d.sampled.toLocaleString()} örnek satır{d.truncated ? ` (tavan ${d.cap.toLocaleString()})` : ''}
              {' · '}pencere toplamı {d.total.toLocaleString()}{' · '}{d.distinct} desen
              {coveredLabel(d) && <span title="Tavan doldu: sayımlar yalnız bu alt pencereyi anlatır, seçili pencerenin tamamını değil (v0.10.441)">{' · '}{coveredLabel(d)}</span>}
              {d.baseline && (d.baseline.degraded
                ? <span className="badge b-warn" style={{ marginLeft: 6 }} title={d.baseline.reason}>taban okunamadı</span>
                : <span title="Δ/YENİ tabanı: hemen önceki eşit pencerenin örneklemesi (ipucu, kanıt değil)">{' · '}taban {d.baseline.sampled.toLocaleString()} satır / {d.baseline.distinct} desen{d.baseline.truncated ? ' (tavan)' : ''}</span>)}
              {d.degraded && <span className="badge b-warn" style={{ marginLeft: 6 }}>{d.reason ?? 'degraded'}</span>}
              {/* v0.10.1896 — eğilim / ilk görülme durumu (kısmi ve hata ayrı söylenir) */}
              {insightsNotes(d.insights).map(n => (
                <span key={n.text} className={n.warn ? 'badge b-warn ml-1.5' : 'ml-1.5'} data-testid="patterns-insights-note">{n.warn ? n.text : `· ${n.text}`}</span>
              ))}
            </>
          ) : 'sayımlar en yeni örnek satırlara göredir') : (
            <>
              {/* v0.10.969 — desenlerle aynı kural: şablon sorgusu hatadayken
                  bayat (ya da hiç okunmamış "0") şablon sayısı gösterilmez;
                  sayımın ne olduğunu anlatan not kalır. */}
              {!tq.isError && (
                <>
                  {trows.length} kalıcı şablon{trows.length >= TEMPLATES_LIMIT ? ` (tavan ${TEMPLATES_LIMIT})` : ''}
                  {' · '}son {since} içinde görülen{params.service ? ` · ${params.service}` : ''}
                  {' · '}
                </>
              )}
              toplam = 5 dk'da ≤1000 satırlık örneklemeden biriken gözlem, pencere sayımı değil
            </>
          )}
        </span>
        {toolbar && <span className="lp-toolbar">{toolbar}</span>}
      </div>
      {aside}
      {tab === 'patterns' && compare && <PatternCompare params={params} mode={compare} onSearch={onSearch} />}
      {tab === 'patterns' && !compare && (
        <>
          <div className="table-wrap is-scroll">
            <table {...dt.tableProps}>
              <DataTableColgroup dt={dt} />
              <DataTableHead dt={dt} />
              <tbody>
                {q.isError || dt.sortedRows.length === 0 ? <DataTableState dt={dt} {...patternsState} /> : dt.sortedRows.map(r => {
                  const share = maxCount > 0 ? (r.count / maxCount) * 100 : 0;
                  return (
                    // v0.10.945 — örnek satır ipucu satırdan şablon hücresine (T7).
                    <tr key={r.hash} className="lp-row cv-row"
                      {...(r.query ? rowActivation(() => onSearch(r.query)) : {})}>
                      <DataTableCell dt={dt} col="template" row={r} value={r.template} title={r.sample} />
                      <td className="num">
                        <span className="lp-bar" style={{ width: `${share}%` }} aria-hidden="true" />
                        <span style={{ position: 'relative' }}>{r.count.toLocaleString()}</span>
                      </td>
                      {insights && (
                        <td data-testid="pattern-spark">
                          {r.trend && r.trend.length > 1
                            ? <Sparkline values={r.trend} mode="count" width={96} height={20}
                                title={`Pencere boyunca bu desene uyan satırlar (${d?.insights?.bucketSec ? Math.round(d.insights.bucketSec / 60) : '?'} dk kovalar)${r.trendPartial ? ' — kısmi yanıt' : ''}`} />
                            : <span className="cell-empty" title="Yalnız ilk 20 desen için çizilir">—</span>}
                        </td>
                      )}
                      <td className="num" title={d?.baseline && !d.baseline.degraded
                        ? `Önceki pencerenin örneklemesinde ${r.prevCount ?? 0} satır; oran örneklemdeki paya göre${r.new ? ' (örneklemde yok — ipucu, kanıt değil)' : ''}`
                        : '"Önceki pencere" kapalı ya da önceki pencere okunamadı'}>
                        {(() => {
                          const c = trendCell(r, d?.baseline, d?.sampled);
                          if (c.kind !== 'new') {
                            return <span className={c.kind === 'ratio' ? (c.up ? 'cell-err' : '') : 'cell-faint'}>{trendLabel(c)}</span>;
                          }
                          const fs = firstSeenBadge(r, d?.insights?.lookbackDays);
                          return (
                            <>
                              <span className="badge b-gray">{trendLabel(c)}</span>
                              {fs && <>{' '}<span className={fs.tone === 'warn' ? 'badge b-warn' : 'cell-faint'} title={fs.title} data-testid="pattern-first-seen">{fs.text}</span></>}
                            </>
                          );
                        })()}
                      </td>
                      <td><span className={sevClass(r.severity)}>{r.severityText || sevName(r.severity)}</span></td>
                      <DataTableCell dt={dt} col="services" row={r} title={r.services.join(', ')}
                        value={`${r.services.join(', ')}${r.serviceCount > r.services.length ? ` +${r.serviceCount - r.services.length}` : ''}`} />
                      <DataTableCell dt={dt} col="lastSeen" row={r} value={agoLabel(r.lastSeen)} title={tsLong(r.lastSeen)} />
                      <DataTableCell dt={dt} col="act" row={r}>
                        {r.query && (
                          <span style={{ display: 'inline-flex', gap: 4, alignItems: 'center' }}>
                            <Button variant="secondary" size="xs" className="lp-search"
                              title={`Ara: ${r.query}`}
                              onClick={e => { e.stopPropagation(); onSearch(r.query); }}>Ara</Button>
                            {/* v0.10.502 (B6) — mevcut metni ezmeden ekle / hariç tut */}
                            <IconButton variant="bare" size="xs" className="ib-add"
                              // v0.10.926 — Tooltip (tetiğe ve kutuya boş title basar;
                              // ata hücre/satır ipucu sızmaz).
                              tooltip={`Mevcut aramaya ekle: ${r.query}`} aria-label={`Mevcut aramaya ekle: ${r.query}`}
                              onClick={e => { e.stopPropagation(); onSearch(r.query, 'and'); }} icon="⊕" />
                            <IconButton variant="bare" size="xs" className="ib-not"
                              tooltip={`Hariç tut: NOT (${r.query})`} aria-label={`Hariç tut: NOT (${r.query})`}
                              onClick={e => { e.stopPropagation(); onSearch(r.query, 'not'); }} icon="⊖" />
                          </span>
                        )}
                      </DataTableCell>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </>
      )}
      {tab === 'templates' && (
        <>
          <div className="table-wrap is-scroll">
            <table {...tdt.tableProps}>
              <DataTableColgroup dt={tdt} />
              <DataTableHead dt={tdt} />
              <tbody>
                {tq.isError || tdt.sortedRows.length === 0 ? <DataTableState dt={tdt} {...templatesState} /> : tdt.sortedRows.map(r => {
                  const share = maxTotal > 0 ? (r.totalCount / maxTotal) * 100 : 0;
                  return (
                    <tr key={r.id} className="lp-row cv-row"
                      {...(r.query ? rowActivation(() => onSearch(r.query)) : {})}>
                      <DataTableCell dt={tdt} col="template" row={r} title={r.sample}>
                        {r.exceptionType && <span className="badge b-err" style={{ marginRight: 6 }}>{r.exceptionType}</span>}
                        {r.template}
                      </DataTableCell>
                      <td className="num">
                        <span className="lp-bar" style={{ width: `${share}%` }} aria-hidden="true" />
                        <span style={{ position: 'relative' }}>{r.totalCount.toLocaleString()}</span>
                      </td>
                      <DataTableCell dt={tdt} col="services" row={r} value={r.services.join(', ')} />
                      <DataTableCell dt={tdt} col="firstSeen" row={r} value={agoLabel(r.firstSeen)} title={tsLong(r.firstSeen)} />
                      <DataTableCell dt={tdt} col="lastSeen" row={r} value={agoLabel(r.lastSeen)} title={tsLong(r.lastSeen)} />
                      <DataTableCell dt={tdt} col="act" row={r}>
                        {r.query && (
                          <Button variant="secondary" size="xs" className="lp-search"
                            title={`Ara: ${r.query}`}
                            onClick={e => { e.stopPropagation(); onSearch(r.query); }}>Ara</Button>
                        )}
                      </DataTableCell>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </>
      )}
    </div>
  );
}
