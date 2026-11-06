// logsView — v0.10.1499 (/logs Kibana-Discover yeniden tasarımı, dilim 2).
//
// Sayfanın üç GÖRÜNÜMÜ: Logs (satır tablosu) | Desenler | Şablonlar. Yeni
// URL parametresi YOK: görünüm v0.10.448'den beri var olan `?panel=` ipucunda
// yaşar (yok = Logs; `patterns` / `templates` = desen görünümleri), yani
// eski derin linkler (anomali/problem → `panel=patterns`) doğrudan desen
// görünümüne iner. `panel` süzgeç kimliğine (logsUrlSig) GİRMEZ — görünüm
// değiştirmek sayfalamayı sıfırlamaz. SAF; Logs.tsx yalnız bunları çağırır.
//
//   • Canlı kuyruk Logs görünümünü ZORLAR (akış satır tablosuna akar; desen
//     görünümünde akan satırı göstermek anlamsız, ES'e de iki okuma olurdu).
//   • Statik liste sorgusu YALNIZ Logs görünümünde koşar: desen görünümünde
//     tablo çizilmez, 100 satırlık ES _search'ü boşa gitmesin (ES-maliyet
//     disiplini; Logs.firstLoad.test `panel=patterns → liste çağrısı yok`).
import { parseLogsPanel } from '@/lib/logsUrl';

export type LogsView = 'logs' | 'patterns' | 'templates';

export function parseLogsView(panel: string | null | undefined): LogsView {
  return parseLogsPanel(panel) ?? 'logs';
}

/** Canlı kuyruk açıkken görünüm her zaman Logs. */
export function effectiveLogsView(view: LogsView, live: boolean): LogsView {
  return live ? 'logs' : view;
}

/** Görünümü `?panel=`'e yazar (Logs = param silinir); yabancı paramlar korunur. */
export function writeLogsView(prev: URLSearchParams, view: LogsView): URLSearchParams {
  const p = new URLSearchParams(prev);
  if (view === 'logs') p.delete('panel'); else p.set('panel', view);
  return p;
}

/** Statik liste sorgusunun kapısı (canlı kuyrukta ve takım kararı beklenirken de kapalı). */
export function logsListEnabled(view: LogsView, live: boolean, gate: boolean): boolean {
  return view === 'logs' && !live && !gate;
}
