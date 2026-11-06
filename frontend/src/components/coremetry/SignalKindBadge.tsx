// coremetry/SignalKindBadge — v0.10.1844 (öğrenme döngüsü Faz 2 dilim 2.4,
// mockup anomaly-detail.html `.sig`). shadcn/badge üzerinde sinyal türü
// kimliği: ikon + metin + tür rengi (yalnız `--sig-*` token'ı). Renk tek
// taşıyıcı DEĞİL — metin ve ikon her zaman çizilir; ikon dekoratif
// (aria-hidden), erişilebilir ad metindir. Çerçevesiz, zeminsiz (mockup):
// tablo hücresinde ve başlık çipleri arasında aynı görünüm.
import { Badge } from '@/components/shadcn/badge';
import { cn } from '@/lib/cn';
import { SIGNAL_KINDS, type SignalKind } from '@/lib/signalKind';
import { SIGNAL_KIND_CLASS } from './signalKindClass';

export interface SignalKindBadgeProps {
  kind: SignalKind;
  /** Etiket; verilmezse türün adı ("Metrik", "Trace", "Log", "Grup"). */
  label?: string;
}

export function SignalKindBadge({ kind, label }: SignalKindBadgeProps) {
  const spec = SIGNAL_KINDS[kind];
  const Icon = spec.icon;
  return (
    <Badge variant="ghost" data-signal={kind} className={cn('gap-1 px-0 text-[11px] font-semibold [&>svg]:size-3.5', SIGNAL_KIND_CLASS[kind])}>
      <Icon aria-hidden="true" />
      {label ?? spec.label}
    </Badge>
  );
}
