import { useCallback, useRef } from 'react';
import { useSearchParams } from 'react-router-dom';

// useUrlPage — `?page=` sayfalamanın TEK kaynağı, KİMLİĞİ SABİT bir yazıcıyla.
//
// ——— NEDEN AYRI BİR HOOK (v0.10.827) ————————————————————————————
//
// Operatör-bildirimli: /services'in alt şeridindeki "Next" ve "Last"
// hiçbir şey yapmıyordu — tık sonrası sayfa 1'de kalıyordu.
//
// Kök neden kütüphanenin sözleşmesinde: react-router-dom 6.30'da
// `useSearchParams`ın döndürdüğü `setSearchParams`
// `useCallback(…, [navigate, searchParams])` ve `searchParams` da
// `location.search`e memo'lu. Yani HER URL yazımı setter'a YENİ BİR
// KİMLİK veriyor.
//
// /services o setter'ı saran bir `setPage`i (useCallback [setSearchParams])
// "filtre değişince sayfayı sıfırla" efektinin bağımlılık listesine
// koymuştu (v0.9.1111). Zincir kendi kuyruğunu yiyordu:
//
//   Next tıkı → ?page=1 yazılır → location.search değişir →
//   searchParams yeni kimlik → setSearchParams yeni kimlik →
//   setPage yeni kimlik → sıfırlama efekti YENİDEN koşar →
//   setPage(0) → sayfa 1'e (yani 0. indekse) geri döner.
//
// Efekt gerçek bir filtre değişimi sanıyordu; olan yalnızca tıkın kendi
// URL yazımıydı. Ölçüm (useUrlPage.test.tsx): tek "Next" tıkında
// sıfırlama efekti İKİ kez koşuyor ve URL `?page=` olmadan kalıyor.
//
// Çözüm sınıfı kapatıyor: setter `useCallback(…, [param])` ile SABİT
// kimlikli, gerçek `setSearchParams` bir ref üzerinden okunuyor. Artık
// bir efektin bağımlılık listesinde durması zararsız — ve dürüst:
// exhaustive-deps'i bastırmak gerekmiyor.
//
// `window.location.search`ten tohumlanıyor, router'ın `prev`inden DEĞİL:
// bu sayfaların kendi paramları (cluster/namespace, `s_*`) ham okumayla
// yazılıyor ve `prev` onlardan sonra BAYAT bir alt küme olabiliyor —
// updater biçimi yabancı paramları sessizce düşürürdü (useUrlRange'in
// v0.9.937'de yazdığı aynı gerekçe).
export function useUrlPage(
  param = 'page',
): [number, (next: number | ((p: number) => number)) => void] {
  const [searchParams, setSearchParams] = useSearchParams();
  const page = Math.max(0, parseInt(searchParams.get(param) ?? '0', 10) || 0);

  // Kimliği sabit tutmanın bedeli: gerçek setter'ı ref'te taşımak. Ref her
  // render'da tazeleniyor, yani çağrı ANINDA güncel setter kullanılıyor.
  const setSearchParamsRef = useRef(setSearchParams);
  setSearchParamsRef.current = setSearchParams;

  const setPage = useCallback((next: number | ((p: number) => number)) => {
    setSearchParamsRef.current(() => {
      const p = new URLSearchParams(window.location.search);
      const cur = Math.max(0, parseInt(p.get(param) ?? '0', 10) || 0);
      // Fonksiyon biçimi URL'deki DEĞERDEN hesaplıyor, closure'daki
      // `page`ten değil: iki hızlı "Next" tıkı arasında closure bayatlar.
      const v = typeof next === 'function' ? next(cur) : next;
      if (v > 0) p.set(param, String(v)); else p.delete(param);
      return p;
    }, { replace: true });
  }, [param]);

  return [page, setPage];
}
