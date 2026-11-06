// coremetry/logs/DisplayOptions — v0.10.1830 (Log Search Faz 1 dilim 1.7;
// audit §6 "Görünüm seçenekleri", mockup görünüm 1 "Görünüm ▾").
//
// Doküman tablosunun görünüm ayarları tek açılır pencerede: gövde satır
// yüksekliği 1/2/3/Otomatik, başlık satır yüksekliği 1/2/3/Otomatik, yoğunluk
// Sıkı/Normal/Geniş, sayfa başına satır 50/100/250/500. Saf sunum: değer ve
// değişiklik çağıranın (URL `rh/hh/den/pp`, lib/logsDisplay). `children`
// yuvası sonraki ayarlar için (örnek boyutu, dilim 1.8). Paylaşılan bileşik:
// servis Logs sekmesinin kompakt kipi de aynısını takacak (dilim 3.10).
import { useState, type ReactNode } from 'react';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/coremetry/overlay';
import { SegmentedControl, type SegmentedOption } from '@/components/coremetry/SegmentedControl';
import { Button } from '@/components/shadcn/button';
import type { LogsDisplay, LogsLines, LogsDensity, LogsPageSize } from '@/lib/types';

const LINES: ReadonlyArray<SegmentedOption<string>> = [
  { value: '1', label: '1' }, { value: '2', label: '2' }, { value: '3', label: '3' }, { value: 'auto', label: 'Otomatik' },
];
const DENSITY: ReadonlyArray<SegmentedOption<LogsDensity>> = [
  { value: 'compact', label: 'Sıkı' },
  { value: 'normal', label: 'Normal', title: 'Genel yoğunluk ayarını izler' },
  { value: 'expanded', label: 'Geniş' },
];
const PAGE: ReadonlyArray<SegmentedOption<string>> = [
  { value: '50', label: '50' }, { value: '100', label: '100' }, { value: '250', label: '250' }, { value: '500', label: '500' },
];
const toLines = (v: string): LogsLines => (v === 'auto' ? 'auto' : (Number(v) as LogsLines));

export interface DisplayOptionsProps {
  value: LogsDisplay;
  onChange(patch: Partial<LogsDisplay>): void;
  /** Ek alanlar (ör. örnek boyutu) — sayfa başına satırdan önce. */
  children?: ReactNode;
  /** Alt not (ör. "Tercih kullanıcıya kaydedilir"). */
  note?: ReactNode;
}

export function DisplayOptions({ value, onChange, children, note }: DisplayOptionsProps) {
  const [open, setOpen] = useState(false);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button variant="secondary" size="sm" aria-haspopup="dialog" data-testid="logs-display-trigger">Görünüm ▾</Button>
      </PopoverTrigger>
      <PopoverContent align="end" aria-label="Görünüm seçenekleri" className="grid w-80 gap-3 text-sm">
        <Group label="Satır yüksekliği · gövde (satır sayısı)">
          <SegmentedControl size="sm" aria-label="Satır yüksekliği · gövde" options={LINES}
            value={String(value.rowLines)} onChange={v => onChange({ rowLines: toLines(v) })} />
        </Group>
        <Group label="Satır yüksekliği · başlık">
          <SegmentedControl size="sm" aria-label="Satır yüksekliği · başlık" options={LINES}
            value={String(value.headerLines)} onChange={v => onChange({ headerLines: toLines(v) })} />
        </Group>
        <Group label="Yoğunluk">
          <SegmentedControl size="sm" aria-label="Yoğunluk" options={DENSITY}
            value={value.density} onChange={v => onChange({ density: v })} />
        </Group>
        {children}
        <Group label="Sayfa başına satır">
          <SegmentedControl size="sm" aria-label="Sayfa başına satır" options={PAGE}
            value={String(value.pageSize)} onChange={v => onChange({ pageSize: Number(v) as LogsPageSize })} />
        </Group>
        {note && <p className="m-0 text-xs text-muted-foreground">{note}</p>}
      </PopoverContent>
    </Popover>
  );
}

function Group({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid gap-1">
      <span className="text-xs text-muted-foreground">{label}</span>
      {children}
    </div>
  );
}
