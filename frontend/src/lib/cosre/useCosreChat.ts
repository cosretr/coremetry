// useCosreChat — v0.10.1618 (CoSRE v2 Faz 2a): AI SDK (`ai` 5.0.276 +
// `@ai-sdk/react` 2.0.279) üstünde v2 sohbet çekirdeği. UI YOK — Faz 2b'nin
// bileşenleri bunu kullanır; bugün uygulamadan import EDİLMEZ (v1 ekranı ve
// giriş chunk'ı değişmez; scripts/bundle-budget.mjs `ai` / `@ai-sdk`'yı ilk
// yükte yasaklar).
//
// Akış: DefaultChatTransport → POST /api/cosre/v2/chat (bayrak cosre_v2,
// kapalıyken 404) → textIdMap sarmalayıcısı → Chat.
//   onData   : yalnız data-reset → resets kümesine (messageId, sıra). Mesaj
//              DEĞİŞMEZ.
//   onError  : temizlik (idempotent; onFinish ardından yine koşar).
//   onFinish : temizlik → isAbort ise partial, isError ise error → kayıt.
//              Kayıt chat.messages'tan (temizlik SONRASI) okunur; onFinish'in
//              `message` argümanı SDK'nın iç kopyası (taslak dahil) — kullanılmaz.
import { useEffect, useRef, useState } from 'react';
import { Chat, useChat } from '@ai-sdk/react';
import { DefaultChatTransport, type ChatTransport } from 'ai';
import type { ChatMessage } from '@/lib/types';
import { parseDataReset } from './protocol';
import { createTextIdMap } from './textIdMap';
import { applyResets, markFinished, recordReset, visibleParts, type CosreUIMessage, type ResetSet } from './resetReducer';
import { hasCompletedExchange, toPersistMessages, toRequestMessages } from './cosrePersist';

export const COSRE_V2_CHAT_API = '/api/cosre/v2/chat';

export interface CosreChatOptions {
  /** Test dikişi; üretimde global fetch. */
  fetch?: typeof globalThis.fetch;
  /** Bitmiş (hatasız) alışverişten sonra arşivlenecek v1 mesajları
   *  (saved_views page='ai-chat'; api.saveAiConversation'a bağlamak 2b/F9). */
  onPersist?: (messages: ChatMessage[]) => void;
}

export interface CosreChatCore {
  chat: Chat<CosreUIMessage>;
  /** Render yardımcısı: akış sırasında reset edilen text parçaları gizli. */
  visibleParts: (m: CosreUIMessage) => CosreUIMessage['parts'];
}

export function createCosreChat(opts: CosreChatOptions = {}): CosreChatCore {
  const map = createTextIdMap();
  const resets: ResetSet = new Map();
  const inner = new DefaultChatTransport<CosreUIMessage>({
    api: COSRE_V2_CHAT_API,
    fetch: opts.fetch,
    prepareSendMessagesRequest: ({ messages }) => ({ body: { messages: toRequestMessages(messages) } }),
  });
  const transport: ChatTransport<CosreUIMessage> = {
    sendMessages: async o => map.wrap(await inner.sendMessages(o)),
    reconnectToStream: async () => null, // v2 ucu akış devamını desteklemez
  };
  // `settle` ve geri çağrılar `chat`e yalnız istek sırasında (kuruluştan
  // sonra) dokunur — TDZ'ye düşmez.
  const settle = () => {
    const next = applyResets(chat.messages, resets);
    if (next !== chat.messages) chat.messages = next;
  };
  const chat: Chat<CosreUIMessage> = new Chat<CosreUIMessage>({
    transport,
    onData: part => {
      const r = parseDataReset(part);
      if (!r) return;
      const at = map.resolve(r.data.textId);
      if (at) recordReset(resets, at.messageId, at.ordinal);
    },
    onError: settle,
    onFinish: ({ message, isAbort, isError }) => {
      settle();
      const flagged = markFinished(chat.messages, message.id, { partial: isAbort, error: isError });
      if (flagged !== chat.messages) chat.messages = flagged;
      if (isError || !opts.onPersist) return;
      const msgs = toPersistMessages(chat.messages);
      if (hasCompletedExchange(msgs)) opts.onPersist(msgs);
    },
  });
  return { chat, visibleParts: m => visibleParts(m, resets) };
}

/** React kancası — Chat örneği bileşen ömrü boyunca tek; onPersist güncel
 *  render'ınki (ref), fetch ilk render'ınki. */
export function useCosreChat(opts: CosreChatOptions = {}) {
  const persistRef = useRef(opts.onPersist);
  useEffect(() => { persistRef.current = opts.onPersist; });
  const [core] = useState(() => createCosreChat({ fetch: opts.fetch, onPersist: m => persistRef.current?.(m) }));
  const helpers = useChat<CosreUIMessage>({ chat: core.chat });
  return { ...helpers, visibleParts: core.visibleParts };
}
