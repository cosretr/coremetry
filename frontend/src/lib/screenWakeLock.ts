// v0.10.2151 (operatör: "login sayfası açıkken Windows 15 dk sonra kilitleniyor") —
// Screen Wake Lock: sayfa GÖRÜNÜRKEN tarayıcı işletim sistemine "ekran gerekli"
// der (video oynatmayla aynı istek); ekran kararmaz, ekran koruyucu devreye girmez.
//
// Sınırı: kurumsal "hareketsizlik sınırı" ilkesi (klavye/fare girdisine bakar) ve
// VDI oturum boşta kalma sayacı bu istekle ERTELENMEZ; onları yalnız ilke sahibi
// istisna ile açar. Güvenli bağlam (HTTPS / localhost) ister; desteklemeyen
// tarayıcıda ve izin reddinde sessizce hiçbir şey yapmaz.
//
// Kilit sekme gizlenince tarayıcı tarafından bırakılır → görünür olunca yeniden alınır.
import { useEffect } from 'react';

export interface WakeSentinel {
  release(): Promise<void>;
  addEventListener(type: 'release', cb: () => void): void;
}
export interface WakeNav { wakeLock?: { request(type: 'screen'): Promise<WakeSentinel> } }
export interface WakeDoc {
  hidden: boolean;
  addEventListener(type: 'visibilitychange', cb: () => void): void;
  removeEventListener(type: 'visibilitychange', cb: () => void): void;
}

/** Ekranı uyanık tutmaya başlar; dönen fonksiyon bırakır. Destek yoksa no-op. */
export function holdScreenAwake(nav: WakeNav, doc: WakeDoc): () => void {
  const api = nav.wakeLock;
  if (!api) return () => {};
  let sentinel: WakeSentinel | null = null, pending = false, off = false;
  const acquire = () => {
    if (off || pending || sentinel || doc.hidden) return;
    pending = true;
    api.request('screen').then(s => {
      pending = false;
      if (off) { void s.release().catch(() => {}); return; }
      sentinel = s;
      s.addEventListener('release', () => { if (sentinel === s) sentinel = null; });
    }).catch(() => { pending = false; }); // izin yok / pil tasarrufu: sessiz
  };
  const onVis = () => acquire();
  doc.addEventListener('visibilitychange', onVis);
  acquire();
  return () => {
    off = true;
    doc.removeEventListener('visibilitychange', onVis);
    const s = sentinel; sentinel = null;
    if (s) void s.release().catch(() => {});
  };
}

/** `active` iken sayfa görünür olduğu sürece ekranı uyanık tutar. */
export function useScreenWakeLock(active: boolean): void {
  useEffect(() => {
    if (!active || typeof navigator === 'undefined' || typeof document === 'undefined') return;
    return holdScreenAwake(navigator as unknown as WakeNav, document);
  }, [active]);
}
