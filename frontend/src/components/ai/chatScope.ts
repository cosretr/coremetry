import type { ChatCommand, ChatScope } from '@/lib/types';

// chatScope — v0.10.1138 (CoSRE sohbet iyileştirmeleri): composer'daki
// @-anmaların ve /-komutlarının SAF çözümleyicisi. Sunucu yarısı
// internal/api/chat_scope.go (komut listesi orada da aynı).
//
// Metin operatörün yazdığı gibi gider (geçmiş aynen görünsün); yanına
// YAPISAL kapsam (`context.scope`) ve komut (`context.command`) konur.
// Anma/komut yoksa ikisi de undefined döner → istek gövdesi bayt bayt eski
// (serbest metin yönlendirmesi değişmez).
//
// Anma biçimleri:
//   @trace:<32hex>  @problem:<id>  @env:<ad>  @team:<ad>  @wiki
//   @<servis>  — yalnız tamamlamadan SEÇİLDİYSE (chosen) ya da servis adı
//                biçimindeyse (küçük harf + tire/nokta: svc-orders). Yapıştırılan
//                stack trace'teki "@Override" / "@Transactional" kapsam olmaz.
// Komut: mesajın BAŞINDA `/wiki|/trace|/rca|/logs|/help` + boşluk/son. "/api/x"
// gibi yollar komut değildir.

export const CHAT_COMMANDS: { cmd: ChatCommand; usage: string; hint: string }[] = [
  { cmd: 'wiki', usage: '/wiki <soru>', hint: "Yalnız kurum wiki'sinde ara" },
  { cmd: 'trace', usage: '/trace <ifade>', hint: 'Trace araması (@servis ile daralt)' },
  { cmd: 'rca', usage: '/rca <servis | @problem:id>', hint: 'Kök neden analizi' },
  { cmd: 'logs', usage: '/logs <ifade>', hint: 'Log araması (alan:"değer" de olur)' },
  { cmd: 'help', usage: '/help', hint: 'Komut listesi (LLM yok)' },
];

export const MENTION_KINDS: { kind: 'trace' | 'problem' | 'env' | 'team' | 'wiki'; insert: string; hint: string }[] = [
  { kind: 'trace', insert: '@trace:', hint: "Trace kimliği (32 hex)" },
  { kind: 'problem', insert: '@problem:', hint: 'Problem kimliği' },
  { kind: 'env', insert: '@env:', hint: 'Ortam' },
  { kind: 'team', insert: '@team:', hint: 'Takım' },
  { kind: 'wiki', insert: '@wiki ', hint: "Yalnız wiki'den cevapla" },
];

const CMD_RE = /^\s*\/(wiki|trace|rca|logs|help)(?=\s|$)/i;
const MENTION_RE = /(^|\s)@([^\s]+)/g;
const SERVICE_SHAPE = /^[a-z0-9][a-z0-9_]*(?:[-.][a-z0-9_]+)+$/;
const TRACE_RE = /^[0-9a-f]{32}$/i;
const TRAIL = /[.,;:!?)\]}'"]+$/;
export const SCOPE_MAX_SERVICES = 5;

export interface ParsedChatInput {
  command?: ChatCommand;
  scope?: ChatScope;
}

/** commandOf — SAF: mesaj başındaki bilinen komut. */
export function commandOf(text: string): ChatCommand | undefined {
  const m = CMD_RE.exec(text);
  return m ? (m[1].toLowerCase() as ChatCommand) : undefined;
}

/** parseChatInput — SAF: metin (+ tamamlamadan seçilen servisler) → kapsam/komut. */
export function parseChatInput(text: string, chosen: readonly string[] = []): ParsedChatInput {
  const command = commandOf(text);
  const scope: ChatScope = {};
  const services: string[] = [];
  const chosenSet = new Set(chosen.map(c => c.toLowerCase()));
  for (const m of text.matchAll(MENTION_RE)) {
    const raw = m[2].replace(TRAIL, '');
    const colon = raw.indexOf(':');
    const head = (colon > 0 ? raw.slice(0, colon) : raw).toLowerCase();
    const val = colon > 0 ? raw.slice(colon + 1) : '';
    if (colon > 0 && (head === 'trace' || head === 'problem' || head === 'env' || head === 'team')) {
      if (!val) continue;
      if (head === 'trace') { if (TRACE_RE.test(val)) scope.trace = val.toLowerCase(); }
      else scope[head] = val;
      continue;
    }
    if (head === 'wiki' && colon < 0) { scope.wiki = true; continue; }
    if (colon >= 0 || !raw) continue;
    if ((chosenSet.has(raw.toLowerCase()) || SERVICE_SHAPE.test(raw)) && !services.includes(raw) && services.length < SCOPE_MAX_SERVICES) {
      services.push(raw);
    }
  }
  if (services.length > 0) scope.services = services;
  const hasScope = Object.keys(scope).length > 0;
  return { ...(command ? { command } : {}), ...(hasScope ? { scope } : {}) };
}

export interface ScopeChip { key: string; label: string; token: string }

/** scopeChips — SAF: composer altındaki "kapsam" çipleri (görsel token'lar). */
export function scopeChips(p: ParsedChatInput): ScopeChip[] {
  const out: ScopeChip[] = [];
  if (p.command) out.push({ key: 'cmd', label: `/${p.command}`, token: `/${p.command}` });
  const s = p.scope;
  if (!s) return out;
  for (const svc of s.services ?? []) out.push({ key: `svc:${svc}`, label: `servis · ${svc}`, token: `@${svc}` });
  if (s.trace) out.push({ key: 'trace', label: `trace · ${s.trace.slice(0, 8)}…`, token: `@trace:${s.trace}` });
  if (s.problem) out.push({ key: 'problem', label: `problem · ${s.problem}`, token: `@problem:${s.problem}` });
  if (s.env) out.push({ key: 'env', label: `env · ${s.env}`, token: `@env:${s.env}` });
  if (s.team) out.push({ key: 'team', label: `takım · ${s.team}`, token: `@team:${s.team}` });
  if (s.wiki) out.push({ key: 'wiki', label: 'yalnız wiki', token: '@wiki' });
  return out;
}

/** removeToken — SAF: çipin ✕'i — token'ı (harf kasası duyarsız) metinden çıkarır. */
export function removeToken(text: string, token: string): string {
  const esc = token.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const re = token.startsWith('/') ? new RegExp(`^\\s*${esc}(?=\\s|$)\\s*`, 'i') : new RegExp(`(^|\\s)${esc}(?=\\s|$|[.,;:!?])`, 'gi');
  return text.replace(re, token.startsWith('/') ? '' : '$1').replace(/\s{2,}/g, ' ').replace(/^\s+/, '');
}
