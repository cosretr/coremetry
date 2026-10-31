// surface.ts — v0.10.1715 (operatör onayı): shadcn overlay içerikleri
// (sheet / dialog / alert-dialog) bg-background = --bg0 SAYFA zemininde
// duruyordu (açık temada gri). Sarmalayıcılar bg-card (= --bg1, v1 Drawer
// paritesi) verir; primitive cn (twMerge) bg-background'u ezer, çağıranın
// kendi bg sınıfı yine kazanır. Üretilmiş components/shadcn/* değişmez.
export const OVERLAY_SURFACE = 'bg-card';
