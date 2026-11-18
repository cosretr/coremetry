// ExceptionPartCard — v0.10.2363 (S5). `exception` kanıt bloğu: tip + mesaj,
// sayı / ilk / son görülme (yükten), ilk çerçeveler, örnek trace —
// ExceptionSampleRow ailesinin SampleTraceId'si (tam id + kopyala + link).
import { SampleTraceId } from '@/features/anomalies/ExceptionSampleRow';
import { tsLong } from '@/lib/utils';
import type { ExceptionPartPayload } from '@/lib/cosre/evidenceParts';
import { fmtNumTR } from '../stepView';
import { EVIDENCE_PART_STRINGS as T } from './strings';
import { PartShell } from './PartShell';

export function ExceptionPartCard({ p }: { p: ExceptionPartPayload }) {
  const meta = [
    T.exceptionCount(fmtNumTR(p.count, 0)),
    p.firstSeenMs ? `${T.firstSeen} ${tsLong(p.firstSeenMs * 1e6)}` : '',
    p.lastSeenMs ? `${T.lastSeen} ${tsLong(p.lastSeenMs * 1e6)}` : '',
  ].filter(Boolean).join(' · ');
  return (
    <PartShell kind="exception" title={<span className="mono">{p.type}</span>} meta={meta} href={p.href}>
      {p.message && <p className="text-muted-foreground break-words">{p.message}</p>}
      {!!p.frames?.length && (
        <pre className="mono mt-1 overflow-x-auto text-xs" aria-label={T.framesLabel}>{p.frames.join('\n')}</pre>
      )}
      {p.sampleTraceId && (
        <div className="mt-1 flex items-center gap-2 text-xs">
          <span className="text-muted-foreground">{T.sampleTrace}</span>
          <SampleTraceId s={{ traceId: p.sampleTraceId }} />
        </div>
      )}
    </PartShell>
  );
}
