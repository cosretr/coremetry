// convState — v0.10.2325 (docs/cosre/conversation-audit.md §6.1 A, karar 4):
// CoSRE v2 konuşma durumunun SAF görünümü. data-state id'si sabit "state":
// mesajın son geçerli parçası; satır son durum taşıyan asistan mesajından.
// Düzenleme yok (karar 4: önce salt okunur tek satır).
import type { CosreConvState } from '@/lib/types';
import { parseDataState } from '@/lib/cosre/protocol';
import type { CosreUIMessage } from '@/lib/cosre/resetReducer';

type Part = CosreUIMessage['parts'][number];

export function convStateOf(parts: readonly Part[]): CosreConvState | null {
  let out: CosreConvState | null = null;
  for (const p of parts) {
    if (p.type === 'data-state') out = parseDataState(p as { type: string; data?: unknown }) ?? out;
  }
  return out;
}

/** Son durum taşıyan asistan mesajının durumu (yoksa null → satır yok). */
export function latestConvState(messages: readonly CosreUIMessage[]): CosreConvState | null {
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    if (m.role !== 'assistant') continue;
    const s = convStateOf(m.parts);
    if (s) return s;
  }
  return null;
}

/** Satır metni: servis · pencere · ortam (+ kodlar / problem). Boşsa ''. */
export function convStateLine(s: CosreConvState | null): string {
  if (!s) return '';
  const parts: string[] = [];
  const svc = s.services && s.services.length > 1 ? s.services.join(', ') : s.service;
  if (svc) parts.push(svc);
  if (s.window?.label) parts.push(s.window.label);
  if (s.env) parts.push(s.env);
  if (s.channelCode) parts.push(`kanal ${s.channelCode}`);
  if (s.functionCode) parts.push(`fonksiyon ${s.functionCode}`);
  if (s.problem) parts.push(`problem ${s.problem}`);
  if (s.clientIp) parts.push(`istemci ${s.clientIp}`); // v0.10.2356 — S2
  return parts.join(' · ');
}

/** Redis'siz kurulum için geri yollanan yankı (karar 2): sunucu doğrular; Redis varken yok sayar. */
export function withStateEcho(ctx: Record<string, unknown> | undefined, s: CosreConvState | null): Record<string, unknown> | undefined {
  if (!s) return ctx;
  return { ...(ctx ?? {}), state: s };
}
