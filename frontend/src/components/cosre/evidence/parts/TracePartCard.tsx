// TracePartCard — v0.10.2363 (S5). `trace` kanıt bloğu: şelalenin özeti —
// giriş servisi / operasyonu, süre ve span sayısı (yükten), hatalı span
// şelalenin kanıt vurgusuyla (.wf-evidence) ve servis rengi TraceWaterfall'ın
// kanonik paletinden (svcColor). Şelalenin kendisi tam sayfada (href).
import { svcColor } from '@/components/traces/shared';
import type { TracePartPayload } from '@/lib/cosre/evidenceParts';
import { fmtMsTR, fmtNumTR } from '../stepView';
import { EVIDENCE_PART_STRINGS as T } from './strings';
import { PartShell } from './PartShell';

function SvcDot({ name }: { name: string }) {
  return <span aria-hidden style={{ display: 'inline-block', width: 8, height: 8, borderRadius: 2, background: svcColor(name), marginRight: 6 }} />;
}

export function TracePartCard({ p }: { p: TracePartPayload }) {
  const e = p.errorSpan;
  return (
    <PartShell kind="trace" href={p.href}
      title={<><SvcDot name={p.service} />{p.service} · <span className="mono">{p.operation}</span></>}
      meta={`${fmtMsTR(p.durationMs)} · ${T.traceSpans(fmtNumTR(p.spanCount, 0))}`}>
      <div className="mono text-xs text-muted-foreground" title={p.traceId}>{p.traceId}</div>
      {e && (
        <div className="wf-evidence mt-1 rounded-sm px-2 py-1 text-xs" data-error-span={e.spanId ?? ''}>
          <div><span className="text-muted-foreground">{T.traceErrorSpan}: </span><SvcDot name={e.service} />{e.service} · <span className="mono">{e.name}</span></div>
          {(e.exceptionType || e.message) && (
            <div className="break-words"><span className="mono">{e.exceptionType}</span>{e.exceptionType && e.message ? ': ' : ''}{e.message}</div>
          )}
        </div>
      )}
    </PartShell>
  );
}
