// escBridge — v0.10.1615 (shadcn Faz 1 R7, plan §7.2, karar d).
//
// Radix katmanlarını Coremetry'nin TEK Esc yığınına (lib/escLayer.ts) bağlar.
// Gerçekler (Radix 1.8.1, @radix-ui/react-dismissable-layer, ölçüldü):
//  - DismissableLayer `keydown`u document'ta YAKALAMA fazında dinler: React
//    işleyicilerinden ve keyboard.ts'in tek document dinleyicisinden ÖNCE.
//  - Kapatırken olayı KENDİSİ `preventDefault()` eder, sonra onDismiss'i
//    çağırır. keyboard.ts ise `e.defaultPrevented`i "bir öğe Esc'i kendi
//    işine harcadı" diye okur ve yığına dokunmaz (:238).
// Planın taslağı (yalnız escPending; preventDefault YOK) bu yüzden yetmez:
// Radix'in kendi preventDefault'u bayrağı zehirler, keyboard.ts hiçbir
// katmanı kapatmaz, sarmalayıcı da Radix'in kapatmasını yok saydığı için
// hiçbir şey kapanmaz (escLayer.contract.test.tsx bunu çiviliyor).
//
// DESEN: onEscapeKeyDown'da (Radix'in denetiminden hemen önce)
//  1. escPending = true → kök onOpenChange(false) Radix'in Esc kapatmasıysa
//     yok sayılır (hangi katmanın kapanacağına yığın karar verir);
//  2. bu olayın BİR SONRAKİ preventDefault çağrısı yutulur (Radix'inki):
//     bayrak temiz kalır, React işleyicileri ve keyboard.ts olayı görür.
//     Yutma kendini siler; mikro görevde de silinir (Radix bir gün
//     çağırmazsa bile bir sonraki dinleyiciye sızmaz).
// Sonra: içteki bir öğe Esc'i harcarsa (preventDefault — artık GERÇEK)
// keyboard.ts dokunmaz; harcamazsa topEscLayer() EN ÜSTTEKİ katmanı kapatır
// — sarmalayıcıdan sonra açılmış elle yazılmış bir Drawer varsa o.
// onPointerDownOutside / onInteractOutside'a dokunulmaz.
import { createContext, useCallback, useContext, useRef } from 'react';
import { useEscLayer } from '@/lib/escLayer';

type EscBridge = (e: KeyboardEvent) => void;
const Ctx = createContext<EscBridge | null>(null);
export const EscBridgeProvider = Ctx.Provider;

/** Olayın bir sonraki preventDefault çağrısını yutar (yalnız birini). */
export function swallowNextPreventDefault(e: Event): void {
  if (e.defaultPrevented || Object.prototype.hasOwnProperty.call(e, 'preventDefault')) return;
  const restore = () => { delete (e as { preventDefault?: unknown }).preventDefault; };
  Object.defineProperty(e, 'preventDefault', { configurable: true, writable: true, value: restore });
  queueMicrotask(restore);
}

/** Denetimli kök için: yığına kayıt + Radix'in Esc kapatmasını süzme. */
export function useOverlayEsc(open: boolean, onOpenChange: (open: boolean) => void) {
  const change = useRef(onOpenChange);
  change.current = onOpenChange;
  const escPending = useRef(false);
  useEscLayer(open, () => change.current(false));
  const onEscapeKeyDown = useCallback<EscBridge>((e) => {
    escPending.current = true;
    queueMicrotask(() => { escPending.current = false; });
    swallowNextPreventDefault(e);
  }, []);
  const rootOnOpenChange = useCallback((next: boolean) => {
    if (!next && escPending.current) { escPending.current = false; return; }
    change.current(next);
  }, []);
  return { onEscapeKeyDown, onOpenChange: rootOnOpenChange };
}

/** İçerik parçaları için: çağıranın onEscapeKeyDown'ı önce, sonra köprü. */
export function useEscapeKeyDown(user?: (e: KeyboardEvent) => void): (e: KeyboardEvent) => void {
  const bridge = useContext(Ctx);
  return useCallback((e: KeyboardEvent) => {
    user?.(e);
    if (!e.defaultPrevented) bridge?.(e);
  }, [bridge, user]);
}
