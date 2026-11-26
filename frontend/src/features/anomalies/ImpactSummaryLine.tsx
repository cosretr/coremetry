// ImpactSummaryLine — v0.10.2482 (Problems "Etki" sütunu + detay başlığının
// Etki kartı): etki özetinin tek satırı. Renk YALNIZ sapan değerde: başarısız
// istek / düşen pod kırmızı, gecikme / biriken mesaj sarı; bağlam parçası
// (trafik payı, istek sayısı, artış) renksiz.
import { Fragment } from 'react';
import type { Lang } from '@/lib/i18n';
import { impactSummaryParts } from '@/lib/impactSummaryView';
import type { StdImpactSummary } from '@/lib/types';

const HOT_CLASS: Record<StdImpactSummary['kind'], string> = { errors: 'cell-err', oom: 'cell-err', latency: 'cell-warn', lag: 'cell-warn' };

export function ImpactSummaryLine({ summary, lang }: { summary: StdImpactSummary; lang: Lang }) {
  const parts = impactSummaryParts(summary, lang) ?? [];
  return (
    <>
      {parts.map((p, i) => (
        <Fragment key={i}>
          {i > 0 && ' · '}
          {p.hot ? <span className={HOT_CLASS[summary.kind]}>{p.text}</span> : p.text}
        </Fragment>
      ))}
    </>
  );
}
