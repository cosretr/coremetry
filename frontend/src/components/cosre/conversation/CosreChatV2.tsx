// CosreChatV2 — v0.10.1647 (CoSRE v2 Faz 2b B1, plan §1.2 / §4). v2 sohbetinin
// kapsayıcısı; kabuk kapısı (CopilotChatGate) bunu YALNIZ dinamik import() ile
// çeker (ayrı tembel chunk) ve bayrak açıkken ilk açılma niyetinde mount eder.
// Bir kez mount olunca mount kalır (v1 deseni: taslak/thread gezinmede korunur).
//
// Açılış yolları (v1 ile aynı, Q1 hariç):
//   • FAB (kapalıyken bu bileşen çizer; kritik-problem rozeti aynı RQ anahtarı)
//   • `?chat=<id>` → açılır. Parametre YAZILMAZ/silinmez: bayrak kapatılınca v1
//     aynı adresle aynı thread'i yükler (iki gerçekleme tek kalıcılık, §1.4).
//   • `coremetry:ai-ask` / AI_PREFILL_EVENT → açılır (gönderme/doldurma B2).
//   • `?ai=` açıklama turu → v1'in (kapı yanına `launcher={false}` mount eder);
//     v2 kendini KAPATIR. `yieldToV1` iken (v1 açıklama örneği mount) gelen
//     ai-ask/prefill'i de v1 kendi dinleyicisiyle alır → v2 kapanır, iki panel
//     üst üste binmez. B7 bu dalı siler.
import { useEffect, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useOpenCriticalCount } from '@/lib/queries';
import { AI_PARAM } from '@/lib/aiSubject';
import { getRaw } from '@/lib/storage';
import { COSRE_DRAWER_WIDTH_KEY, cosreSheetWidth } from '@/lib/cosre/sheetWidth';
import { AI_PREFILL_EVENT } from '@/components/ai/aiEvents';
import { AI_DRAWER_WIDTH } from '@/components/ai/answerCard';
import { CopilotFab } from '@/components/ai/CopilotFab';
import { TraceExplainNudge } from '@/components/ai/TraceExplainNudge';
import { CosreSheet } from './CosreSheet';

const OPEN_EVENTS = ['coremetry:ai-ask', AI_PREFILL_EVENT] as const;

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

  useEffect(() => {
    const h = () => setOpen(!yieldToV1);
    for (const t of OPEN_EVENTS) window.addEventListener(t, h);
    return () => { for (const t of OPEN_EVENTS) window.removeEventListener(t, h); };
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
      <CosreSheet open={open} onOpenChange={setOpen} width={width} />
    </>
  );
}
