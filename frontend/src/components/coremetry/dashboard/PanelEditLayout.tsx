// coremetry/dashboard/PanelEditLayout — v0.10.2004 (Dashboards Faz 3 dilim
// 3.7; audit §5.4, mockup görünüm 6).
//
// Panel düzenleyicinin kabuğu: 520 px'lik `position:fixed` kutu yerine
// sayfa içinde BÖLÜNMÜŞ görünüm — solda canlı önizleme, sağda seçenekler;
// dar ekranda alt alta. Akışta durur (yüzen / yapışkan şerit yok).
// Esc = Vazgeç (lib/escLayer yığını: açık bir menü / açılır liste önce
// kapanır), açılışta odak başlık alanında; kapanınca odak düzenlemeyi açan
// panele döner (çağıranın işi: data-panel-id).
import { useEffect, useRef, type ReactNode } from 'react';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { useEscLayer } from '@/lib/escLayer';

export interface PanelEditLayoutProps {
  title: string;
  onTitle(v: string): void;
  dashboardName: string;
  preview: ReactNode;
  options: ReactNode;
  /** Önizleme altındaki satır (ör. "Explorer'da aç"). */
  previewFooter?: ReactNode;
  onCancel(): void;
  onDone(): void;
  onDelete(): void;
}

export function PanelEditLayout({ title, onTitle, dashboardName, preview, options, previewFooter, onCancel, onDone, onDelete }: PanelEditLayoutProps) {
  const titleRef = useRef<HTMLInputElement>(null);
  useEscLayer(true, onCancel);
  useEffect(() => { titleRef.current?.focus(); }, []);
  return (
    <section aria-label="Panel düzenle" className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="text-base font-semibold">Panel düzenle</h2>
        <span className="text-sm text-muted-foreground">{dashboardName}</span>
        <Input ref={titleRef} value={title} aria-label="Panel başlığı" className="h-8 min-w-48 flex-1 sm:max-w-md"
          onChange={e => onTitle(e.target.value)} />
        <span className="flex-1" />
        <Button type="button" variant="ghost-danger" onClick={onDelete}>Paneli sil</Button>
        <Button type="button" variant="outline" onClick={onCancel} title="Değişiklikleri geri al ve kapat (Esc)">Vazgeç</Button>
        <Button type="button" onClick={onDone}>Tamam</Button>
      </div>
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1fr)_380px]">
        <div className="flex min-w-0 flex-col gap-2">
          <div className="rounded-md border p-2.5" aria-label="Önizleme">
            <div className="mb-1.5 text-xs font-semibold text-muted-foreground">Önizleme · canlı</div>
            {preview}
          </div>
          {previewFooter}
        </div>
        <div className="flex min-w-0 flex-col gap-2" aria-label="Seçenekler">{options}</div>
      </div>
    </section>
  );
}
