// requestContext — v0.10.1650 (CoSRE v2 Faz 2b B2, plan §10). v2 composer'ının
// gönderdiği `context` = v1'in (useChatThread.send → api.copilotChat) AYNI
// kuralla kurduğu nesne: ekran kapsamı (bağlam şeridi çıkarmaları uygulanmış),
// model profili, Derin düşün, Kod hapı + konuşma kod kilidi, @-kapsam / /komut
// (trace bağlamında @ düşer), gönderimin sabit penceresi, açıklama niyeti.
// Kurucu ortak (lib/copilotChatContext). v1'de olup v2'de HENÜZ olmayanlar:
// `conversation` (sunucu kimliği — kalıcılık B6), `wikiRefs` (kaynak
// parçaları B5), açıklama turunun takip bağlamı explain/subject (`?ai=` B7).
import { buildCopilotChatContext } from '@/lib/copilotChatContext';
import { hasDrawerContext, parseChatInput, scopeForRequest } from '@/components/ai/chatScope';
import type { ChatRequestContext } from '@/components/ai/chatContextScope';
import type { ChatSendOpts } from '@/components/ai/useChatThread';

export interface CosreAskSelection {
  /** Seçili model profili; '' = sunucu varsayılanı. */
  profile: string;
  deep: boolean;
  /** Kod hapı açık VE sunucu kod okuyabiliyor (cosreAsk `code`). */
  code: boolean;
}

export function cosreRequestContext(
  screen: ChatRequestContext, sel: CosreAskSelection, text: string, so: ChatSendOpts | undefined, codeLocked: boolean,
): Record<string, unknown> | undefined {
  const o = so?.window ? { ...screen, rangeS: so.window.rangeS, toMs: so.window.toMs } : screen;
  const parsed = scopeForRequest(so?.parsed ?? parseChatInput(text.trim(), so?.chosen ?? []), hasDrawerContext(o));
  return buildCopilotChatContext({
    service: o.service || undefined,
    operation: o.operation || undefined,
    rangeS: o.rangeS || undefined,
    trace: o.trace || undefined,
    env: o.env || undefined,
    toMs: o.toMs || undefined,
    profile: sel.profile || undefined,
    page: o.page || undefined,
    pinnedPage: o.pinnedPage || undefined,
    scope: parsed.scope,
    command: parsed.command,
    deep: sel.deep || undefined,
    attempt: so?.attempt,
    explainAsk: so?.explainAsk,
    code: (sel.code || codeLocked) || undefined,
    codeLocked: codeLocked || undefined,
  });
}
