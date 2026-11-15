import { Link } from 'react-router-dom';
import { Sparkles } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import { emitAiPrefill } from '@/components/ai/aiEvents';
import { serviceHref } from '@/lib/serviceHref';
import type { TimeRange } from '@/lib/types';
import { podAgeText, podCosreQuestion } from './podMeta';

// PodMetaRow — /pod meta satırı + "CoSRE'ye sor" eylemi (Clusters S10, plan
// docs/frontend/clusters-shadcn-plan.md §0 D8, §10.1; operatör yanıtları
// 2026-11-15). KPI şeridinin yerini alır (şerit RED serilerinden türüyordu;
// RED artık kapalı bölümde). Alanlar zaten yüklü veriden: Servis (URL ya da
// entity_seen ilk servis, "N servis" ipucu), İş yükü (entity zinciri ya da
// pod adı sezgiseli), Node (Thanos pod satırı, S8), Yaş (entity ömrü), Sürüm
// (service.version / app.kubernetes.io/version etiketi). Olmayan alan çizilmez.
// "CoSRE'ye sor": CoSRE çekmecesini açar, soruyu cluster / namespace / servis
// bağlamıyla DOLDURUR, göndermez (emitAiPrefill — tur ve komut paleti emsali).
export interface PodMeta {
  service?: string;
  serviceCount?: number;
  workload?: { label: string; href?: string };
  node?: string;
  validFrom?: string;
  version?: string;
}

export function PodMetaRow({ meta, cluster, namespace, pod, range, nowMs }: {
  meta: PodMeta;
  cluster: string;
  namespace: string;
  pod: string;
  range: TimeRange;
  nowMs: number;
}) {
  const age = meta.validFrom ? podAgeText(meta.validFrom, nowMs) : '';
  return (
    <div className="pod-meta">
      <dl className="pod-meta__list">
        {meta.service && (
          <div><dt>Servis</dt><dd title={meta.serviceCount && meta.serviceCount > 1 ? `${meta.serviceCount} servis — tamamı «Taşıdığı servisler»de` : undefined}>
            <Link to={serviceHref(meta.service, { range })}>{meta.service}</Link>
            {meta.serviceCount && meta.serviceCount > 1 ? <span className="pod-meta__more"> +{meta.serviceCount - 1}</span> : null}
          </dd></div>
        )}
        {meta.workload && (
          <div><dt>İş yükü</dt><dd>{meta.workload.href ? <Link to={meta.workload.href}>{meta.workload.label}</Link> : <span className="mono">{meta.workload.label}</span>}</dd></div>
        )}
        {meta.node && <div><dt>Node</dt><dd className="mono">{meta.node}</dd></div>}
        {age && <div><dt>Yaş</dt><dd>{age}</dd></div>}
        {meta.version && <div><dt>Sürüm</dt><dd className="mono">{meta.version}</dd></div>}
      </dl>
      <Button variant="outline" size="sm"
        onClick={() => emitAiPrefill({ question: podCosreQuestion({ cluster, namespace, pod, service: meta.service }) })}>
        <Sparkles aria-hidden="true" /> CoSRE'ye sor
      </Button>
    </div>
  );
}
