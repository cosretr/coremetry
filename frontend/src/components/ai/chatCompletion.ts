// chatCompletion — v0.10.687 (D4 "girişte ad tamamlama", cosre-chat-parity
// planı; ai-ui-patterns #7). SAF; React yok. Testi chatCompletion.test.ts.
//
// Tetik (plan §soru 2 cevabı: hem açık hem otomatik):
//   - '@' öneki: 1+ karakter → AÇIK tetik (Slack mention alışkanlığı).
//   - öneksiz: token ≥3 karakter VE '-' içeriyor (bu kurulumdaki servis adları
//     tireli) → OTOMATİK tetik. Sıradan sözcükler ("neden", "yavaş") sunucuya
//     GİTMEZ — picker kuralı: her tuşta katalog çekilmez.
//
// v0.10.1138 — @ ve / kapsamı:
//   - '@' tek başına → anma türleri menüsü (@trace: @problem: @env: @team: @wiki);
//     '@x' → eşleşen türler + servis adayları (sunucu araması).
//   - '@env:<q>' → ortam adayları, '@team:<q>' → takım adayları (sunucu).
//   - '@trace:' / '@problem:' → aday yok (serbest kimlik).
//   - mesajın BAŞINDA '/x' → komut menüsü (chatScope.CHAT_COMMANDS).
//   AÇIK '@' ile seçilen servis '@ad' olarak kalır (kapsam anması); otomatik
//   tetikte (tireli token) eskisi gibi çıplak ad yazılır.
import { CHAT_COMMANDS, MENTION_KINDS } from './chatScope';

export type CompletionKind = 'service' | 'mention' | 'env' | 'team' | 'command';

export interface CompletionQuery {
  query: string;
  /** Token'ın metindeki aralığı ('@' dahil) — applyCompletion bunu değiştirir. */
  start: number;
  end: number;
  explicit: boolean;
  /** v0.10.1138 — ne tamamlanıyor (yoksa 'service': v0.10.687 davranışı). */
  kind?: CompletionKind;
}

/** Bir öneri satırı: ekrandaki etiket + metne yazılacak parça. */
export interface CompletionItem {
  label: string;
  insert: string;
  hint?: string;
  /** Servis anmasıysa kanonik ad (composer "seçildi" kümesine ekler). */
  service?: string;
}

export function completionQuery(text: string, caret: number): CompletionQuery | null {
  const upto = text.slice(0, Math.max(0, Math.min(caret, text.length)));
  const m = /(\S+)$/.exec(upto);
  if (!m) return null;
  const token = m[1];
  const start = upto.length - token.length;
  const end = upto.length;
  if (token.startsWith('/') && upto.slice(0, start).trim() === '' && /^\/[a-z]*$/i.test(token)) {
    return { query: token.slice(1).toLowerCase(), start, end, explicit: true, kind: 'command' };
  }
  if (token.startsWith('@')) {
    const body = token.slice(1);
    const lower = body.toLowerCase();
    if (lower.startsWith('env:')) return { query: body.slice(4), start, end, explicit: true, kind: 'env' };
    if (lower.startsWith('team:')) return { query: body.slice(5), start, end, explicit: true, kind: 'team' };
    if (body.includes(':')) return null; // @trace:<id>, @problem:<id> — serbest kimlik
    return { query: body, start, end, explicit: true, kind: body.length >= 1 ? 'service' : 'mention' };
  }
  if (token.length >= 3 && token.includes('-')) return { query: token, start, end, explicit: false, kind: 'service' };
  return null;
}

/** staticItems — SAF: sunucusuz öneriler (komut menüsü, anma türleri). */
export function staticItems(q: CompletionQuery | null): CompletionItem[] {
  if (!q) return [];
  if (q.kind === 'command') {
    return CHAT_COMMANDS.filter(c => c.cmd.startsWith(q.query))
      .map(c => ({ label: c.usage, insert: `/${c.cmd} `, hint: c.hint }));
  }
  if (q.explicit && (q.kind === 'mention' || q.kind === 'service')) {
    const lq = q.query.toLowerCase();
    return MENTION_KINDS.filter(k => k.insert.slice(1).startsWith(lq))
      .map(k => ({ label: k.insert.trim(), insert: k.insert, hint: k.hint }));
  }
  return [];
}

/** nameItems — SAF: sunucu adları → öneri satırları (türüne göre yazım). */
export function nameItems(q: CompletionQuery | null, names: readonly string[]): CompletionItem[] {
  if (!q) return [];
  switch (q.kind ?? 'service') {
    case 'env': return names.map(n => ({ label: n, insert: `@env:${n} `, hint: 'ortam' }));
    case 'team': return names.map(n => ({ label: n, insert: `@team:${n} `, hint: 'takım' }));
    case 'service':
      return names.map(n => (q.explicit
        ? { label: n, insert: `@${n} `, hint: 'servis', service: n }
        : { label: n, insert: `${n} ` }));
    default: return [];
  }
}

/** applyCompletion — token'ı seçilen parçayla değiştirir; imleç parçanın sonrasında.
 *  `insert` boşlukla bitmiyorsa (ör. '@env:') boşluk eklenmez — tür seçildi,
 *  değer yazılacak. Çıplak ad (v0.10.687 çağrısı) eskisi gibi ad + boşluk. */
export function applyCompletion(text: string, q: CompletionQuery, insertOrName: string | CompletionItem): { text: string; caret: number } {
  const ins = typeof insertOrName === 'string' ? `${insertOrName} ` : insertOrName.insert;
  const next = text.slice(0, q.start) + ins + text.slice(q.end);
  return { text: next, caret: q.start + ins.length };
}

/** moveHighlight — listede sarmalı gezinme. */
export function moveHighlight(i: number, delta: number, n: number): number {
  if (n <= 0) return 0;
  return (((i + delta) % n) + n) % n;
}
