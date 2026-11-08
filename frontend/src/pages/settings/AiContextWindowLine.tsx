// AiContextWindowLine — v0.10.1991 (operatör: "Coremetry modelin context değerini sunucudan okusun, ona
// göre göndersin; Settings'te otomatik görünsün"). Profil başına yürürlükteki bağlam penceresi ve kaynağı
// (sunucudan okundu / model adından tahmin). CoSRE'nin uzun kanıt (traceRecords, wiki) bütçesi bu sayıdan.
import { useQuery } from '@tanstack/react-query';
import { Button } from '@/components/shadcn/button';
import { api } from '@/lib/api';
import { fmtNum } from '@/lib/utils';

export function AiContextWindowLine() {
  const q = useQuery({
    queryKey: ['ai-context-window'],
    queryFn: () => api.aiContextWindow(false),
    staleTime: 60_000,
    retry: false,
  });
  const refresh = async () => { await api.aiContextWindow(true); void q.refetch(); };
  const rows = q.data?.profiles ?? [];
  return (
    <div className="grid gap-1 text-[length:var(--fs-sm)]">
      <div className="flex items-center gap-2 font-semibold text-(--text2)">
        Bağlam penceresi
        <span className="font-normal text-(--text3)">— sunucudan otomatik okunur; CoSRE uzun kanıtı buna göre böler</span>
        <Button type="button" variant="link" size="xs" className="h-auto px-0" onClick={() => void refresh()}>Yeniden oku</Button>
      </div>
      {q.isPending && <div className="text-(--text3)">Okunuyor…</div>}
      {q.isError && <div className="text-(--text3)">Okunamadı.</div>}
      {rows.map(r => (
        <div key={r.profileId} className="flex flex-wrap gap-x-2">
          <span className="font-mono">{r.model || r.profileId}</span>
          <span>{r.window.tokens > 0 ? `${fmtNum(r.window.tokens)} jeton` : 'bilinmiyor'}</span>
          <span className="text-(--text3)">
            {r.window.source === 'server' ? 'sunucudan' : r.window.source === 'name' ? 'model adından tahmin' : ''}
            {r.window.modelMax ? ` · model üst sınırı ${fmtNum(r.window.modelMax)}` : ''}
          </span>
          {r.window.detail && r.window.source === 'server' && r.window.detail.includes('kırpılır') && (
            <span className="text-status-warn">{r.window.detail}</span>
          )}
        </div>
      ))}
    </div>
  );
}
