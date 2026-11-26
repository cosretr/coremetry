// StdOriginLine — v0.10.2473: Problem standardının "Köken" satırı. Liste
// satırı (Inbox, katlanan küme ya da süzgeç yüzünden tek kalan üye) ve detay
// başlığı (ProblemStandardBlock) AYNI çekirdeği okur (stdOriginView):
//
//   Köken: payments — çağrı zincirinde en derinde
//
// Ad entity kaydına bağlanır (entityHref, kimlik `svc:…` / `db:…`; ad
// eşlemesi yok; servis / veritabanı / kuyruk kaydı pod ya da cluster
// olmadığından hepsi /entity?id=<kimlik>). Alan yoksa / güven belirsizse hiçbir şey çizilmez.
import { Link } from 'react-router';
import { useLang } from '@/lib/i18n';
import { stdOriginView } from '@/lib/problemStandardView';
import { entityHref } from '@/lib/entityHref';
import type { EntityType, ProblemStandard } from '@/lib/types';

export function StdOriginLine({ std, className = 'triage-sub' }: { std?: ProblemStandard | null; className?: string }) {
  const lang = useLang();
  const v = stdOriginView(std, lang);
  if (!v) return null;
  return (
    <div className={className} data-testid="std-origin" title={v.title}>
      {v.label}:{' '}
      <Link to={entityHref({ type: v.entity.type as EntityType, id: v.entity.id, name: v.entity.name, clusterId: '' })} className="cell-link mono"
        onClick={e => e.stopPropagation()}>{v.name}</Link>
      {' — '}{v.reason}
    </div>
  );
}
