// AnomalyPartCard — v0.10.2363 (S5). `anomaly` kanıt bloğu: ML kaydının adı
// MLRecordCard'ın yardımcısıyla (mlDetectorLabel, arayüz dilinde), sinyal +
// servis / parça, gözlenen / taban ve başlangıç (yükten; oran türetilmez),
// örnek trace'ler SampleTraceId ile. Grafik yok — kayıt tam sayfada (href).
import { useLang } from '@/lib/i18n';
import { mlDetectorLabel } from '@/lib/mlDetectorLabels';
import { tsLong } from '@/lib/utils';
import { SampleTraceId } from '@/features/anomalies/ExceptionSampleRow';
import type { AnomalyPartPayload } from '@/lib/cosre/evidenceParts';
import { fmtNumTR } from '../stepView';
import { EVIDENCE_PART_STRINGS as T } from './strings';
import { PartShell } from './PartShell';

export function AnomalyPartCard({ p }: { p: AnomalyPartPayload }) {
  const lang = useLang();
  const where = [p.service, p.part].filter(Boolean).join(' · ');
  return (
    <PartShell kind="anomaly" href={p.href}
      title={`${mlDetectorLabel(p.detector, lang)} · ${p.signal}`} meta={where || undefined}>
      <div className="text-xs">
        {T.anomalyObserved} <b>{fmtNumTR(p.observed)}</b> · {T.anomalyBaseline} <b>{fmtNumTR(p.baseline)}</b>
        {p.startedMs ? <> · {T.anomalyStarted} {tsLong(p.startedMs * 1e6)}</> : null}
      </div>
      {!!p.sampleTraceIds?.length && (
        <div className="mt-1 flex flex-wrap items-center gap-2 text-xs">
          <span className="text-muted-foreground">{T.anomalyTraces}</span>
          {p.sampleTraceIds.slice(0, 3).map(id => <SampleTraceId key={id} s={{ traceId: id }} />)}
        </div>
      )}
    </PartShell>
  );
}
