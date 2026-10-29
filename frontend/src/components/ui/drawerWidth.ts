// drawerWidth — v0.10.1570: Drawer sol kenar tutamağının genişlik sınırları (SAF).
export const DRAWER_MIN_WIDTH = 380;

/** Sürüklenen genişlik — taban DRAWER_MIN_WIDTH, tavan ekranın solunda 160px pay. */
export function clampDrawerWidth(w: number, viewport: number): number {
  const max = Math.max(DRAWER_MIN_WIDTH, viewport - 160);
  return Math.round(Math.min(max, Math.max(DRAWER_MIN_WIDTH, w)));
}
