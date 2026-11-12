// coremetry/dashboard/TemplateGallery — v0.10.1997 (Dashboards Faz 3 dilim
// 3.3; audit §5.5, mockup görünüm 4 "Şablondan oluştur").
//
// Tek satır, dört MetricsQL şablonu; tablo birincil kalır (audit §5.2
// "Şablon şeridi"). Her kartta panel sayısı ve ön kontrol sonucu: gereken
// metriklerin hepsi var / eksikler adıyla / denetlenemedi. Eksik metrik
// kurulumu ENGELLEMEZ — operatör hangi panelin boş kalacağını bilerek alır.
// Yalnız editor+ çizer (kurulum yazma eylemi); liste açılınca bir kez
// okunur, polling yok (sunucu 5 dk önbellek).
import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Badge } from '@/components/shadcn/badge';
import { Button } from '@/components/shadcn/button';
import { api } from '@/lib/api';
import { toast } from '@/lib/toast';
import type { DashboardTemplate } from '@/lib/types';

/** SAF: kartın ön kontrol satırı. */
export function templateStatus(t: DashboardTemplate): { tone: 'ok' | 'warn' | 'muted'; text: string } {
  if (t.missing.length > 0) return { tone: 'warn', text: `eksik: ${t.missing.join(', ')}` };
  if (!t.checked) return { tone: 'muted', text: 'Gereken metrikler denetlenemedi' };
  return { tone: 'ok', text: 'Gereken metrikler: var' };
}

export function TemplateGallery() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [busy, setBusy] = useState<string | null>(null);
  const q = useQuery({
    queryKey: ['dashboards', 'templates'],
    queryFn: ({ signal }) => api.dashboardTemplates(signal),
    staleTime: 5 * 60_000,
  });
  const create = async (t: DashboardTemplate) => {
    if (busy) return;
    setBusy(t.id);
    try {
      const d = await api.createDashboardFromTemplate(t.id);
      await qc.invalidateQueries({ queryKey: ['dashboards', 'list'] });
      toast.success(`"${d.name}" şablondan oluşturuldu`);
      navigate(`/dashboard?id=${encodeURIComponent(d.id)}`);
    } catch (e) {
      toast.error('Şablondan oluşturulamadı: ' + (e instanceof Error ? e.message : String(e)));
    } finally {
      setBusy(null);
    }
  };

  if (q.isError) return null; // galeri yardımcı; hata listeyi gölgelemez
  const items = q.data?.templates ?? [];
  return (
    <section aria-label="Şablondan oluştur" className="mb-3 flex flex-col gap-1.5">
      <h2 className="text-sm font-semibold">
        Şablondan oluştur <span className="font-normal text-muted-foreground">MetricsQL panelleri · gereken metrikler önceden denetlenir</span>
      </h2>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-4">
        {q.isLoading
          ? <p className="text-xs text-muted-foreground">Şablonlar yükleniyor…</p>
          : items.map(t => {
            const st = templateStatus(t);
            return (
              <Button key={t.id} type="button" variant="outline" loading={busy === t.id}
                className="h-auto flex-col items-start gap-0.5 px-3 py-2 text-left whitespace-normal"
                title={t.description} onClick={() => void create(t)}>
                <span className="font-semibold">{t.name}</span>
                <span className="text-xs font-normal text-muted-foreground">{t.panelCount} panel · {t.tags.join(', ')}</span>
                {st.tone === 'warn'
                  ? <Badge variant="outline" className="max-w-full truncate text-status-warning">{st.text}</Badge>
                  : <span className="text-xs font-normal text-muted-foreground">{st.text}</span>}
              </Button>
            );
          })}
      </div>
    </section>
  );
}
