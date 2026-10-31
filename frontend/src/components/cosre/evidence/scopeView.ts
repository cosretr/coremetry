// scopeView — CoSRE v2 araç seti dilim 5: kapsam rozetinin SAF görünümü
// (data-scope, 7 neden; tools-audit Q1 "kapsamsız hiçbir zaman sessiz olmaz").
//   • team → nötr (kapsamlı); kapsamsız nedenler uyarı; okuma hatası kırmızı.
//   • Rozet son asistan mesajının kapsamını gösterir; kapsam bir önceki
//     cevabınkinden farklıysa o mesajın üstüne tek satır not.
import type { CosreScope } from '@/lib/types';
import { parseDataScope } from '@/lib/cosre/protocol';
import type { CosreUIMessage } from '@/lib/cosre/resetReducer';
import { EVIDENCE_STRINGS as T, SCOPE_LABELS } from './strings';

type Part = CosreUIMessage['parts'][number];

export type ScopeTone = 'ok' | 'warn' | 'err';

export interface ScopeView {
  tone: ScopeTone;
  label: string;
  text: string;
  /** Popover çipleri (≤20 ad, sunucu tavanı). */
  services: string[];
  /** Listeye sığmayan servis sayısı (count − gösterilen). */
  more: number;
  /** Değişim karşılaştırması için imza. */
  sig: string;
}

export function scopeView(s: CosreScope): ScopeView {
  const team = s.team ?? '';
  const def = SCOPE_LABELS[s.reason];
  const services = s.reason === 'team' ? (s.services ?? []) : [];
  const more = s.reason === 'team' ? Math.max(0, s.count - services.length) : 0;
  return {
    tone: s.reason === 'team' ? 'ok' : s.reason === 'error' ? 'err' : 'warn',
    label: def.label(team, s.count),
    text: def.text(team, s.count),
    services,
    more,
    sig: [s.reason, team, s.count, services.join(',')].join('|'),
  };
}

/** Mesajın kapsamı (data-scope id'si sabit "scope": son geçerli parça). */
export function scopeOf(parts: readonly Part[]): CosreScope | null {
  let out: CosreScope | null = null;
  for (const p of parts) {
    if (p.type === 'data-scope') out = parseDataScope(p as { type: string; data?: unknown }) ?? out;
  }
  return out;
}

/** Rozetin kapsamı: kapsam taşıyan SON asistan mesajı (yoksa null → rozet yok). */
export function latestScope(messages: readonly CosreUIMessage[]): CosreScope | null {
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    if (m.role !== 'assistant') continue;
    const s = scopeOf(m.parts);
    if (s) return s;
  }
  return null;
}

/** Kapsamı bir önceki kapsamlı cevaptan farklı olan mesajlar → tek satır not.
 *  İlk kapsam not üretmez (rozet zaten söyler). */
export function scopeChangeNotes(messages: readonly CosreUIMessage[]): Map<string, string> {
  const out = new Map<string, string>();
  let prev: string | undefined;
  for (const m of messages) {
    if (m.role !== 'assistant') continue;
    const s = scopeOf(m.parts);
    if (!s) continue;
    const v = scopeView(s);
    if (prev !== undefined && prev !== v.sig) out.set(m.id, T.scopeNote(v.label));
    prev = v.sig;
  }
  return out;
}
