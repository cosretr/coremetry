// ProblemSignals — v0.10.2481 (Problems tek sayfa): Problem satırı kendi
// sinyallerini YERİNDE gösterir. "Kanıt" hücresindeki sayı gerçek bir düğme
// (aria-expanded / aria-controls; klavyeyle açılır, satır tıkını
// tetiklemez); kapalı başlar. İlk açılışta Problem detayının olay çizelgesi
// ucu okunur (GET /api/problems/{id}/events — useProblemEvents, aynı satır
// verisi); kapatınca satır DOM'da gizli kalır → tekrar açmak istek atmaz.
// En çok 5 kısa satır + "tümünü gör" (Problem sayfası). Liste önceden
// getirme yapmaz (ES-cost kuralı: yalnız açılınca).
import { useCallback, useState } from 'react';
import { Link } from 'react-router';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import { useProblemEvents } from '@/lib/queries/problems';
import { fmtClock } from '@/lib/utils';
import { t, tf, useLang } from '@/lib/i18n';
import { problemSignalLines } from '@/features/anomalies/problemEvents';

/** Açık + en az bir kez açılmış satır kimlikleri. */
export function useRowExpand() {
  const [open, setOpen] = useState<ReadonlySet<string>>(() => new Set());
  const [seen, setSeen] = useState<ReadonlySet<string>>(() => new Set());
  const toggle = useCallback((id: string) => {
    setOpen(prev => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id); else next.add(id);
      return next;
    });
    setSeen(prev => (prev.has(id) ? prev : new Set(prev).add(id)));
  }, []);
  return { isOpen: (id: string) => open.has(id), wasOpened: (id: string) => seen.has(id), toggle };
}

export function ProblemSignalsToggle({ n, open, controls, onToggle }: { n: number; open: boolean; controls: string; onToggle: () => void }) {
  const lang = useLang();
  return (
    <Button variant="ghost" size="xs" aria-expanded={open} aria-controls={controls}
      aria-label={tf('inbox.sig.toggle', lang, { n })}
      onClick={e => { e.stopPropagation(); onToggle(); }}>
      {open ? <ChevronDown aria-hidden="true" /> : <ChevronRight aria-hidden="true" />}
      {tf('inbox.evidence', lang, { n })}
    </Button>
  );
}

const clock = (ns: number) => fmtClock(new Date(ns / 1e6)).slice(0, 5);

export function ProblemSignalsRow({ id, open, problemId, service, href, colSpan }: {
  id: string; open: boolean; problemId: string; service: string; href: string | null; colSpan: number;
}) {
  const lang = useLang();
  const q = useProblemEvents(problemId, true);
  const { lines, total } = problemSignalLines(q.data?.items, service, clock, lang);
  return (
    <tr id={id} hidden={!open} className="ib-sig-row" data-testid="problem-signals">
      <td colSpan={colSpan}>
        {q.isLoading ? <span className="cell-faint">{t('inbox.sig.loading', lang)}</span>
          : q.isError ? <span className="cell-faint">{t('inbox.sig.error', lang)}</span>
            : lines.length === 0 ? <span className="cell-faint">{t('inbox.sig.empty', lang)}</span>
              : (
                <ul className="ib-sig-list" aria-label={t('inbox.sig.aria', lang)}>
                  {lines.map(l => (
                    <li key={l.key} className={l.live ? undefined : 'is-ended'}>
                      <span className="mono">{l.time}</span>
                      {' · '}<span className="mono">{l.service}</span>
                      {' · '}{l.href ? <Link to={l.href}>{l.what}</Link> : l.what}
                      {l.vals && <> ({l.vals})</>}
                      {!l.live && <> {t('pev.ended', lang)}</>}
                    </li>
                  ))}
                </ul>
              )}
        {href && total > 0 && (
          <Link to={href} className="ib-sig-all">{total > lines.length ? tf('inbox.sig.allN', lang, { n: total }) : t('inbox.sig.all', lang)}</Link>
        )}
      </td>
    </tr>
  );
}
