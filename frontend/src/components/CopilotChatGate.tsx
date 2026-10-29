import { lazy, Suspense, useEffect, useRef, useState, type MutableRefObject } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useOpenCriticalCount } from '@/lib/queries';
import { AI_PARAM } from '@/lib/aiSubject';
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

const CopilotChat = lazy(() => import('./CopilotChat').then(m => ({ default: m.CopilotChat })));

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
  const [mounted, setMounted] = useState(false);
  const [openFirst, setOpenFirst] = useState(false);
  const queue = useRef<Queued[]>([]);
  const chatLive = useRef(false);
  // Adres niyeti render sırasında mandallanır (türetilmiş state deseni): bir
  // kez mount olan sohbet param düşse de mount kalır.
  const wantUrl = urlWantsChat(sp);
  if (wantUrl && !mounted) setMounted(true);

  useEffect(() => {
    const h = (e: Event) => {
      if (chatLive.current) return; // sohbet kendi dinleyicisiyle alır
      queue.current.push({ type: e.type, detail: (e as CustomEvent<unknown>).detail });
      setMounted(true);
    };
    for (const t of OPEN_EVENTS) window.addEventListener(t, h);
    return () => { for (const t of OPEN_EVENTS) window.removeEventListener(t, h); };
  }, []);

  // Sohbet mount olunca kendi poll'unu yürütür (aynı RQ anahtarı).
  const criticalOpen = useOpenCriticalCount({ enabled: enabled === true && !mounted }).data ?? 0;

  if (!enabled) return null;
  if (!mounted) {
    return (
      <>
        <TraceExplainNudge />
        <CopilotFab criticalOpen={criticalOpen} onClick={() => { setOpenFirst(true); setMounted(true); }} />
      </>
    );
  }
  return (
    <Suspense fallback={<GateLoading />}>
      <CopilotChat defaultOpen={openFirst} />
      <ReplayQueued queue={queue} live={chatLive} />
    </Suspense>
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
