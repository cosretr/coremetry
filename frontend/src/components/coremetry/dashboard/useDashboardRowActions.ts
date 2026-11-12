// coremetry/dashboard/useDashboardRowActions — Dashboards Faz 3 dilim 3.1
// (audit §5.2 "Satır eylemleri: Aç · Çoğalt · Dışa aktar · Sil").
//
// Liste satırının ⋯ menüsü (coremetry DataTable `rowActions` — tabloda tek
// paylaşılan menü). Çoğalt ve Sil editor+ (sunucu da editorRoles ister);
// Dışa aktar herkes (salt okuma). Sil onaylı ve kırmızı (`danger: true`);
// sunucu tarafı `dashboard.delete` audit kaydını zaten yazar.
import { useNavigate } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { Copy, Download, ExternalLink, Trash2 } from 'lucide-react';
import { useConfirm } from '@/components/coremetry/Confirm';
import type { MenuItemSpec } from '@/components/coremetry/DataTable';
import { api } from '@/lib/api';
import { downloadDashboard } from '@/lib/dashboardIO';
import { copyName } from '@/lib/dashboardList';
import { toast } from '@/lib/toast';
import type { DashboardSummary } from '@/lib/types';

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));

export function useDashboardRowActions(canWrite: boolean, all: readonly DashboardSummary[]): (d: DashboardSummary) => MenuItemSpec[] {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const confirm = useConfirm();
  const refresh = () => qc.invalidateQueries({ queryKey: ['dashboards', 'list'] });

  const duplicate = async (d: DashboardSummary) => {
    try {
      const full = await api.getDashboard(d.id);
      const created = await api.createDashboard({
        name: copyName(full.name, all.map(x => x.name)),
        description: full.description ?? '',
        tags: full.tags ?? [],
        panels: full.panels ?? [],
        variables: full.variables ?? [],
      });
      await refresh();
      toast.success(`"${created.name}" oluşturuldu`);
    } catch (e) {
      toast.error('Çoğaltılamadı: ' + errText(e));
    }
  };

  const exportOne = async (d: DashboardSummary) => {
    try {
      downloadDashboard(await api.getDashboard(d.id));
    } catch (e) {
      toast.error('Dışa aktarılamadı: ' + errText(e));
    }
  };

  const remove = async (d: DashboardSummary) => {
    if (!await confirm({
      title: 'Pano silinsin mi?',
      body: `"${d.name || d.id}" panosu panelleri ve değişkenleriyle kalıcı olarak silinecek. Önce dışa aktarabilirsiniz.`,
      confirmLabel: 'Panoyu sil',
      danger: true,
    })) return;
    try {
      await api.deleteDashboard(d.id);
      await refresh();
      toast.success(`"${d.name}" silindi`);
    } catch (e) {
      toast.error('Silinemedi: ' + errText(e));
    }
  };

  return (d: DashboardSummary) => {
    const items: MenuItemSpec[] = [
      { id: 'open', label: 'Aç', icon: ExternalLink, onSelect: () => navigate(`/dashboard?id=${encodeURIComponent(d.id)}`) },
      { id: 'export', label: 'Dışa aktar (JSON)', icon: Download, onSelect: () => { void exportOne(d); } },
    ];
    if (canWrite) {
      items.splice(1, 0, { id: 'duplicate', label: 'Çoğalt', icon: Copy, onSelect: () => { void duplicate(d); } });
      items.push({ id: 'delete', label: 'Sil', icon: Trash2, danger: true, onSelect: () => { void remove(d); } });
    }
    return items;
  };
}
