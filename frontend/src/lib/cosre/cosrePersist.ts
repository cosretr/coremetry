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
import type { ChatMessage, ChatQueryEvidence } from '@/lib/types';
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

/** data-evidence-summary'nin kanıt listesi (yoksa boş). */
function evidenceList(m: CosreUIMessage): ChatQueryEvidence[] {
  for (const p of m.parts) {
    if (p.type !== 'data-evidence-summary') continue;
    const list = (p.data as { evidence?: unknown } | undefined)?.evidence;
    return Array.isArray(list) ? (list as ChatQueryEvidence[]) : [];
  }
  return [];
}

/** Arşive yazılacak mesajlar: hatalı ve boş metinli düşer, son 40.
 *  v0.10.1664 (B6) — v1 persistMessages ile ALAN ALAN eşit: kanıt çipleri
 *  (data-evidence-summary → evidence), açıklama künyesi (metadata.explain),
 *  tur kimliği (turnId → exchangeId → archiveExchangeId; Geçmiş'ten açılan
 *  tur yeniden kayıtta kimliğini kaybetmez), kod kilidi. Bloklar ASLA
 *  istemciden gitmez (v1 kuralı; sunucu zarftan doldurur). */
export function toPersistMessages(messages: CosreUIMessage[]): ChatMessage[] {
  const keep = messages
    .filter(m => isChatRole(m.role) && !failed(m) && messageText(m).trim() !== '')
    .map<ChatMessage>(m => {
      const role = m.role as ChatMessage['role'];
      if (role !== 'assistant') return { role, text: messageText(m) };
      const md = m.metadata;
      const id = md?.turnId || md?.exchangeId || md?.archiveExchangeId;
      const ev = evidenceList(m);
      return {
        role, text: messageText(m),
        ...(ev.length ? { evidence: ev } : {}),
        ...(md?.explain ? { explain: md.explain } : {}),
        ...(id ? { exchangeId: id } : {}),
        ...(hasCodeLock(m) ? { codeRead: true } : {}),
      };
    });
  return keep.length > COSRE_PERSIST_CAP ? keep.slice(-COSRE_PERSIST_CAP) : keep;
}

/**
 * fromPersistMessages — v0.10.1664 (CoSRE v2 Faz 2b B6, plan §1.4): arşiv
 * (GET /api/ai/conversations/{id}) → v2 mesajları; v1 restoreTurns'ün ikizi.
 * Asistan: metin → text parçası; blocks (yalnız sunucu doldurur) → data-block-*
 * (canlı kart çizicileri); evidence → data-evidence-summary; codeRead →
 * data-code-lock; exchangeId → metadata.archiveExchangeId (canlı exchangeId
 * DEĞİL: arşivde sinyal düğmeleri kapalı — "ölü affordance" dersi); explain →
 * metadata.explain. Kimlikler konuşma + sıra (`<convId>:<i>`), tekil.
 */
export function fromPersistMessages(messages: ChatMessage[] | undefined, convId: string): CosreUIMessage[] {
  if (!messages?.length) return [];
  return messages
    .filter(m => (m.role === 'user' || m.role === 'assistant') && (m.text ?? '').trim() !== '')
    .map<CosreUIMessage>((m, i) => {
      const id = `${convId}:${i}`;
      const text = { type: 'text' as const, text: m.text ?? '', state: 'done' as const };
      if (m.role === 'user') return { id, role: 'user', parts: [text] };
      const parts: CosreUIMessage['parts'] = [text];
      for (const b of m.blocks ?? []) {
        parts.push({ type: `data-block-${b.type}`, id: b.id, data: { seq: b.seq, final: b.final, payload: b.payload } });
      }
      if (m.evidence?.length) parts.push({ type: 'data-evidence-summary', id: 'evidence', data: { evidence: m.evidence } });
      if (m.codeRead) parts.push({ type: 'data-code-lock', id: 'code-lock', data: { locked: true } });
      const metadata: NonNullable<CosreUIMessage['metadata']> = {
        ...(m.exchangeId ? { archiveExchangeId: m.exchangeId } : {}),
        ...(m.explain ? { explain: m.explain } : {}),
        ...(m.blocks?.length && m.blocksAt ? { blocksAt: m.blocksAt } : {}),
        ...(m.blocksDropped?.length ? { blocksDropped: m.blocksDropped } : {}),
      };
      return { id, role: 'assistant', parts, ...(Object.keys(metadata).length ? { metadata } : {}) };
    });
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
