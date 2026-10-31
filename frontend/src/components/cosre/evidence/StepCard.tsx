// StepCard — CoSRE v2 adım kartı (araç seti dilim 5; mockup onayı 2026-10-31).
//   • Varsayılan KAPALI: yalnız başlık satırı (durum simgesi, insan dilinde
//     başlık, süre). Araç adı açılınca etiket olarak görünür.
//   • evidence/text: kaynak · satır/seri · env · kısaltma etiketleri, ≤3 satır
//     önizleme, sorgu açılırı (Q6), page_url varsa alt bağlantı + satırlar
//     tıklanır; yoksa satırlar tıklanmaz görünür.
//   • skipped: gri, gerekçeyle. error: kırmızı kutu, "okunamadı ≠ yok".
//   • v0.10.1710 (mockup A): "Girdiler" satırı (araç argümanları), "Ham çıktı"
//     kapalı açılırı (sunucu önizlemesi, salt okunur), altta İlgili / İlgisiz
//     (aynı cosre_signals kaydı: detail "s<i>", cevabın exchangeId'si).
import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Check, ChevronRight, Loader2, Minus, X } from 'lucide-react';
import { Badge } from '@/components/shadcn/badge';
import { Button } from '@/components/shadcn/button';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/shadcn/collapsible';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/shadcn/table';
import { cn } from '@/lib/cn';
import { isPlainLeftClick } from '@/lib/a11y';
import { EVIDENCE_STRINGS as T } from './strings';
import { fmtMsTR, type StepPreview, type StepView } from './stepView';
import { QueryBlock } from './QueryBlock';
import { RelevanceToggle } from '@/components/ai/EvidenceRelevance';

function RawOutput({ raw }: { raw: string }) {
  return (
    <Collapsible data-cosre-raw="" className="data-[state=open]:basis-full">
      <CollapsibleTrigger asChild>
        <Button variant="link" size="xs" className="h-auto px-0 text-muted-foreground">{T.rawToggle}</Button>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <pre className="mt-1 max-h-56 overflow-auto rounded-md border border-border bg-muted p-2 font-mono text-xs leading-relaxed break-words whitespace-pre-wrap">{raw}</pre>
      </CollapsibleContent>
    </Collapsible>
  );
}

function StatusIcon({ kind }: { kind: StepView['kind'] }) {
  switch (kind) {
    case 'skipped': return <Minus className="size-4 text-muted-foreground" aria-label={T.skipAria} />;
    case 'error': return <X className="size-4 text-status-critical" aria-label={T.errAria} />;
    case 'running': return <Loader2 className="size-4 animate-spin text-muted-foreground" aria-label={T.runAria} />;
    default: return <Check className="size-4 text-status-ok" aria-label={T.okAria} />;
  }
}

function meta(v: StepView): string {
  if (v.kind === 'skipped') return T.skippedMeta;
  if (v.kind === 'running') return T.runningMeta;
  if (v.kind === 'error' && v.durationMs === undefined) return T.errorMeta;
  return v.durationMs !== undefined ? fmtMsTR(v.durationMs) : '';
}

// Sohbet kanıt önizlemesi (T1 muafı: ≤3 sabit satır, sunucu sırası, sıralanmaz).
function Preview({ p }: { p: StepPreview }) {
  const navigate = useNavigate();
  const clickable = p.rows.some(r => r.href);
  return (
    <>
      <Table className="text-xs" data-cosre-preview="">
        <TableHeader>
          <TableRow>
            {p.columns.map(c => <TableHead key={c.label} className={cn('h-7 px-1.5', c.numeric && 'text-right')}>{c.label}</TableHead>)}
          </TableRow>
        </TableHeader>
        <TableBody>
          {p.rows.map(r => (
            <TableRow key={r.key} data-href={r.href}
              className={r.href ? 'cursor-pointer' : 'hover:bg-transparent'}
              onClick={r.href ? e => { if (isPlainLeftClick(e)) navigate(r.href!); } : undefined}>
              {r.cells.map((c, j) => (
                <TableCell key={j} className={cn('max-w-0 truncate px-1.5 py-1', p.columns[j]?.numeric && 'text-right tabular-nums', c.mono && 'font-mono', c.bad && 'font-medium text-status-critical')}
                  title={c.text}>
                  {j === 0 && r.href ? <Link to={r.href} className="text-inherit no-underline" onClick={e => e.stopPropagation()}>{c.text}</Link> : c.text}
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {(p.more > 0 || p.moreUnknown) && (
        <p className="text-xs text-muted-foreground">{p.more > 0 ? T.more(p.more, clickable) : T.moreUnknown}</p>
      )}
    </>
  );
}

export function StepCard({ v, exchangeId }: { v: StepView; exchangeId?: string }) {
  const [open, setOpen] = useState(false);
  const muted = v.kind === 'skipped';
  return (
    <Collapsible open={open} onOpenChange={setOpen} className="border-t border-border first:border-t-0"
      data-cosre-step={v.id} data-kind={v.kind} role="listitem">
      <CollapsibleTrigger asChild>
        <Button variant="ghost" size="sm" aria-expanded={open}
          className={cn('h-auto w-full justify-start gap-2 rounded-none px-3 py-2 text-left font-normal', muted && 'text-muted-foreground')}>
          <StatusIcon kind={v.kind} />
          <span className="min-w-0 flex-1 truncate">
            <span className="font-medium">{v.title}</span>
            {v.subtitle && <span className="text-muted-foreground"> · {v.subtitle}</span>}
          </span>
          <span className="text-xs whitespace-nowrap text-muted-foreground tabular-nums">{meta(v)}</span>
          <ChevronRight className={cn('size-3 text-muted-foreground transition-transform', open && 'rotate-90')} aria-hidden />
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="flex min-w-0 flex-col gap-2 pr-3 pb-3 pl-9 text-sm">
          <div className="flex flex-wrap gap-1">
            {v.tags.map(t => (
              <Badge key={t.label} variant="outline" className={cn('font-normal text-muted-foreground', t.warn && 'border-status-warn-border text-status-warn')}>{t.label}</Badge>
            ))}
          </div>
          {v.kind === 'skipped' && v.reason && <p className="text-muted-foreground">{T.reasonPrefix} {v.reason}</p>}
          {v.kind === 'error' && (
            <p className="rounded-md bg-status-critical-bg px-2 py-1.5 text-status-critical" role="alert">
              {T.errorPrefix}{v.errorText ? `: ${v.errorText}` : ''}
            </p>
          )}
          {v.preview && <Preview p={v.preview} />}
          {/* v0.10.1729 (mockup A) — Girdiler · Sorgu · Ham çıktı tek satır; açılan blok satırı tam genişlikte alır. */}
          {(v.inputs || v.query || v.raw) && (
            <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1 text-xs text-muted-foreground" data-cosre-step-io="">
              {v.inputs && (
                <span className="min-w-0 break-all" data-cosre-inputs="">
                  {T.inputsPrefix} <span className="font-mono text-foreground">{v.inputs}</span>
                </span>
              )}
              {v.query && <QueryBlock query={v.query} queryKind={v.queryKind} source={v.source} />}
              {v.raw && <RawOutput raw={v.raw} />}
            </div>
          )}
          {v.pageUrl && (
            <Link to={v.pageUrl} className="text-xs text-primary hover:underline" data-cosre-page-url="">{v.pageLabel} ↗</Link>
          )}
          <div className="flex flex-wrap items-center gap-1.5 border-t border-border pt-1.5 text-xs text-muted-foreground">
            <span>{T.relevancePrompt}</span>
            <RelevanceToggle exchangeId={exchangeId} stepId={`s${v.i}`} />
          </div>
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}
