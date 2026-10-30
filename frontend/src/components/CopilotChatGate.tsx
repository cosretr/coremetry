import { lazy, Suspense, useEffect, useRef, useState, type MutableRefObject } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useOpenCriticalCount } from '@/lib/queries';
import { AI_PARAM } from '@/lib/aiSubject';
import { pickChatImpl, type ChatImpl } from '@/lib/cosre/pickChatImpl';
import { useCopilotConfig } from './ai/useCopilotEnabled';
import { AI_PREFILL_EVENT } from './ai/aiEvents';
import { TraceExplainNudge } from './ai/TraceExplainNudge';
import { CopilotFab } from './ai/CopilotFab';
import { Spinner } from './Spinner';

// CopilotChatGate — v0.10.1599. Kabuk CoSRE sohbetini artık TEMBEL yükler:
// CopilotChat + components/ai/* ana yığının yarısıydı ve her sayfa açılışında
// indiriliyordu, oysa sohbet yalnız açılınca gerekir. Kapı küçük ve eager
// kalır; sohbet yığınını ilk AÇILMA niyetinde çeker ve sonra hep mount kalır
// (eski davranış: kabukta tek mount, thread/taslak/pin state'i gezinmede korunur).
//
// Açılma niyetleri (CopilotChat'in kapalıyken tepki verdiği her yol):
//   • FAB tıklaması            → sohbet `defaultOpen` ile açık başlar.
//   • `?ai=` / `?chat=` adreste → sohbet mount olur, kendi URL effect'leri
//                                 niyeti eskisi gibi tüketir (URL'de durur).
//   • `coremetry:ai-ask` / AI_PREFILL_EVENT olayları → olay SIRAYA alınır,
//     sohbet mount olunca (dinleyicileri takıldıktan sonra) aynen yeniden
//     yayınlanır; yazılan/gönderilen soru kaybolmaz.
// Kapalı başlatıcı (FAB + kritik rozet + nudge) burada eager çizilir; FAB ve
// marka işareti ai/CopilotFab.tsx'te, sohbetle paylaşılır (tek anatomi).
//
// v0.10.1647 (CoSRE v2 Faz 2b B1, plan §1.2) — kapı İKİ tembel gerçekleme
// tanır; seçim saf `pickChatImpl(cfg, niyet)`:
//   • cosreV2 kapalı → her niyet v1; aşağıdaki v1 dalı bugünküyle BİREBİR.
//   • cosreV2 açık → FAB / ?chat= / ai-ask / prefill → v2 (ayrı chunk,
//     components/cosre — yalnız dinamik import()); `?ai=` açıklama turu B7'ye
//     dek v1: v2 mount iken bile yanına `<CopilotChat launcher={false} />`
//     (kiosk deseni v0.10.732, FAB yok) mount edilir, v2 kendini kapatır.

const CopilotChat = lazy(() => import('./CopilotChat').then(m => ({ default: m.CopilotChat })));
const CosreChatV2 = lazy(() => import('./cosre/conversation/CosreChatV2').then(m => ({ default: m.CosreChatV2 })));

const AI_ASK_EVENT = 'coremetry:ai-ask';
const OPEN_EVENTS = [AI_ASK_EVENT, AI_PREFILL_EVENT] as const;

/** URL bir sohbet niyeti taşıyor mu (sohbet mount olunca kendisi tüketir). */
export function urlWantsChat(sp: URLSearchParams): boolean {
  return sp.has(AI_PARAM) || sp.has('chat');
}

type Queued = { type: string; detail: unknown };

export function CopilotChatGate() {
  const cfg = useCopilotConfig(true);
  const enabled = cfg ? cfg.enabled : null;
  const [sp] = useSearchParams();
  // Gerçekleme başına mandal: bir kez mount olan sohbet niyet düşse de mount kalır.
  const [mounted, setMounted] = useState<Record<ChatImpl, boolean>>({ v1: false, v2: false });
  const [openFirst, setOpenFirst] = useState(false);
  const queue = useRef<Queued[]>([]);
  const chatLive = useRef(false);
  const mount = (impl: ChatImpl) => setMounted(m => (m[impl] ? m : { ...m, [impl]: true }));
  // Adres niyeti render sırasında mandallanır (türetilmiş state deseni).
  // cfg gelmeden (null) mandal yok — bayrak bilinmeden yanlış gerçekleme seçilmesin.
  if (cfg) {
    const ai = sp.has(AI_PARAM) ? pickChatImpl(cfg, 'aiSubject') : null;
    const chat = sp.has('chat') ? pickChatImpl(cfg, 'chatParam') : null;
    if ((ai && !mounted[ai]) || (chat && !mounted[chat])) {
      setMounted(m => ({ v1: m.v1 || ai === 'v1' || chat === 'v1', v2: m.v2 || ai === 'v2' || chat === 'v2' }));
    }
  }
  // Olay niyeti: kuyruğa alınır, gerçekleme cfg bilinince seçilir (render'da).
  const [pendingEvt, setPendingEvt] = useState<'ask' | 'prefill' | null>(null);
  if (cfg && pendingEvt) {
    setPendingEvt(null);
    mount(pickChatImpl(cfg, pendingEvt));
  }
  // v2 kipinde v1 açıklama örneği mount iken pencere olayını v1 KENDİ
  // dinleyicisiyle alır; kuyruğa alıp yeniden yayınlamak v1'e ikinci kez
  // gönderirdi (v2 bu durumda kendini kapatır — yieldToV1).
  const v1Yields = useRef(false);
  useEffect(() => { v1Yields.current = cfg?.cosreV2 === true && mounted.v1; }, [cfg, mounted.v1]);

  useEffect(() => {
    const h = (e: Event) => {
      if (chatLive.current || v1Yields.current) return; // sohbet kendi dinleyicisiyle alır
      queue.current.push({ type: e.type, detail: (e as CustomEvent<unknown>).detail });
      setPendingEvt(e.type === AI_PREFILL_EVENT ? 'prefill' : 'ask');
    };
    for (const t of OPEN_EVENTS) window.addEventListener(t, h);
    return () => { for (const t of OPEN_EVENTS) window.removeEventListener(t, h); };
  }, []);

  const v2 = cfg?.cosreV2 === true;
  // FAB'ı kapı çizdiği sürece poll; sohbet mount olunca kendi poll'unu yürütür (aynı RQ anahtarı).
  const gateFab = v2 ? !mounted.v2 : !mounted.v1;
  const criticalOpen = useOpenCriticalCount({ enabled: enabled === true && gateFab }).data ?? 0;

  if (!enabled) return null;
  const launcher = (impl: ChatImpl) => (
    <>
      <TraceExplainNudge />
      <CopilotFab criticalOpen={criticalOpen} onClick={() => { setOpenFirst(true); mount(impl); }} />
    </>
  );
  if (!v2) {
    if (!mounted.v1) return launcher('v1');
    return (
      <Suspense fallback={<GateLoading />}>
        <CopilotChat defaultOpen={openFirst} />
        <ReplayQueued queue={queue} live={chatLive} />
      </Suspense>
    );
  }
  return (
    <>
      {mounted.v2 ? (
        <Suspense fallback={<GateLoading />}>
          <CosreChatV2 defaultOpen={openFirst} yieldToV1={mounted.v1} />
          <ReplayQueued queue={queue} live={chatLive} />
        </Suspense>
      ) : launcher(pickChatImpl(cfg, 'fab'))}
      {mounted.v1 && (
        <Suspense fallback={null}>
          <CopilotChat launcher={false} />
        </Suspense>
      )}
    </>
  );
}

// Sohbetin KARDEŞİ ve ondan SONRA: aynı commit'te mount olurlar ve pasif
// effect'ler kardeş sırasıyla koşar — CopilotChat'in pencere dinleyicileri
// takılmış olur, sıradaki olaylar ancak o zaman yeniden yayınlanır.
function ReplayQueued({ queue, live }: { queue: MutableRefObject<Queued[]>; live: MutableRefObject<boolean> }) {
  useEffect(() => {
    live.current = true;
    for (const q of queue.current.splice(0)) {
      window.dispatchEvent(new CustomEvent(q.type, { detail: q.detail }));
    }
  }, [queue, live]);
  return null;
}

function GateLoading() {
  return (
    <div role="status" aria-busy="true" aria-label="CoSRE yükleniyor…"
      style={{ position: 'fixed', right: 18, bottom: 18, zIndex: 'var(--z-fab)', width: 48, height: 48,
        display: 'grid', placeItems: 'center' }}>
      <Spinner />
    </div>
  );
}
