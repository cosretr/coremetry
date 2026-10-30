// coremetry/DetailSheet — v0.10.1669 (etkileşim dilimi S1, plan §2.1). DONDURULMUŞ
// API: `ui/Drawer`ın her özelliği burada karşılığını bulur, böylece bir sayfa
// `<Drawer>` → `<DetailSheet>` takasını yeniden tasarlamadan yapar (S2–S7).
// Değişiklik = ek alan (operatör onayı), mevcut alanın anlamı değişmez.
import type { ReactNode } from 'react';

export interface DetailSheetProps {
  /** Denetimli. Drawer `onClose` → `onOpenChange(false)`. */
  open: boolean;
  onOpenChange(open: boolean): void;
  /** Başlık (Drawer `header`'ın ad kısmı); erişilebilir diyalog başlığı da budur. */
  title: ReactNode;
  /** Başlığın sağındaki rozetler / küçük bilgiler. */
  badges?: ReactNode;
  /** Varlık tutarlılığı: öznenin tam sayfası → başlıkta "Sayfayı aç ↗". */
  pageHref?: string;
  /** px, varsayılan 560 (Drawer ile aynı). */
  width?: number;
  /** Verilirse sol kenar tutamağı: sürüklenen genişlik bu localStorage anahtarında
   *  kalır, ekrana kelepçelenir, çift tık varsayılana döner (Q9: tüm detay çekmeceleri). */
  resizeKey?: string;
  /** true (varsayılan) = perde + odak tuzağı (Drawer `backdrop`); false = eşlik kipi (Q8: detaylar modal kalır). */
  modal?: boolean;
  /** Yalnız `modal=false`: boş alana tıklama kapatır (lib/emptyAreaClose). */
  closeOnEmptyClick?: boolean;
  /** Ekran okuyucu adı; yoksa CM_STRINGS.detailSheet. */
  ariaLabel?: string;
  /** Gövdenin altında sabit satır (eylemler). */
  footer?: ReactNode;
  /** Gövde sınıfı (Drawer `bodyStyle` yerine — inline stil yok). */
  bodyClassName?: string;
  /** Katalog: portal içeriğine hücre teması. */
  contentProps?: Record<`data-${string}`, string>;
  children?: ReactNode;
}
