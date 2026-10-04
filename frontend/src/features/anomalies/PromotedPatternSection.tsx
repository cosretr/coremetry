import { useMemo } from 'react';
import { useLogPatternSeries, useProblemSourceEvent } from '@/lib/queries';
import { PROMOTED_ANOMALY_RULE_PREFIX } from '@/lib/problemSubject';
import type { Problem } from '@/lib/types';
import { anomalyChartWindow } from './anomalyDetail';
import { SignalLink } from './detailSections';
import { LogPatternCountSection } from './LogPatternCountSection';
import {
  logPatternSeriesArgs, patternLogsPivot, promotedPatternChartEvent, type LogPatternChartEvent,
} from './logPatternSeries';

// PromotedPatternSection — v0.10.1106 (operatör kuyruğu, onaylı). v0.10.1060
// "Desen sayısı" grafiğini yalnız anomali OLAYI detayına koydu ve terfi
// Problem'ini (`anomaly-auto:`) bilerek dışarıda bıraktı ("kaynak olayı ayrıca
// okumak gerekir"). Operatör artışın ne zaman başladığını Problem detayında,
// kaynak olayı açmadan görmek istiyor.
//
// Kaynak olay GET /api/problems/{id}/source-event'ten (yalnız terfi Problem'i
// detayı açıkken; kimliği sunucu ayrıştırır, tek sınırlı okuma, 30 s). Grafik
// yalnız hasLogPatternSeries kaynağında (promotedPatternChartEvent); olay
// okunurken / okunamazsa / desen dışı türde bölüm HİÇ çizilmez — hangi türün
// grafiği olacağı okuma bitmeden bilinmez, boş bir kutu "eşleşme yok" diye
// okunurdu. İki parça aynı anahtarla okur (tek istek, React Query paylaşır).

function usePromotedChartEvent(problem: Problem): LogPatternChartEvent | null {
  const promoted = (problem.ruleId ?? '').startsWith(PROMOTED_ANOMALY_RULE_PREFIX);
  const q = useProblemSourceEvent(problem.id, promoted);
  const src = q.data?.sourceEvent;
  return useMemo(
    () => (promoted
      ? promotedPatternChartEvent({ startedAt: problem.startedAt, resolvedAt: problem.resolvedAt, status: problem.status }, src)
      : null),
    [promoted, problem.startedAt, problem.resolvedAt, problem.status, src]);
}

/** Sol kolonun ilk bölümü: kaynak log deseninin sayısı (olay detayıyla aynı grafik). */
export function PromotedLogPatternCount({ problem }: { problem: Problem }) {
  const ev = usePromotedChartEvent(problem);
  return ev ? <LogPatternCountSection event={ev} /> : null;
}

/** "Correlated signals"ın ilk satırı: desene uyan satırlarla /logs (olay
 *  detayının PatternLogsAction'ı ile aynı üretici, aynı pencere). */
export function PromotedPatternLogsLink({ problem }: { problem: Problem }) {
  const ev = usePromotedChartEvent(problem);
  return ev ? <PatternLogsPivotRow ev={ev} /> : null;
}

function PatternLogsPivotRow({ ev }: { ev: LogPatternChartEvent }) {
  const win = useMemo(
    () => anomalyChartWindow({ startedAt: ev.startedAt, lastSeen: ev.lastSeen }),
    [ev.startedAt, ev.lastSeen]);
  const args = useMemo(
    () => logPatternSeriesArgs({ pattern: ev.pattern, startedAt: ev.startedAt, lastSeen: ev.lastSeen, status: ev.status }),
    [ev.pattern, ev.startedAt, ev.lastSeen, ev.status]);
  // Grafikle AYNI anahtar (ikinci istek yok); yoklamayı grafik bölümü yapar.
  const q = useLogPatternSeries(args, { live: false });
  const pivot = useMemo(
    () => (q.data === null ? null : patternLogsPivot(ev.pattern, q.data, win)),
    [ev.pattern, q.data, win]);
  if (!pivot) return null;
  return (
    <>
      <SignalLink to={pivot.href} label="≡ Logları aç (desen)" sub="desene uyan satırlar, olay penceresi" />
      {pivot.topServices.length > 0 && (
        <div className="pd-pivot-note">En çok: {pivot.topServices.join(', ')}</div>
      )}
    </>
  );
}
