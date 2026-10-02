import { Badge } from '@/components/ui';
import type { PriorDeploy } from '@/lib/types';
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
// v0.10.1054 — `priorDeploy`: kuralın "olası neden" saymadığı deploy atılmaz;
// ipucunun yeni satırında "deploy <sürüm> N dk önce — öncesinde de
// görülüyordu" (renk yok, rozet aynı).
export function RecurringMarker({ episodeCount, firstStartedAt, priorDeploy, line = false }: {
  episodeCount?: number;
  firstStartedAt?: number;
  priorDeploy?: PriorDeploy;
  line?: boolean;
}) {
  const r = anomalyRecurrence({ episodeCount, firstStartedAt });
  if (!r) return null;
  const badge = (
    <Badge tone="neutral" style={{ fontSize: 9 }} title={recurrenceTitle(r, undefined, priorDeploy)} data-recurring={r.count}>
      yinelenen
    </Badge>
  );
  return line ? <div className="ib-when__ago">{badge}</div> : badge;
}
