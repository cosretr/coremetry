// notifySrc.ts — v0.10.2484 (precision programı A.3 / karar 8). Bildirim
// bağlantıları (Go notify.withNotifySrc: e-posta, Slack, Teams, webhook, özet,
// susturma sayfasının "aç" bağlantısı) `?src=notify` taşır; tarayıcı RUM
// documentLoad span'inin url.full'u bunu kaydeder → "bildirimden tıklandı"
// ölçülür. Uygulama parametreyi OKUMAZ (hiçbir süzgeç, sorgu anahtarı ya da
// bağlam onu kullanmaz). Problems sayfası sayfa YÜKLENDİKTEN sonra (RUM ölçüsü
// alınmış olsun) parametreyi adres çubuğundan siler: kopyalanıp paylaşılan
// bağlantı tıklamayı yanlışlıkla "bildirimden" saymasın. Diğer parametreler ve
// geçmiş kaydı korunur (replace).
import { useEffect } from 'react';
import { useSearchParams } from 'react-router';

export const NOTIFY_SRC_KEY = 'src';
export const NOTIFY_SRC_VALUE = 'notify';

/** SAF: parametre varsa onsuz arama dizgesi ('?' önekli ya da ''); yoksa null. */
export function stripNotifySrc(search: string): string | null {
  const sp = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search);
  if (sp.get(NOTIFY_SRC_KEY) !== NOTIFY_SRC_VALUE) return null;
  sp.delete(NOTIFY_SRC_KEY);
  const rest = sp.toString();
  return rest ? `?${rest}` : '';
}

/** RUM documentLoad span'i `load` olayında kurulur; silme ondan sonra. */
const STRIP_DELAY_MS = 1500;

/** Sayfa yüklendikten sonra `?src=notify`'ı adres çubuğundan siler (replace). */
export function useStripNotifySrc(): void {
  const [sp, setSp] = useSearchParams();
  const has = sp.get(NOTIFY_SRC_KEY) === NOTIFY_SRC_VALUE;
  useEffect(() => {
    if (!has) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const strip = () => {
      timer = setTimeout(() => {
        setSp(prev => {
          const next = new URLSearchParams(prev);
          if (next.get(NOTIFY_SRC_KEY) === NOTIFY_SRC_VALUE) next.delete(NOTIFY_SRC_KEY);
          return next;
        }, { replace: true });
      }, STRIP_DELAY_MS);
    };
    if (document.readyState === 'complete') strip();
    else window.addEventListener('load', strip, { once: true });
    return () => {
      window.removeEventListener('load', strip);
      if (timer) clearTimeout(timer);
    };
  }, [has, setSp]);
}
