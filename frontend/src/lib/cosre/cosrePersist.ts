// cosrePersist — v0.10.1618 (CoSRE v2 Faz 2a): v2 UIMessage[] → v1 tel
// şekilleri (SAF). v1'in kaydettiği şey DEĞİŞMEZ: aynı saved_views
// page='ai-chat' blobu, aynı ChatMessage alanları (components/ai/chatPersist.ts
// persistMessages ile birebir kurallar). Kayıt yalnız finish'ten sonra ve
// reset temizliğinden SONRAKİ chat.messages'tan (useCosreChat.ts).
//
// Sunucu kimlikleri: asistan mesajının id'si `start.messageId` (ULID) —
// istemci üretmez. ChatMessage'ta mesaj kimliği alanı yok (v1 şeması); tura
// bağlanan kimlik metadata.turnId / exchangeId → ChatMessage.exchangeId
// (v1 turnArchiveId sırası: turnId önce).
import type { ChatMessage } from '@/lib/types';
import type { CosreUIMessage } from './resetReducer';

/** components/ai/chatPersist.ts PERSIST_MESSAGE_CAP ile eşit (test pinler). */
export const COSRE_PERSIST_CAP = 40;

/** v1 useChatThread history notu ile bayt bayt aynı (test pinler). */
export const STOPPED_NOTE = '\n\n[Bu cevap operatör tarafından YARIDA DURDURULDU — tamamlanmadı.]';

export function messageText(m: CosreUIMessage): string {
  return m.parts.flatMap(p => (p.type === 'text' ? [p.text] : [])).join('\n\n');
}

/** Mesaj kod okundu kilidi taşıyor mu (data-code-lock {locked:true}). v0.10.1650 —
 *  v2 composer'ı konuşma kilidini buradan türetir (v1 codeLockedOf ikizi). */
export function hasCodeLock(m: CosreUIMessage): boolean {
  return m.parts.some(p => p.type === 'data-code-lock' && (p.data as { locked?: unknown })?.locked === true);
}

function failed(m: CosreUIMessage): boolean {
  return m.metadata?.error === true || m.parts.some(p => p.type === 'data-error');
}

const isChatRole = (r: string): r is ChatMessage['role'] => r === 'user' || r === 'assistant';

/** Arşive yazılacak mesajlar: hatalı ve boş metinli düşer, son 40. */
export function toPersistMessages(messages: CosreUIMessage[]): ChatMessage[] {
  const keep = messages
    .filter(m => isChatRole(m.role) && !failed(m) && messageText(m).trim() !== '')
    .map<ChatMessage>(m => {
      const role = m.role as ChatMessage['role'];
      const id = m.metadata?.turnId || m.metadata?.exchangeId;
      const codeRead = hasCodeLock(m);
      return {
        role, text: messageText(m),
        ...(role === 'assistant' && id ? { exchangeId: id } : {}),
        ...(role === 'assistant' && codeRead ? { codeRead: true } : {}),
      };
    });
  return keep.length > COSRE_PERSIST_CAP ? keep.slice(-COSRE_PERSIST_CAP) : keep;
}

/** Kaydetmeye değer mi (v1 hasCompletedExchange): soru + cevap. */
export function hasCompletedExchange(msgs: ChatMessage[]): boolean {
  return msgs.some(m => m.role === 'user') && msgs.some(m => m.role === 'assistant');
}

/** POST /api/cosre/v2/chat gövdesinin geçmişi — v1 ile aynı {role,text}
 *  (hatalı tur elenir, durdurulan tur yarım olduğunu söyler). Bağlam
 *  (context) Faz 2 F6'da. */
export function toRequestMessages(messages: CosreUIMessage[]): ChatMessage[] {
  return messages
    .filter(m => isChatRole(m.role) && !failed(m))
    .map(m => ({
      role: m.role as ChatMessage['role'],
      text: m.metadata?.partial ? `${messageText(m)}${STOPPED_NOTE}` : messageText(m),
    }));
}
