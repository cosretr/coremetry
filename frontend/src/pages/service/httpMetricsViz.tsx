// httpMetricsViz.tsx — v0.10.1247 (operatör: HTTP Metrics "Grafana ile birebir").
// Grafana panel karşılıkları, bağımlılıksız SVG (uPlot zaman serisi için
// kalır; pasta/gauge uPlot'ta yok, ağır kütüphane eklenmez):
//   stat  (colorMode background + graphMode area) → HttpStat
//   gauge (SLI)                                    → HttpGauge
//   piechart (donut + tablo lejant: değer, %)      → HttpDonut
// Renkler tema token'larından (globals.css); sabit hex yok.
import { useState, type CSSProperties, type ReactNode } from 'react';
import { Sparkline } from '@/components/Sparkline';
import { Badge, Button, Card, PanelTitle } from '@/components/ui';
import { httpEnvScopeNote, type HttpEnvScopeView } from '@/lib/httpEnvScopeNote';
import { Spinner } from '@/components/Spinner';
import { MultiLineChart } from '@/components/MultiLineChart';
import { seriesPalette, inkOn } from '@/lib/chartFmt';
import {
  fmtShare, donutSlices, httpSeriesColors, httpPanelZeroBase, fillStepGaps, type BarRow, type DonutSlice, type HttpTone,
} from '@/lib/httpMetrics';
import type { ChartTimeRegion } from '@/lib/chart/overlays';
import type { SpanMetricSeries } from '@/lib/types';

const TONE_VAR: Record<'ok' | 'warn' | 'err', string> = { ok: 'var(--ok)', warn: 'var(--warn)', err: 'var(--err)' };
const TONE_TOKEN: Record<'ok' | 'warn' | 'err', string> = { ok: '--ok', warn: '--warn', err: '--err' };
/** Renkli zemin üstü yazı rengi: token'ın çözülmüş değerinden (inkOn). */
function inkFor(tone: 'ok' | 'warn' | 'err'): string {
  const c = getComputedStyle(document.documentElement).getPropertyValue(TONE_TOKEN[tone]).trim();
  return c ? inkOn(c) : 'var(--text)';
}

export type StatTone = HttpTone | 'ok';

/**
 * Grafana stat paneli. `tone` verilirse arka plan eşik rengi (yeşil / sarı /
 * kırmızı, panodaki colorMode=background); verilmezse değer `accent` renginde
 * (Toplam İstek mavi, Throughput mor). Altta alan grafiği (graphMode=area).
 */
export function HttpStat({ label, value, tone, accent = 'var(--info)', spark, title }: {
  label: string; value: string; tone?: StatTone; accent?: string; spark?: (number | null)[]; title?: string;
}) {
  const bg = tone ? TONE_VAR[tone] : undefined;
  const ink = tone ? inkFor(tone) : undefined;
  return (
    <div title={title} style={{
      position: 'relative', overflow: 'hidden', borderRadius: 6, minWidth: 0, minHeight: 104,
      border: '1px solid var(--border)', padding: '8px 10px',
      background: bg ? `color-mix(in srgb, ${bg} 82%, var(--bg1))` : 'var(--bg1)',
    }}>
      <div style={{ fontSize: 12, fontWeight: 600, color: ink ?? 'var(--text2)', position: 'relative', zIndex: 1 }}>{label}</div>
      <div style={{
        fontSize: 30, fontWeight: 600, textAlign: 'center', marginTop: 8, position: 'relative', zIndex: 1,
        color: ink ?? accent, fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap',
      }}>{value}</div>
      {spark && spark.length > 1 && (
        <div style={{ position: 'absolute', left: 0, right: 0, bottom: 0, opacity: bg ? 0.45 : 0.6 }}>
          <Sparkline values={spark} width={400} height={34} color={ink ?? accent} />
        </div>
      )}
    </div>
  );
}

/** Grafana gauge (yarım yay): 0–1 oran, eşik rengi ile dolu. */
export function HttpGauge({ label, ratio, tone, text }: { label: string; ratio: number | null; tone?: StatTone; text: string }) {
  const r = 46, cx = 60, cy = 58, w = 10;
  const v = ratio === null ? 0 : Math.max(0, Math.min(1, ratio));
  const pt = (f: number) => {
    const a = Math.PI * (1 - f);
    return `${cx + r * Math.cos(a)},${cy - r * Math.sin(a)}`;
  };
  const arc = (f: number) => `M ${pt(0)} A ${r} ${r} 0 0 1 ${pt(f)}`;
  // Dış eşik halkası (Grafana showThresholdMarkers): %0–95 kırmızı, %95–99 sarı, %99+ yeşil.
  const R2 = r + 8;
  const at = (f: number, rr: number) => { const a = Math.PI * (1 - f); return `${cx + rr * Math.cos(a)},${cy - rr * Math.sin(a)}`; };
  const band = (a: number, b: number) => `M ${at(a, R2)} A ${R2} ${R2} 0 0 1 ${at(b, R2)}`;
  const color = TONE_VAR[tone ?? 'ok'];
  return (
    <div style={{ borderRadius: 6, border: '1px solid var(--border)', padding: '8px 10px', background: 'var(--bg1)', minHeight: 104, minWidth: 0 }}>
      <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--text2)' }}>{label}</div>
      <svg viewBox="-4 -6 128 72" style={{ width: '100%', maxHeight: 84, display: 'block' }} role="img" aria-label={`${label}: ${text}`}>
        <path d={arc(1)} fill="none" stroke="var(--bg2)" strokeWidth={w} strokeLinecap="round" />
        {/* Eşik bandı (panodaki showThresholdMarkers): %95 kırmızı · %99 sarı · üstü yeşil. */}
        <path d={band(0, 0.95)} fill="none" stroke="var(--err)" strokeWidth={3} />
        <path d={band(0.95, 0.99)} fill="none" stroke="var(--warn)" strokeWidth={3} />
        <path d={band(0.99, 1)} fill="none" stroke="var(--ok)" strokeWidth={3} />
        {v > 0 && <path d={arc(v)} fill="none" stroke={color} strokeWidth={w} strokeLinecap="round" />}
        <text x={cx} y={cy - 4} textAnchor="middle" fontSize={15} fontWeight={600} fill="var(--text)">{text}</text>
      </svg>
    </div>
  );
}

/**
 * Grafana piechart (donut) + tablo lejant (değer, yüzde).
 * v0.10.1258 (operatör: "pasta dilimleri tıklanamıyor, tooltip yok") —
 * dilim ya da lejant satırı üzerine gelince öne çıkar, merkezde ad / değer /
 * yüzde yazar; `onSelect` verilirse tık (ve Enter) seçer. Klavye: satırlar
 * odaklanabilir (lejant satırı ui Button atomu).
 */
export function HttpDonut({ slices, fmt, colorOf, onSelect }: {
  slices: DonutSlice[]; fmt: (v: number) => string; colorOf?: (label: string, i: number) => string | undefined;
  onSelect?: (label: string) => void;
}) {
  const [hover, setHover] = useState<number | null>(null);
  const pal = seriesPalette();
  const color = (l: string, i: number) => colorOf?.(l, i) || pal[i % pal.length];
  const R = 42, C = 2 * Math.PI * R;
  const active = hover !== null ? slices[hover] : null;
  let acc = 0;
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'minmax(120px, 180px) minmax(0, 1fr)', gap: 12, alignItems: 'center' }}>
      <svg viewBox="0 0 120 120" style={{ width: '100%', display: 'block' }} role="img" aria-label="Dağılım"
        onMouseLeave={() => setHover(null)}>
        <g transform="rotate(-90 60 60)">
          {slices.map((s, i) => {
            const len = (s.pct / 100) * C;
            const el = (
              <circle key={s.label} cx={60} cy={60} r={R} fill="none" stroke={color(s.label, i)}
                strokeWidth={hover === i ? 24 : 18} opacity={hover === null || hover === i ? 1 : 0.35}
                strokeDasharray={`${len} ${C - len}`} strokeDashoffset={-acc}
                style={{ cursor: onSelect ? 'pointer' : 'default', transition: 'stroke-width .12s, opacity .12s' }}
                onMouseEnter={() => setHover(i)}
                onClick={onSelect ? () => onSelect(s.label) : undefined}>
                <title>{`${s.label}: ${fmt(s.value)} (${fmtShare(s.pct)})`}</title>
              </circle>
            );
            acc += len;
            return el;
          })}
        </g>
        {active ? (
          <>
            <text x={60} y={56} textAnchor="middle" fontSize={11} fontWeight={600} fill="var(--text)">{fmtShare(active.pct)}</text>
            <text x={60} y={70} textAnchor="middle" fontSize={8} fill="var(--text2)">{fmt(active.value)}</text>
          </>
        ) : slices[0] && slices[0].pct >= 8 && (
          <text x={60} y={64} textAnchor="middle" fontSize={12} fontWeight={600} fill="var(--text)">{fmtShare(slices[0].pct)}</text>
        )}
      </svg>
      <div style={{ display: 'grid', gap: 2, fontSize: 12, minWidth: 0 }}>
        {active && (
          <div className="mono dt-trunc" title={active.label} style={{ fontSize: 11, color: 'var(--text)', fontWeight: 600, marginBottom: 2 }}>
            {active.label}
          </div>
        )}
        {slices.map((s, i) => {
          const row = (
            <span style={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) auto auto', columnGap: 10, alignItems: 'center', width: '100%' }}>
              <span className="mono" title={s.label} style={{ display: 'flex', gap: 6, alignItems: 'center', minWidth: 0 }}>
                <span aria-hidden="true" style={{ flex: '0 0 auto', width: 10, height: 3, borderRadius: 1, background: color(s.label, i) }} />
                <span className="dt-trunc">{s.label}</span>
              </span>
              <span style={{ fontVariantNumeric: 'tabular-nums', textAlign: 'right' }}>{fmt(s.value)}</span>
              <span style={{ fontVariantNumeric: 'tabular-nums', textAlign: 'right', color: 'var(--text2)' }}>{fmtShare(s.pct)}</span>
            </span>
          );
          return onSelect ? (
            <Button key={s.label} variant="ghost" size="xs" onClick={() => onSelect(s.label)}
              onMouseEnter={() => setHover(i)} onMouseLeave={() => setHover(null)}
              onFocus={() => setHover(i)} onBlur={() => setHover(null)}
              title={`${s.label} — süzgeç olarak uygula`}
              style={{ justifyContent: 'stretch', textAlign: 'left', background: hover === i ? 'var(--bg2)' : undefined }}>
              {row}
            </Button>
          ) : (
            <div key={s.label} onMouseEnter={() => setHover(i)} onMouseLeave={() => setHover(null)}
              style={{ padding: '2px 4px', borderRadius: 3, background: hover === i ? 'var(--bg2)' : undefined }}>
              {row}
            </div>
          );
        })}
      </div>
    </div>
  );
}

const cssToken = (t: string) => getComputedStyle(document.documentElement).getPropertyValue(t);

/**
 * v0.10.1352 — HTTP Metrics zaman serisi paneli (sekmenin `chart()` gövdesi,
 * endpoint sayfasının metrik kipiyle PAYLAŞILIR): Card + PanelTitle +
 * MultiLineChart, tüm seriler (≤60), açık lejant, sabit status/yüzdelik
 * renkleri. Seriler çağıranda relabel'lı gelir.
 *
 * v0.10.1355 (operatör-raporlu, prod) — çizim politikası BURADA, iki yüzey
 * için tek yer: (a) y tabanı 0 (`httpPanelZeroBase(unit)` → mevcut
 * `zeroBase` kanalı; p50 2.621–2.653 ms artık 30 µs'lik gürültüyü panel
 * boyu oynaklık gibi çizmez); (b) eksik adımlar null (`fillStepGaps`,
 * `stepSec` = sorgunun adımı) — seyrek seri boşluğu köprülemez, tek örnek
 * CorePanel'in yalıtık-nokta filtresiyle nokta olur; (c) `note` grafiğin
 * altında tek satır soluk açıklama (field-hint).
 */
export function HttpChartPanel({
  title, series, unit, stepSec, loading, syncKey, legendKey, right, note, regions, onZoom, onZoomReset, legendOpen = true, seriesColors,
}: {
  /** v0.10.2055 — seri renkleri (ad → çözülmüş renk); verilirse panelin kod paletinin yerine (anomali detayı: sapan pod). */
  seriesColors?: ReadonlyMap<string, string>;
  title: string; series: SpanMetricSeries[]; unit: string;
  /** Range sorgusunun adımı (sn) — `stepSeconds(windowSec)`; boşluk dolgusu buna göre. */
  stepSec: number;
  loading?: boolean;
  syncKey: string; legendKey: string; right?: ReactNode;
  note?: ReactNode;
  regions?: ChartTimeRegion[];
  onZoom?: (fromUnixSec: number, toUnixSec: number) => void;
  onZoomReset?: () => void;
  /** v0.10.1547 — seri lejantı varsayılan açık mı (Kafka panelleri kapalı ister). */
  legendOpen?: boolean;
}) {
  const drawn = fillStepGaps(series, stepSec);
  return (
    <Card header={<PanelTitle right={right}>{title}</PanelTitle>}>
      {loading ? <Spinner /> : series.length === 0
        ? <div className="field-hint">Bu pencerede veri yok.</div>
        : <MultiLineChart series={drawn} unit={unit} height={220} syncKey={syncKey} legendStorageKey={legendKey}
            maxSeries={60} legendDefaultOpen={legendOpen} regions={regions} onZoom={onZoom} onZoomReset={onZoomReset}
            zeroBase={httpPanelZeroBase(unit)}
            seriesColors={seriesColors?.size ? seriesColors : httpSeriesColors(series.map(x => x.groupKey[0] ?? ''), cssToken)} />}
      {note && <div className="field-hint">{note}</div>}
    </Card>
  );
}

/** v0.10.1352 — donut paneli (sekmenin `donut()` gövdesi, paylaşılan). */
export function HttpDonutPanel({ title, rows, loading, fmt, colorOf, onSelect }: {
  title: string; rows: BarRow[]; loading?: boolean; fmt: (v: number) => string;
  colorOf?: (label: string) => string | undefined; onSelect?: (label: string) => void;
}) {
  return (
    <Card header={<PanelTitle>{title}</PanelTitle>}>
      {loading ? <Spinner /> : rows.length === 0 ? <div className="field-hint">Bu pencerede veri yok.</div>
        : <HttpDonut slices={donutSlices(rows)} fmt={fmt} colorOf={colorOf} onSelect={onSelect} />}
    </Card>
  );
}

/**
 * v0.10.1377 — metrik kipi ortam süzgeci bandı (HTTP Metrics sekmesi, endpoint
 * sayfası, Overview): uygulandıysa hangi etiket=değer ile (b-info), uygulanamadı
 * / çözülemediyse serilerin TÜM ortamları kapsadığını (b-warn) söyler. Metin
 * SAF httpEnvScopeNote'tan; env/cluster seçili değilse hiçbir şey çizmez.
 */
export function HttpEnvScopeBand({ view, style }: { view: HttpEnvScopeView; style?: CSSProperties }) {
  const n = httpEnvScopeNote(view);
  if (!n) return null;
  return (
    <Badge tone={n.tone} title={n.title} data-testid="http-env-scope-band" data-state={view.state}
      style={{ whiteSpace: 'normal', textAlign: 'left', lineHeight: 1.4, ...style }}>
      {n.text}
    </Badge>
  );
}
