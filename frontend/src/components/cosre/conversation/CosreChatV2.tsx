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
// v0.10.1660 (B5) — takip çipleri askChip'e, "↺ Yeniden dene" SDK regenerate'ine,
// cevabın `open` yönergesi bir kez SPA navigasyonuna bağlı.
//
// v0.10.1672 (B7) — bağlam paritesi: gövdeye `conversation`, `wikiRefs` ve son
// açıklama turunun takip bağlamı (ai/useExplainContext — v1 ile AYNI kanca:
// explain / subject / trace / page / env / pencere; bağlam şeridinde "✨ trace"
// çipi, canlı TraceContextStrip). Kullanıcı mesajı niyeti metadata'da taşır
// (explainAsk fresh'siz + pencere) → bekleyen cevap baştan açıklama balonu,
// "↺ Yeniden dene" aynı niyet ve pencereyle.
//
// Açılış yolları (v1 ile aynı):
//   • FAB (kapalıyken bu bileşen çizer; kritik-problem rozeti aynı RQ anahtarı)
//   • `?chat=<id>` → açılır ve o konuşma YÜKLENİR (v0.10.1664 B6); açık
//     thread ⇔ `?chat=` (lib/chatUrl.syncChatParam, v1 ile aynı saf çekirdek,
//     replace:true). Kayıt finish'ten sonra chat.messages'tan (useConversation):
//     aynı uç + aynı blob → bayrak çevrilince thread iki UI'da da açılır.
//     Başlıkta 🕘 Geçmiş (v1 paneli, CopilotChat ChatHistoryPanel); sol kenar tutamağı
//     (SheetResizeHandle) v1 ile aynı genişlik anahtarı.
//   • `coremetry:ai-ask` → açılır + soruyu gönderir; AI_PREFILL_EVENT → açılır +
//     composer'ı doldurur (GÖNDERMEZ) — v1 köprüleriyle aynı.
//   • `?ai=<özne>` (✨ Explain düğmeleri, nudge, derin linkler) → v0.10.1672
//     (B7) v1 ile aynı TEK SEFERLİK niyet: Sheet açılır, açıklama SOHBET TURU
//     olarak gider (context.explainAsk), `?ai=` (+ `?aicode=`) hemen düşer.
import { useEffect, useMemo, useRef, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { mergeOpenHref } from '@/lib/openHref';
import { isSafeAppPath } from '@/lib/safeAppPath';
import { useOpenCriticalCount } from '@/lib/queries';
import { AI_CODE_PARAM, AI_SRC_PARAM, formatAiParam } from '@/lib/aiSubject';
import { takeMetricPanelAsk } from '@/lib/pageContextOverlay';
import { toChatTurns } from '@/lib/cosre/toChatTurn';
import { useAiSubject } from '@/components/ai/useAiSubject';
import { useExplainContext } from '@/components/ai/useExplainContext';
import { prevWikiRefs } from '@/components/ai/chatWikiRefs';
import { TraceContextStrip } from '@/components/ai/TraceContextStrip';
import { getRaw } from '@/lib/storage';
import { syncChatParam } from '@/lib/chatUrl';
import type { AiConversationSummary, ChatTurn } from '@/lib/types';
import { useChatHistory } from '@/components/ai/useChatHistory';
import { cosreFullPageHref } from '@/components/ai/drawerFullPage';
import { useConversation } from './useConversation';
import { SheetResizeHandle } from './SheetResizeHandle';
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
import { explainAskFor, explainAskSubject, explainTurnSendOpts, withoutFresh } from '@/components/ai/explainTurn';
import type { ChatSendOpts } from '@/components/ai/useChatThread';
import { ChatHistoryPanel, CosreComposerForm, CosreHeaderContext, CosreWelcome, deleteConversation } from '@/components/CopilotChat';
import { Button } from '@/components/shadcn/button';
import { CosreSheet } from './CosreSheet';
import { MessageList } from './MessageList';
import { cosreRequestContext } from './requestContext';
import { COSRE_V2_STRINGS as T } from './strings';

export interface CosreChatV2Props {
  defaultOpen?: boolean;
}

export function CosreChatV2({ defaultOpen = false }: CosreChatV2Props) {
  const [open, setOpen] = useState(defaultOpen);
  const [sp] = useSearchParams();

  // Adres niyeti render sırasında (türetilmiş state; kapı deseni).
  const chatParam = sp.get('chat') ?? '';
  const [seenChat, setSeenChat] = useState('');
  if (chatParam !== seenChat) {
    setSeenChat(chatParam);
    if (chatParam && !open) setOpen(true);
  }

  // v1 ile ORTAK kancalar (ai/cosreAsk): bağlam, composer girdisi, karşılama.
  const ask = useCosreAskContext();
  const conv = useConversation();
  const { messages, status, error, sendMessage, stop, setMessages, regenerate, visibleParts } = useCosreChat({ onPersist: conv.persist });
  const busy = status === 'submitted' || status === 'streaming';
  const busyRef = useRef(busy);
  busyRef.current = busy;
  const codeLocked = useMemo(() => messages.some(hasCodeLock), [messages]);
  // v0.10.1664 (B6) — taslak konuşmanın yuvasında (v1 ile aynı anahtar).
  const composer = useCosreComposerInput(conv.conversationId, ask.page, ask.chipTrace);
  const welcome = useCosreWelcomeData(ask.enabled === true && open && messages.length === 0, ask.env);
  const scrollRef = useRef<HTMLDivElement>(null);
  const { pin: pinBottom, atBottom } = useStickToBottomState(scrollRef, [messages, open]);

  // Gönderim: v1 send'in v2 karşılığı. Akarken gelen soru: akışı durdur, sonra
  // gönder (v1 "durdur ve gönder"). Güncel değerler ref'ten (dinleyiciler bayat
  // closure çağırmasın — v1 useChatThread deseni).
  // v0.10.1672 (B7) — konuşmanın v1 tur görünümü: takip bağlamı + wikiRefs v1
  // saf yardımcılarından (activeExplainFrom / prevWikiRefs) aynı kuralla.
  const turns = useMemo(() => toChatTurns(messages, visibleParts), [messages, visibleParts]);
  const explainCtx = useExplainContext({
    turns, ctxExcluded: ask.ctxExcluded, setCtxExcluded: ask.setCtxExcluded,
    baseCtxItems: ask.baseCtxItems, conversationId: conv.conversationId,
  });
  const live = useRef({ ask, codeLocked, busy, sendMessage, stop, pinBottom, prefill: composer.prefill, turns, explainCtx, conv });
  live.current = { ask, codeLocked, busy, sendMessage, stop, pinBottom, prefill: composer.prefill, turns, explainCtx, conv };
  /** Gönderimin `context`i — v1 useChatThread.send ile aynı katmanlar. */
  const contextFor = (q: string, so: ChatSendOpts | undefined, base: readonly ChatTurn[]) => {
    const c = live.current;
    const { explain, subject, ...scope } = c.explainCtx.resolve(base);
    return cosreRequestContext({ ...c.ask.reqCtx, ...scope }, { profile: c.ask.profile, deep: c.ask.deep, code: c.ask.code }, q, so, c.codeLocked,
      { conversation: c.conv.idRef.current, wikiRefs: prevWikiRefs(base), explain, subject });
  };
  const send = (text: string, so?: ChatSendOpts) => {
    const c = live.current;
    const q = text.trim();
    if (!q) return;
    const context = contextFor(q, so, c.turns);
    // Kullanıcı turu niyeti `fresh`SİZ saklar (v1 v0.10.1406) + sabit pencere.
    const metadata = {
      ...(so?.explainAsk ? { explainAsk: withoutFresh(so.explainAsk) } : {}),
      ...(so?.window ? { window: so.window } : {}),
    };
    const go = () => c.sendMessage({ text: q, ...(Object.keys(metadata).length ? { metadata } : {}) }, context ? { body: { context } } : undefined);
    if (c.busy) void c.stop().then(go);
    else void go();
  };
  // "↺ Yeniden dene": SDK regenerate aynı geçmişi yeniden yollar; gövde v1 retry
  // gibi aynı niyet (fresh'siz) + aynı pencere + attempt:'retry'.
  const retry = () => {
    const c = live.current;
    const idx = c.turns.map(t => t.role).lastIndexOf('user');
    if (idx < 0) return;
    const ut = c.turns[idx];
    const so: ChatSendOpts = { attempt: 'retry', ...(ut.explainAsk ? { explainAsk: ut.explainAsk } : {}), ...(ut.window ? { window: ut.window } : {}) };
    const context = contextFor(ut.text ?? '', so, c.turns.slice(0, idx));
    pinBottom();
    void regenerate(context ? { body: { context } } : undefined);
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
    conv.clear();
    ask.setPinned(null);
    ask.setCtxExcluded(new Set());
    composer.expectDraft(null);
  };

  // v0.10.1664 (B6) — `?chat=<id>` URL → state (ref sig-guard: aynı değer bir
  // kez yüklenir; akış sürerken load kendini iptal eder) ve state → URL
  // (syncChatParam; replace:true, yabancı paramlar korunur). v1 ile birebir.
  const { sp: askSp, setSp } = ask;
  const loadRef = useRef(conv.load);
  loadRef.current = conv.load;
  const chatParamRef = useRef('');
  const openConversation = (id: string) => {
    composer.expectDraft(id);
    return loadRef.current(id, () => busyRef.current, setMessages);
  };
  const openRef = useRef(openConversation);
  openRef.current = openConversation;
  useEffect(() => {
    const want = askSp.get('chat') ?? '';
    if (!want || chatParamRef.current === want) return;
    chatParamRef.current = want;
    if (want !== conv.conversationId) {
      // Silinmiş / okunamayan thread: v1 gibi boş sohbet kalır (karşılama çizilir).
      openRef.current(want).catch(() => {});
    }
  }, [askSp, conv.conversationId]);
  useEffect(() => {
    const next = syncChatParam(askSp, conv.conversationId, open);
    if (next) setSp(next, { replace: true });
  }, [askSp, setSp, conv.conversationId, open]);

  // ── Geçmiş (v1 paneli + kancası; liste yalnız açılışta) ──
  const [showHistory, setShowHistory] = useState(false);
  const history = useChatHistory(open && showHistory);
  const openThread = async (t: AiConversationSummary) => {
    try {
      await openConversation(t.id);
      setShowHistory(false);
    } catch (e) {
      history.setError(e instanceof Error ? e.message : String(e));
    }
  };
  const removeThread = async (t: AiConversationSummary) => {
    if (await history.remove(t, deleteConversation) && conv.conversationId === t.id) clear();
  };

  // v0.10.1660 (B5) — sunucunun `open` yönergesi (v1 answer.open, D7b): bitmiş
  // cevapta BİR KEZ, yalnız uygulama-içi güvenli yol, SPA ile (Sheet açık kalır).
  const navigate = useNavigate();
  const opened = useRef(new Set<string>());
  const lastMsg = messages.at(-1);
  useEffect(() => {
    if (status !== 'ready' || lastMsg?.role !== 'assistant') return;
    const to = lastMsg.metadata?.open;
    if (!to || opened.current.has(lastMsg.id)) return;
    opened.current.add(lastMsg.id);
    // v0.10.1672 (B7) — v1 onOpen ile aynı: isSafeAppPath kapısı + aynı sayfada
    // parametre birleştirme (mergeOpenHref), replace (hedef ?ai= ise AYNI Sheet'te tur).
    if (!isSafeAppPath(to)) return;
    const href = mergeOpenHref(to, window.location.pathname, window.location.search);
    if (href) navigate(href, { replace: true });
  }, [status, lastMsg, navigate]);

  // v0.10.1672 (B7) — `?ai=<özne>` TEK SEFERLİK niyet (v1 v0.10.1400/1405/1407 ile
  // aynı): Sheet açılır, açıklama sohbet turu olarak gider, `?ai=` + `?aicode=`
  // düşer. Ref muhafızı: StrictMode çift effect'i / yeniden render tek tur.
  const [rawSubject, setSubject] = useAiSubject();
  const rawSubjectKey = rawSubject ? formatAiParam(rawSubject) : '';
  const explainAskSentRef = useRef('');
  useEffect(() => {
    if (!rawSubject) { explainAskSentRef.current = ''; return; }
    if (ask.enabled !== true || explainAskSentRef.current === rawSubjectKey) return;
    explainAskSentRef.current = rawSubjectKey;
    const panel = rawSubject.kind === 'metric_panel' ? takeMetricPanelAsk(rawSubjectKey) : undefined;
    const x = explainAskFor(rawSubject, sp.get(AI_SRC_PARAM) || 'ai-param', undefined, sp.get(AI_CODE_PARAM) === '1', panel);
    setOpen(true);
    setSubject(null);
    if (x) { live.current.pinBottom(); sendRef.current(x.userText, explainTurnSendOpts(x)); }
  }, [rawSubject, rawSubjectKey, ask.enabled, sp, setSubject]);

  useEffect(() => {
    const onAsk = (e: Event) => {
      setOpen(true);
      const q = (e as CustomEvent<{ question?: string }>).detail?.question;
      if (q) { live.current.pinBottom(); sendRef.current(q); }
    };
    const onPrefill = (e: Event) => {
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
  }, []);

  // Rozet: kapalıyken (FAB görünürken) poll; RQ gizli sekmede durur.
  const criticalOpen = useOpenCriticalCount({ enabled: !open }).data ?? 0;
  // Genişlik her açılışta okunur (v1'de sürüklenip saklanmış olabilir).
  // v0.10.1664 (B6) — tutamakla seçilen genişlik (anahtara da yazılır) saklanandan önce gelir.
  const [dragW, setDragW] = useState<number | null>(null);
  const storedW = useMemo(
    () => (open ? cosreSheetWidth(getRaw(COSRE_DRAWER_WIDTH_KEY), AI_DRAWER_WIDTH, window.innerWidth) : AI_DRAWER_WIDTH),
    [open],
  );
  const width = dragW ?? storedW;

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
          <Link to={cosreFullPageHref(messages.length > 0 ? conv.conversationId : null)} className="cosre-page__home cm-fullpage"
            data-fullpage="" title={T.fullPageTitle}>{T.fullPage}</Link>
          <Button variant="ghost" size="sm" className="cm-head-btn" onClick={() => setShowHistory(h => !h)}
            aria-expanded={showHistory} title={T.historyTitle}>{T.history}</Button>
          {messages.length > 0 && (
            <Button variant="secondary" size="sm" onClick={clear} title={T.clearTitle}>{T.clear}</Button>
          )}
        </>}>
        <SheetResizeHandle width={width} onWidth={setDragW}
          onReset={() => setDragW(cosreSheetWidth(null, AI_DRAWER_WIDTH, window.innerWidth))} />
        <div className="cosre-root cosre-root--drawer" data-cosre-body="">
          {/* v0.10.1672 (B7) — etkin açıklama turu bir trace ve sayfa canlı bağlamını
              yayınlıyorsa şerit (v1 ile aynı yer: başlığın altı). */}
          {explainCtx.activeExplain?.meta.kind === 'trace' && explainCtx.liveTraceCtx && (
            <TraceContextStrip traceId={explainCtx.activeExplain.meta.id} ctx={explainCtx.liveTraceCtx} />
          )}
          {showHistory && (
            <ChatHistoryPanel history={history} activeId={conv.conversationId}
              onNew={() => { clear(); setShowHistory(false); }}
              onOpen={t => void openThread(t)} onDelete={t => void removeThread(t)} />
          )}
          <div className="cm-thread-wrap">
            <div ref={scrollRef} className="cm-thread flex-1 overflow-y-auto" role="log" aria-live="polite" aria-busy={busy} aria-label={T.threadLabel}>
              <div className="cm-thread__col">
                {messages.length === 0 ? (
                  <CosreWelcome firstName={ask.user?.firstName} env={ask.env} data={welcome} wiki={ask.wikiOn} ctxStarter={ask.ctxStarter}
                    onPrefill={composer.prefill} onAsk={askChip} onAskExplain={askExplain} onSubmit={submit} />
                ) : (
                  <MessageList messages={messages} visibleParts={visibleParts} status={status} error={error}
                    onAsk={askChip} onRetry={retry} />
                )}
              </div>
            </div>
            {!atBottom && messages.length > 0 && (
              <Button variant="secondary" size="sm" className="cm-jump" onClick={pinBottom}
                title={T.jumpBottomTitle} aria-label={T.jumpBottomLabel}>{T.jumpBottom}</Button>
            )}
          </div>
          <CosreComposerForm input={composer} ask={ask} busy={busy} stop={() => void stop()} codeLocked={codeLocked}
            ctxItems={explainCtx.ctxItems} onRemoveCtx={explainCtx.removeCtx} submit={submit} />
        </div>
      </CosreSheet>
    </>
  );
}
