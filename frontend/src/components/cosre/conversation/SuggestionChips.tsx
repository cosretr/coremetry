// SuggestionChips — v0.10.2364 (docs/cosre/interaction-audit.md §6 I3): cevabın
// altındaki SONRAKİ ADIM çipleri (data-suggestions). interaction açıkken
// FollowupChips'in yerine çizilir; aynı Chip bileşeni.
//
// Tık: etiket kullanıcı mesajı olur, gövdeye `suggestion {id, token}` eklenir;
// sunucu niyeti jetondan (router'sız) yürütür — istemci niyet yollamaz.
// Sinyal `suggestion_click` {chip, rule, tool, rank} (ağırlık 0, etiket değil).
import { Chip } from '@/components/ui';
import '@/styles/cosreChat.css';
import { api } from '@/lib/api';
import { parseDataSuggestions } from '@/lib/cosre/protocol';
import type { CosreSuggestion, CosreSuggestions } from '@/lib/types';

export interface SuggestionChipsProps {
  suggestions: CosreSuggestions;
  /** Yoksa çip çizilmez (bitmemiş / eski cevap). */
  onSuggest?: (chip: CosreSuggestion) => void;
  disabled?: boolean;
}

/** SAF: suggestion_click ayrıntısı (≤512 B). */
export function suggestionClickDetail(c: CosreSuggestion, rank: number): string {
  return JSON.stringify({ chip: c.id, rule: c.rule, tool: c.intent.tool, rank });
}

/** SAF: suggestion_ignored ayrıntısı. */
export function suggestionIgnoredDetail(s: CosreSuggestions): string {
  return JSON.stringify({ chips: s.chips.map(c => c.id), rules: s.chips.map(c => c.rule) });
}

/** SAF: son mesaj öneri çipli asistan cevabıysa çipleri (yoksa null). */
export function pendingSuggestions(messages: readonly { role: string; parts: readonly { type: string; data?: unknown }[] }[]): CosreSuggestions | null {
  const last = messages.at(-1);
  if (last?.role !== 'assistant') return null;
  const p = last.parts.findLast(x => x.type === 'data-suggestions');
  return p ? parseDataSuggestions(p) : null;
}

/** v0.10.2364 (I4) — çipler görünürken kullanıcı kendisi yazdı: `suggestion_ignored`,
 *  alışveriş başına bir kez (`sent` kimlikleri tutar). Ölçüm; çalışma zamanı kararı değil. */
export function noteSuggestionsIgnored(messages: Parameters<typeof pendingSuggestions>[0], sent: Set<string>): void {
  const s = pendingSuggestions(messages);
  const xid = s?.exchangeId;
  if (!s || !xid || sent.has(xid)) return;
  sent.add(xid);
  api.aiSignal(xid, 'suggestion_ignored', suggestionIgnoredDetail(s));
}

export function SuggestionChips({ suggestions, onSuggest, disabled }: SuggestionChipsProps) {
  if (!onSuggest) return null;
  const xid = suggestions.exchangeId;
  return (
    <div className="cm-followups" data-cosre-suggestions="">
      {suggestions.chips.map((c, i) => (
        <Chip key={c.id} pill disabled={disabled} data-suggestion-rule={c.rule}
          onClick={() => {
            if (xid) api.aiSignal(xid, 'suggestion_click', suggestionClickDetail(c, i + 1));
            onSuggest(c);
          }}>
          ↳ {c.label}
        </Chip>
      ))}
    </div>
  );
}
