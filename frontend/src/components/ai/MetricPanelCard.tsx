import { lazy, Suspense, type ReactNode } from 'react';
import { Link } from 'react-router';
import { Chip } from '@/components/ui/Chip';
import { DrawerSection } from '@/components/ui/Drawer';
import { Spinner } from '@/components/Spinner';
import { fmtClock } from '@/lib/utils';
import type { MetricPanelBlockPayload } from '@/lib/types';
import { chatLinkTargetProps, useChatLinkNewTab } from './chatLinkTarget';

// v0.10.1435 — tembel: grafik (CorePanel → uPlot) yalnız panel kartı çizilince
// yüklenir; sohbetin öteki turları ve jsdom testleri grafik motorunu yüklemez
// (LogSpikeCard emsali).
const EvidenceChart = lazy(() => import('./EvidenceChips').then(m => ({ default: m.EvidenceChart })));

// MetricPanelCard — v0.10.1435 (Panelden "Bu grafiği açıkla"). Metrics /
// Explore / dashboard grafiğinin ✨'ı CoSRE sohbetinin bir turu; bu kart o
// turun gövdesi (künye renderer 'metric-panel', ChatBubble dağıtır). SUNUM —
// veri çekmez: grafik (brush "seçili" bölgesiyle) + deterministik ölçüm
// satırları cevabın `metric_panel` bloğundan, ANLATIM `children` (balonun
// ExplainBody'si). Model düşse de kanıt doğru kalır. Arşivden geri yüklenen
// turda blok yoktur → balon ExplainBody (künye notu ölçüm özetini taşır).
export function MetricPanelCard({ p, children }: { p: MetricPanelBlockPayload; children?: ReactNode }) {
  const linkProps = chatLinkTargetProps(useChatLinkNewTab());
  const brush = !!p.brushFromNs && !!p.brushToNs && p.brushToNs > p.brushFromNs;
  const linkLabel = p.loc.startsWith('d.') ? 'Panoyu aç →' : "Metrics'te aç →";

  return (
    <div className="ls-explain" data-metric-panel-card="">
      <div className="ls-explain__meta">
        <Chip pill title={p.query || undefined}>{p.title.length > 48 ? `${p.title.slice(0, 47)}…` : p.title}</Chip>
        <Chip pill>{`${fmtClock(p.fromNs / 1e6)} – ${fmtClock(p.toNs / 1e6)}`}</Chip>
        {brush && <Chip pill tone="accent">{`seçili ${fmtClock(p.brushFromNs! / 1e6)} – ${fmtClock(p.brushToNs! / 1e6)}`}</Chip>}
        {(p.selected ?? []).map(s => <Chip key={s} pill tone="accent">{`seri: ${s}`}</Chip>)}
        {p.cmp && <Chip pill>önceki pencereyle</Chip>}
      </div>

      <DrawerSection title="Grafik">
        {p.chart.series.length > 0
          ? (
            <Suspense fallback={<Spinner />}>
              <EvidenceChart p={p.chart} />
            </Suspense>
          )
          : <div className="field-hint">bu pencerede seri yok</div>}
        {!!p.missing?.length && (
          <div className="field-hint ls-explain__warn">
            ⚠ seçili seri ({p.missing.join(', ')}) sorgunun ilk 20 serisi dışında — ölçülmedi
          </div>
        )}
      </DrawerSection>

      {p.stats.length > 0 && (
        <DrawerSection title="Ölçüm">
          <ul className="ls-explain__notes" data-metric-panel-stats="">
            {p.stats.map(l => <li key={l} className="field-hint">{l}</li>)}
          </ul>
        </DrawerSection>
      )}

      <DrawerSection title="Anlatım">{children}</DrawerSection>

      {(!!p.notes?.length || p.href) && (
        <DrawerSection title="Sonraki adım">
          {(p.notes ?? []).map(n => <div key={n} className="field-hint">{n}</div>)}
          {p.href && <Link className="ls-explain__cta" {...linkProps} to={p.href}>{linkLabel}</Link>}
        </DrawerSection>
      )}
    </div>
  );
}
