// coremetry/metrics/KindBadge — v0.10.1862. Metrik tür rozeti (sayaç /
// gösterge / histogram / özet); tür ad son ekinden TAHMİN edildiyse
// "tahmini" eki (audit §1 risk 1: yanlış tahmin yanlış fonksiyon seçer —
// operatör kaynağı görmeli).
import { Badge } from '@/components/shadcn/badge';
import type { MxKind } from '@/lib/metricsExplorer/model';
import { KIND_LABEL } from './labels';

export function KindBadge({ kind, estimated }: { kind: MxKind; estimated?: boolean }) {
  return (
    <Badge variant="secondary" className="font-sans font-normal"
      title={estimated ? 'Tür ad son ekinden tahmin edildi' : undefined}>
      {KIND_LABEL[kind]}{estimated ? ' · tahmini' : ''}
    </Badge>
  );
}
