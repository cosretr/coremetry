// radixJsdom — Faz 2 dilim 1 R7 (plan R-14: polyfill'ler src/test/'e BİR kez).
// jsdom'da olmayan ve Radix'in çağırdığı tarayıcı API'leri. Yan etkili import:
//   import '@/test/radixJsdom';
// - ResizeObserver: Radix Checkbox `<form>` içinde kabarcık inputunun
//   boyutunu useSize ile ölçer; Popper (popover/select) da kullanır.
// - scrollIntoView / pointer capture: Radix Select/Popper.
// Yalnız eksikse tanımlanır (gerçek tarayıcıya ya da başka bir polyfill'e
// dokunmaz). Davranış eklemez: gözlemci hiçbir zaman tetiklenmez.
const g = globalThis as unknown as Record<string, unknown>;
g.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} };
if (typeof Element !== 'undefined') {
  const proto = Element.prototype as unknown as Record<string, unknown>;
  proto.scrollIntoView ??= function () {};
  proto.hasPointerCapture ??= () => false;
  proto.releasePointerCapture ??= () => {};
}
export {};
