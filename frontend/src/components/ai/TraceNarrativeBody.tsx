// TraceNarrativeBody — v0.10.2480 (operatör-bildirimli, prod v0.10.2478:
// "Trace açıklaması daha iyi olsun istiyorum ama çok da göz yormasın").
// Trace açıklamasının ANLATIM biçimi; operatörün onayladığı düzen:
//   TÜR satırı (etiket · HTTP kodu · süre) → KARAR (modelden, akarken çizilir)
//   → Akış (deterministik tek satır) → LOG (görünen 1–2 satır + "Trace'in
//   loglarını aç"; dürüst boş durumlar) → Sonraki adım → katlanır Kanıt (yalnız
//   yukarıda görünmeyenler) → dipnot.
// Akış / LOG / Kanıt sunucunun `trace_evidence` bloğundan (modelden ÖNCE gelir):
// model beklenmeden çizilir. Saatler izleyicinin saat diliminde (Window çipiyle
// aynı kural, formatTraceClock). Renk yalnız sapan değerde (HTTP ≥400, p50 ×2+).
import { useMemo, useState } from 'react';
import { Link } from 'react-router';
import { useT } from '@/lib/i18n';
import { KeyValue, KeyValueRow } from '@/components/coremetry/KeyValue';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { HlText } from './HlText';
import { traceHref } from '@/lib/traceHref';
import { formatTraceClock } from '@/lib/traceAiContext';
import type { TraceEvidenceHop, TraceEvidenceLog, TraceEvidenceView } from '@/lib/types';
import {
  TRACE_LABEL_KEY, extraTraceLogs, fmtTraceMs, parseTraceNarrative, traceEvidenceSummary, traceFlowLine,
  traceHeadStatus, traceLogEmpty, visibleTraceLogs,
} from '@/lib/traceNarrative';

const LOG_MSG_MAX = 160;
const fill = (s: string, vars: Record<string, string | number>) => s.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k]) : m));
const errCls = (bad: boolean) => (bad ? 'text-(--err)' : undefined);

function LogLine({ l }: { l: TraceEvidenceLog }) {
  const tr = useT();
  const [open, setOpen] = useState(false);
  const clock = formatTraceClock(l.tsNs);
  const long = l.message.length > LOG_MSG_MAX;
  const lvlBad = /^(ERR|FATAL|CRIT)/i.test(l.level);
  return (
    <div className="cx-evidence" data-tn-log="">
      {clock && <span className="mono" title={clock.tzLabel}>{clock.time} </span>}
      <b>{l.service}</b> <span className={errCls(lvlBad)}>{l.level}</span>
      {l.count && l.count > 1 ? <span> ×{l.count}</span> : null}
      {': '}
      <span className="mono">“{open || !long ? l.message : `${l.message.slice(0, LOG_MSG_MAX)}…`}”</span>
      {long && (
        <Button variant="link" size="xs" onClick={() => setOpen(o => !o)}>{open ? tr("ai.tn.less") : tr("ai.tn.more")}</Button>
      )}
      {l.corrId && <span className="mono field-hint"> · {l.corrId}</span>}
    </div>
  );
}

function hopValue(h: TraceEvidenceHop, tr: (k: string) => string) {
  const ratio = h.p50Ms && h.p50Ms > 0 ? h.durMs / h.p50Ms : 0;
  return (
    <>
      {h.kind ? `${h.kind} · ` : ''}{h.call}
      {h.status ? <> · <span className={errCls(h.status >= 400)}>HTTP {h.status}</span></> : null}
      {h.error ? <> · <span className={errCls(true)}>{h.error}</span></> : null}
      {' · '}<span className={errCls(ratio >= 2)}>{fmtTraceMs(h.durMs)}</span>
      {h.p50Ms ? <span className="field-hint"> ({fill(tr('ai.tn.ev.p50'), { p50: fmtTraceMs(h.p50Ms) })}, ×{ratio.toFixed(1)})</span> : null}
    </>
  );
}

export function TraceNarrativeBody({ text, busy, view, traceId }: {
  text: string;
  busy: boolean;
  /** sunucunun kanıt bloğu; gelmediyse Akış / LOG satırı yükleniyor çizer */
  view: TraceEvidenceView | null;
  traceId: string;
}) {
  const tr = useT();
  const n = useMemo(() => parseTraceNarrative(text, busy), [text, busy]);
  const fallback = !busy && !n.verdict && !!view?.fallback;
  const status = traceHeadStatus(view);
  const empty = view ? traceLogEmpty(view) : null;
  const visible = view ? visibleTraceLogs(view) : [];
  const extra = view ? extraTraceLogs(view) : [];
  const sum = view ? traceEvidenceSummary(view) : null;
  const sumText = sum ? [
    sum.spans > 0 ? fill(tr('ai.tn.sum.spans'), { n: sum.spans }) : '',
    sum.hops > 0 ? fill(tr('ai.tn.sum.hops'), { n: sum.hops }) : '',
    sum.extraLogs > 0 ? fill(tr('ai.tn.sum.logs'), { n: sum.extraLogs }) : '',
    sum.oracle > 0 ? fill(tr('ai.tn.sum.oracle'), { n: sum.oracle }) : '',
    sum.stack ? tr('ai.tn.sum.stack') : '',
  ].filter(Boolean).join(' · ') : '';
  const hasDetails = !!view && (!!view.chain?.length || !!view.critical || !!view.frame || !!view.fields?.length
    || !!view.oracle?.length || !!view.others?.length || extra.length > 0);
  return (
    <div data-trace-narrative="">
      {(n.label || status || view?.rootMs) && (
        <div className="cx-evidence" data-tn-head="">
          {n.label && <span className="chip" data-tn-label=""><b>{tr(`ai.tn.label.${TRACE_LABEL_KEY[n.label]}`)}</b></span>}
          {status ? <span className={errCls(status >= 400)}> · HTTP {status}</span> : null}
          {view?.rootMs ? <span> · {fmtTraceMs(view.rootMs)}</span> : null}
        </div>
      )}
      {(n.verdict || fallback || busy) && (
        <div className="cx-verdict" data-tn-verdict="">
          <span className="cx-verdict-k">{tr('ai.tn.verdict')}</span>
          {n.verdict ? <HlText text={n.verdict} /> : fallback ? view!.fallback : null}
          {busy && <span className="cm-ai-cursor" />}
          {fallback && <div className="field-hint" data-tn-fallback="">{tr('ai.tn.fallbackNote')}</div>}
        </div>
      )}
      {!view && <div className="cx-evidence"><Spinner label={tr('ai.tn.loading')} /></div>}
      {view && (
        <div className="cx-evidence" data-tn-flow="">
          <span className="cx-verdict-k">{tr('ai.tn.flow')}</span><span className="mono">{traceFlowLine(view, {
            error: tr('ai.tn.flow.error'), toClient: tr('ai.tn.flow.toClient'), hops: tr('ai.tn.flow.hops'), endErr: tr('ai.tn.flow.endErr'),
            share: tr('ai.tn.ev.share'), external: tr('ai.tn.flow.external'), waiting: tr('ai.tn.flow.waiting'), ok: tr('ai.tn.flow.ok'),
          })}</span>
        </div>
      )}
      {view && (
        <div data-tn-logrow="" style={{ marginBottom: 8 }}>
          <div className="cx-evidence" style={{ marginBottom: 2 }}>
            <span className="cx-verdict-k">{tr('ai.tn.log')}</span>
            {view.logRows > 0 && <span>{fill(tr('ai.tn.logCount'), { n: view.logRows })} · </span>}
            <Link to={traceHref(traceId, { tab: 'logs' })}>{tr('ai.tn.openLogs')}</Link>
          </div>
          {visible.map((l, i) => <LogLine key={i} l={l} />)}
          {empty && <div className="cx-evidence field-hint" data-tn-log-empty={empty}>{fill(tr(`ai.tn.log.${empty}`), { n: view.logRows })}</div>}
        </div>
      )}
      {n.next && (
        <p className="cm-md-p" data-tn-next=""><b>{tr('ai.tn.next')}:</b> <HlText text={n.next} /></p>
      )}
      {view && hasDetails && (
        <details className="cm-md-details" data-tn-evidence="">
          <summary>{tr('ai.tn.evidence')}{sumText ? ` · ${sumText}` : ''}</summary>
          <div className="cm-md-details__b">
            <KeyValue labelWidth="wide">
              {view.chain?.map((h, i) => (
                <KeyValueRow key={`c${h.spanId}${i}`} k={i === 0 ? `${tr('ai.tn.ev.chain')} · ${h.service}` : h.service} v={hopValue(h, tr)} />
              ))}
              {view.critical && (
                <KeyValueRow k={tr('ai.tn.ev.critical')} v={`${view.critical.service} · ${view.critical.target} · ${fmtTraceMs(view.critical.durMs)} · ${fill(tr('ai.tn.ev.share'), { n: view.critical.sharePct })}`} />
              )}
              {view.frame && <KeyValueRow k={tr('ai.tn.ev.stack')} mono v={`${view.frame.exType ? `${view.frame.exType} · ` : ''}${view.frame.frame}`} />}
              {view.fields?.map(f => <KeyValueRow key={`f${f.k}`} k={`${tr('ai.tn.ev.fields')} · ${f.k}`} mono v={f.v} />)}
              {view.oracle?.map((o, i) => (
                <KeyValueRow key={`o${i}`} k={`${tr('ai.tn.ev.oracle')}${o.operation ? ` · ${o.operation}` : ''}`}
                  v={<>{formatTraceClock(o.tsNs)?.time ?? ''} {o.errorCode ? <span className={errCls(true)}>{o.errorCode}</span> : null} {o.message}{o.count && o.count > 1 ? ` ×${o.count}` : ''}</>} />
              ))}
              {view.others?.map((h, i) => <KeyValueRow key={`x${h.spanId}${i}`} k={`${tr('ai.tn.ev.others')} · ${h.service}`} v={hopValue(h, tr)} />)}
              {extra.map((l, i) => (
                <KeyValueRow key={`l${i}`} k={`${tr('ai.tn.ev.logs')} · ${l.service}`}
                  v={`${formatTraceClock(l.tsNs)?.time ?? ''} ${l.level}: ${l.message}${l.count && l.count > 1 ? ` ×${l.count}` : ''}`} />
              ))}
            </KeyValue>
          </div>
        </details>
      )}
      {!busy && <div className="field-hint" data-tn-footer="">{tr('ai.tn.footer')}</div>}
    </div>
  );
}
