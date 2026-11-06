// pages/metrics/CodePanel — v0.10.1863 (dilim 2.3) → v0.10.1864 (dilim 2.4;
// mockup görünüm 2). MetricsQL kipi:
//   · dil rozeti (VictoriaMetrics · MetricsQL / ClickHouse · PromQL (alt
//     küme) / Thanos) + kapsam çipi (sorgu metni değişmez; tek tıkla kapanır)
//   · Parçacık ekle ▾ (WITH, default, topk_avg, rollup_rate,
//     keep_metric_names — MetricsQL'e özgü; CH'de nedeniyle kapalı)
//   · Biçimlendir, Çalıştır ⌘↵ (bölgenin TEK birincil eylemi)
//   · renklendirme + tamamlama + hata konumu (coremetry/metrics/MetricsQLEditor)
//   · konumlu hata satırı: "Satır L, sütun C: … Sorgu çalıştırılmadı; son
//     başarılı sonuç aşağıda duruyor." (400 ile 502 ayrı metin)
//   · "Builder'a çevir": yalnız builder biçimindeki metin; değilse nedeni yanında
// Kaynak seçimi (Varsayılan / VM / CH / Thanos) MQE PromQL sekmesinden aynen taşındı.
import { useEffect, useState, type ReactNode } from 'react';
import { ChevronDown } from 'lucide-react';
import { Badge } from '@/components/shadcn/badge';
import { Button } from '@/components/shadcn/button';
import { NativeSelect, NativeSelectOption } from '@/components/shadcn/native-select';
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger,
} from '@/components/coremetry/overlay';
import { MetricsQLEditor } from '@/components/coremetry/metrics';
import type { MetricsCapabilities } from '@/lib/types';
import { MX_MAX_CODE, type MxQuery, type MxSrc } from '@/lib/metricsExplorer/model';
import type { RunError } from '@/lib/metricsExplorer/run';
import { languageBadge } from '@/lib/metricsExplorer/result';
import { formatMql } from '@/lib/metricsExplorer/lex';
import { parseBuilderForm } from '@/lib/metricsExplorer/parse';
import { applySnippet, SNIPPETS } from '@/lib/metricsExplorer/snippets';

export interface CodePanelProps {
  code: string;
  src: MxSrc;
  caps?: MetricsCapabilities;
  canThanos: boolean;
  thanosCluster: string;
  onRun(code: string): void;
  onSrc(src: MxSrc): void;
  /** Uygulanan metnin hatası ve operatörün metnine çevrilmiş konumu. */
  error?: RunError | null;
  errorPos?: number;
  scopeChip?: ReactNode;
  /** Kapsam etiket önerileri için (tamamlama). */
  cluster?: string;
  onToBuilder(q: MxQuery, notes: string[]): void;
}

export function CodePanel({
  code, src, caps, canThanos, thanosCluster, onRun, onSrc, error, errorPos, scopeChip, cluster, onToBuilder,
}: CodePanelProps) {
  const [draft, setDraft] = useState(code);
  const [snipOpen, setSnipOpen] = useState(false);
  useEffect(() => { setDraft(code); }, [code]);
  const tooLong = draft.length > MX_MAX_CODE;
  const run = () => { if (!tooLong && draft.trim()) onRun(draft); };
  const metricsql = src === 'thanos' ? false : src === 'ch' ? false : src === 'vm' ? true : (caps ? caps.metricsql : true);
  const mqlReason = src === 'thanos' ? 'Thanos PromQL konuşur' : 'Yalnız VictoriaMetrics (MetricsQL); ClickHouse PromQL alt kümesi desteklemez';
  const conv = draft.trim() ? parseBuilderForm(draft) : null;
  const applied = draft.trim() === code.trim();
  const showErr = applied && error;
  const errLine = showErr && error!.line && error!.col ? `Satır ${error!.line}, sütun ${error!.col}: ` : '';
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
        <DropdownMenu open={snipOpen} onOpenChange={setSnipOpen}>
          <DropdownMenuTrigger asChild>
            <Button type="button" variant="outline" size="sm">Parçacık ekle <ChevronDown aria-hidden="true" /></Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="min-w-72">
            {SNIPPETS.map(s => (
              <DropdownMenuItem key={s.id} disabled={!metricsql} title={metricsql ? undefined : mqlReason}
                onSelect={() => setDraft(d => applySnippet(s.id, d))} className="flex-col items-start gap-0">
                <span className="font-mono text-xs">{s.label}</span>
                <span className="text-xs text-muted-foreground">{metricsql ? s.hint : mqlReason}</span>
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
        <Button type="button" variant="outline" size="sm" onClick={() => setDraft(d => formatMql(d))} disabled={!draft.trim()}>Biçimlendir</Button>
        <Button type="button" size="sm" onClick={run} disabled={tooLong || !draft.trim()}>
          Çalıştır <kbd className="font-mono text-xs opacity-80">⌘↵</kbd>
        </Button>
      </div>
      {src === 'thanos' && !thanosCluster && (
        <p role="status" className="text-xs text-status-warn">Thanos sorgusu bir cluster ister: bağlam çubuğundan cluster seçin.</p>
      )}
      <MetricsQLEditor value={draft} onChange={setDraft} onRun={run} errorPos={showErr ? errorPos : undefined}
        metricsql={metricsql} metricsqlReason={mqlReason} cluster={cluster} />
      {tooLong && <p role="alert" className="text-xs text-status-critical">Sorgu 8 KiB sınırını aşıyor.</p>}
      {showErr && (
        <div role="alert" className="rounded-md border border-status-critical-border bg-status-critical-bg px-3 py-2 text-sm">
          <b className="text-status-critical">{error!.status === 400 ? 'Sözdizimi hatası' : 'Sorgu çalıştırılamadı'}</b>{' '}
          {errLine}{error!.detail || error!.message}{error!.status === 400 ? ' Sorgu çalıştırılmadı; son başarılı sonuç aşağıda duruyor.' : ''}
        </div>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-xs text-muted-foreground"><kbd className="font-mono">Ctrl Space</kbd> tamamlama</span>
        <span className="flex-1" />
        <Button type="button" variant="outline" size="sm" disabled={!conv || !conv.ok}
          onClick={() => { if (conv?.ok) onToBuilder(conv.query, conv.notes); }}>Builder'a çevir</Button>
        {conv && !conv.ok && <span className="text-xs text-muted-foreground">{conv.reason}</span>}
      </div>
    </div>
  );
}
