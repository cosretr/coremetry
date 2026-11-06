// coremetry/logs/StackBlock + ExceptionBadge + LogExceptionSection — v0.10.1882
// (Log Search Faz 3 dilim 3.3; audit §6 "Stack trace bloğu, exception rozeti",
// mockup görünüm 1 tablo hücresi + görünüm 2 "Exception" bölümü).
//
// StackBlock: başlık satırları ("Caused by" dahil) + UYGULAMA çerçeveleri
// vurgulu görünür, framework çerçeveleri katlı ("▸ N framework çerçevesi gizli —
// aç/kapat"). Uygulama satırı kararı sunucunun (`appLines`); istemci tahmin
// etmez. ExceptionBadge: rozet + metin birlikte ("▲ OutOfMemoryError · kaynak
// tükenmesi"). ui/ importu ve satır içi stil yok.
import { useState, type MouseEvent } from 'react';
import { Button } from '@/components/shadcn/button';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { exceptionBadgeText, exceptionClassLabel, exceptionStackText, foldStack, stackLines } from '@/lib/logException';
import type { LogException, LogRow } from '@/lib/types';

export function ExceptionBadge({ e }: { e: LogException }) {
  const cls = exceptionClassLabel(e.class);
  return (
    <StatusBadge size="sm" status={e.critical ? 'critical' : 'warn'}
      title={`${e.type}${cls ? ` — ${cls}` : ''}${e.cause ? ` · kök sebep ${e.cause}` : ''}`}>
      {exceptionBadgeText(e)}
    </StatusBadge>
  );
}

export interface StackBlockProps {
  stack: string;
  appLines?: readonly number[];
  /** Başlangıçta açık mı (framework çerçeveleri görünür). */
  defaultOpen?: boolean;
  /** Tablo hücresi içinde tık satır tıkına kabarmasın. */
  stopPropagation?: boolean;
}

export function StackBlock({ stack, appLines, defaultOpen = false, stopPropagation = false }: StackBlockProps) {
  const [open, setOpen] = useState(defaultOpen);
  const lines = stackLines(stack, appLines);
  const { shown, hidden } = foldStack(lines);
  const view = open ? lines : shown;
  const toggle = (ev: MouseEvent) => { if (stopPropagation) ev.stopPropagation(); setOpen(o => !o); };
  return (
    <div className="grid gap-1" data-testid="stack-block">
      <pre className="m-0 max-h-80 overflow-auto rounded-sm border bg-(--bg2) p-2 font-mono text-[length:var(--fs-xs)] break-words whitespace-pre-wrap">
        {view.map((l, i) => (
          <span key={i} className={'block ' + (l.app ? 'font-semibold text-(--text)' : l.head ? 'text-status-critical' : 'text-(--text3)')}
            data-app={l.app ? '1' : undefined}>{l.text || ' '}</span>
        ))}
      </pre>
      {hidden > 0 && (
        <Button variant="ghost" size="xs" className="justify-self-start" aria-expanded={open} onClick={toggle}>
          {open ? '▾ framework çerçevelerini gizle' : `▸ ${hidden} framework çerçevesi gizli — aç/kapat`}
        </Button>
      )}
    </div>
  );
}

/** Detay çekmecesinin "Exception" bölümü (mockup görünüm 2). */
export function LogExceptionSection({ row }: { row: LogRow }) {
  const e = row.exception;
  if (!e) return null;
  const stack = exceptionStackText(row, e);
  return (
    <section aria-label="Exception" className="grid gap-2" data-testid="log-exception">
      <div className="flex flex-wrap items-center gap-2">
        <h3 className="m-0 text-[length:var(--fs-sm)] font-semibold">Exception</h3>
        <ExceptionBadge e={e} />
        <span className="text-[length:var(--fs-xs)] text-(--text3)">
          {e.appFrames} uygulama · {Math.max(0, e.frames - e.appFrames)} framework çerçevesi
        </span>
      </div>
      <p className="m-0 font-mono text-[length:var(--fs-sm)] break-words">
        {e.type}{e.message ? `: ${e.message}` : ''}
      </p>
      {e.cause && <p className="m-0 text-[length:var(--fs-xs)] text-(--text2)">Kök sebep: <span className="font-mono">{e.cause}</span></p>}
      {e.frames > 0 && <StackBlock stack={stack} appLines={e.appLines} />}
    </section>
  );
}
