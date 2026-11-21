import { useEffect, useRef } from 'react';

// AI-drawer ↔ sayfa köprüsü (v0.9.477). Çekmece AppShell'de, TEK yerde
// mount edilir; kanıt (evidence) sözleşmesini kullanan waterfall / örnek-
// trace tabloları ise sayfa state'inde yaşar. Aradaki bağı prop yerine
// window CustomEvent taşıyor — emsal: CopilotExplain'in `coremetry:ai-ask`
// köprüsü (v0.9.165). Böylece çekmece hiçbir sayfayı, sayfalar da çekmeceyi
// import etmez; tek-commit geri alma kolay kalır.
//
// v0.9.408 / v0.9.414 sözleşmesi AYNEN korunur: explain-trace'in
// evidenceSpanIds'i waterfall satırlarını, explain-exception'ın
// evidenceTraceIds'i örnek-trace satırlarını `.wf-evidence` ile kutular.
export const AI_EVIDENCE_EVENT = 'coremetry:ai-evidence';

export type AIEvidenceDetail = { spanIds?: string[]; traceIds?: string[] };
// Kanıt satırına tıklama: v0.10.1033'ten beri dinleyicisi yok (useAiFocus
// kaldırıldı); satır DOM üzerinden kaydırılır (scrollToAttr). v0.10.1413 —
// dinleyicisiz `coremetry:ai-focus` olayı ve yayıcısı da silindi.

export function emitAiEvidence(detail: AIEvidenceDetail) {
  if (typeof window === 'undefined') return;
  window.dispatchEvent(new CustomEvent(AI_EVIDENCE_EVENT, { detail }));
}

// v0.10.1336 — rehberli tur adım 5: CoSRE çekmecesini AÇ + composer'ı
// DOLDUR, GÖNDERME. `coremetry:ai-ask` köprüsü (CopilotChat) soruyu hemen
// gönderir — tur onu KULLANMAZ. Dinleyici CopilotChat'te ai-ask'ın yanında:
// setOpen(true) + prefill(q, q.length) (doldur + imleç sonda + odak; gönderim
// yolu yok). Sözleşme: components/ai/aiPrefill.contract.test.tsx.
export const AI_PREFILL_EVENT = 'coremetry:ai-prefill';
export type AIPrefillDetail = { question?: string };

// wave 3 — CoSRE yan paneli: aç / aç-kapa (⌘I / Ctrl+I, komut paleti). Kapı
// (CopilotChatGate) sohbet yüklenmemişken olayı sıraya alır ve sohbeti yükler;
// sohbet (v1 / v2) mount olunca kendi dinleyicisiyle uygular. Konuşma kapanışta
// korunur: sohbet bir kez mount olduktan sonra kabukta kalır.
export const COSRE_PANEL_EVENT = 'coremetry:cosre-panel';
export type CosrePanelDetail = { mode: 'open' | 'toggle' };

export function emitCosrePanel(mode: CosrePanelDetail['mode']) {
  if (typeof window === 'undefined') return;
  window.dispatchEvent(new CustomEvent<CosrePanelDetail>(COSRE_PANEL_EVENT, { detail: { mode } }));
}

/** Olayın açık/kapalı geçişi (saf): toggle tersine çevirir, open her zaman açar. */
export function nextPanelOpen(open: boolean, d: CosrePanelDetail | undefined): boolean {
  return d?.mode === 'toggle' ? !open : true;
}

export function emitAiPrefill(detail: AIPrefillDetail) {
  if (typeof window === 'undefined') return;
  window.dispatchEvent(new CustomEvent(AI_PREFILL_EVENT, { detail }));
}

// Handler ref'te tutulur: çağıran satır-içi arrow geçse bile dinleyici
// her render'da sökülüp takılmaz.
function useWindowEvent<T>(name: string, handler: (detail: T) => void) {
  const ref = useRef(handler);
  ref.current = handler;
  useEffect(() => {
    const h = (e: Event) => ref.current((e as CustomEvent<T>).detail);
    window.addEventListener(name, h);
    return () => window.removeEventListener(name, h);
  }, [name]);
}

export function useAiEvidence(handler: (d: AIEvidenceDetail) => void) {
  useWindowEvent<AIEvidenceDetail>(AI_EVIDENCE_EVENT, handler);
}

// scrollToAttr — kanıt satırına git. Waterfall satırları `data-span-id`,
// exception örnek satırları `data-trace-id` taşır; çekmece kapandıktan
// SONRA (bir frame) kaydır, yoksa overlay hâlâ ekranı kaplıyor olur.
export function scrollToAttr(attr: 'data-span-id' | 'data-trace-id', value: string) {
  if (typeof window === 'undefined') return;
  requestAnimationFrame(() => {
    // Öznitelik seçicisi tırnak içinde: yalnız " ve \ kaçırılır (id'ler
    // hex, ama elle üretilmiş bir değer seçiciyi bozmasın).
    const sel = `[${attr}="${value.replace(/["\\]/g, '\\$&')}"]`;
    document.querySelector(sel)?.scrollIntoView({ block: 'center', behavior: 'smooth' });
  });
}
