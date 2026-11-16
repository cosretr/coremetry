// ProblemGraphSection.tsx — v0.10.2275 (entity grafı ile Problem
// birleştirme S1, docs/problems/entity-graph-merge-design.md §4): Problem
// detayında "Neden tek Problem" (gölge). Evaluator liderinin gölge pasının
// hesapladığı bağlar: üyeler (başlangıç sırası, gerekçe çipi = kural
// kimliği, graftaki yol) ve aynı pencerede açık olup bağlanmayanlar.
//
// Salt okunur ÖNİZLEME: Problem'i, sahibini, önceliğini, bildirimi ya da
// listeyi değiştirmez ve bunu bölümde söyler. Bağ ve bağlanmayan yoksa
// (çoğu Problem) — ve yüklenirken / hatada — HİÇBİR ŞEY çizmez: sayfa
// bugünkü gibi kalır. Cümleler kural kimliğinden kurulur (EN/TR, i18n).
// Viewer görür.
import { useMemo } from 'react';
import { fmtClock } from '@/lib/utils';
import { tf, useLang } from '@/lib/i18n';
import { useProblemGraph } from '@/lib/queries/problems';
import { graphCounts, graphSectionMode, type GraphT } from '@/lib/problemGraph';
import { ProblemGraphMembers, ProblemGraphNearMisses } from '@/components/coremetry/problems/ProblemGraphTable';
import { Sect } from './detailSections';

const clockHM = (ns: number) => fmtClock(ns / 1e6).slice(0, 5);

export function ProblemGraphSection({ problemId }: { problemId: string }) {
  const q = useProblemGraph(problemId);
  const lang = useLang();
  const t: GraphT = useMemo(() => (key, vars) => tf(key, lang, vars), [lang]);
  const d = q.data;
  const mode = graphSectionMode(d);
  if (!d || !mode) return null;
  const note = <p className="pd-pivot-note" data-testid="pg-shadow-note">{t('pg.shadowNote')}</p>;
  if (mode === 'near') {
    return (
      <div data-testid="pg-section">
        <Sect title={t('pg.near.title')} sub={`${t('pg.near.sub')} · ${t('pg.shadowBadge')}`}>
          {note}
          <ProblemGraphNearMisses near={d.nearMisses} t={t} clock={clockHM} />
        </Sect>
      </div>
    );
  }
  const c = graphCounts(d);
  return (
    <div data-testid="pg-section">
      <Sect title={t('pg.title')} sub={`${t('pg.sub', { n: c.entities, r: c.rules })} · ${t('pg.shadowBadge')}`}>
        {note}
        <ProblemGraphMembers members={d.members} near={d.nearMisses} t={t} clock={clockHM} truncated={d.truncated} />
      </Sect>
    </div>
  );
}
