// copilotChatContext — v0.10.1648 (CoSRE v2 Faz 2b B2a, plan §10). Sohbet
// isteğinin `context` nesnesi; api.copilotChat'in gövdesinden SATIR SATIR
// çıkarıldı (anahtar sırası ve "yokken anahtar yok" kuralı aynı → v1 gövdesi
// bayt bayt eski). İkinci tüketici v2 (POST /api/cosre/v2/chat, aynı sunucu
// ayrıştırıcısı copilotChatWith): composer'ın gönderdiği kapsam iki yolda TEK.
import type { ChatAttempt, ChatCommand, ChatExplainAsk, ChatScope, PageContext } from './types';
import { browserTz } from './browserTz';

export interface CopilotChatContextFields {
  service?: string;
  operation?: string;
  explain?: string;
  subject?: string;
  rangeS?: number;
  trace?: string;
  env?: string;
  toMs?: number;
  profile?: string;
  conversation?: string;
  page?: PageContext;
  pinnedPage?: PageContext;
  wikiRefs?: string[];
  scope?: ChatScope;
  command?: ChatCommand;
  deep?: boolean;
  attempt?: ChatAttempt;
  explainAsk?: ChatExplainAsk;
  code?: boolean;
  codeLocked?: boolean;
}

/** `context` gövdesi; hiçbir alan yoksa (ve dilim UTC + IANA adı yoksa) undefined. */
export function buildCopilotChatContext(f: CopilotChatContextFields, now?: Date): Record<string, unknown> | undefined {
  // v0.10.437 (D6) — tarayıcı saat dilimi: mutlak tarih/saat soruları
  // ("08/08/2026 04-08 arası") operatörün yerel saatinde yorumlanır.
  // v0.10.445 — IANA adı da gider (sabit ofset DST'yi bilmez).
  // v0.10.745 — çift lib/browserTz'den; Explain/insight uçlarıyla aynı kaynak.
  const { tz, tzOffsetMin } = browserTz(now);
  return f.service || f.operation || f.explain || f.subject || f.rangeS || f.trace || f.env || f.toMs || f.profile || f.conversation || f.page || f.pinnedPage || (f.wikiRefs && f.wikiRefs.length > 0) || f.scope || f.command || f.deep || f.attempt || f.explainAsk || f.code || f.codeLocked || tzOffsetMin !== 0 || tz
    ? {
        ...(tzOffsetMin !== 0 ? { tzOffsetMin } : {}),
        ...(tz ? { tz } : {}),
        ...(f.service ? { service: f.service } : {}),
        ...(f.operation ? { operation: f.operation } : {}),
        ...(f.explain ? { explain: f.explain } : {}),
        ...(f.subject ? { subject: f.subject } : {}),
        ...(f.rangeS && f.rangeS > 0 ? { rangeS: f.rangeS } : {}),
        ...(f.trace ? { trace: f.trace } : {}),
        ...(f.env ? { env: f.env } : {}),
        ...(f.toMs && f.toMs > 0 ? { toMs: f.toMs } : {}),
        ...(f.profile ? { profile: f.profile } : {}),
        ...(f.conversation ? { conversation: f.conversation } : {}),
        ...(f.page ? { page: f.page } : {}),
        ...(f.pinnedPage ? { pinnedPage: f.pinnedPage } : {}),
        ...(f.wikiRefs && f.wikiRefs.length > 0 ? { wikiRefs: f.wikiRefs } : {}),
        ...(f.scope ? { scope: f.scope } : {}),
        ...(f.command ? { command: f.command } : {}),
        ...(f.deep ? { deep: true } : {}),
        ...(f.attempt ? { attempt: f.attempt } : {}),
        ...(f.explainAsk ? { explainAsk: f.explainAsk } : {}),
        ...(f.code ? { code: true } : {}),
        ...(f.codeLocked ? { codeLocked: true } : {}),
      }
    : undefined;
}
