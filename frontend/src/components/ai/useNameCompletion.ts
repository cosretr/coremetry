import { useEffect, useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { keys } from '@/lib/queries/keys';
import { completionQuery, moveHighlight, nameItems, staticItems, type CompletionItem, type CompletionQuery } from './chatCompletion';

// useNameCompletion — v0.10.687 (D4): CoSRE girişindeki token için sunucu
// taraflı servis adı adayları. Debounce 180 ms (picker kuralı), boş
// sorguda İSTEK YOK (enabled), aynı önek 5 dk cache (keys.services.names —
// ServicePicker ile aynı anahtar, iki yüzey birbirini ısıtır). Kapatılan
// (Esc) sorgu aynı token için yeniden açılmaz; token değişince yine açılır.
//
// v0.10.1138 — @ ve / kapsamı: komut menüsü ve anma türleri sunucusuz
// (staticItems); `@env:` mevcut /api/environments?q= aramasını, `@team:`
// /api/copilot/scope-names aramasını kullanır — istemcide katalog süzme yok.
export interface NameCompletion {
  open: boolean;
  items: CompletionItem[];
  highlight: number;
  cq: CompletionQuery | null;
  setHighlight: (i: number) => void;
  move: (delta: number) => void;
  dismiss: () => void;
}

export function useNameCompletion(text: string, caret: number): NameCompletion {
  const cq = useMemo(() => completionQuery(text, caret), [text, caret]);
  const kind = cq?.kind ?? 'service';
  const live = cq?.query ?? '';
  const [dq, setDq] = useState('');
  useEffect(() => {
    const t = setTimeout(() => setDq(live), 180);
    return () => clearTimeout(t);
  }, [live]);
  const q = useQuery({
    queryKey: keys.services.names(dq, 20), // v0.10.874 — anahtar gerçek limiti taşır (istek 20)
    queryFn: () => api.serviceNames(dq, 20),
    enabled: dq.length > 0 && !!cq && kind === 'service',
    staleTime: 5 * 60_000,
  });
  const envQ = useQuery({
    queryKey: ['chat-complete', 'env', dq],
    queryFn: () => api.environments(dq || undefined),
    enabled: !!cq && kind === 'env' && dq === live,
    staleTime: 5 * 60_000,
  });
  const teamQ = useQuery({
    queryKey: ['chat-complete', 'team', dq],
    queryFn: () => api.copilotScopeNames('team', dq),
    enabled: !!cq && kind === 'team' && dq === live,
    staleTime: 60_000,
  });
  const items = useMemo<CompletionItem[]>(() => {
    if (!cq) return [];
    const fixed = staticItems(cq);
    if (kind === 'command' || kind === 'mention') return fixed;
    if (dq !== cq.query) return fixed;
    const names = kind === 'env' ? (envQ.data?.environments ?? [])
      : kind === 'team' ? (teamQ.data?.names ?? [])
        : (q.data?.names ?? []);
    return [...fixed, ...nameItems(cq, names.slice(0, 8))];
  }, [cq, kind, dq, q.data, envQ.data, teamQ.data]);
  const [highlight, setHighlight] = useState(0);
  useEffect(() => { setHighlight(0); }, [dq, kind]);
  const [dismissed, setDismissed] = useState<string | null>(null);
  const sig = cq ? `${kind}:${cq.query}` : null;
  const open = !!cq && items.length > 0 && dismissed !== sig;
  return {
    open, items, highlight, cq, setHighlight,
    move: d => setHighlight(h => moveHighlight(h, d, items.length)),
    dismiss: () => setDismissed(sig),
  };
}
