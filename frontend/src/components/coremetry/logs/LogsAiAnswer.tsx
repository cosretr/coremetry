// coremetry/logs/LogsAiAnswer — Log Search Faz 6 (audit §7). Log yüzeylerinin
// tek-atış CoSRE cevap kartı: "Bu logları açıkla" (6.3), satır + runbook (6.4)
// ve servis log özeti (6.7) aynı kartı çizer. Klasik açıklama düzeni: cevap
// eylemin altında, akışta (yüzen / yapışkan şerit yok). Saf sunum: istek ve
// durum çağıranda. Yükleniyor / hata / boş durumları her zaman bir metin
// gösterir (boş panel yok). ui/ importu ve satır içi stil yok.
import type { ReactNode } from 'react';
import { Button } from '@/components/shadcn/button';
import { Spinner, Empty } from '@/components/Spinner';
import { RenderedMarkdown } from '@/components/Markdown';

export type LogsAiAnswerState =
  | { status: 'loading' }
  | { status: 'error'; message: string }
  | { status: 'done'; text: string; note?: string };

export interface LogsAiAnswerProps {
  title: string;
  /** Kapsam / girdi özeti (ör. "checkout · 15 dk · 2 seçili satır"). */
  subtitle?: string;
  state: LogsAiAnswerState;
  onClose(): void;
  onRetry?(): void;
  /** Cevabın altındaki deterministik bağlantılar (sunucudan, modelden değil). */
  footer?: ReactNode;
  testId?: string;
}

export function LogsAiAnswer({ title, subtitle, state, onClose, onRetry, footer, testId }: LogsAiAnswerProps) {
  return (
    <section aria-label={title} className="grid gap-2 rounded-md border bg-(--bg2) px-3.5 py-3 text-[length:var(--fs-sm)]"
      data-testid={testId}>
      <div className="flex flex-wrap items-center gap-2">
        <h4 className="m-0 text-[length:var(--fs-xs)] font-bold tracking-wide text-(--text2) uppercase">{title}</h4>
        {subtitle && <span className="text-[length:var(--fs-xs)] text-(--text3)">{subtitle}</span>}
        <span className="flex-1" />
        <Button variant="ghost" size="xs" aria-label="Cevabı kapat" onClick={onClose}>Kapat</Button>
      </div>
      {state.status === 'loading' && <Spinner label="CoSRE kanıtı topluyor ve anlatıyor…" />}
      {state.status === 'error' && (
        <Empty compact icon="!" title="Cevap alınamadı"
          action={onRetry ? <Button variant="outline" size="sm" onClick={onRetry}>Yeniden dene</Button> : undefined}>
          {state.message}
        </Empty>
      )}
      {state.status === 'done' && (
        state.text.trim()
          ? <div className="leading-relaxed"><RenderedMarkdown text={state.text} /></div>
          : <Empty compact icon="○" title="Anlatılacak kanıt yok">{state.note ?? 'Bu kapsamda log okunamadı ya da örneklenen satır yok.'}</Empty>
      )}
      {state.status === 'done' && state.note && state.text.trim() && (
        <p className="m-0 text-[length:var(--fs-xs)] text-status-warn" role="status">{state.note}</p>
      )}
      {state.status === 'done' && footer}
    </section>
  );
}
