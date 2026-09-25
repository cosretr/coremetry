import type { HTMLAttributes, ReactNode } from 'react';
import { ButtonGroupSizeContext, type ButtonGroupSize } from './buttonGroupContext';

// ButtonGroup — v0.10.919 (buton bütünlüğü, Seçenek B; operatör onayı
// 2026-09-25).
//
// Eş, commit-olmayan eylem/araç kümesi: satır eylemleri (Edit · Disable ·
// Delete), dışa aktarım biçimleri (CSV · NDJSON), triage fiilleri (Ack ·
// Resolve · Ignore), zoom. Depoda 115 çok-butonlu kabın 47'si bunu elle
// kuruyordu (`<span style={{display:'inline-flex',gap:4}}>`, `{' '}` metin
// düğümleri, sayfaya özel `.xxx-actions` sınıfları) — altı farklı aralık.
//
// SINIRLAR — üç komşu atomla çakışmasın:
//   • Tek seçim (radyo) → SegmentedControl. Orada grup TEK Tab durağı
//     (roving tabindex); burada her buton KENDİ Tab durağı. Bu klavye
//     farkı iki atomun sınırıdır ve sözleşme testinde çivili.
//   • Form/kart commit satırı (İptal · Kaydet) → ActionRow. Sıra politikası
//     ORADA (yıkıcı solda, onay sağda). ButtonGroup yazarın sırasını
//     korur; satır eylemlerinde gelenek yıkıcı SONDA (Alerts, Monitors).
//   • `role="group"`, `toolbar` DEĞİL: APG toolbar ok tuşu gezinmesi vaat
//     eder, o da SegmentedControl'ün davranışı.
//
// ÇOCUKLAR JSX `<Button>`/`<IconButton>` KALIR — `items=[{variant:…}]`
// gibi bir veri API'si YOK: destructiveConfirm kapısı (Gate B) dosyada
// literal `variant="ghost-danger"` arıyor; veri nesnesi onu `variant:`
// yapıp kapıyı kör ederdi.
//
// `attached` (bitişik kenarlar) yalnız KENARLI varyantlar içindir
// (secondary / IconButton secondary): ghost'un kenarı şeffaf, primary'nin
// hiç yok — birleşim çizgisi görünmez olurdu. Bitişik grupta yıkıcı buton
// olmaz (yan yana yapışık bir Sil yanlış tıkı davet eder). Kural
// `ButtonGroup.contract.test.tsx`in kaynak taramasında.
//
// `overflow: hidden` YOK (`.segmented`in aksine): global `:focus-visible`
// halkası (outline-offset 2px) kırpılmasın; birleşim her butonun kendi
// 1px kenarının -1px bindirmesiyle kurulur.

export interface ButtonGroupProps
  extends Omit<HTMLAttributes<HTMLDivElement>, 'role' | 'aria-label' | 'children'> {
  /** Zorunlu: grubun erişilebilir adı (SegmentedControl / IconButton ile aynı kural). */
  'aria-label': string;
  children: ReactNode;
  /** Bitişik kenarlar (yalnız kenarlı varyantlar). Varsayılan: aralıklı. */
  attached?: boolean;
  /** Çocuk Button/IconButton'lara devredilir; çocuğun açık `size`ı kazanır. */
  size?: ButtonGroupSize;
}

export function ButtonGroup({ attached, size, className, children, ...rest }: ButtonGroupProps) {
  const classes = [
    'btn-group',
    attached ? 'is-attached' : '',
    className,
  ].filter(Boolean).join(' ');
  return (
    <div role="group" className={classes} {...rest}>
      <ButtonGroupSizeContext.Provider value={size ?? null}>
        {children}
      </ButtonGroupSizeContext.Provider>
    </div>
  );
}
