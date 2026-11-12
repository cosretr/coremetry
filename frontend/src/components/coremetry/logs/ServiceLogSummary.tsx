// coremetry/logs/ServiceLogSummary — Log Search Faz 6 dilim 6.7 (audit §7
// "Servis hızlı özeti"). Servis Logs sekmesinin (CompactLogSearch) "Log özeti"
// eylemi + cevap kartı. Kanıt "Bu logları açıkla" (6.3) ile aynı sunucu
// katmanından (desen özeti + önceki pencereyle fark); CoSRE üç bölümle anlatır.
// Arama / seviye süzgeci bilerek gitmez: özet servisin tüm loglarıdır.
// ES: yalnız tıkla, 2 arama. Copilot kapalıyken düğme çizilmez.
// ui/ importu ve satır içi stil yok.
import { useEffect, useRef, useState } from 'react';
import { Button } from '@/components/shadcn/button';
import { IconSparkles } from '@/components/icons';
import { useCopilotEnabled } from '@/components/ai/useCopilotEnabled';
import { api } from '@/lib/api';
import type { ServiceLogSummaryResponse } from '@/lib/types';
import { LogsAiAnswer, type LogsAiAnswerState } from './LogsAiAnswer';

export interface ServiceLogSummaryCtl {
  state: LogsAiAnswerState | null;
  res: ServiceLogSummaryResponse | null;
  run(): void;
  close(): void;
}

export function useServiceLogSummary(p: { service: string; env?: string; from: number; to: number }): ServiceLogSummaryCtl {
  const [state, setState] = useState<LogsAiAnswerState | null>(null);
  const [res, setRes] = useState<ServiceLogSummaryResponse | null>(null);
  const ctl = useRef<AbortController | null>(null);
  useEffect(() => () => ctl.current?.abort(), []);
  // Servis / pencere değişince eski özet kapanır (başka kapsamı anlatıyordu).
  useEffect(() => { ctl.current?.abort(); setState(null); setRes(null); }, [p.service, p.env, p.from, p.to]);
  const run = () => {
    ctl.current?.abort();
    const ac = new AbortController();
    ctl.current = ac;
    setState({ status: 'loading' });
    setRes(null);
    api.explainServiceLogs({ service: p.service, env: p.env || undefined, from: p.from, to: p.to }, ac.signal)
      .then(r => {
        if (ac.signal.aborted) return;
        setRes(r);
        setState({ status: 'done', text: r.explanation ?? '',
          note: r.degraded ? 'Log backend yavaş ya da erişilemez — özet çıkarılamadı.' : (r.sampled === 0 ? 'Bu pencerede servisin logu yok.' : undefined) });
      })
      .catch((e: unknown) => {
        if (!ac.signal.aborted) setState({ status: 'error', message: e instanceof Error ? e.message : 'CoSRE cevap veremedi.' });
      });
  };
  const close = () => { ctl.current?.abort(); setState(null); setRes(null); };
  return { state, res, run, close };
}

export function ServiceLogSummaryButton({ ctl }: { ctl: ServiceLogSummaryCtl }) {
  const enabled = useCopilotEnabled();
  if (enabled !== true) return null;
  return (
    <Button variant="secondary" size="sm" onClick={ctl.run} disabled={ctl.state?.status === 'loading'} data-testid="service-log-summary"
      title="CoSRE servisin bu penceredeki loglarını üç cümleyle özetler: durum, önceki pencereye göre değişen, bakılacak ilk şey (2 log araması)">
      <IconSparkles /> Log özeti
    </Button>
  );
}

export function ServiceLogSummaryResult({ ctl, service, onSearchPattern }: { ctl: ServiceLogSummaryCtl; service: string; onSearchPattern(query: string): void }) {
  if (!ctl.state) return null;
  const r = ctl.res;
  return (
    <LogsAiAnswer title="Log özeti" state={ctl.state} onClose={ctl.close} onRetry={ctl.run} testId="service-log-summary-answer"
      subtitle={`${service} · tüm loglar${r?.sampled ? ` · ${r.sampled.toLocaleString('tr-TR')} örnek satır` : ''}`}
      footer={r && r.patterns.length > 0 ? (
        <div className="flex flex-wrap items-center gap-1.5">
          {r.patterns.map(p => (
            <Button key={p.query} variant="outline" size="xs" onClick={() => onSearchPattern(p.query)} title={`Aramaya ekle (AND): ${p.query}`}>
              {p.kind === 'new' ? 'Yeni' : 'Artan'}: <span className="max-w-[28ch] truncate font-mono">{p.label}</span>
            </Button>
          ))}
        </div>
      ) : undefined} />
  );
}
