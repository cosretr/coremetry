// coremetry/logs/TraceChip — v0.10.1883 (Log Search Faz 3 dilim 3.4; audit §4
// "Çip + önizleme", "Çip menüsü", "Örneklenmiş tespiti"; mockup görünüm 1
// `.tchip` + hover kartı). Veri çağırandan: sayfa başına TEK
// POST /api/logs/trace-preview yanıtı — çip ve kart EK İSTEK ATMAZ.
//
// Beş durum: found (✓ / ✕ + süre) · pending ("henüz yazılmadı") · sampled
// ("örneklenmiş" — sampling Collector'da) · unrecognized (çip YOK, düz metin) ·
// error ("önizleme alınamadı" — asla "örneklenmiş" denmez). Yükleniyorken
// yalnız kimlik. Kart: üzerine gelince (ya da tık / klavye) açılır; menü
// "Bu trace'e filtrele (f)", "Trace görünümünde aç (t)", "trace_id kopyala".
// Servis SAYISI gösterilmez (MV'de yok — dilim 3.9; uydurma yok).
import { useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { Button } from '@/components/shadcn/button';
import { Popover, PopoverAnchor, PopoverContent } from '@/components/coremetry/overlay';
import { KeyValue } from '@/components/coremetry/KeyValue';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { CopyButton } from '@/components/CopyButton';
import { fmtClock } from '@/lib/utils';
import type { TracePreview } from '@/lib/types';

const short = (id: string) => (id.length > 12 ? `${id.slice(0, 8)}…` : id);
export const fmtTraceDuration = (ms: number) =>
  ms >= 1000 ? `${(ms / 1000).toLocaleString('tr-TR', { maximumFractionDigits: 2 })} sn` : `${Math.round(ms).toLocaleString('tr-TR')} ms`;

/** Çip metni + tonu (SAF; TraceChip.test). */
export function traceChipView(id: string, p: TracePreview | undefined): { text: string; tone: 'ok' | 'err' | 'muted' | 'plain'; title: string } {
  const s = short(id);
  if (!p) return { text: s, tone: 'plain', title: id };
  switch (p.state) {
    case 'found': {
      const err = (p.errorCount ?? 0) > 0;
      return { text: `${err ? '✕' : '✓'} ${s} · ${fmtTraceDuration(p.durationMs ?? 0)}`, tone: err ? 'err' : 'ok',
        title: `${id} — ${err ? `${p.errorCount} hatalı span` : 'hata yok'} · ${p.spanCount ?? 0} span` };
    }
    // v0.10.1986 (operatör: "henüz yazılmadı yazmasına gerek yok" — dar kolonda kesiliyordu): yalnız ◌ + kimlik; açıklama ipucunda.
    case 'pending': return { text: `◌ ${s}`, tone: 'muted', title: `${id} — trace henüz ClickHouse'a yazılmadı (log 2 dk'dan yeni)` };
    case 'sampled': return { text: `○ ${s} · örneklenmiş`, tone: 'muted', title: `${id} — trace ClickHouse'ta yok: örneklenmiş (sampling Collector'da)` };
    case 'error': return { text: `${s} · önizleme alınamadı`, tone: 'muted', title: `${id} — önizleme okunamadı (ClickHouse hata / zaman aşımı)` };
    default: return { text: id, tone: 'plain', title: id };
  }
}

export interface TraceChipProps {
  traceId: string;
  preview?: TracePreview;
  onFilter?(traceId: string): void;
  onOpen?(traceId: string): void;
  /**
   * v0.10.1968 (operatör: "eskiden log sayfasında trace'e basınca gidiyordu, artık çalışmıyor") —
   * verilirse çip GERÇEK bağlantıdır: tık trace sayfasına gider (yeni sekme çalışır), kart ve menü
   * üzerine gelince açılır. Verilmezse tık kartı açar/kapar (eski davranış).
   */
  href?: string;
}

const TONE: Record<string, string> = {
  ok: 'text-status-ok',
  err: 'text-status-critical',
  muted: 'text-(--text3)',
  plain: 'text-(--text2)',
};

export function TraceChip({ traceId, preview, onFilter, onOpen, href }: TraceChipProps) {
  const [open, setOpen] = useState(false);
  const timer = useRef<number | null>(null);
  if (preview?.state === 'unrecognized') {
    return href
      ? <Link to={href} className="font-mono" title="Tanınmayan trace kimliği biçimi" onClick={e => e.stopPropagation()}>{traceId}</Link>
      : <span className="font-mono text-(--text2)" title="Tanınmayan trace kimliği biçimi">{traceId}</span>;
  }
  const v = traceChipView(traceId, preview);
  const hold = (next: boolean, ms: number) => {
    if (timer.current) window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setOpen(next), ms);
  };
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverAnchor asChild>
        {href ? (
          <Button asChild variant="ghost" size="xs" className={'h-5 px-1 font-mono ' + TONE[v.tone]}>
            <Link to={href} title={`${v.title} — tıkla: trace sayfası`} data-testid="trace-chip" data-state-kind={preview?.state ?? 'loading'}
              onMouseEnter={() => hold(true, 250)} onMouseLeave={() => hold(false, 200)}
              onClick={e => e.stopPropagation()}>
              {v.text}
            </Link>
          </Button>
        ) : (
          <Button variant="ghost" size="xs" className={'h-5 px-1 font-mono ' + TONE[v.tone]} title={v.title}
            aria-haspopup="dialog" aria-expanded={open} data-testid="trace-chip" data-state-kind={preview?.state ?? 'loading'}
            onMouseEnter={() => hold(true, 250)} onMouseLeave={() => hold(false, 200)}
            onClick={e => { e.stopPropagation(); setOpen(o => !o); }}>
            {v.text}
          </Button>
        )}
      </PopoverAnchor>
      <PopoverContent align="start" className="grid w-80 gap-2 p-3 text-sm" aria-label="Trace önizlemesi"
        onMouseEnter={() => hold(true, 0)} onMouseLeave={() => hold(false, 200)} onClick={e => e.stopPropagation()}>
        <div className="flex items-center gap-2">
          <span className="truncate font-mono font-semibold" title={traceId}>{short(traceId)}{traceId.length > 12 ? traceId.slice(-4) : ''}</span>
          {preview?.state === 'found' && (
            <StatusBadge size="sm" status={(preview.errorCount ?? 0) > 0 ? 'critical' : 'ok'}>
              {(preview.errorCount ?? 0) > 0 ? '✕ hata var' : '✓ hata yok'}
            </StatusBadge>
          )}
        </div>
        {preview?.state === 'found' ? (
          <KeyValue items={[
            { id: 'svc', k: 'Kök servis', v: preview.rootService },
            { id: 'root', k: 'Kök span', v: preview.rootName, mono: true },
            { id: 'dur', k: 'Süre', v: fmtTraceDuration(preview.durationMs ?? 0) },
            { id: 'spans', k: 'Span', v: (preview.spanCount ?? 0).toLocaleString('tr-TR') },
            { id: 'start', k: 'Başlangıç', v: preview.startNs ? fmtClock(preview.startNs / 1e6) : undefined },
          ]} />
        ) : (
          <p className="m-0 text-[length:var(--fs-xs)] text-(--text2)">{v.title.replace(`${traceId} — `, '')}</p>
        )}
        <div className="grid gap-0.5 border-t pt-2">
          {onFilter && <Button variant="ghost" size="sm" className="justify-between" onClick={() => { setOpen(false); onFilter(traceId); }}>Bu trace'e filtrele <kbd className="kbd">f</kbd></Button>}
          {href && <Button asChild variant="ghost" size="sm" className="justify-start"><Link to={href} onClick={() => setOpen(false)}>Trace sayfasında aç</Link></Button>}
          {onOpen && <Button variant="ghost" size="sm" className="justify-between" onClick={() => { setOpen(false); onOpen(traceId); }}>Trace görünümünde aç <kbd className="kbd">t</kbd></Button>}
          <span className="inline-flex items-center gap-2 px-3 text-[length:var(--fs-sm)]">trace_id kopyala <CopyButton value={traceId} title="trace_id kopyala" /></span>
        </div>
      </PopoverContent>
    </Popover>
  );
}
