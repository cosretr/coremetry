// LogsQueryBar — v0.10.1498 (/logs Kibana-Discover yeniden tasarımı, dilim 1;
// operatör onaylı taslak 2026-10-24 — v0.10.1332 "Filtre barı · Search son
// filtrenin arkasında" mockup pinini BİLİNÇLİ olarak geçersiz kılar, bkz.
// docs/DECISIONS.md).
//
// Çubukta YALNIZ sorgunun kendisi: Service · Cluster · KQL · [Search | ↻ HH:MM].
// Search ile ↻ tek düğme (Kibana "Update" ⇄ "Refresh"): taslak uygulanan
// süzgeçle aynıyken düğme pencere sonunu gösterir ve tık pencereyi şimdiye
// taşır; taslak değiştiyse "Search" der ve uygular. Canlı kuyrukta pencere
// yok → hep "Search". Sağdaki eylem yuvası (PageControls `actions`, telefonda
// katlanmaz): + Filter ▾ · Live tail · Saved ▾ · Share ▾ · ⋯ — menüler
// Logs.tsx'te kurulup `menus` olarak gelir. Saf sunum: durum Logs.tsx'te.
//
// v0.10.1821 (Log Search Faz 1 dilim 1.5, mockup görünüm 1) — shadcn sayfa
// dilimi: çubuk coremetry/FilterBar (PageControls bileşimi) üstünde; sorgu
// kutusu coremetry/logs/LogQueryInput (command listesi: Değerler / Alanlar /
// Geçmiş, dil rozeti KQL / Lucene); çubuğun ALTINDA akışta konumlu hata
// satırı (LogQueryErrorLine, role=alert). Süzgeç hapları ayrı satırda kalır
// (LogsFilterRow) — FilterBar'ın çip listesi bu dilimde boş. ui/ importu
// ve satır içi stil yok.
import type { ReactNode } from 'react';
import { ServicePicker } from '@/components/ServicePicker';
import { FilterBar } from '@/components/coremetry/FilterBar';
import { LogQueryInput, LogQueryErrorLine } from '@/components/coremetry/logs';
import { Button } from '@/components/shadcn/button';
import { Toggle } from '@/components/shadcn/toggle';
import { NativeSelect, NativeSelectOption } from '@/components/shadcn/native-select';
import type { LogsUrlFilter } from '@/lib/logsUrl';
import type { LogQueryError, LogsQueryLang } from '@/lib/types';
import { fmtClock } from '@/lib/utils';

export interface LogsQueryBarProps {
  draft: LogsUrlFilter;
  onDraft: (next: LogsUrlFilter) => void;
  clusters: string[];
  autocompleteSince: string;
  fields: string[];
  fieldsTotal?: number;
  /** Taslağı uygula (Search). */
  onApply: () => void;
  /** Pencereyi şimdiye taşı + yeniden sorgula (↻). */
  onRefresh: () => void;
  /** Taslak uygulanan süzgeçten farklı mı (isDraftDirty). */
  dirty: boolean;
  /** Pencere sonu (ns) — ↻ etiketi; yoksa yalnız ↻. */
  to?: number;
  live: boolean;
  onToggleLive: () => void;
  liveGap: boolean;
  /** + Filter ▾ — çubukta Live'ın solunda. */
  addFilter: ReactNode;
  /** Saved ▾ · Share ▾ · ⋯ — Live'ın sağında. */
  menus: ReactNode;
  /** v0.10.1820 — yerel sorgu geçmişi (en yeni önde). */
  history?: string[];
  /** v0.10.1821 — dil rozeti: uygulanan aramanın ES'teki dili. */
  queryLang: LogsQueryLang;
  /** `?lang=lucene` zorla geçiş açık mı. */
  forcedLucene: boolean;
  onToggleLang: () => void;
  /** Sunucunun 400 reddi (v0.10.1818) + GÖNDERİLEN sorgu metni. */
  queryError?: LogQueryError | null;
  sentQuery?: string;
  /** İstemci denetimi (kqlLint) mesajı — Enter'da. */
  lint?: string | null;
  onLint?: (msg: string | null) => void;
}

const NO_CHIPS: never[] = [];
const noop = () => {};

export function LogsQueryBar(p: LogsQueryBarProps) {
  const { draft } = p;
  const refreshMode = !p.dirty && !p.live;
  return (
    <>
      <FilterBar chips={NO_CHIPS} onChange={noop} sticky className="ld-qbar" actions={(
        <>
          {p.addFilter}
          {/* v0.10.1318 (K3) — canlı kuyruk bir KİP: tint + nabız noktası
              (.live-on yalnız yerleşim), birincil dolgu değil. v0.10.1821 —
              shadcn Toggle: açık hâl `pressed` (aria-pressed + data-state=on
              seçim tinti); koşullu birincil yok. */}
          <Toggle variant="outline" size="sm" pressed={p.live}
            className={p.live ? 'live-on px-3' : 'px-3'}
            onPressedChange={p.onToggleLive}
            title="Stream the latest logs live (server-pushed; pauses when the tab is hidden)">
            {p.live ? '⏸ Pause Live' : '▶ Live tail'}
          </Toggle>
          {p.live && p.liveGap && (
            <span className="badge b-warn" title="A busy service produced more lines than one tick could read — narrow the filter to see them all.">
              ⚠ high volume — some lines skipped
            </span>
          )}
          {p.menus}
        </>
      )}>
        <ServicePicker value={draft.service} onChange={v => p.onDraft({ ...draft, service: v })}
          placeholder="Service…" width={170} onEnter={p.onApply} />
        {/* v0.5.471 — cluster selector. /api/clusters: typically 1-5 entries
            per install, so a plain select is the right shape. */}
        {p.clusters.length > 0 && (
          <NativeSelect size="sm" value={draft.cluster} aria-label="Cluster"
            onChange={e => p.onDraft({ ...draft, cluster: e.target.value })}
            title="Filter logs to a single k8s/openshift cluster"
            className="ld-cluster">
            <NativeSelectOption value="">All clusters</NativeSelectOption>
            {p.clusters.map(c => <NativeSelectOption key={c} value={c}>{c}</NativeSelectOption>)}
          </NativeSelect>
        )}
        <LogQueryInput
          className="ld-kql"
          // v0.9.291 — değer tamamlaması pencereyle sınırlı (Logs.tsx memo'su).
          since={p.autocompleteSince}
          // v0.9.955 / v0.9.970 — alan adları fields panelinin AYNI
          // /api/logs/fields durumundan (ek ES turu yok) + gerçek yol sayısı.
          fields={p.fields}
          fieldsTotal={p.fieldsTotal}
          history={p.history}
          // v0.9.1004 — telefonda yüzeyde kalır (PageControls lead işareti).
          data-pc-lead
          lang={p.queryLang} forcedLucene={p.forcedLucene} onToggleLang={p.onToggleLang}
          onLint={p.onLint}
          value={draft.search}
          onChange={v => p.onDraft({ ...draft, search: v })}
          onSubmit={p.onApply}
          placeholder='Ara… (KQL: level:error and service.name:"orders-api")' />
        {/* Tek birincil (K4). ↻ kipinde etiket PENCERE SONU (sorgulanan an),
            fetch zamanı değil (cevap 15 sn sunucu önbelleğinden gelebilir —
            v0.10.440 B3). `data-pc-pin`: telefonda panele inmez. */}
        <span className="search-go" data-pc-pin>
          {refreshMode ? (
            <Button aria-label="Yenile" onClick={p.onRefresh}
              title={p.to ? `Veri penceresi ${fmtClock(p.to / 1e6)} anında bitiyor — ↻ pencereyi şimdiye taşır ve yeniden sorgular` : 'Yeniden sorgula'}>
              ↻{p.to ? ` ${fmtClock(p.to / 1e6)}` : ''}
            </Button>
          ) : (
            <Button onClick={p.onApply}>Search</Button>
          )}
          <kbd className="kbd" title="Arama kutusunda (ve + Filter içindeki Trace ID kutusunda) Enter">
            Enter <span aria-hidden="true">↵</span>
          </kbd>
        </span>
      </FilterBar>
      <LogQueryErrorLine lint={p.lint} error={p.queryError} query={p.sentQuery} />
    </>
  );
}
