// LoadStatus — v0.10.1834 (Log Search Faz 1 dilim 1.11; audit §5 "akışlı
// yükleme: satırlar / histogram / alan istatistikleri ayrı").
//
// Her iş kendi çipinde: "◌ Satırlar…" → "✓ Satırlar 0,4 sn" (Server-Timing
// `total`, yoksa tarayıcı ölçümü; ipucunda kırılım) / "⚠ … hata" /
// "⊘ … iptal". İşler birbirini BEKLEMEZ — ilk satırlar histogramdan önce
// gelir. "Sorguyu iptal et" uçuştaki her isteği iptal eder (v0.10.1822
// sinyalleri). Düzen kaymaz: çipler sabit en küçük genişlikte, düğme boşta
// `invisible` (yer tutar, odak/okuyucu dışı). aria-live="polite": durum
// değişimi okunur, kesmez. Servis Logs sekmesi kompakt kipte aynı bileşeni
// bağlayacak (dilim 3.10) — /logs'a özgü bilgi taşımaz.
import { Button } from '@/components/shadcn/button';
import type { LoadPhase } from '@/lib/types';
import { formatLoadSec, loadTimingMs, loadTimingTitle } from '@/lib/serverTiming';

export interface LoadStatusItem {
  key: string;
  label: string;
  /** null = bu iş bu aramada yok (çip çizilmez). */
  phase: LoadPhase | null;
}

export interface LoadStatusProps {
  items: LoadStatusItem[];
  onCancel?: () => void;
}

function chipText(label: string, p: LoadPhase): string {
  switch (p.state) {
    case 'loading': return `◌ ${label}…`;
    case 'error': return `⚠ ${label} hata`;
    case 'canceled': return `⊘ ${label} iptal`;
    default: {
      const t = formatLoadSec(loadTimingMs(p.timing));
      return t ? `✓ ${label} ${t}` : `✓ ${label}`;
    }
  }
}

export function LoadStatus({ items, onCancel }: LoadStatusProps) {
  const shown = items.filter(i => i.phase);
  const busy = shown.some(i => i.phase!.state === 'loading');
  return (
    <span className="inline-flex items-center gap-1.5" data-testid="logs-load-status">
      <span role="status" aria-live="polite" className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
        {shown.map(i => (
          <span key={i.key} data-phase={i.phase!.state} data-testid={`load-${i.key}`}
            title={i.phase!.state === 'done' ? loadTimingTitle(i.phase!.timing) : undefined}
            className={[
              'inline-block min-w-[8.5em] whitespace-nowrap tabular-nums',
              i.phase!.state === 'error' ? 'text-destructive' : '',
              i.phase!.state === 'loading' ? 'motion-safe:animate-pulse' : '',
            ].filter(Boolean).join(' ')}>
            {chipText(i.label, i.phase!)}
          </span>
        ))}
      </span>
      {onCancel && (
        <Button variant="ghost" size="sm" className={busy ? 'h-6 px-2 text-xs' : 'invisible h-6 px-2 text-xs'}
          aria-hidden={busy ? undefined : true} tabIndex={busy ? undefined : -1}
          onClick={onCancel}>
          Sorguyu iptal et
        </Button>
      )}
    </span>
  );
}
