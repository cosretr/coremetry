// pages/metrics/CodePanel — v0.10.1863 (Metrics Explorer Faz 2 dilim 2.3).
//
// MetricsQL kipinin ilk hâli: metin alanı + "Çalıştır ⌘↵" + kaynak seçimi
// (Varsayılan / VictoriaMetrics / ClickHouse / Thanos) + dil rozeti + kapsam
// çipi. Kod kipi YAZDIKÇA çalışmaz (bölge başına tek birincil eylem: Çalıştır).
// Renklendirme, tamamlama, konumlu hata, "builder'a çevir" ve geçmiş dilim
// 2.4'te (MetricsQLEditor).
import { useEffect, useState } from 'react';
import { Badge } from '@/components/shadcn/badge';
import { Button } from '@/components/shadcn/button';
import { Textarea } from '@/components/shadcn/textarea';
import { NativeSelect, NativeSelectOption } from '@/components/shadcn/native-select';
import type { MetricsCapabilities } from '@/lib/types';
import { MX_MAX_CODE, type MxSrc } from '@/lib/metricsExplorer/model';
import type { RunError } from '@/lib/metricsExplorer/run';
import { languageBadge } from '@/lib/metricsExplorer/result';

export interface CodePanelProps {
  code: string;
  src: MxSrc;
  caps?: MetricsCapabilities;
  canThanos: boolean;
  thanosCluster: string;
  onRun(code: string): void;
  onSrc(src: MxSrc): void;
  error?: RunError | null;
  scopeChip?: React.ReactNode;
}

export function CodePanel({ code, src, caps, canThanos, thanosCluster, onRun, onSrc, error, scopeChip }: CodePanelProps) {
  const [draft, setDraft] = useState(code);
  useEffect(() => { setDraft(code); }, [code]);
  const tooLong = draft.length > MX_MAX_CODE;
  const run = () => { if (!tooLong) onRun(draft); };
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="secondary">{languageBadge(caps, src)}</Badge>
        {scopeChip}
        <span className="flex-1" />
        <label className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
          Kaynak
          <NativeSelect size="sm" value={src} onChange={e => onSrc(e.target.value as MxSrc)} aria-label="Sorgu kaynağı">
            <NativeSelectOption value="">Varsayılan</NativeSelectOption>
            <NativeSelectOption value="vm">VictoriaMetrics</NativeSelectOption>
            <NativeSelectOption value="ch">ClickHouse</NativeSelectOption>
            <NativeSelectOption value="thanos" disabled={!canThanos}>Thanos{canThanos ? '' : ' (editor+)'}</NativeSelectOption>
          </NativeSelect>
        </label>
        <Button type="button" size="sm" onClick={run} disabled={tooLong || !draft.trim()}>
          Çalıştır <kbd className="font-mono text-xs opacity-80">⌘↵</kbd>
        </Button>
      </div>
      {src === 'thanos' && !thanosCluster && (
        <p role="status" className="text-xs text-status-warn">Thanos sorgusu bir cluster ister: bağlam çubuğundan cluster seçin.</p>
      )}
      <Textarea value={draft} onChange={e => setDraft(e.target.value)} rows={6} spellCheck={false}
        aria-label="MetricsQL sorgusu" aria-invalid={error?.status === 400 || tooLong || undefined}
        placeholder={'sum by (service_name) (rate(http_server_request_duration_seconds_count[$__rate_interval]))'}
        className="font-mono text-xs md:text-xs"
        onKeyDown={e => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); run(); } }} />
      {tooLong && <p role="alert" className="text-xs text-status-critical">Sorgu 8 KiB sınırını aşıyor.</p>}
    </div>
  );
}
