// coremetry/logs/LogRunbookSection — Log Search Faz 6 dilim 6.4 (audit §7 "Tek
// satır açıklama + runbook"). Açılan log satırının (satır içi açılım ve
// `?doc=` çekmecesi) "Runbook ile açıkla" eylemi. Sunucu satırı logstore'dan
// yeniden okur, exception özetini stackparse ile çıkarır, runbook'larda sözcük
// araması yapar (RAG'e dayanmaz) ve CoSRE dört bölümle anlatır. Mevcut "CoSRE'ye
// sor" (explain-log) değişmedi; bu ayrı bir giriş noktası.
//
// ES: yalnız tıkla, 1 arama (pod eşlenmemişse 2). Copilot kapalıysa çizilmez.
// ui/ importu ve satır içi stil yok.
import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { Button } from '@/components/shadcn/button';
import { useCopilotEnabled } from '@/components/ai/useCopilotEnabled';
import { api } from '@/lib/api';
import { logLineSubjectOf } from '@/lib/logLineSubject';
import type { LogRow, LogRunbookResponse } from '@/lib/types';
import { LogsAiAnswer, type LogsAiAnswerState } from './LogsAiAnswer';

export interface LogRunbookSectionProps {
  row: LogRow;
  env?: string;
}

export function LogRunbookSection({ row, env }: LogRunbookSectionProps) {
  const enabled = useCopilotEnabled();
  const [state, setState] = useState<LogsAiAnswerState | null>(null);
  const [res, setRes] = useState<LogRunbookResponse | null>(null);
  const ctl = useRef<AbortController | null>(null);
  useEffect(() => () => ctl.current?.abort(), []);
  const subject = row.origin ? null : logLineSubjectOf(row);
  if (enabled !== true || !subject || subject.kind !== 'log') return null;
  const run = () => {
    ctl.current?.abort();
    const ac = new AbortController();
    ctl.current = ac;
    setState({ status: 'loading' });
    setRes(null);
    api.explainLogRunbook({ tsNs: subject.tsNs, service: subject.id, pod: subject.pod, traceId: subject.traceId, hash: subject.hash, env: env ?? '' }, ac.signal)
      .then(r => {
        if (ac.signal.aborted) return;
        setRes(r);
        setState({ status: 'done', text: r.explanation ?? '',
          note: r.matched === false ? 'Tam eşleşme bulunamadı — aynı saniyedeki en yakın satır açıklandı.' : undefined });
      })
      .catch((e: unknown) => {
        if (ac.signal.aborted) return;
        setState({ status: 'error', message: e instanceof Error ? e.message : 'CoSRE cevap veremedi.' });
      });
  };
  const close = () => { ctl.current?.abort(); setState(null); setRes(null); };
  return (
    <div className="grid gap-2" data-testid="log-runbook">
      {!state && (
        <div>
          <Button variant="outline" size="sm" onClick={run}
            title="CoSRE satırı, exception özetini ve bu kurulumun runbook’larından en uygun olanı birlikte anlatır (1 log araması)">
            Runbook ile açıkla
          </Button>
        </div>
      )}
      {state && (
        <LogsAiAnswer title="Satır ve runbook" state={state} onClose={close} onRetry={run} testId="log-runbook-answer"
          subtitle={res ? (res.runbooks.length > 0 ? `${res.runbooks.length} runbook eşleşti` : res.runbookError ? 'runbook listesi okunamadı' : 'eşleşen runbook yok') : undefined}
          footer={res && res.runbooks.length > 0 ? (
            <div className="flex flex-wrap items-center gap-1.5">
              {res.runbooks.map(rb => (
                <Button key={rb.id} variant="ghost" size="xs" asChild>
                  <Link to={rb.href} title={`Sözcük eşleşmesi · skor ${rb.score.toFixed(2)}`}>{rb.title}</Link>
                </Button>
              ))}
            </div>
          ) : undefined} />
      )}
    </div>
  );
}
