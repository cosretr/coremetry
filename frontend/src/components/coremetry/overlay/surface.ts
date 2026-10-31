// surface.ts — v0.10.1715 (operatör onayı): shadcn overlay içerikleri
// (sheet / dialog / alert-dialog) bg-background = --bg0 SAYFA zemininde
// duruyordu (açık temada gri). Sarmalayıcılar bg-card (= --bg1, v1 Drawer
// paritesi) verir; primitive cn (twMerge) bg-background'u ezer, çağıranın
// kendi bg sınıfı yine kazanır. Üretilmiş components/shadcn/* değişmez.
//
// v0.10.1721 (operatör: CoSRE v2 çekmecesinde "resim çerçevesi gibi" siyah
// kenar). index.css Tailwind preflight'ını almıyor; renksiz `border` /
// `border-l` kenar rengini currentColor'dan (≈ siyah metin) alır. Overlay
// içerikleri kenar rengini token'dan (border-border = --border) açıkça
// taşır; çağıranın border-* rengi yine kazanır (twMerge).
export const OVERLAY_BORDER = 'border-border';
export const OVERLAY_SURFACE = `bg-card ${OVERLAY_BORDER}`;
