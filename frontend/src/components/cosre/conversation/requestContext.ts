// requestContext — v0.10.1650 (CoSRE v2 Faz 2b B2, plan §10). v2 composer'ının
// gönderdiği `context` = v1'in (useChatThread.send → api.copilotChat) AYNI
// kuralla kurduğu nesne: ekran kapsamı (bağlam şeridi çıkarmaları uygulanmış),
// model profili, Derin düşün, Kod hapı + konuşma kod kilidi, @-kapsam / /komut
// (trace bağlamında @ düşer), gönderimin sabit penceresi, açıklama niyeti.
// Kurucu ortak (lib/copilotChatContext). v0.10.1672 (B7) — v1 paritesi tamam:
// `conversation` (sunucu kimliği), `wikiRefs` (önceki cevabın wiki sayfaları),
// açıklama turunun takip bağlamı explain / subject (+ trace / page / env /
// pencere — ai/useExplainContext, çağıran ekran kapsamının üstüne biner).
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

/** v0.10.1672 (B7) — konuşmaya bağlı alanlar (v1 useChatThread send ile aynı). */
export interface CosreThreadFields {
  conversation?: string | null;
  wikiRefs?: string[];
  /** Son açıklama turunun takip bağlamı (ai/useExplainContext.resolve). */
  explain?: string;
  subject?: string;
}

export function cosreRequestContext(
  screen: ChatRequestContext, sel: CosreAskSelection, text: string, so: ChatSendOpts | undefined, codeLocked: boolean,
  thread: CosreThreadFields = {},
): Record<string, unknown> | undefined {
  const o = so?.window ? { ...screen, rangeS: so.window.rangeS, toMs: so.window.toMs } : screen;
  const parsed = scopeForRequest(so?.parsed ?? parseChatInput(text.trim(), so?.chosen ?? []),
    hasDrawerContext({ ...o, explain: thread.explain, subject: thread.subject }));
  return buildCopilotChatContext({
    explain: thread.explain || undefined,
    subject: thread.subject || undefined,
    conversation: thread.conversation || undefined,
    wikiRefs: thread.wikiRefs?.length ? thread.wikiRefs : undefined,
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
