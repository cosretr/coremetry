// coremetry/dashboard/VariablePickers — v0.10.2001 (Dashboards Faz 3 dilim
// 3.8; audit §5.3 "Değişkenler", mockup görünüm 5 "service: orders-api ▾").
//
// Sunucu aramalı iki seçici (eager liste YOK — hard constraint):
//   • label    → /api/metrics/discover/label-values (LabelPick ile aynı uç;
//                VM'de limit ≤ 200, pencere ≤ 24 sa). `multi` açıksa çoklu
//                seçim; değerler MetricsQL'de =~"a|b" olarak açılır.
//   • database → /api/attribute-values (db.namespace + eski db.name),
//                ad yazdıkça sunucuda süzülür. v0.9.759'un /api/databases
//                kataloğunu önden çekip <select>'e dökmesi kalktı.
// Seçim URL'de (çağıran router üzerinden yazar); bileşen yalnız çizer.
import { useState } from 'react';
import { ChevronDown, X } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/coremetry/overlay/Popover';
import { PickList } from '@/components/coremetry/metrics/PickList';
import { useServerSearch } from '@/components/coremetry/metrics/useServerSearch';
import { LABEL_PICK_LIMIT, labelMatch } from '@/components/coremetry/metrics/labels';
import { api } from '@/lib/api';
import type { DashboardVariable, MetricsDiscoverLabelValues } from '@/lib/types';

/** SAF: tetik düğmesinin metni. */
export function varSummary(values: readonly string[]): string {
  if (values.length === 0) return '(hepsi)';
  if (values.length === 1) return values[0];
  return `${values[0]} +${values.length - 1}`;
}

function Trigger({ name, values, onClear }: { name: string; values: readonly string[]; onClear(): void }) {
  return (
    <span className="inline-flex items-center gap-0.5">
      <PopoverTrigger asChild>
        <Button type="button" variant="outline" size="xs" className="max-w-64 font-mono">
          <span className="text-muted-foreground">{name}:</span>
          <span className="truncate">{varSummary(values)}</span>
          <ChevronDown aria-hidden="true" />
        </Button>
      </PopoverTrigger>
      {values.length > 0 && (
        <Button type="button" variant="ghost" size="icon-xs" aria-label={`${name} seçimini temizle`} onClick={onClear}>
          <X />
        </Button>
      )}
    </span>
  );
}

export function LabelVarPicker({ def, values, onChange }: {
  def: DashboardVariable;
  values: readonly string[];
  onChange(values: string[]): void;
}) {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState('');
  const name = def.label || def.name;
  const match = labelMatch(def.metric ?? '');
  const res = useServerSearch<MetricsDiscoverLabelValues>(open && !!def.labelName,
    `${def.labelName}\u0000${match ?? ''}\u0000${q}`,
    signal => api.metricsDiscoverLabelValues({ label: def.labelName ?? '', match, q: q.trim() || undefined, limit: LABEL_PICK_LIMIT }, signal));
  const d = res.data;
  const rows = d && d.supported ? d.values : [];
  const pick = (v: string) => {
    if (def.multi) {
      onChange(values.includes(v) ? values.filter(x => x !== v) : [...values, v]);
    } else {
      onChange([v]);
      setOpen(false);
    }
  };
  const footer = !def.labelName
    ? <span>Değişkende etiket adı tanımlı değil</span>
    : d && !d.supported
      ? <span>Öneri yok: {d.reason} · değeri yazıp ↵</span>
      : d && d.supported
        ? <span>Sunucu araması · {rows.length}{d.hasMore ? '+' : ''} değer{def.multi ? ' · çoklu seçim' : ''}</span>
        : null;
  return (
    <Popover open={open} onOpenChange={o => { setOpen(o); if (o) setQ(''); }}>
      <Trigger name={name} values={values} onClear={() => onChange([])} />
      <PopoverContent align="start" className="w-80 p-0">
        <PickList label={`${name} değeri ara`} placeholder="Değer" query={q} onQuery={setQ} allowFree
          loading={res.loading} error={res.error ? `Değerler okunamadı: ${res.error}` : null}
          items={rows.map(v => ({ value: v, label: v, aside: values.includes(v) ? 'seçili' : undefined }))}
          footer={footer} onPick={pick} />
      </PopoverContent>
    </Popover>
  );
}

export function DatabaseVarPicker({ def, values, onChange }: {
  def: DashboardVariable;
  values: readonly string[];
  onChange(values: string[]): void;
}) {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState('');
  const name = def.label || def.name;
  const res = useServerSearch<string[]>(open, q, async () => {
    const term = q.trim() || undefined;
    const [a, b] = await Promise.all([
      api.attributeValues('db.namespace', '24h', 20, term),
      api.attributeValues('db.name', '24h', 20, term),
    ]);
    return [...new Set([...(a ?? []), ...(b ?? [])].map(x => x.value).filter(Boolean))].sort().slice(0, 20);
  });
  const rows = res.data ?? [];
  return (
    <Popover open={open} onOpenChange={o => { setOpen(o); if (o) setQ(''); }}>
      <Trigger name={name} values={values} onClear={() => onChange([])} />
      <PopoverContent align="start" className="w-80 p-0">
        <PickList label={`${name} veritabanı ara`} placeholder="Veritabanı adı" query={q} onQuery={setQ} allowFree
          loading={res.loading} error={res.error ? `Veritabanları okunamadı: ${res.error}` : null}
          items={rows.map(v => ({ value: v, label: v }))}
          footer={<span>Sunucu araması · son 24 sa · ilk {rows.length}</span>}
          onPick={v => { onChange([v]); setOpen(false); }} />
      </PopoverContent>
    </Popover>
  );
}
