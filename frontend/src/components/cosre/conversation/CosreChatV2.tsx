// CosreChatV2 — v0.10.1647 (CoSRE v2 Faz 2b B1, plan §1.2 / §4). v2 sohbetinin
// kapsayıcısı; kabuk kapısı (CopilotChatGate) bunu YALNIZ dinamik import() ile
// çeker (ayrı tembel chunk) ve bayrak açıkken ilk açılma niyetinde mount eder.
// Bir kez mount olunca mount kalır (v1 deseni: taslak/thread gezinmede korunur).
//
// v0.10.1650 (B2, plan §10 bağlayıcı revizyon): v2, v1'in açılış ekranını ve
// composer'ını AYNEN kullanır — CosreWelcome (selam + hata özeti, takım /
// servis / operasyon seçicileri, başlangıç çipleri, "Bu trace'i açıkla"),
// CosreComposerForm (Aa / Derin / Kod, model seçici, `@` kapsam + `/` komut,
// Bağlam çipleri), CosreHeaderContext (pin) — aynı kancalarla (ai/cosreAsk).
// Yalnız cevap yolu farklı: gönderim useCosreChat → POST /api/cosre/v2/chat,
// gövdenin `context`i v1 kuralıyla (requestContext); cevap MessageList
// (renderMessage motoru, visibleParts ile reset gizleme). Sohbet state'i bu
// her-zaman-mount bileşende: Sheet kapanınca içerik söker, konuşma kalır.
//
// Açılış yolları (v1 ile aynı, Q1 hariç):
//   • FAB (kapalıyken bu bileşen çizer; kritik-problem rozeti aynı RQ anahtarı)
//   • `?chat=<id>` → açılır. Parametre YAZILMAZ/silinmez: bayrak kapatılınca v1
//     aynı adresle aynı thread'i yükler (iki gerçekleme tek kalıcılık, §1.4).
//     Thread'in v2'ye yüklenmesi B6.
//   • `coremetry:ai-ask` → açılır + soruyu gönderir; AI_PREFILL_EVENT → açılır +
//     composer'ı doldurur (GÖNDERMEZ) — v1 köprüleriyle aynı.
//   • `?ai=` açıklama turu → v1'in (kapı yanına `launcher={false}` mount eder);
//     v2 kendini KAPATIR. `yieldToV1` iken (v1 açıklama örneği mount) gelen
//     ai-ask/prefill'i de v1 kendi dinleyicisiyle alır → v2 kapanır, iki panel
//     üst üste binmez. B7 bu dalı siler.
import { useEffect, useMemo, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useOpenCriticalCount } from '@/lib/queries';
import { AI_PARAM } from '@/lib/aiSubject';
import { getRaw } from '@/lib/storage';
import type { ChatExplainAsk } from '@/lib/types';
import { COSRE_DRAWER_WIDTH_KEY, cosreSheetWidth } from '@/lib/cosre/sheetWidth';
import { useCosreChat } from '@/lib/cosre/useCosreChat';
import { hasCodeLock } from '@/lib/cosre/cosrePersist';
import { AI_PREFILL_EVENT, type AIPrefillDetail } from '@/components/ai/aiEvents';
import { AI_DRAWER_WIDTH } from '@/components/ai/answerCard';
import { CopilotFab } from '@/components/ai/CopilotFab';
import { TraceExplainNudge } from '@/components/ai/TraceExplainNudge';
import { useCosreAskContext, useCosreComposerInput, useCosreWelcomeData } from '@/components/ai/cosreAsk';
import { useStickToBottomState } from '@/components/ai/stickToBottom';
import { explainAskFor, explainAskSubject, explainTurnSendOpts } from '@/components/ai/explainTurn';
import type { ChatSendOpts } from '@/components/ai/useChatThread';
import { CosreComposerForm, CosreHeaderContext, CosreWelcome } from '@/components/CopilotChat';
import { Button } from '@/components/shadcn/button';
import { CosreSheet } from './CosreSheet';
import { MessageList } from './MessageList';
import { cosreRequestContext } from './requestContext';
import { COSRE_V2_STRINGS as T } from './strings';

export interface CosreChatV2Props {
  defaultOpen?: boolean;
  /** v1 açıklama örneği (Q1-a) mount mu: pencere olaylarını v1 alır. */
  yieldToV1?: boolean;
}

export function CosreChatV2({ defaultOpen = false, yieldToV1 = false }: CosreChatV2Props) {
  const [open, setOpen] = useState(defaultOpen);
  const [sp] = useSearchParams();

  // Adres niyetleri render sırasında (türetilmiş state; kapı deseni).
  const aiParam = sp.has(AI_PARAM);
  const chatParam = sp.get('chat') ?? '';
  const [seenAi, setSeenAi] = useState(false);
  const [seenChat, setSeenChat] = useState('');
  if (aiParam !== seenAi) {
    setSeenAi(aiParam);
    if (aiParam && open) setOpen(false);
  }
  if (chatParam !== seenChat) {
    setSeenChat(chatParam);
    if (chatParam && !aiParam && !open) setOpen(true);
  }

  // v1 ile ORTAK kancalar (ai/cosreAsk): bağlam, composer girdisi, karşılama.
  const ask = useCosreAskContext();
  const { messages, status, error, sendMessage, stop, setMessages, visibleParts } = useCosreChat();
  const busy = status === 'submitted' || status === 'streaming';
  const codeLocked = useMemo(() => messages.some(hasCodeLock), [messages]);
  // Konuşma kimliği B6'ya dek yok (kalıcılık) → taslak "yeni konuşma" yuvasında.
  const composer = useCosreComposerInput(null, ask.page, ask.chipTrace);
  const welcome = useCosreWelcomeData(ask.enabled === true && open && messages.length === 0, ask.env);
  const scrollRef = useRef<HTMLDivElement>(null);
  const { pin: pinBottom, atBottom } = useStickToBottomState(scrollRef, [messages, open]);

  // Gönderim: v1 send'in v2 karşılığı. Akarken gelen soru: akışı durdur, sonra
  // gönder (v1 "durdur ve gönder"). Güncel değerler ref'ten (dinleyiciler bayat
  // closure çağırmasın — v1 useChatThread deseni).
  const live = useRef({ ask, codeLocked, busy, sendMessage, stop, pinBottom, prefill: composer.prefill });
  live.current = { ask, codeLocked, busy, sendMessage, stop, pinBottom, prefill: composer.prefill };
  const send = (text: string, so?: ChatSendOpts) => {
    const c = live.current;
    const q = text.trim();
    if (!q) return;
    const context = cosreRequestContext(c.ask.reqCtx, { profile: c.ask.profile, deep: c.ask.deep, code: c.ask.code }, q, so, c.codeLocked);
    const go = () => c.sendMessage({ text: q }, context ? { body: { context } } : undefined);
    if (c.busy) void c.stop().then(go);
    else void go();
  };
  const sendRef = useRef(send);
  sendRef.current = send;

  const submit = (text: string) => {
    const picked = composer.chosen;
    composer.setInput(''); composer.setChosen([]); pinBottom();
    send(text, { chosen: picked });
  };
  // Takip / başlangıç çipi: composer seçimleri taşınmaz, çipin kapsamı gider (v1 askChip).
  const askChip = (text: string, so?: ChatSendOpts) => {
    composer.setInput(''); composer.setChosen([]); pinBottom();
    send(text, so);
  };
  // "Bu trace'i açıkla" (bağlam çipi): v1 ile aynı niyet (context.explainAsk).
  const askExplain = (a: ChatExplainAsk) => {
    const x = explainAskFor(explainAskSubject(a), a.src, undefined, undefined, a.panel);
    if (x) askChip(x.userText, explainTurnSendOpts(x));
  };
  // Temizle: v1 startNew — akış durur, mesajlar + pin + bağlam çıkarmaları düşer,
  // yeni konuşmanın taslağı yüklenir.
  const clear = () => {
    if (busy) void stop();
    setMessages([]);
    ask.setPinned(null);
    ask.setCtxExcluded(new Set());
    composer.expectDraft(null);
  };

  useEffect(() => {
    const onAsk = (e: Event) => {
      if (yieldToV1) { setOpen(false); return; }
      setOpen(true);
      const q = (e as CustomEvent<{ question?: string }>).detail?.question;
      if (q) { live.current.pinBottom(); sendRef.current(q); }
    };
    const onPrefill = (e: Event) => {
      if (yieldToV1) { setOpen(false); return; }
      setOpen(true);
      const q = (e as CustomEvent<AIPrefillDetail>).detail?.question;
      if (q) live.current.prefill(q, q.length);
    };
    window.addEventListener('coremetry:ai-ask', onAsk);
    window.addEventListener(AI_PREFILL_EVENT, onPrefill);
    return () => {
      window.removeEventListener('coremetry:ai-ask', onAsk);
      window.removeEventListener(AI_PREFILL_EVENT, onPrefill);
    };
  }, [yieldToV1]);

  // Rozet: kapalıyken (FAB görünürken) poll; RQ gizli sekmede durur.
  const criticalOpen = useOpenCriticalCount({ enabled: !open }).data ?? 0;
  // Genişlik her açılışta okunur (v1'de sürüklenip saklanmış olabilir).
  const width = useMemo(
    () => (open ? cosreSheetWidth(getRaw(COSRE_DRAWER_WIDTH_KEY), AI_DRAWER_WIDTH, window.innerWidth) : AI_DRAWER_WIDTH),
    [open],
  );

  return (
    <>
      {!open && <TraceExplainNudge />}
      {!open && <CopilotFab criticalOpen={criticalOpen} onClick={() => setOpen(true)} />}
      <CosreSheet
        open={open}
        onOpenChange={setOpen}
        width={width}
        headerContext={<CosreHeaderContext currentService={ask.currentService} env={ask.env} pinned={ask.pinned} setPinned={ask.setPinned} page={ask.page} />}
        headerActions={<>
          <Link to="/cosre" className="cosre-page__home cm-fullpage" title={T.fullPageTitle}>{T.fullPage}</Link>
          {messages.length > 0 && (
            <Button variant="secondary" size="sm" onClick={clear} title={T.clearTitle}>{T.clear}</Button>
          )}
        </>}>
        <div className="cosre-root cosre-root--drawer" data-cosre-body="">
          <div className="cm-thread-wrap">
            <div ref={scrollRef} className="cm-thread flex-1 overflow-y-auto" role="log" aria-live="polite" aria-busy={busy} aria-label={T.threadLabel}>
              <div className="cm-thread__col">
                {messages.length === 0 ? (
                  <CosreWelcome firstName={ask.user?.firstName} env={ask.env} data={welcome} wiki={ask.wikiOn} ctxStarter={ask.ctxStarter}
                    onPrefill={composer.prefill} onAsk={askChip} onAskExplain={askExplain} onSubmit={submit} />
                ) : (
                  <MessageList messages={messages} visibleParts={visibleParts} status={status} error={error} />
                )}
              </div>
            </div>
            {!atBottom && messages.length > 0 && (
              <Button variant="secondary" size="sm" className="cm-jump" onClick={pinBottom}
                title={T.jumpBottomTitle} aria-label={T.jumpBottomLabel}>{T.jumpBottom}</Button>
            )}
          </div>
          <CosreComposerForm input={composer} ask={ask} busy={busy} stop={() => void stop()} codeLocked={codeLocked}
            ctxItems={ask.baseCtxItems} onRemoveCtx={k => ask.setCtxExcluded(prev => new Set(prev).add(k))} submit={submit} />
        </div>
      </CosreSheet>
    </>
  );
}
