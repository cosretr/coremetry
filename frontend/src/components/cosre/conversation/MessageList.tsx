// MessageList — v0.10.1650 (CoSRE v2 Faz 2b B2, plan §3 / §10). v2'nin CEVAP
// yolu: AI SDK mesajları v1 balonlarının sınıflarıyla (cm-msg --user / --ai)
// çizilir — operatör aynı sohbeti görür, yalnız akış farklıdır.
//   • Kullanıcı metni: v1 ChatMarkdown (güvensiz metin kuralı, ```chart kod kalır).
//   • Asistan metni: visibleParts(m) — akışta reset edilen taslak GİZLİ, bitince
//     kalıcı silinmiş (2a çekirdeği); her text parçası ChatAnswerMarkdown
//     (= renderMessage motoru), akış imleci yalnız son metinde.
//   • Durdurma: metadata.partial → v1'in "yarım kaldı" notu. Hata: chat.error →
//     chatErrorText (v1 dili).
//   • Adımlar / kanıt / veri blokları B3–B5: bilinmeyen parça ÇİZİLMEZ (çökmez).
// Sunumsal: Chat örneği yok (katalog ve testler statik mesajla da çizer).
import { ChatAnswerMarkdown, ChatMarkdown } from '@/components/ai/ChatBubble';
import { AiThinking } from '@/components/ai/AiThinking';
import { chatErrorText } from '@/components/ai/chatErrorText';
import { messageText } from '@/lib/cosre/cosrePersist';
import type { CosreUIMessage } from '@/lib/cosre/resetReducer';
import { COSRE_V2_STRINGS as T } from './strings';

/** useChat durumu (ai ChatStatus ile aynı küme; tip importu katalogdan bağımsız). */
export type CosreChatStatus = 'submitted' | 'streaming' | 'ready' | 'error';
/** v2 sohbet mesajı (katalog / testler lib/cosre'yi doğrudan anmasın diye). */
export type CosreChatMessage = CosreUIMessage;
type Part = CosreUIMessage['parts'][number];

export interface MessageListProps {
  messages: readonly CosreUIMessage[];
  visibleParts: (m: CosreUIMessage) => Part[];
  status: CosreChatStatus;
  error?: Error;
}

export function MessageList({ messages, visibleParts, status, error }: MessageListProps) {
  const busy = status === 'submitted' || status === 'streaming';
  const last = messages.at(-1);
  return (
    <>
      {messages.map((m, i) => {
        if (m.role === 'user') return <UserMessage key={m.id} m={m} />;
        if (m.role !== 'assistant') return null;
        const isLast = i === messages.length - 1;
        return <AssistantMessage key={m.id} m={m} parts={visibleParts(m)} live={isLast && busy} streaming={isLast && status === 'streaming'} />;
      })}
      {/* İlk parça gelmeden (yalnız soru gitti): v1 bekleyiş göstergesi. */}
      {busy && last?.role !== 'assistant' && (
        <div className="cm-msg cm-msg--ai" data-cosre-thinking=""><AiThinking /></div>
      )}
      {status === 'error' && error && <ErrorLine error={error} />}
    </>
  );
}

function UserMessage({ m }: { m: CosreUIMessage }) {
  return (
    <div className="cm-msg cm-msg--user">
      <div className="cm-msg-user"><ChatMarkdown text={messageText(m)} /></div>
    </div>
  );
}

function AssistantMessage({ m, parts, live, streaming }: { m: CosreUIMessage; parts: Part[]; live: boolean; streaming: boolean }) {
  const texts = parts.flatMap(p => (p.type === 'text' && p.text !== '' ? [p.text] : []));
  if (texts.length === 0) {
    return live ? <div className="cm-msg cm-msg--ai" data-cosre-thinking=""><AiThinking /></div> : null;
  }
  return (
    <div className="cm-msg cm-msg--ai" data-cosre-message={m.id}>
      <div className="cm-msg-ai__who" aria-hidden="true">
        <img src="/favicon.svg" width={14} height={14} alt="" draggable={false} /> {T.title}
      </div>
      {texts.map((t, j) => (
        <ChatAnswerMarkdown key={j} text={t} streaming={streaming && j === texts.length - 1} allowedLinks={m.metadata?.allowedLinks} />
      ))}
      {m.metadata?.partial && !live && (
        <div className="cm-msg-note" data-cosre-partial="">{T.stoppedLead} <b>{T.stoppedWord}</b> {T.stoppedTail}</div>
      )}
    </div>
  );
}

function ErrorLine({ error }: { error: Error }) {
  const ev = chatErrorText(error.message);
  return (
    <div className="cm-msg-note text-destructive" role="alert" title={ev.raw ?? undefined}>⚠ {ev.text}</div>
  );
}
