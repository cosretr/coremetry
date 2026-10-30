// CosreScopeSearch — CoSRE v2 karşılaması (mockup B, 2026-10-31). Takım /
// servis / operasyon seçimi için sunucu-aramalı Command (coremetry/SearchCommand).
// Satır içi Service/Operation/TeamPicker kutularının YERİNE; veri kaynağı
// onlarla AYNI (usePickerSearch, 180 ms debounce, sıra bekçisi; katalog önden
// çekilmez):
//   team      → /api/copilot/scope-names?kind=team (TeamPicker)
//   service   → /api/services/names (ServicePicker)
//   operation → /api/copilot/operation-search (OperationPicker source="cosre", ≥2 harf)
import { useState } from 'react';
import { Users, Search, Workflow } from 'lucide-react';
import { api } from '@/lib/api';
import { usePickerSearch } from '@/components/usePickerSearch';
import { COSRE_OP_SEARCH_MIN } from '@/components/OperationPicker';
import { SearchCommand } from '@/components/coremetry/SearchCommand';
import { tCosre as t } from '@/components/ai/capabilityHints';

export type CosreScopeKind = 'team' | 'service' | 'operation';

const ICON = { team: Users, service: Search, operation: Workflow } as const;
const PLACEHOLDER: Record<CosreScopeKind, string> = {
  team: 'cosre.cap.search.team',
  service: 'cosre.cap.search.service',
  operation: 'cosre.cap.search.operation',
};
const HEADING: Record<CosreScopeKind, string> = {
  team: 'cosre.welcome.search.teams',
  service: 'cosre.welcome.search.services',
  operation: 'cosre.welcome.search.operations',
};
const SEARCH_LIMIT = 50;

function runSearch(kind: CosreScopeKind, q: string): Promise<{ items: string[]; total: number }> {
  const s = q.trim();
  switch (kind) {
    case 'team':
      return api.copilotScopeNames('team', s).then(r => ({ items: r.names, total: r.names.length }));
    case 'service':
      return api.serviceNames(s, SEARCH_LIMIT).then(r => ({ items: r.names, total: r.total }));
    case 'operation':
      if (s.length < COSRE_OP_SEARCH_MIN) return Promise.resolve({ items: [], total: 0 });
      return api.copilotOperationSearch(s).then(r => ({ items: r.names, total: r.total }));
  }
}

export function CosreScopeSearch({ kind, onPick, autoFocus = false, className }: {
  kind: CosreScopeKind;
  onPick: (name: string) => void;
  autoFocus?: boolean;
  className?: string;
}) {
  const [q, setQ] = useState('');
  const search = usePickerSearch(q, `cosre-welcome:${kind}`, s => runSearch(kind, s));
  const Icon = ICON[kind];
  const tooShort = kind === 'operation' && q.trim().length < COSRE_OP_SEARCH_MIN;
  const stateKind = tooShort ? 'tooShort' : search.status ?? 'empty';
  const stateText = stateKind === 'loading' ? t('cosre.welcome.search.loading')
    : stateKind === 'error' ? t('cosre.welcome.search.error')
      : stateKind === 'tooShort' ? t('cosre.welcome.search.tooShort') : t('cosre.welcome.search.empty');
  return (
    <SearchCommand data-search={kind} query={q} onQueryChange={setQ} placeholder={t(PLACEHOLDER[kind])}
      heading={t(HEADING[kind])} items={search.items} stateText={stateText} stateKind={stateKind}
      onPick={onPick} icon={<Icon aria-hidden="true" />} autoFocus={autoFocus} className={className} />
  );
}
