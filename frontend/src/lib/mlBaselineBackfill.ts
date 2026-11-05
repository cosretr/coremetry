// mlBaselineBackfill.ts — v0.10.1815 (öğrenme döngüsü Faz 1 dilim 1.4):
// Settings › Anomaly › ML "Baseline geçmişini doldur" metinleri (SAF).
import type { MLBaselineBackfillPlan, MLBaselineBackfillView } from './types';

export const BACKFILL_SIGNAL_TR: Record<string, string> = { log: 'Log', trace: 'Trace', metric: 'Metrik' };

const n = (v: number) => v.toLocaleString('tr-TR');

/** Süre metni: "45 sn" / "12 dk" / "2,5 sa". */
export function durationTR(sec: number): string {
  if (sec < 60) return `${Math.max(0, Math.round(sec))} sn`;
  if (sec < 3600) return `${Math.round(sec / 60)} dk`;
  return `${(sec / 3600).toLocaleString('tr-TR', { maximumFractionDigits: 1 })} sa`;
}

/** Onay diyaloğunun tahmin cümlesi: "yaklaşık N ES sorgusu, M VM sorgusu, tahmini süre". */
export function backfillEstimateText(p: MLBaselineBackfillPlan): string {
  return `yaklaşık ${n(p.es)} ES sorgusu, ${n(p.vm)} VM sorgusu, ${n(p.ch)} CH okuması · tahmini süre ${durationTR(p.estimatedSec)} (istekler arası ${n(p.intervalMs)} ms)`;
}

/** Plan içinde koşmayacak sinyaller: "Trace: VictoriaMetrics yapılandırılmamış". */
export function backfillSkippedLines(p: MLBaselineBackfillPlan): string[] {
  return Object.keys(p.perSignal).filter(k => p.perSignal[k].skipped).map(k => `${BACKFILL_SIGNAL_TR[k] ?? k}: ${p.perSignal[k].skipped}`);
}

/** Durum başlığı + sinyal başına "gün X/Y · satır · hata" satırları. null = hiç istek yok. */
export function backfillProgress(v: MLBaselineBackfillView | undefined): { head: string; lines: { signal: string; text: string; warn: boolean }[] } | null {
  if (!v?.request && !v?.status) return null;
  const st = v.status;
  const req = v.request;
  if (!st || (req && st.id !== req.id)) {
    return { head: req?.cancel ? 'İptal istendi' : 'Sırada — ML lideri 30 sn içinde başlatır', lines: [] };
  }
  const head = st.complete ? 'Tamamlandı'
    : st.cancelled ? 'İptal edildi'
      : st.running ? (req?.cancel ? 'İptal ediliyor' : 'Sürüyor')
        : `Duraklı${st.stopped ? ` — ${st.stopped}` : ''} (kaldığı günden sürer)`;
  const lines = st.signals.map(sig => {
    const s = st.perSignal[sig];
    if (!s) return { signal: sig, text: `${BACKFILL_SIGNAL_TR[sig] ?? sig} · kayıt yok`, warn: false };
    if (s.skipped) return { signal: sig, text: `${BACKFILL_SIGNAL_TR[sig] ?? sig} · atlandı: ${s.skipped}`, warn: false };
    const bits = [`gün ${s.daysDone}/${s.daysTotal}`, `${n(s.rows)} satır`];
    if (s.errors) bits.push(`${n(s.errors)} hata${s.lastError ? ` (son: ${s.lastError})` : ''}`);
    return { signal: sig, text: `${BACKFILL_SIGNAL_TR[sig] ?? sig} · ${bits.join(' · ')}`, warn: s.errors > 0 };
  });
  return { head, lines };
}
