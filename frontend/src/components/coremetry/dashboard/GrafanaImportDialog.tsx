// coremetry/dashboard/GrafanaImportDialog — v0.10.2002 (Dashboards Faz 3
// dilim 3.9; audit §5.5, mockup görünüm 4 "Grafana JSON · önce rapor").
//
// Kuru çalıştırma: dosya seçilir → lib/grafanaImport saf dönüştürücüsü
// çalışır → rapor gösterilir (alınan paneller notlarıyla, atlanan her panel
// ve değişken NEDENİYLE). "İçe aktar" denmeden sunucuya hiçbir şey gitmez;
// onayda var olan POST /api/dashboards (audit dashboard.create) kullanılır.
import { useRef, useState } from 'react';
import { Button } from '@/components/shadcn/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/coremetry/overlay/Dialog';
import { api } from '@/lib/api';
import { importGrafanaDashboard, type GrafanaImportResult } from '@/lib/grafanaImport';
import { toast } from '@/lib/toast';

export function GrafanaImportDialog({ open, onOpenChange, onImported }: {
  open: boolean;
  onOpenChange(open: boolean): void;
  onImported(id: string): void;
}) {
  const fileRef = useRef<HTMLInputElement>(null);
  const [result, setResult] = useState<GrafanaImportResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const reset = () => { setResult(null); setError(null); };
  const onFile = async (f: File) => {
    reset();
    try {
      setResult(importGrafanaDashboard(await f.text()));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      if (fileRef.current) fileRef.current.value = '';
    }
  };
  const commit = async () => {
    if (!result) return;
    setBusy(true);
    try {
      const d = await api.createDashboard(result.dashboard);
      toast.success(`"${d.name}" Grafana'dan içe aktarıldı`);
      onOpenChange(false);
      reset();
      onImported(d.id);
    } catch (e) {
      toast.error('İçe aktarılamadı: ' + (e instanceof Error ? e.message : String(e)));
    } finally {
      setBusy(false);
    }
  };
  const r = result?.report;
  return (
    <Dialog open={open} onOpenChange={o => { onOpenChange(o); if (!o) reset(); }}>
      <DialogContent className="max-h-[85vh] overflow-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Grafana panosu içe aktar</DialogTitle>
          <DialogDescription>
            Önce rapor: Prometheus / VictoriaMetrics kaynaklı grafik, tek değer, gösterge, ısı haritası, not ve satır
            panelleri alınır. Dönüşümler, geçersiz kılmalar, karışık kaynak, Loki / Tempo, repeat ve alarmlar alınmaz.
          </DialogDescription>
        </DialogHeader>
        <input ref={fileRef} type="file" accept="application/json,.json" className="hidden" aria-hidden="true" tabIndex={-1}
          onChange={e => { const f = e.target.files?.[0]; if (f) void onFile(f); }} />
        <div className="flex items-center gap-2">
          <Button type="button" variant="outline" onClick={() => fileRef.current?.click()}>Grafana JSON dosyası seç</Button>
          {result && <span className="truncate text-sm font-medium">{result.dashboard.name}</span>}
        </div>
        {error && <p role="alert" className="text-sm text-status-critical">Okunamadı: {error}</p>}
        {r && (
          <div className="flex flex-col gap-3 text-sm" aria-label="Kuru çalıştırma raporu">
            <p>
              <b>{r.panels.length}</b> panel alınacak, <b>{r.skipped.length}</b> panel atlanacak · <b>{r.variables.length}</b> değişken
              alınacak, <b>{r.skippedVariables.length}</b> değişken atlanacak.
            </p>
            {r.skipped.length > 0 && (
              <section>
                <h3 className="font-semibold">Atlanan paneller</h3>
                <ul className="list-disc pl-5">
                  {r.skipped.map((s, i) => <li key={i}><b>{s.title}</b>: {s.reason}</li>)}
                </ul>
              </section>
            )}
            {r.skippedVariables.length > 0 && (
              <section>
                <h3 className="font-semibold">Atlanan değişkenler</h3>
                <ul className="list-disc pl-5">
                  {r.skippedVariables.map((s, i) => <li key={i}><span className="font-mono">{s.name}</span>: {s.reason}</li>)}
                </ul>
              </section>
            )}
            <section>
              <h3 className="font-semibold">Alınan paneller</h3>
              <ul className="list-disc pl-5">
                {r.panels.map((p, i) => (
                  <li key={i}>
                    <b>{p.title}</b> <span className="text-muted-foreground">→ {p.as}</span>
                    {p.notes.length > 0 && <span className="text-muted-foreground"> · {p.notes.join(' · ')}</span>}
                  </li>
                ))}
              </ul>
            </section>
            {r.notes.length > 0 && <p className="text-muted-foreground">{r.notes.join(' · ')}</p>}
          </div>
        )}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>Vazgeç</Button>
          <Button type="button" disabled={!result || r?.panels.length === 0} loading={busy} onClick={() => void commit()}>
            İçe aktar
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
