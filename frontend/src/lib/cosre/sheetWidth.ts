// sheetWidth — v0.10.1647 (CoSRE v2 Faz 2b B1, plan §4.1 / Q5). v2 Sheet'i v1
// çekmecesiyle AYNI genişliği okur: aynı localStorage anahtarı (v1
// CopilotChat.tsx `resizeKey`, pinli) ve aynı kelepçe (lib/drawerWidth).
// B1'de tutamak yok (B6): yalnız v1'de sürüklenip saklanan genişlik okunur.
import { DRAWER_MIN_WIDTH, clampDrawerWidth } from '@/lib/drawerWidth';

/** v1 `<Drawer resizeKey>` ile aynı anahtar — iki gerçekleme tek genişlik. */
export const COSRE_DRAWER_WIDTH_KEY = 'coremetry-cosre-drawer-w';

/** SAF: saklanan ham değer → px. Geçersiz / tabanın altı → varsayılan; sonuç
 *  ekrana kelepçeli (v1 Drawer'ın okuma kuralı + clampDrawerWidth). */
export function cosreSheetWidth(raw: string | null, fallback: number, viewport: number): number {
  const n = parseInt(raw ?? '', 10);
  const w = Number.isFinite(n) && n >= DRAWER_MIN_WIDTH ? n : fallback;
  return clampDrawerWidth(w, viewport);
}
