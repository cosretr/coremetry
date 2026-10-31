// TeamPickCard — v0.10.1725 (operatör onayı, mockup "Takımını seç"). CoSRE
// v2'de takım biçimli soru + data-scope no_team (teamPick) → cevabın altında.
// Model takımları metin olarak sıralamaz; seçim burada:
//   • arama SUNUCUDA (karşılama seçicisiyle aynı uç: /api/copilot/scope-names,
//     usePickerSearch 180 ms debounce + sıra bekçisi; katalog önden çekilmez),
//   • satır = takım + "N servis"; seçili satırda ✓ (coremetry/SearchCommand:
//     cmdk listbox, ok tuşları + Enter),
//   • "Seç ve yeniden sor" → takım karşılamanın deposuna yazılır (çağıran) ve
//     aynı soru o takımla yeniden sorulur; "Kapsamsız devam et" → aynı soru
//     açık kapsamsız bayrağıyla (takım bir daha sorulmaz).
// Sunumsal: gönderim çağıranda (CosreChatV2).
import { useMemo, useState } from 'react';
import { Users } from 'lucide-react';
import { api } from '@/lib/api';
import { usePickerSearch } from '@/components/usePickerSearch';
import { Button } from '@/components/shadcn/button';
import { SearchCommand } from '@/components/coremetry/SearchCommand';
import { COSRE_V2_STRINGS as T } from './strings';
import { teamServicesLabel } from './teamPick';

interface TeamRow { name: string; services?: number }

function searchTeams(q: string): Promise<{ items: TeamRow[]; total: number }> {
  return api.copilotScopeNames('team', q.trim()).then(r => {
    const items = r.names.map((name, i) => ({ name, services: r.services?.[i] }));
    return { items, total: items.length };
  });
}

export interface TeamPickCardProps {
  /** Ön seçim (karşılamada seçilmiş takım varsa). */
  initial?: string;
  disabled?: boolean;
  onPick(team: string): void;
  onUnscoped(): void;
}

export function TeamPickCard({ initial = '', disabled = false, onPick, onUnscoped }: TeamPickCardProps) {
  const [q, setQ] = useState('');
  const [sel, setSel] = useState(initial);
  const search = usePickerSearch(q, 'cosre-teampick', searchTeams);
  const names = useMemo(() => search.items.map(t => t.name), [search.items]);
  const counts = useMemo(() => new Map(search.items.map(t => [t.name, t.services])), [search.items]);
  const stateKind = search.status ?? 'empty';
  const stateText = stateKind === 'loading' ? T.teamPickLoading
    : stateKind === 'error' ? T.teamPickError : T.teamPickEmpty;
  return (
    <div className="cm-msg cm-msg--ai" data-cosre-teampick="">
      <div className="rounded-md border border-border bg-card text-sm" role="group" aria-label={T.teamPickTitle}>
        <div className="flex items-start gap-2 px-3 pt-3 pb-2">
          <Users aria-hidden="true" className="mt-0.5 size-4 text-muted-foreground" />
          <div className="min-w-0">
            <div className="font-medium">{T.teamPickTitle}</div>
            <p className="text-muted-foreground">{T.teamPickSubtitle}</p>
          </div>
        </div>
        <SearchCommand data-search="teampick" query={q} onQueryChange={setQ} placeholder={T.teamPickSearch}
          heading={T.teamPickHeading} items={names} stateText={stateText} stateKind={stateKind}
          onPick={setSel} selected={sel} selectedLabel={T.teamPickSelected}
          meta={name => teamServicesLabel(counts.get(name))} disabled={disabled}
          className="rounded-none border-t border-border bg-transparent" />
        <div className="flex items-center justify-end gap-2 border-t border-border px-3 py-2">
          <Button size="sm" variant="secondary" disabled={disabled} onClick={onUnscoped} data-teampick-unscoped="">
            {T.teamPickUnscoped}
          </Button>
          <Button size="sm" disabled={disabled || !sel} onClick={() => sel && onPick(sel)} data-teampick-submit="">
            {T.teamPickSubmit}
          </Button>
        </div>
      </div>
    </div>
  );
}
