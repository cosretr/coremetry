// TraceNarrativeBody — v0.10.2480 (operatör-bildirimli, prod v0.10.2478:
// "Trace açıklaması daha iyi olsun istiyorum ama çok da göz yormasın").
// v0.10.2483 (operatör onaylı düzen: "açıklama trace altındaki TÜM logları
// kapsasın … tek bir açıklama olsun; logları ayrıca altta göstermene gerek yok,
// sekmede görüyor — onu da açıklamaya dahil et"):
//   TÜR satırı (etiket · HTTP kodu · süre) → yaygınlık satırı (sunucu verisi,
//   okuma başarılıysa) → KARAR (modelden, iki paragraf: ne oldu; loglar ne
//   gösteriyor — akarken çizilir; log saati sunucunun modele verdiği bir satırın
//   saatiyle BİREBİR eşleşirse Loglar sekmesine bağlantı) + altında sunucu
//   notu ("N log satırının hepsi incelendi" / "N satırdan M tanesi" + "Loglar
//   sekmesinde aç") → [Kod bulgusu — yalnız "Kodu da incele" turu] → Sonraki
//   adım → AÇIK "Hata zinciri" (zincir yoksa "Kritik yol") → KAPALI "Diğer
//   kanıt" → dipnot. Akış ve LOG satırları kalktı.
// Zincir / yaygınlık / Diğer kanıt sunucunun `trace_evidence` bloğundan (modelden
// ÖNCE gelir): model beklenmeden çizilir. Saatler izleyicinin saat diliminde
// (formatTraceClock). Renk yalnız sapan değerde (HTTP ≥400, p50 ×2+, yaygın).
import { useMemo, type ReactNode } from 'react';
import { Link } from 'react-router';
import { useLang, useT } from '@/lib/i18n';
import { KeyValue, KeyValueRow } from '@/components/coremetry/KeyValue';
import { Spinner } from '@/components/Spinner';
import { HlText } from './HlText';
import { traceHref } from '@/lib/traceHref';
import { operationTracesHref } from '@/lib/pivotHref';
import { formatTraceClock } from '@/lib/traceAiContext';
import type { AICodeContext, TraceEvidenceHop, TraceEvidencePrevalence, TraceEvidenceStep, TraceEvidenceView } from '@/lib/types';
import { CodeSourceFooter } from './CodeSourceFooter';
import {
  TRACE_LABEL_KEY, fmtTraceMs, hasOtherEvidence, parseTraceNarrative, splitLogClocks, traceHeadStatus, traceHopNote,
  traceLogClockSet, traceLogsNote, traceOtherEvidence, tracePrevalenceKey,
} from '@/lib/traceNarrative';

const fill = (s: string, vars: Record<string, string | number>) => s.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k]) : m));
const errCls = (bad: boolean) => (bad ? 'text-(--err)' : undefined);

/** Şablonu bir değişkende böler; o değişken `node` olarak çizilir (bağlantı). */
function fillNode(tpl: string, vars: Record<string, string | number>, linkVar: string | null, node: (v: string) => ReactNode): ReactNode {
  if (!linkVar || !tpl.includes(`{${linkVar}}`)) return fill(tpl, vars);
  const [a, b] = tpl.split(`{${linkVar}}`);
  return <>{fill(a, vars)}{node(String(vars[linkVar]))}{fill(b, vars)}</>;
}

function callLine(x: { service: string; kind?: string; call: string; status?: number; durMs: number }, ratio = 0) {
  return (
    <>
      <b>{x.service}</b>{x.kind ? ` · ${x.kind}` : ''} · {x.call}
      {x.status ? <> · <span className={errCls(x.status >= 400)}>HTTP {x.status}</span></> : null}
      {' · '}<span className={errCls(ratio >= 2)}>{fmtTraceMs(x.durMs)}</span>
    </>
  );
}

function PrevalenceLine({ p }: { p: TraceEvidencePrevalence }) {
  const tr = useT();
  const lang = useLang();
  const key = tracePrevalenceKey(p);
  if (!key) return null;
  const num = (n: number | undefined) => new Intl.NumberFormat(lang === 'tr' ? 'tr-TR' : 'en-US').format(n ?? 0);
  const vars = {
    when: tr(p.endsAtTrace ? 'ai.tn.prev.when.trace' : 'ai.tn.prev.when.now'),
    n: num(key === 'opOver' ? p.over : p.errors), calls: num(p.calls),
    thr: fmtTraceMs(p.overMs ?? 0), p95: fmtTraceMs(p.p95Ms ?? 0),
  };
  const href = operationTracesHref({
    window: { fromNs: p.fromNs, toNs: p.toNs }, operation: p.op, service: p.service,
    hasError: p.kind === 'error', minMs: key === 'opOver' ? p.overMs : undefined,
  });
  const linkVar = key === 'errOne' ? null : key === 'opPlain' ? 'calls' : 'n';
  return (
    <div className={`cx-evidence ${p.wide ? 'text-(--warn)' : 'field-hint'}`} data-tn-prevalence={p.wide ? 'wide' : 'narrow'} title={`${p.service} · ${p.op}`}>
      {fillNode(tr(`ai.tn.prev.${key}`), vars, linkVar, v => <Link to={href} title={tr('ai.tn.prev.open')}>{v}</Link>)}
    </div>
  );
}

function ChainBlock({ view }: { view: TraceEvidenceView }) {
  const tr = useT();
  const chain = view.chain ?? [];
  const skipAt = view.chainSkipped ? chain.length - 1 : -1;
  return (
    <div className="cx-evidence" data-tn-chain="">
      <span className="cx-verdict-k">{tr('ai.tn.chain')}</span>
      <ol style={{ margin: '4px 0 0', paddingLeft: 20 }}>
        {chain.map((h: TraceEvidenceHop, i) => {
          const note = traceHopNote(h, i, chain.length);
          return (
            <li key={`${h.spanId}${i}`} data-tn-hop={i + 1} value={i === skipAt ? i + 1 + (view.chainSkipped ?? 0) : undefined}>
              {i === skipAt && <div className="field-hint">{fill(tr('ai.tn.hop.skipped'), { n: view.chainSkipped ?? 0 })}</div>}
              {i === 0 && <span className="chip" data-tn-source="">{tr('ai.tn.hop.source')}</span>}{i === 0 ? ' ' : null}
              {callLine(h, note?.ratio ?? 0)}
              {note && (
                <div className="field-hint" data-tn-hopnote="">
                  {[
                    note.source ? <span key="s" className={`mono ${errCls(true) ?? ''}`}>{note.source}</span> : null,
                    note.p50Ms ? <span key="m">{fill(tr('ai.tn.hop.median'), { p50: fmtTraceMs(note.p50Ms), x: (note.ratio ?? 0).toFixed(1) })}</span> : null,
                    note.root ? <span key="r">{tr('ai.tn.hop.root')}</span> : null,
                  ].filter(Boolean).flatMap((n, j) => (j ? [' · ', n] : [n]))}
                </div>
              )}
            </li>
          );
        })}
      </ol>
    </div>
  );
}

function CriticalBlock({ steps }: { steps: TraceEvidenceStep[] }) {
  const tr = useT();
  return (
    <div className="cx-evidence" data-tn-critpath="">
      <span className="cx-verdict-k">{tr('ai.tn.critical')}</span>
      <ol style={{ margin: '4px 0 0', paddingLeft: 20 }}>
        {steps.map((s, i) => {
          const ratio = s.p50Ms ? s.durMs / s.p50Ms : 0;
          const note = [
            s.sharePct ? fill(tr('ai.tn.crit.share'), { n: s.sharePct }) : '',
            s.p50Ms ? fill(tr('ai.tn.crit.median'), { p50: fmtTraceMs(s.p50Ms) }) : '',
            s.root ? tr('ai.tn.hop.root') : '',
          ].filter(Boolean).join(' · ');
          return (
            <li key={`${s.spanId}${i}`} data-tn-step={i + 1}>
              {callLine(s, ratio)}
              {note && <div className="field-hint">{note}</div>}
            </li>
          );
        })}
      </ol>
    </div>
  );
}

export function TraceNarrativeBody({ text, busy, view, traceId, code }: {
  text: string;
  busy: boolean;
  /** sunucunun kanıt bloğu; gelmediyse zincir yerine yükleniyor çizilir */
  view: TraceEvidenceView | null;
  traceId: string;
  /** v0.10.2482 — "Kodu da incele" turunun kod künyesi (içeriksiz; Diğer kanıt'ta çizilir) */
  code?: AICodeContext | null;
}) {
  const tr = useT();
  const n = useMemo(() => parseTraceNarrative(text, busy), [text, busy]);
  const clocks = useMemo(() => traceLogClockSet(view), [view]);
  const fallback = !busy && !n.verdict && !!view?.fallback;
  const status = traceHeadStatus(view);
  const logsHref = traceHref(traceId, { tab: 'logs' });
  const logsNote = view ? traceLogsNote(view) : null;
  const other = view ? traceOtherEvidence(view) : null;
  const codeFiles = code?.files?.length ?? 0;
  const otherSum = other ? [
    other.stack ? tr('ai.tn.sum.stack') : '',
    other.fields ? fill(tr('ai.tn.sum.fields'), { n: other.fields }) : '',
    other.critical ? tr('ai.tn.sum.critical') : '',
    other.oracle ? fill(tr('ai.tn.sum.oracle'), { n: view?.oracleRows || other.oracle }) : '',
    other.others ? fill(tr('ai.tn.sum.others'), { n: other.others + (view?.othersMore ?? 0) }) : '',
    codeFiles ? fill(tr('ai.tn.sum.code'), { n: codeFiles }) : '',
  ].filter(Boolean).join(' · ') : '';
  const para = (p: string, i: number) => (
    <p className="cm-md-p" key={i} data-tn-para={i + 1} style={{ margin: i ? '6px 0 0' : 0 }}>
      {splitLogClocks(p, clocks).map((seg, j) => (seg.clock
        ? <Link key={j} to={logsHref} className="mono" data-tn-clock="" title={tr('ai.tn.logClock')}>{seg.text}</Link>
        : <HlText key={j} text={seg.text} />))}
      {busy && i === n.paras.length - 1 && <span className="cm-ai-cursor" />}
    </p>
  );
  return (
    <div data-trace-narrative="">
      {(n.label || status || view?.rootMs) && (
        <div className="cx-evidence" data-tn-head="">
          {n.label && <span className="chip" data-tn-label=""><b>{tr(`ai.tn.label.${TRACE_LABEL_KEY[n.label]}`)}</b></span>}
          {status ? <span className={errCls(status >= 400)}> · HTTP {status}</span> : null}
          {view?.rootMs ? <span> · {fmtTraceMs(view.rootMs)}</span> : null}
        </div>
      )}
      {view?.prevalence && <PrevalenceLine p={view.prevalence} />}
      {(n.verdict || fallback || busy) && (
        <div className="cx-verdict" data-tn-verdict="">
          <span className="cx-verdict-k">{tr('ai.tn.verdict')}</span>
          {n.paras.length ? n.paras.map(para) : fallback ? view!.fallback : null}
          {busy && !n.paras.length && <span className="cm-ai-cursor" />}
          {fallback && <div className="field-hint" data-tn-fallback="">{tr('ai.tn.fallbackNote')}</div>}
        </div>
      )}
      {view && (
        <div className="cx-evidence field-hint" data-tn-logsnote={logsNote?.kind ?? ''}>
          {logsNote && <span>{fill(tr(`ai.tn.logs.${logsNote.kind}`), {
            n: 'total' in logsNote ? logsNote.total : 0, m: logsNote.kind === 'some' ? logsNote.sent : 0,
          })} · </span>}
          <Link to={logsHref}>{tr('ai.tn.openLogs')}</Link>
        </div>
      )}
      {n.code && (
        <p className="cm-md-p" data-tn-code=""><b>{tr('ai.tn.code')}:</b> <HlText text={n.code} /></p>
      )}
      {n.next && (
        <p className="cm-md-p" data-tn-next=""><b>{tr('ai.tn.next')}:</b> <HlText text={n.next} /></p>
      )}
      {!view && <div className="cx-evidence"><Spinner label={tr('ai.tn.loading')} /></div>}
      {view?.chain?.length ? <ChainBlock view={view} /> : view?.critPath?.length ? <CriticalBlock steps={view.critPath} /> : null}
      {view && other && hasOtherEvidence(other, codeFiles) && (
        <details className="cm-md-details" data-tn-evidence="">
          <summary>{tr('ai.tn.other')}{otherSum ? ` · ${otherSum}` : ''}</summary>
          <div className="cm-md-details__b">
            <KeyValue labelWidth="wide">
              {view.frame && <KeyValueRow k={tr('ai.tn.ev.stack')} mono v={`${view.frame.exType ? `${view.frame.exType} · ` : ''}${view.frame.frame}`} />}
              {view.fields?.map(f => <KeyValueRow key={`f${f.k}`} k={`${tr('ai.tn.ev.fields')} · ${f.k}`} mono v={f.v} />)}
              {other.critical && view.critical && (
                <KeyValueRow k={tr('ai.tn.ev.critical')} v={`${view.critical.service} · ${view.critical.target} · ${fmtTraceMs(view.critical.durMs)} · ${fill(tr('ai.tn.ev.share'), { n: view.critical.sharePct })}`} />
              )}
              {view.oracle?.map((o, i) => (
                <KeyValueRow key={`o${i}`} k={`${tr('ai.tn.ev.oracle')}${o.operation ? ` · ${o.operation}` : ''}`}
                  v={<>{formatTraceClock(o.tsNs)?.time ?? ''} {o.errorCode ? <span className={errCls(true)}>{o.errorCode}</span> : null} {o.message}{o.count && o.count > 1 ? ` ×${o.count}` : ''}</>} />
              ))}
              {view.others?.map((h, i) => <KeyValueRow key={`x${h.spanId}${i}`} k={`${tr('ai.tn.ev.others')} · ${h.service}`} v={callLine(h)} />)}
            </KeyValue>
            {/* v0.10.2482 — kod turunun KAYNAK künyesi (dosya, satır aralığı, işaretli satır, depo, DevOps
                linki — mevcut CodeSourceFooter). Kod İÇERİĞİ yok (v0.9.1254 kuralı). */}
            {codeFiles > 0 && code && <div data-tn-codesource=""><CodeSourceFooter code={code} /></div>}
          </div>
        </details>
      )}
      {!busy && <div className="field-hint" data-tn-footer="">{tr('ai.tn.footer')}</div>}
    </div>
  );
}
