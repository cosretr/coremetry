// LoginHttpCard — v0.10.1908 (Login yeniden tasarımı dilim 2.3, audit §6.2).
// Özet sütunundaki "OpenTelemetry metrikleri · son 24 saat" kartı: iki gömülü
// CorePanelMulti (Throughput durum kodu serileri + Response time ort/p95),
// ortak `syncKey` (imleç eşlenir), tek statik lejant. LAZY: @grafana chunk'ı
// yalnız `http_24h` geldikten sonra iner (LoginSummary.tsx `lazy` ile çağırır).
// Veri çekmez; sunucu hazır diziyi verir. Seriler zaman = unix ns, x ekseni
// `http_24h.from/to` (ms) → saniye.
import { useMemo } from 'react';
import { cn } from 'cn';
import { CorePanelMulti, type CorePanelMultiItem } from '@/components/chart/corePanelEntry';
import { msSyncKey } from '@/lib/chart/syncNamespace';
import { HTTP_FAMILY } from '@/lib/redFamily';
import { statusCodeSeries } from '@/lib/httpMetrics';
import { useT } from '@/lib/i18n';
import { httpCardTitle, responseHead, throughputHead } from '@/lib/loginSummary';
import type { LoginSummaryHttp, SpanMetricSeries } from '@/lib/types';

const SYNC = msSyncKey('login-http');
const PANEL_H = 96;
const AVG_TOKEN = '--info';
const P95_TOKEN = '--purple'; // kayıtlı p95 rengi --orange 4xx ile çakışırdı (audit §6.2)

// Tailwind literal ister: token → sınıf haritası (yalnız bu kümedeki tokenlar).
const TEXT_CLASS: Readonly<Record<string, string>> = {
  '--ok': 'text-(--ok)', '--info': 'text-(--info)', '--orange': 'text-(--orange)',
  '--err': 'text-(--err)', '--purple': 'text-(--purple)',
};

/** Sunucu durum serisi `groupKey=["200"]` (ham kod) → statusCodeSeries'in beklediği `etiket="kod"` biçimi. */
function asLabelled(series: SpanMetricSeries[]): SpanMetricSeries[] {
  const k = HTTP_FAMILY.labels.status;
  return series.map(s => ({ ...s, groupKey: [`${k}="${s.groupKey[0] ?? ''}"`] }));
}

interface LegendItem { name: string; token: string }

export default function LoginHttpCard({ http, orgName }: { http: LoginSummaryHttp; orgName?: string }) {
  const t = useT();
  const xRange = useMemo(() => ({ from: http.from / 1000, to: http.to / 1000 }), [http.from, http.to]);
  const { tputItems, legend } = useMemo(() => {
    const { series, tokens } = statusCodeSeries(asLabelled(http.status ?? []));
    const items: CorePanelMultiItem[] = series.map(sr => ({
      name: sr.groupKey[0], role: 'data' as const,
      color: `var(${tokens.get(sr.groupKey[0]) ?? '--text3'})`, series: [sr],
    }));
    const lg: LegendItem[] = series.map(sr => ({ name: sr.groupKey[0], token: tokens.get(sr.groupKey[0]) ?? '--text3' }));
    return { tputItems: items, legend: lg };
  }, [http.status]);
  const rtItems = useMemo(() => {
    const out: CorePanelMultiItem[] = [];
    if (http.avg) out.push({ name: t('login.http.avg'), role: 'data', color: `var(${AVG_TOKEN})`, series: [http.avg] });
    if (http.p95) out.push({ name: 'p95', role: 'data', color: `var(${P95_TOKEN})`, series: [http.p95] });
    return out;
  }, [http.avg, http.p95, t]);
  const rtLegend: LegendItem[] = [
    ...(http.avg ? [{ name: t('login.http.avg'), token: AVG_TOKEN }] : []),
    ...(http.p95 ? [{ name: 'p95', token: P95_TOKEN }] : []),
  ];
  const rtHead = responseHead(http.summary, t);

  return (
    <section className="rounded-[10px] border border-border bg-card px-3.5 py-3">
      <header className="mb-2 flex items-baseline justify-between gap-2">
        <h3 className="m-0 truncate text-[length:var(--fs-sm)] font-semibold">{httpCardTitle(orgName, t)}</h3>
        <span className="shrink-0 text-[length:var(--fs-xs)] text-muted-foreground">{t('login.http.scope')}</span>
      </header>

      <div className="mb-0.5 flex items-baseline justify-between gap-2 text-[length:var(--fs-2xs)] text-muted-foreground">
        <span>{t('login.http.throughput')}</span>
        <span className="tabular-nums">{throughputHead(http.summary, t)}</span>
      </div>
      <CorePanelMulti embedded title="" hideLegend height={PANEL_H} storageKey="login-tput"
        syncKey={SYNC} xRange={xRange} unit="reqps" items={tputItems} />

      {rtItems.length > 0 && (
        <>
          <div className="mb-0.5 mt-2 flex items-baseline justify-between gap-2 text-[length:var(--fs-2xs)] text-muted-foreground">
            <span>{t('login.http.rt')}</span>
            <span className="tabular-nums">{rtHead}</span>
          </div>
          <CorePanelMulti embedded title="" hideLegend height={PANEL_H} storageKey="login-rt"
            syncKey={SYNC} xRange={xRange} unit="s" items={rtItems} />
        </>
      )}

      {/* Tek, statik lejant (iki panelin kendi lejantı gizli). */}
      <ul className="m-0 mt-1.5 flex list-none flex-wrap gap-x-2.5 gap-y-0.5 p-0 text-[length:var(--fs-xs)] text-muted-foreground">
        {[...legend, ...rtLegend].map(l => (
          <li key={`${l.name}-${l.token}`} className="flex items-center gap-1">
            <span aria-hidden="true" className={cn('font-bold', TEXT_CLASS[l.token] ?? 'text-(--text3)')}>—</span>
            {l.name}
          </li>
        ))}
      </ul>
    </section>
  );
}
