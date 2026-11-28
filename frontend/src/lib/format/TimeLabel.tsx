import { useLang } from '../i18n';
import { NO_DATA } from './lang';
import { absTime, relTime } from './time';

// <Time ns> — v0.10.2484 (denetim BÖLÜM 2, A7): göreli metin birincil,
// mutlak damga (izleyicinin saat dilimi, "dd.mm.yyyy HH:mm:ss") title'da.
// Makine okunur ISO `dateTime` özniteliğinde. nowMs testler için.
export function Time({ ns, nowMs, className }: { ns: number | null | undefined; nowMs?: number; className?: string }) {
  const lang = useLang();
  if (typeof ns !== 'number' || !Number.isFinite(ns) || ns === 0) return <span className={className}>{NO_DATA}</span>;
  return (
    <time className={className} dateTime={new Date(ns / 1e6).toISOString()} title={absTime(ns)}>
      {relTime(ns, lang, nowMs)}
    </time>
  );
}
