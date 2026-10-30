// lib/palette/prompt — v0.10.1658 (etkileşim dilimi P2, plan §1.1, Q3/Q12).
// Eylem parametre kipinin (v0.5.457) SAF görünüm modeli: lib/actions ACTIONS
// birebir kalır (Q12); kip artık cmdk'nin aynı listesinde bir "sayfa" —
// başlık = eylem + adım (1/2), satırlar = text: Çalıştır/İleri · id-suggest:
// öneriler · duration: süre seçenekleri (eski çipler artık klavyeyle de
// seçilir). Esc listeye döner (Q3) — başlık bunu söyler.
import { DEFAULT_DURATIONS, type Action, type ParamValues, type SuggestItem } from '@/lib/actions';

export type PromptPick =
  | { type: 'text' }
  | { type: 'suggest'; item: SuggestItem }
  | { type: 'duration'; seconds: number };

export interface PromptRow { id: string; label: string; hint?: string; pick: PromptPick }

export interface PromptModel {
  heading: string;
  /** Girdi kutusu: text → param değeri, diğerleri → arama/süzme metni. */
  inputValue: string;
  placeholder: string;
  rows: PromptRow[];
  loading: boolean;
  empty: string;
}

export interface PromptState {
  action: Action;
  paramIdx: number;
  paramValues: ParamValues;
  suggestQuery: string;
  suggestResults: SuggestItem[];
  suggestLoading: boolean;
  running: boolean;
}

export function promptModel(s: PromptState): PromptModel {
  const cur = s.action.params[s.paramIdx];
  const n = s.action.params.length;
  const step = n > 1 ? ` · ${s.paramIdx + 1}/${n}` : '';
  const heading = `${s.action.label}${step} — esc: back to search`;
  const isLast = s.paramIdx + 1 >= n;
  if (s.running) {
    return { heading, inputValue: '', placeholder: cur?.label ?? '', rows: [], loading: true, empty: 'Running…' };
  }
  if (!cur) return { heading, inputValue: '', placeholder: '', rows: [], loading: false, empty: '' };
  if (cur.kind === 'duration') {
    const f = s.suggestQuery.trim().toLowerCase();
    const opts = (cur.durations ?? DEFAULT_DURATIONS).filter(o => !f || o.label.toLowerCase().includes(f));
    return {
      heading, inputValue: s.suggestQuery, placeholder: `${cur.label}…`, loading: false,
      empty: 'No matching duration.',
      rows: opts.map(o => ({ id: `dur:${o.label}`, label: o.label, hint: isLast ? '↵ run' : '↵ next', pick: { type: 'duration', seconds: o.seconds } })),
    };
  }
  if (cur.kind === 'id-suggest') {
    return {
      heading, inputValue: s.suggestQuery, placeholder: cur.placeholder || cur.label,
      loading: s.suggestLoading,
      empty: s.suggestLoading ? 'Searching…' : 'No matches. Type to search.',
      rows: s.suggestResults.map(r => ({ id: `sug:${r.id}`, label: r.label, hint: r.hint, pick: { type: 'suggest', item: r } })),
    };
  }
  // 'text'
  const v = s.paramValues[cur.name];
  const text = typeof v === 'string' ? v : '';
  // Boş + zorunlu: satır durur (başlık görünür kalsın), ↵ no-op (eski davranış).
  const blocked = cur.required && !text.trim();
  return {
    heading, inputValue: text, placeholder: cur.placeholder || cur.label, loading: false, empty: '',
    rows: [{ id: 'text:submit', label: isLast ? `Run: ${s.action.label}` : 'Next', hint: blocked ? `${cur.label} required` : '↵', pick: { type: 'text' } }],
  };
}
