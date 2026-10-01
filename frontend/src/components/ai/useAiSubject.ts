import { useCallback, useMemo } from 'react';
import { useSearchParams } from 'react-router-dom';
import { AI_CODE_PARAM, AI_PARAM, AI_SRC_PARAM, formatAiParamForUrl, parseAiParam, type AISrc, type AISubject } from '@/lib/aiSubject';

// useAiSubject — AI çekmecesinin açık öznesi, ADRESTEN okunur/yazılır
// (v0.9.477). Ev kuralı: her operatör seçimi `setSearchParams(prev => …,
// { replace: true })` ile yazılır, yabancı parametreler KORUNUR ve seçim
// history'ye durak eklemez.
export function useAiSubject(): [AISubject | null, (s: AISubject | null, src?: AISrc) => void] {
  const [searchParams, setSearchParams] = useSearchParams();
  const raw = searchParams.get(AI_PARAM);
  // v0.10.731 — kısa biçim (`?ai=trace`) sayfanın kendi kimliğinden çözülür.
  // Taban CANLI adres çubuğu (aşağıdaki setSubject ile aynı gerekçe: /trace
  // ?span=/?tab='ı ham replaceState ile yazdığı için router bayat kalabilir).
  const pageParams = useMemo(() => {
    const live = typeof window !== 'undefined' ? window.location.search : '';
    const next = new URLSearchParams(live || searchParams.toString());
    searchParams.forEach((v, k) => { if (!next.has(k)) next.append(k, v); });
    return next;
  }, [searchParams]);
  const subject = useMemo(() => parseAiParam(raw, pageParams), [raw, pageParams]);

  const setSubject = useCallback((s: AISubject | null, src?: AISrc) => {
    setSearchParams(prev => {
      // `prev` router'ın konumundan gelir; Trace sayfası ?span= / ?tab='ı
      // ham history.replaceState ile yazdığı için router BAYAT kalabilir —
      // o hâlde prev'i kopyalamak seçili span'i adresten SİLERDİ (ev
      // kuralının yasakladığı "yabancı parametre kaybı"). Canlı adres
      // çubuğu her zaman üst küme olduğundan onu taban alıp prev'de olup
      // canlıda olmayan anahtarları üstüne ekliyoruz.
      const live = typeof window !== 'undefined' ? window.location.search : '';
      const next = new URLSearchParams(live || prev.toString());
      prev.forEach((v, k) => { if (!next.has(k)) next.append(k, v); });
      // Kısa biçim `next` üzerinden çözülür: sayfa kimliği zaten içinde.
      if (s) next.set(AI_PARAM, formatAiParamForUrl(s, next));
      else next.delete(AI_PARAM);
      // v0.10.81 — aicode YALNIZ paylaşılan linkte yaşar: uygulama içi
      // her açılış/kapanış/özne değişimi onu siler. Silmeseydik bir kez
      // işaretlenen kutu SONRAKİ öznelere de sızardı ve v0.10.60'ın
      // "her açılışta kapalı" kararı arka kapıdan geri dönerdi.
      next.delete(AI_CODE_PARAM);
      // v0.10.432 (D8) — açılış kaynağı da yalnız o açılışta yaşar.
      if (s && src) next.set(AI_SRC_PARAM, src);
      else next.delete(AI_SRC_PARAM);
      return next;
    }, { replace: true });
  }, [setSearchParams]);

  return [subject, setSubject];
}
