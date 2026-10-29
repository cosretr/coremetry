import { useQuery } from '@tanstack/react-query';
import { Spinner } from '@/components/Spinner';
import { Badge } from '@/components/ui';
import { api } from '@/lib/api';

// v0.10.1604 (operatör kararı 2026-10-29 §4) — /ai sayfasına ayrı bir v2
// görünümü EKLENMEDİ; yerine Settings › CoSRE başında tek satırlık durum:
// CoSRE v2 sohbet bayrağı açık mı (GET /api/admin/cosre-v2, admin). Salt
// okunur (Settings sade palet: yeşil yok, açık = info); bayrak PUT /api/admin/cosre-v2 ile çevrilir (audit'li). Sunucu
// memo'su 30 s: değişiklik diğer podlara ≤30 s'de yayılır, açık akışlar
// kesilmez (v0.10.1602).
export function CosreV2StatusLine() {
  const q = useQuery({
    queryKey: ['admin', 'cosre-v2'],
    queryFn: () => api.cosreV2Flag(),
    staleTime: 30_000,
  });
  return (
    <div className="field-hint" style={{ display: 'flex', alignItems: 'center', gap: 8, margin: '4px 0 8px' }}
      data-testid="cosre-v2-status">
      <span>CoSRE v2 sohbeti:</span>
      {q.isPending ? <Spinner label="CoSRE v2 durumu okunuyor…" />
        : q.isError ? <span title={q.error instanceof Error ? q.error.message : ''}>durum okunamadı</span>
        : <Badge tone={q.data.enabled ? 'info' : 'neutral'}>{q.data.enabled ? 'açık' : 'kapalı'}</Badge>}
    </div>
  );
}
