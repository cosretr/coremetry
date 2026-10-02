import { Badge } from '@/components/ui';
import { anomalyRecurrence, recurrenceTitle } from './detailSummary';

// RecurringMarker — v0.10.1049, yinelenen anomali ayrımı.
//
// Operatör: "Yinelenen anomali ayrımı: her gece tekrar eden bir anomali artık
// her seferinde 'yeni' görünüyor ve önceki deploy'a bağlanıyor." Problems
// kuyruğunun anomali satırı ve /anomalies geçmiş satırı aynı TEK kelimeyi
// basar: nötr "yinelenen" (Badge tone neutral = .badge .b-gray — renk yalnız
// sapan değerde, durum paleti kuralı). Sayı ve ilk görülme tarihi ipucunda.
// Yinelenmemiş (sayaç yok / ≤ 1) satırda HİÇBİR ŞEY çizilmez (sarmalayıcı da).
// `line`: tarih hücresinde damganın altına, yaş satırıyla aynı kalıpta.
export function RecurringMarker({ episodeCount, firstStartedAt, line = false }: {
  episodeCount?: number;
  firstStartedAt?: number;
  line?: boolean;
}) {
  const r = anomalyRecurrence({ episodeCount, firstStartedAt });
  if (!r) return null;
  const badge = (
    <Badge tone="neutral" style={{ fontSize: 9 }} title={recurrenceTitle(r)} data-recurring={r.count}>
      yinelenen
    </Badge>
  );
  return line ? <div className="ib-when__ago">{badge}</div> : badge;
}
