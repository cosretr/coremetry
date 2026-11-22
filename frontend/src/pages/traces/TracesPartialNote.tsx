// TracesPartialNote — v0.10.2466 (Operator-reported 2026-11-22). Liste okuma
// bütçesini aştığında sunucu 500 yerine KISMİ sonuç döner (`partial` zarfı,
// internal/api/traces_long_wait.go). Not dürüstçe söyler: hangi andan sonrası
// kesin, ne kadar sürdü / sınır neydi; "Daha uzun bekle" düğmesi YALNIZ bu
// sorguyu uzun bütçeyle yeniden koşar (operatörün açık isteği — otomatik yok).
import { Button } from '@/components/shadcn/button';
import { t as tl, tf, type Lang } from '@/lib/i18n';
import type { TracesPartial } from '@/lib/types';
import { tsLong } from '@/lib/utils';

/** SAF: "25.1 sn / 25 sn" — geçen süre ve sınır (ondalık tek hane). */
export function partialElapsedText(p: Pick<TracesPartial, 'elapsedMs' | 'limitSec'>): string {
  return `${(p.elapsedMs / 1000).toFixed(1)} / ${p.limitSec}`;
}

export function TracesPartialNote({ partial, lang, busy, onLongWait }: {
  partial: TracesPartial;
  lang: Lang;
  busy?: boolean;
  onLongWait: () => void;
}) {
  const covered = partial.coveredFromNs
    ? tf('trc.partial.covered', lang, { t: tsLong(partial.coveredFromNs) })
    : tl('trc.partial.none', lang);
  return (
    <span className="badge b-warn trl-ml6" data-traces-partial="1" title={tl('trc.partial.tip', lang)}>
      ⚠ {tl('trc.partial.label', lang)} · {covered} · {tf('trc.partial.elapsed', lang, { v: partialElapsedText(partial) })}
      {partial.longWaitSec > 0 && (
        <Button variant="outline" size="xs" className="trl-ml6" disabled={busy} onClick={onLongWait} data-traces-long-wait="1">
          {tf('trc.partial.longWait', lang, { s: partial.longWaitSec })}
        </Button>
      )}
    </span>
  );
}
