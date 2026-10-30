// useSheetParam — v0.10.1669 (etkileşim dilimi S1, plan §2.1, Q6). Detay
// çekmecesinin açık/kapalı durumu URL'de: `?<key>=<id>` paylaşılabilir, Geri
// çekmeceyi kapatır. Değer URL'den TÜRETİLİR (lib/urlParams ilkesi — yerel
// kopya yok, tek-yön okuma sınıfı doğmaz). Açılış push (Geri kapatır), özne
// değişimi / kapanış replace:true; yabancı parametreler korunur, aynı değere
// yazım no-op (imza muhafızı).
import { useCallback } from 'react';
import { useSearchParams } from 'react-router-dom';

/** SAF: prev + (key, v) → yeni params; değişiklik yoksa null. */
export function nextSheetParams(prev: URLSearchParams, key: string, v: string | null): URLSearchParams | null {
  const cur = prev.get(key);
  const want = v && v.trim() ? v : null;
  if (cur === want) return null;
  const next = new URLSearchParams(prev);
  if (want === null) next.delete(key); else next.set(key, want);
  return next;
}

export function useSheetParam(key: string): [string | null, (v: string | null) => void] {
  const [sp, setSp] = useSearchParams();
  const raw = sp.get(key);
  const value = raw && raw.trim() ? raw : null;
  const set = useCallback((v: string | null) => {
    // Açılış (yok → id) geçmişe GİRER ki Geri çekmeceyi kapatsın; özne
    // değiştirme ve kapatma replace (geçmiş şişmez).
    const next = nextSheetParams(sp, key, v);
    if (!next) return;
    setSp(next, { replace: !(sp.get(key) === null && next.get(key) !== null) });
  }, [key, sp, setSp]);
  return [value, set];
}
