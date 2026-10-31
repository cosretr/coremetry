// EndpointPickCard — v0.10.1737 (operatör onayı, mockup "Endpoint seç"). CoSRE
// v2'de tek servisi anıp rota söylemeyen soru (sunucu data-pick kind=endpoint)
// → cevabın altında. Model endpoint listesi yazmaz; seçim burada:
//   • ilk satırlar SUNUCU yükünden (hata % sonra p95'e göre sıralı, ilk 4),
//   • yazınca arama SUNUCUDA (/api/endpoints search — usePickerSearch 180 ms
//     debounce + sıra bekçisi; servisin endpoint kataloğu önden çekilmez),
//   • satır = mono route · req/s · p95 · hata (sayılar sağa yaslı, yalnız sapan
//     değer renkli); seçili satırda ✓ (coremetry/SearchCommand, cmdk listbox),
//   • "İncele" → seçilen route aynı sohbette yeni tur; "Hepsini özetle" → aynı
//     soru kart atlanarak (bugünkü servis özeti).
// Sunumsal: gönderim çağıranda (CosreChatV2).
import { useMemo, useState } from 'react';
import { Waypoints } from 'lucide-react';
import { api } from '@/lib/api';
import type { CosreEndpointPick, CosreEndpointPickItem } from '@/lib/types';
import { cn } from '@/lib/cn';
import { usePickerSearch } from '@/components/usePickerSearch';
import { Button } from '@/components/shadcn/button';
import { SearchCommand } from '@/components/coremetry/SearchCommand';
import { COSRE_V2_STRINGS as T } from './strings';
import { fmtErrPct, fmtP95, fmtRps, isBadErr, isBadP95, moreLabel, pickItemsFromRows } from './endpointPick';

/** Arama sayfası (sunucu tavanı; kart yalnız ilk sayfayı çizer). */
const SEARCH_LIMIT = 20;
const COLS = 'grid grid-cols-[64px_64px_56px] gap-2 text-right tabular-nums';

export interface EndpointPickCardProps {
  pick: CosreEndpointPick;
  disabled?: boolean;
  onInspect(route: string): void;
  onSummarize(): void;
}

export function EndpointPickCard({ pick, disabled = false, onInspect, onSummarize }: EndpointPickCardProps) {
  const [q, setQ] = useState('');
  const [sel, setSel] = useState(pick.items[0]?.route ?? '');
  // Boş sorgu: sunucu yükü (istek yok). Dolu sorgu: /api/endpoints araması —
  // pencere tıklama anında (render'da now() yok, v0.5.184).
  const run = (term: string): Promise<{ items: CosreEndpointPickItem[]; total: number }> => {
    const t = term.trim();
    if (!t) return Promise.resolve({ items: pick.items, total: pick.total });
    const to = Date.now() * 1e6;
    return api.endpoints({ from: to - pick.rangeS * 1e9, to, service: pick.service, env: pick.env, search: t,
      limit: SEARCH_LIMIT, sort: 'errorRate', dir: 'desc' })
      .then(r => { const items = pickItemsFromRows(r?.rows, pick.rangeS); return { items, total: items.length }; });
  };
  const search = usePickerSearch(q, `cosre-eppick:${pick.service}:${pick.env ?? ''}`, run);
  const rows = q.trim() ? search.items : pick.items;
  const byRoute = useMemo(() => new Map(rows.map(r => [r.route, r])), [rows]);
  const routes = useMemo(() => rows.map(r => r.route), [rows]);
  const stateKind = q.trim() ? (search.status ?? 'empty') : 'empty';
  const stateText = stateKind === 'loading' ? T.endpointPickLoading
    : stateKind === 'error' ? T.endpointPickError : T.endpointPickEmpty;
  const more = q.trim() ? '' : moreLabel(pick);
  const heading = (
    <span className="flex items-center gap-2 text-[11px] uppercase tracking-wide" data-eppick-header="">
      <span className="size-4 shrink-0" aria-hidden="true" />
      <span className="min-w-0 flex-1">{T.endpointPickColRoute}</span>
      <span className={COLS}>
        <span>{T.endpointPickColRps}</span><span>{T.endpointPickColP95}</span><span>{T.endpointPickColErr}</span>
      </span>
    </span>
  );
  const meta = (route: string) => {
    const r = byRoute.get(route);
    if (!r) return null;
    return (
      <span className={COLS} data-eppick-metrics="">
        <span>{fmtRps(r.rps)}</span>
        <span className={cn(isBadP95(r.p95Ms) && 'text-status-critical')} data-bad={isBadP95(r.p95Ms) ? '' : undefined}>{fmtP95(r.p95Ms)}</span>
        <span className={cn(isBadErr(r.errPct) && 'text-status-critical')} data-bad={isBadErr(r.errPct) ? '' : undefined}>{fmtErrPct(r.errPct)}</span>
      </span>
    );
  };
  return (
    <div className="cm-msg cm-msg--ai" data-cosre-endpointpick="">
      <div className="rounded-md border border-border bg-card text-sm" role="group" aria-label={T.endpointPickTitle}>
        <div className="flex items-start gap-2 px-3 pt-3 pb-2">
          <Waypoints aria-hidden="true" className="mt-0.5 size-4 text-muted-foreground" />
          <div className="min-w-0">
            <div className="font-medium">{T.endpointPickTitle}</div>
            <p className="text-muted-foreground">{T.endpointPickSubtitle}</p>
          </div>
        </div>
        <SearchCommand data-search="endpointpick" query={q} onQueryChange={setQ} placeholder={T.endpointPickSearch}
          heading={heading} items={routes} stateText={stateText} stateKind={stateKind}
          onPick={setSel} selected={sel} selectedLabel={T.endpointPickSelected} meta={meta} disabled={disabled}
          className="rounded-none border-t border-border bg-transparent" />
        {more && <div className="px-5 pb-2 text-xs text-muted-foreground" data-eppick-more="">{more}</div>}
        <div className="flex items-center justify-end gap-2 border-t border-border px-3 py-2">
          <Button size="sm" variant="secondary" disabled={disabled} onClick={onSummarize} data-eppick-summarize="">
            {T.endpointPickSummarize}
          </Button>
          <Button size="sm" disabled={disabled || !sel} onClick={() => sel && onInspect(sel)} data-eppick-submit="">
            {T.endpointPickSubmit}
          </Button>
        </div>
      </div>
    </div>
  );
}
