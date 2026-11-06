// coremetry/metrics/AddToDashboardDialog — v0.10.1863 (Metrics Explorer Faz
// 2 dilim 2.3; mockup "Dashboard'a ekle"). MQE'nin aynı akışı: yeni pano ya
// da var olan panoya panel ekle, sonra panoya git. Pano listesi açılınca
// okunur (önden çekilmez). Yazma editor+ (sunucu kapısı); düğme viewer'da
// gerekçesiyle kapalı.
import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { NativeSelect, NativeSelectOption } from '@/components/shadcn/native-select';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/coremetry/overlay';
import { api } from '@/lib/api';
import { toast } from '@/lib/toast';
import type { DashboardSummary, Panel } from '@/lib/types';

export interface AddToDashboardDialogProps {
  open: boolean;
  onOpenChange(open: boolean): void;
  /** Açılış anında kurulan paneller (çağıran derler). */
  buildPanels(): Panel[];
}

export function AddToDashboardDialog({ open, onOpenChange, buildPanels }: AddToDashboardDialogProps) {
  const navigate = useNavigate();
  const [list, setList] = useState<DashboardSummary[] | null>(null);
  const [target, setTarget] = useState('new');
  const [name, setName] = useState('');
  const [saving, setSaving] = useState(false);
  useEffect(() => {
    if (!open) return;
    let live = true;
    setList(null); setTarget('new'); setName('');
    api.listDashboards().then(d => { if (live) setList(d ?? []); }).catch(() => { if (live) setList([]); });
    return () => { live = false; };
  }, [open]);

  const save = async () => {
    const panels = buildPanels();
    if (!panels.length) { toast.error('Eklenecek sorgu yok: en az bir görünür sorgu kurun.'); return; }
    setSaving(true);
    try {
      let id: string;
      if (target === 'new') {
        id = (await api.createDashboard({ name: name.trim() || 'Yeni pano', description: '', panels, variables: [] })).id;
      } else {
        const d = await api.getDashboard(target);
        await api.updateDashboard(target, { name: d.name, description: d.description, panels: [...(d.panels ?? []), ...panels], variables: d.variables ?? [], tags: d.tags });
        id = target;
      }
      toast.success(`${panels.length} panel panoya eklendi`);
      onOpenChange(false);
      navigate(`/dashboard?id=${encodeURIComponent(id)}`);
    } catch (e) {
      toast.error(`Kaydedilemedi: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Dashboard'a ekle</DialogTitle>
          <DialogDescription>Görünür her sorgu bir panel olur; panel çalışan MetricsQL'i taşır.</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3">
          <label className="flex flex-col gap-1 text-sm">
            Pano
            <NativeSelect value={target} onChange={e => setTarget(e.target.value)} disabled={list === null} aria-label="Hedef pano" className="w-full">
              <NativeSelectOption value="new">+ Yeni pano</NativeSelectOption>
              {(list ?? []).map(d => <NativeSelectOption key={d.id} value={d.id}>{d.name}</NativeSelectOption>)}
            </NativeSelect>
          </label>
          {target === 'new' && (
            <label className="flex flex-col gap-1 text-sm">
              Yeni panonun adı
              <Input value={name} onChange={e => setName(e.target.value)} placeholder="Yeni pano" aria-label="Yeni panonun adı" />
            </label>
          )}
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>Vazgeç</Button>
          <Button type="button" onClick={() => { void save(); }} loading={saving} disabled={list === null}>Ekle</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
