// ProblemEventTimeline.tsx — v0.10.2163 (Davis tarzı tek Problem D4, spec
// docs/plans/spec-davis-style-problem-merge.md). Servis başına tek Problem'e
// katılan sinyallerin (üyeler) kısa çizelgesi: "Ne oluyor" kutusunun altında
// en çok EVENT_LINES_SHOWN satır; tam liste ve ham değerler kapalı "Teknik
// kanıt"ta (ProblemEventEvidence — aynı sorgu anahtarı, ikinci istek yok).
//
// Ek bilgidir: yüklenirken yer tutucu / spinner YOK, üyesi olmayan Problem'de
// (ve hata durumunda) hiçbir şey çizilmez — sayfa bugünkü gibi kalır. Metin
// SAF yardımcıdan (./problemEvents); bileşen yalnız çizer. Viewer görür.
import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { fmtClock } from '@/lib/utils';
import { useProblemEvents } from '@/lib/queries/problems';
import { Sect } from './detailSections';
import { problemEventLines, splitEventLines, type ProblemEventLine } from './problemEvents';
import { t, tf, useLang } from '@/lib/i18n'; // v0.10.2286 — metinler arayüz dilinde (TR aynı)

// Saat operatörün tarayıcı diliminde, "14:02" (fmtClock = sayfanın tek saat biçimi).
const clockHM = (ns: number) => fmtClock(ns / 1e6).slice(0, 5);

function useEventLines(problemId: string): ProblemEventLine[] {
  const q = useProblemEvents(problemId);
  const lang = useLang();
  return useMemo(() => problemEventLines(q.data?.items, clockHM, lang), [q.data, lang]);
}

function EventLine({ line, raw }: { line: ProblemEventLine; raw?: boolean }) {
  return (
    <li className={line.live ? 'pd-events__item' : 'pd-events__item pd-events__item--done'}>
      {line.href ? <Link to={line.href}>{line.text}</Link> : line.text}
      {raw && line.raw && <span className="pd-events__raw"> · {line.raw}</span>}
    </li>
  );
}

/** "Ne oluyor" altındaki kısa çizelge (≤ EVENT_LINES_SHOWN satır). */
export function ProblemEventTimeline({ problemId }: { problemId: string }) {
  const lines = useEventLines(problemId);
  const lang = useLang();
  if (lines.length === 0) return null;
  const { head, rest } = splitEventLines(lines);
  return (
    <ul className="pd-events" data-testid="pd-events" aria-label={t('pev.aria', lang)}>
      {head.map(l => <EventLine key={l.key} line={l} />)}
      {rest > 0 && <li className="pd-events__more">{tf('pev.more', lang, { n: rest })}</li>}
    </ul>
  );
}

/** "Teknik kanıt" içindeki tam liste (ham değerlerle). Üye yoksa bölüm yok. */
export function ProblemEventEvidence({ problemId }: { problemId: string }) {
  const lines = useEventLines(problemId);
  const lang = useLang();
  if (lines.length === 0) return null;
  return (
    <Sect title={t('pev.title', lang)} sub={tf('pev.count', lang, { n: lines.length })}>
      <ul className="pd-events" data-testid="pd-events-full">
        {lines.map(l => <EventLine key={l.key} line={l} raw />)}
      </ul>
    </Sect>
  );
}
