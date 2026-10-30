// useConversation — v0.10.1664 (CoSRE v2 Faz 2b B6, plan §1.4). v2 sohbetinin
// kalıcı kimliği: v1 useChatThread'in saveNow / load / clear üçlüsünün ikizi,
// AYNI uç ve AYNI blob (saved_views page='ai-chat') → bayrak çevrilince thread
// iki UI'da da açılır.
//   • persist(msgs): useCosreChat onFinish'ten (reset temizliği SONRASI
//     chat.messages; durdurulan tur partial, hatalı tur hiç çağırmaz) TEK yazım.
//     Kimliği SUNUCU basar; yanıttan devralınır, sonraki yazımlar taşır.
//     Ateşle-ve-unut: hata yutulur (sonraki tur tüm geçmişi yeniden yazar).
//   • load(id): arşiv → fromPersistMessages → setMessages; akış sürerken
//     yüklemez (yarım cevabın üstüne başka konuşma basılmaz).
//   • clear(): yeni konuşma — kimlik düşer.
// Kuşak (gen): load/clear her geçişte artar; uçuştaki kaydın yanıtı yeni
// konuşmanın kimliğini ezmez (v1 v0.10.944 kuralı).
import { useCallback, useRef, useState } from 'react';
import { api } from '@/lib/api';
import type { ChatMessage } from '@/lib/types';
import { fromPersistMessages } from '@/lib/cosre/cosrePersist';
import type { CosreUIMessage } from '@/lib/cosre/resetReducer';

export interface CosreConversation {
  conversationId: string | null;
  /** Gönderim anında okunur (bayat closure yok). */
  idRef: { readonly current: string | null };
  persist(msgs: ChatMessage[]): void;
  load(id: string, busy: () => boolean, setMessages: (m: CosreUIMessage[]) => void): Promise<boolean>;
  clear(): void;
}

export function useConversation(): CosreConversation {
  const [conversationId, setConversationId] = useState<string | null>(null);
  const idRef = useRef<string | null>(null);
  const gen = useRef(0);

  const persist = useCallback((msgs: ChatMessage[]) => {
    const g = gen.current;
    api.saveAiConversation({ id: idRef.current ?? undefined, messages: msgs })
      .then(c => {
        if (g !== gen.current) return;
        idRef.current = c.id;
        setConversationId(c.id);
      })
      .catch(() => { /* sessiz — sonraki tur yeniden dener (v1 schedulePersist) */ });
  }, []);

  const load = useCallback(async (id: string, busy: () => boolean, setMessages: (m: CosreUIMessage[]) => void) => {
    if (busy()) return false;
    const c = await api.aiConversation(id);
    if (busy()) return false;
    gen.current++;
    setMessages(fromPersistMessages(c.messages, c.id));
    idRef.current = c.id;
    setConversationId(c.id);
    return true;
  }, []);

  const clear = useCallback(() => {
    gen.current++;
    idRef.current = null;
    setConversationId(null);
  }, []);

  return { conversationId, idRef, persist, load, clear };
}
