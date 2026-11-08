// TraceSummaryLine — v0.10.1952 (operatör: tekil trace mockup'ından "düz cümle özeti").
// Başlık kartının altında tek paragraf: süre ve sonuç, en uzun dış çağrının payı,
// kök hata (kod ya da mesaj), en büyük tekrar grubu. Parçalar lib/traceSummary'den;
// kanıtı olmayan parça cümleden düşer. Metinler i18n (trace.sum.*).
import { Fragment, type ReactNode } from 'react';
import { useT } from '@/lib/i18n';
import { fmtNs } from '@/lib/utils';
import type { TraceSummary } from '@/lib/traceSummary';

/** "{k}" yer tutucularını düğümlerle doldurur. */
function fill(tpl: string, vals: Record<string, ReactNode>): ReactNode[] {
  return tpl.split(/(\{\w+\})/).map((part, i) => {
    const m = /^\{(\w+)\}$/.exec(part);
    return <Fragment key={i}>{m && m[1] in vals ? vals[m[1]] : part}</Fragment>;
  });
}

const clip = (s: string, n = 140) => (s.length > n ? s.slice(0, n - 1) + '…' : s);

export function TraceSummaryLine({ sum }: { sum: TraceSummary }) {
  const t = useT();
  const d = (ms: number) => <b>{fmtNs(ms * 1e6)}</b>;
  const parts: ReactNode[] = [];
  const http = sum.httpStatus ? t('trace.sum.http').replace('{s}', String(sum.httpStatus)) : '';
  parts.push(fill(t(sum.failed ? 'trace.sum.failed' : 'trace.sum.took'), { d: d(sum.totalMs), http }));
  if (sum.hot) {
    parts.push(fill(t('trace.sum.hot'), {
      p: <b>{Math.round(sum.hot.share * 100)}</b>, svc: <b>{sum.hot.service}</b>, target: <b>{sum.hot.target}</b>, d: fmtNs(sum.hot.ms * 1e6),
    }));
  }
  if (sum.firstError) {
    const msg = <span className="trace-sum__err">{sum.firstError.code ?? clip(sum.firstError.message || t('trace.sum.noMsg'))}</span>;
    parts.push(fill(t(sum.errorCount > 1 ? 'trace.sum.errN' : 'trace.sum.err1'), { n: <b>{sum.errorCount}</b>, svc: <b>{sum.firstError.service}</b>, msg }));
  }
  if (sum.repeat) {
    parts.push(fill(t('trace.sum.repeat'), { n: <b>{sum.repeat.count}</b>, name: <b>{sum.repeat.name}</b> }));
  }
  return (
    <p className="trace-sum" data-testid="trace-summary-line">
      {parts.map((p, i) => <Fragment key={i}>{i > 0 ? ' ' : ''}{p}</Fragment>)}
    </p>
  );
}
