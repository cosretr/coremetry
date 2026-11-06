// LogsFilterRow — v0.10.1498 (/logs Kibana-Discover yeniden tasarımı, dilim 1;
// operatör onaylı taslak 2026-10-24).
//
// Etkin süzgeçlerin TEK satırı: trace kilidi, desen, takım, with-trace çipleri,
// alan hapları, derlenmiş sorgu (KQL ⓘ). Her çip eylemi oto-uygular (state +
// sayfalama + URL Logs.tsx'te).
//
// v0.10.1854 (Log Search Faz 2 dilim 2.3; audit §6 "Filtre hapları", mockup
// görünüm 1 `.pill`) — haplar coremetry/FilterBar FilterChipRow'da: sabit (⊙) /
// ters (DEĞİL) / kapalı haplar, ✎ çip düzenleyici (FilterChipEditor; değer
// önerisi sunucu `_terms_enum`, gecikmeli + iptalli). Sabit hap servis / sorgu
// değişince, "Tümünü temizle"de ve ⋯ Sıfırla'da KALIR; URL'de ayrı `pins`.
// Satır shadcn'e geçti: ui/ importu ve satır içi stil yok.
import { useCallback, useState } from 'react';
import { Button } from '@/components/shadcn/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/coremetry/overlay';
import { FilterChipRow } from '@/components/coremetry/FilterBar';
import { CopyButton } from '@/components/CopyButton';
import { TeamPicker } from '@/components/TeamPicker';
import { api } from '@/lib/api';
import { chipToLogFilter, logFilterToChip, LOG_FILTER_OPS, type LogFilter } from '@/lib/logFilters';
import type { FilterChip } from '@/lib/filterChips';
import type { LogsUrlFilter } from '@/lib/logsUrl';
import { isSameTeam } from '@/lib/logsTeam';
import { useT } from '@/lib/i18n';

export interface LogsFilterRowProps {
  filter: LogsUrlFilter;
  onClearTrace: () => void;
  onClearPattern: () => void;
  onToggleHasTrace: () => void;
  // Takım
  myTeamName: string;
  teamGate: boolean;
  teamPickerOpen: boolean;
  teamDraft: string;
  onTeamDraft: (v: string) => void;
  onToggleTeamPicker: () => void;
  onSetTeam: (team: string) => void;
  onClearTeam: () => void;
  /** Alan hapları (sabitler dahil). */
  filters: LogFilter[];
  /** Hap satırının her eylemi: yeni TAM liste (sıra korunur). */
  onPills: (next: LogFilter[]) => void;
  /** Tümünü temizle — sabit haplar kalır (çağıran karar verir). */
  onClearPills: () => void;
  since?: string;
  /** Backend'e giden `search=` cümlesi (pill'ler + serbest metin). */
  compiledSearch: string;
  /** KQL kutusunda çıplak trace id varsa o id ('' = öneri yok). */
  traceHint: string;
  onUseTraceHint: () => void;
}

const CHIP = 'trace-lock';

export function LogsFilterRow(p: LogsFilterRowProps) {
  const tr = useT();
  const { filter, filters, compiledSearch, since } = p;
  const [kqlOpen, setKqlOpen] = useState(false);
  // Değer önerisi: mevcut `_terms_enum` ucu (pencereyle sınırlı, sunucuda 30 sn
  // önbellek) — düzenleyici 180 ms gecikme + iptal sinyaliyle çağırır.
  const suggestValues = useCallback((key: string, q: string, signal: AbortSignal) =>
    api.logsFieldValues(key, q, 20, since, signal).then(r => r?.values ?? []), [since]);
  const suggestTeam = !filter.team && !!p.myTeamName && !p.teamGate;
  const unpinned = filters.filter(f => !f.pinned).length;
  const any = !!filter.traceId || !!filter.pattern || !!filter.team || filter.hasTrace
    || filters.length > 0 || suggestTeam || !!p.traceHint || !!compiledSearch;
  if (!any) return null;
  const chips: FilterChip[] = filters.map(logFilterToChip);
  return (
    <FilterChipRow className="ld-frow" ariaLabel="Etkin süzgeçler"
      chips={chips} ops={LOG_FILTER_OPS} suggestValues={suggestValues}
      onChange={next => p.onPills(next.map(chipToLogFilter))}
      leading={(
        <>
          {filter.traceId && (
            <span className={CHIP}>
              <span>Filtered to trace</span>
              <code>{filter.traceId}</code>
              {filter.spanId && (<>
                <span>· span</span>
                <code>{filter.spanId}</code>
              </>)}
              <Button variant="outline" size="xs" onClick={p.onClearTrace}>✕ Clear</Button>
            </span>
          )}
          {/* v0.10.1071 — desen süzgeci ÇİP: sunucu dedektörün yüklemini uygular. */}
          {filter.pattern && (
            <span className={CHIP} data-testid="logs-pattern-chip">
              <span>desen:</span>
              <code title="Küratörlü log deseni — dedektörün eşleşme yüklemi sunucuda uygulanır">{filter.pattern}</code>
              <Button variant="ghost" size="xs" onClick={p.onClearPattern}
                aria-label={`Desen süzgecini kaldır: ${filter.pattern}`}
                title="Desen süzgecini kaldır (arama metni ve diğer süzgeçler kalır)">×</Button>
            </span>
          )}
          {/* v0.10.1477 — takım süzgeci ÇİP: "Değiştir" TeamPicker'ı açar. */}
          {filter.team && (
            <span className={CHIP} data-testid="logs-team-chip">
              <span>{p.myTeamName && isSameTeam(filter.team, p.myTeamName) ? tr('logs.team.mine') : tr('logs.team.label')}:</span>
              <code title={tr('logs.team.chipTitle')}>{filter.team}</code>
              <Button variant="ghost" size="xs" data-testid="logs-team-change"
                onClick={p.onToggleTeamPicker}>{tr('logs.team.change')}</Button>
              {p.teamPickerOpen && (
                <TeamPicker value={p.teamDraft} onChange={p.onTeamDraft} width={190}
                  placeholder={tr('logs.team.pick')}
                  onEnter={v => { const t = (v ?? p.teamDraft).trim(); if (t) p.onSetTeam(t); }} />
              )}
              <Button variant="ghost" size="xs" onClick={p.onClearTeam}
                aria-label={`${tr('logs.team.remove')}: ${filter.team}`}
                title={tr('logs.team.removeTitle')}>×</Button>
            </span>
          )}
          {filter.hasTrace && (
            <span className={CHIP} data-testid="logs-hastrace-chip">
              <span>◆ {tr('logs.dv.withTrace')}</span>
              <Button variant="ghost" size="xs" onClick={p.onToggleHasTrace}
                aria-label={tr('logs.dv.withTraceRemove')} title={tr('logs.dv.withTraceRemove')}>×</Button>
            </span>
          )}
        </>
      )}
      trailing={(
        <>
          {unpinned > 0 && (
            <Button variant="ghost" size="xs" onClick={p.onClearPills}
              title="Sabit olmayan alan süzgeçlerini kaldır (sabit haplar ve serbest metin kalır)">
              Tümünü temizle
            </Button>
          )}
          {suggestTeam && (
            <Button variant="ghost" size="xs" data-testid="logs-team-apply"
              title={tr('logs.team.applyTitle')}
              onClick={() => p.onSetTeam(p.myTeamName)}>{tr('logs.team.mine')}: {p.myTeamName}</Button>
          )}
          {p.traceHint && (
            <Button variant="ghost" size="xs" data-testid="logs-trace-hint" onClick={p.onUseTraceHint}
              title={tr('logs.dv.asTraceTitle')}>
              ⌗ {tr('logs.dv.asTrace')}
            </Button>
          )}
          {/* v0.10.418 (B7) — UYGULANAN arama cümlesi (backend'e giden `search=`). */}
          {compiledSearch && (
            <Popover open={kqlOpen} onOpenChange={setKqlOpen}>
              <PopoverTrigger asChild>
                <Button variant="ghost" size="xs" className="ld-kql-btn" aria-haspopup="dialog"
                  title={tr('logs.dv.appliedSearch')}>KQL ⓘ</Button>
              </PopoverTrigger>
              <PopoverContent align="end" aria-label={tr('logs.dv.appliedSearch')} className="grid w-[420px] gap-1.5 break-all">
                <div className="ld-kql-head">
                  <span>{tr('logs.dv.appliedSearch')}</span>
                  <CopyButton value={compiledSearch} title="Arama cümlesini kopyala (KQL/Lucene alt kümesi)" />
                </div>
                <code title={compiledSearch}>{compiledSearch}</code>
              </PopoverContent>
            </Popover>
          )}
        </>
      )} />
  );
}
