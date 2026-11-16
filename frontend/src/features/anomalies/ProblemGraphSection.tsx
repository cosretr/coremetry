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
import { lazy, Suspense, useMemo } from 'react';
import { fmtClock } from '@/lib/utils';
import { tf, useLang } from '@/lib/i18n';
import { useProblemGraph } from '@/lib/queries/problems';
import { graphCounts, graphSectionMode, type GraphT } from '@/lib/problemGraph';
import { ProblemGraphMembers, ProblemGraphNearMisses } from '@/components/coremetry/problems/ProblemGraphTable';
import { BlastTable, OriginEvidence, OriginLine } from '@/components/coremetry/problems/ProblemOriginBlocks';
import { Sect } from './detailSections';

const clockHM = (ns: number) => fmtClock(ns / 1e6).slice(0, 5);

// v0.10.2279 (S2) — yol çizimi tembel: ilk paket baytı yok.
const ProblemGraphDiagram = lazy(() => import('@/components/coremetry/problems/ProblemGraphDiagram'));

function useGraphT(): GraphT {
  const lang = useLang();
  return useMemo(() => (key, vars) => tf(key, lang, vars), [lang]);
}

/** v0.10.2279 (S2) — manşetin altındaki "Olası köken" satırı. Aynı sorgu
 *  anahtarı (bölümle tek istek); bağlı küme yoksa hiçbir şey çizmez. */
export function ProblemOriginLine({ problemId }: { problemId: string }) {
  const q = useProblemGraph(problemId);
  const t = useGraphT();
  const o = q.data?.origin;
  if (!o || graphSectionMode(q.data) !== 'members') return null;
  return <OriginLine origin={o} t={t} clock={clockHM} />;
}

export function ProblemGraphSection({ problemId }: { problemId: string }) {
  const q = useProblemGraph(problemId);
  const t = useGraphT();
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
        <Suspense fallback={null}>
          <ProblemGraphDiagram members={d.members} origin={d.origin} t={t} clock={clockHM} />
        </Suspense>
        <ProblemGraphMembers members={d.members} near={d.nearMisses} t={t} clock={clockHM} truncated={d.truncated} />
      </Sect>
      {d.origin && (
        <div className="pd-cols pd-cols-11">
          <div className="pd-col">
            {d.origin.blast && (
              <Sect title={t('pg.blast.title')} sub={`${t('pg.blast.sub')} · ${t('pg.shadowBadge')}`}>
                <BlastTable origin={d.origin} t={t} />
              </Sect>
            )}
          </div>
          <div className="pd-col">
            <Sect title={t('pg.ev.title')} sub={`${t('pg.ev.sub')} · ${t('pg.shadowBadge')}`}>
              <OriginEvidence origin={d.origin} t={t} clock={clockHM} />
            </Sect>
          </div>
        </div>
      )}
    </div>
  );
}
