// coremetry/metrics/MetricPicker — v0.10.1862 (Metrics Explorer Faz 2 dilim
// 2.2; mockup görünüm 1 "Metrik seç" açılır listesi).
//
// SUNUCU araması (`/api/metrics/names?q=&limit=20&cluster=`): tuş başına en
// çok 20 satır (audit §6 "seçici tuş başına ≤ 50 satır"), katalog önden
// çekilmez. Satır: ad (mono) · tür rozeti · birim · açıklama + tür kaynağı
// ("tahmini" = ad son ekinden). Alt satır dürüst: "N sonuçtan ilk 20 ·
// kapsam: <cluster>".
import { useState } from 'react';
import { ChevronDown } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/coremetry/overlay';
import { api } from '@/lib/api';
import type { MetricSourceParam } from '@/lib/metricSource';
import type { MetricInfo, MetricNameSearchResult } from '@/lib/types';
import { kindOf } from '@/lib/metricsExplorer/model';
import { PickList } from './PickList';
import { KindBadge } from './KindBadge';
import { useServerSearch } from './useServerSearch';
import { METRIC_PICK_LIMIT } from './labels';

export interface MetricPickerProps {
  value: string;
  /** Seçilen satır (tür/birim varsayılanları için). */
  onPick(m: MetricInfo): void;
  kind?: ReturnType<typeof kindOf>['kind'];
  kindEstimated?: boolean;
  /** Bağlam çubuğunun cluster seçimi (kapsam açıkken). */
  cluster?: string;
  src?: MetricSourceParam;
  disabled?: boolean;
}

function typeSourceText(m: MetricInfo): string {
  switch (m.typeSource) {
    case 'catalog': return 'Tür kaynağı: katalog.';
    case 'vm-metadata': return 'Tür kaynağı: VictoriaMetrics metadata.';
    case 'inferred': return 'Tür kaynağı: tahmini (ad son eki).';
    default: return '';
  }
}

export function MetricPicker({ value, onPick, kind, kindEstimated, cluster, src, disabled }: MetricPickerProps) {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState('');
  const res = useServerSearch<MetricNameSearchResult | null>(open, `${q}\u0000${cluster ?? ''}\u0000${src ?? ''}`,
    signal => api.metricNamesSearch('', q.trim() || undefined, METRIC_PICK_LIMIT, 0, src, { cluster, signal }));
  const rows = res.data?.names ?? [];
  const total = res.data?.total ?? rows.length;
  return (
    <Popover open={open} onOpenChange={o => { setOpen(o); if (o) setQ(value); }}>
      <PopoverTrigger asChild>
        <Button type="button" variant="outline" size="xs" disabled={disabled}
          className="max-w-[28rem] gap-1.5 font-mono" aria-label={value ? `Metrik: ${value}` : 'Metrik seç'}>
          <span className="truncate">{value || 'Metrik seç'}</span>
          {value && kind && <KindBadge kind={kind} estimated={kindEstimated} />}
          <ChevronDown aria-hidden="true" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[34rem] max-w-[calc(100vw-2rem)] p-0">
        <PickList label="Metrik ara" placeholder="Metrik ara (sunucu araması)" query={q} onQuery={setQ}
          loading={res.loading} error={res.error ? `Metrik listesi okunamadı: ${res.error}` : null}
          emptyText={q.trim() ? `"${q.trim()}" ile eşleşen metrik yok` : 'Metrik yok'}
          items={rows.map(m => {
            const k = kindOf(m.name, m.type);
            return {
              value: m.name,
              label: m.name,
              aside: <span className="inline-flex items-center gap-1.5"><KindBadge kind={k.kind} estimated={m.typeSource === 'inferred' || k.estimated} />{m.unit && <span>{m.unit}</span>}</span>,
              detail: [m.help || m.description, typeSourceText(m)].filter(Boolean).join(' ') || undefined,
            };
          })}
          footer={<span>Sunucu araması · {total > rows.length ? `${total} sonuçtan ilk ${rows.length}` : `${rows.length} sonuç`}{cluster ? ` · kapsam: ${cluster}` : ''}</span>}
          onPick={name => {
            const m = rows.find(r => r.name === name);
            if (m) onPick(m);
            setOpen(false);
          }} />
      </PopoverContent>
    </Popover>
  );
}
