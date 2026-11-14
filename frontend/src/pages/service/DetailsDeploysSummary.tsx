import { useServiceRollouts7d } from '@/lib/queries/services';
import { deploysSummary } from './detailsSummaries';

// DetailsDeploysSummary — v0.10.2132 (servis sekmeleri S1): "Runtime &
// rollouts" kapalı satırının cümlesi. Props şeridiyle (DetailsPropsStrip)
// AYNI 7 günlük okuma — aynı React Query anahtarı, ek istek YOK; yalnız
// Details sekmesinde monte edilir (diğer sekmelerde okuma doğmasın diye
// Service.tsx gövdesinde değil burada).
export function DetailsDeploysSummary({ service }: { service: string }) {
  const q = useServiceRollouts7d(service);
  return <>{deploysSummary(q.data ? q.data.rollouts ?? [] : undefined)}</>;
}
