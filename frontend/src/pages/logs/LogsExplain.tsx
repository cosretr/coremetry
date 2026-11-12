// LogsExplain — Log Search Faz 6 dilim 6.3 (audit §7 "Bu logları açıkla").
// Logs tablosunun araç satırındaki eylem + cevap kartı. Kanıt sunucuda
// (POST /api/copilot/explain-logs): sayfanın süzgeci + penceresinin desen
// özeti, önceki pencereyle fark ve kullanıcının AÇTIĞI satırlar (satır içi
// açılan ya da çekmecedeki; ≤12). Seçim yoksa sunucu en temsili satırları
// desenlerden seçer. İstek YALNIZ tıkla (2 ES araması); sayfa açılışına ES
// isteği eklenmez. Bağlantılar sunucudan (modelden değil): trace'ler ve
// yeni / artan desenler — desen tıkı mevcut süzgece AND'lenir.
import { useEffect, useRef, useState } from 'react';
import { Button } from '@/components/shadcn/button';
import { Link } from 'react-router-dom';
import { IconSparkles } from '@/components/icons';
import { useCopilotEnabled } from '@/components/ai/useCopilotEnabled';
import { LogsAiAnswer, type LogsAiAnswerState } from '@/components/coremetry/logs/LogsAiAnswer';
import { api, type LogsParams } from '@/lib/api';
import { EXPLAIN_LOGS_MAX_ROWS, explainRowsOf } from '@/lib/logsExplain';
import type { ExplainLogsResponse, LogRow } from '@/lib/types';

export interface LogsExplainCtl {
  state: LogsAiAnswerState | null;
  meta: ExplainLogsResponse | null;
  selected: number;
  run(): void;
  close(): void;
}

/** İstek + durum. `params` ve `rows` tık anındaki hâliyle gönderilir. */
export function useLogsExplain(params: LogsParams, rows: readonly LogRow[]): LogsExplainCtl {
  const [state, setState] = useState<LogsAiAnswerState | null>(null);
  const [meta, setMeta] = useState<ExplainLogsResponse | null>(null);
  const [selected, setSelected] = useState(0);
  const ctl = useRef<AbortController | null>(null);
  useEffect(() => () => ctl.current?.abort(), []);
  const run = () => {
    ctl.current?.abort();
    const ac = new AbortController();
    ctl.current = ac;
    const body = explainRowsOf(rows);
    setSelected(body.length);
    setState({ status: 'loading' });
    setMeta(null);
    api.explainLogs(params, body, ac.signal).then(r => {
      if (ac.signal.aborted) return;
      setMeta(r);
      setState({
        status: 'done', text: r.explanation ?? '',
        note: r.degraded ? 'Log backend yavaş ya da erişilemez — desen özeti okunamadı; cevap yalnız seçili satırlara dayanıyor.' : undefined,
      });
    }).catch((e: unknown) => {
      if (ac.signal.aborted) return;
      setState({ status: 'error', message: e instanceof Error ? e.message : 'CoSRE cevap veremedi.' });
    });
  };
  const close = () => { ctl.current?.abort(); setState(null); setMeta(null); };
  return { state, meta, selected, run, close };
}

export function LogsExplainButton({ ctl, disabled, openCount }: { ctl: LogsExplainCtl; disabled?: boolean; openCount: number }) {
  const enabled = useCopilotEnabled();
  if (enabled !== true) return null;
  const busy = ctl.state?.status === 'loading';
  return (
    <Button variant="secondary" size="sm" disabled={disabled || busy} onClick={ctl.run} data-testid="logs-explain"
      title={openCount > 0
        ? `CoSRE açtığın ${Math.min(openCount, EXPLAIN_LOGS_MAX_ROWS)} satırı ve süzgeçteki desenleri anlatır (2 log araması)`
        : 'CoSRE süzgeçteki desenleri, önceki pencereyle farkı ve temsili satırları anlatır (2 log araması). Satır açarsan onlar da gider.'}>
      <IconSparkles /> Bu logları açıkla
    </Button>
  );
}

export function LogsExplainResult({ ctl, onSearchPattern }: { ctl: LogsExplainCtl; onSearchPattern(query: string): void }) {
  if (!ctl.state) return null;
  const m = ctl.meta;
  const parts = [ctl.selected > 0 ? `${ctl.selected} seçili satır` : 'temsili satırlar'];
  if (m?.sampled) parts.push(`${m.sampled.toLocaleString('tr-TR')} örnek satır`);
  const hasLinks = !!m && (m.traces.length > 0 || m.patterns.length > 0);
  return (
    <LogsAiAnswer title="Bu loglar ne anlatıyor" subtitle={parts.join(' · ')} state={ctl.state}
      onClose={ctl.close} onRetry={ctl.run} testId="logs-explain-answer"
      footer={hasLinks ? (
        <div className="flex flex-wrap items-center gap-1.5" data-testid="logs-explain-links">
          {m.patterns.map(p => (
            <Button key={p.query} variant="outline" size="xs" onClick={() => onSearchPattern(p.query)}
              title={`Süzgece ekle (AND): ${p.query}`}>
              {p.kind === 'new' ? 'Yeni' : 'Artan'}: <span className="max-w-[28ch] truncate font-mono">{p.label}</span>
            </Button>
          ))}
          {m.traces.map(t => (
            <Button key={t.href} variant="ghost" size="xs" asChild>
              <Link to={t.href}>{t.label}</Link>
            </Button>
          ))}
        </div>
      ) : undefined} />
  );
}
