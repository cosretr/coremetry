import { createContext } from 'react';

// buttonGroupContext — v0.10.919 (buton bütünlüğü, Seçenek B).
//
// ButtonGroup'un `size`ını çocuklarına taşıyan kanal. Ayrı dosyada:
// Button/IconButton bunu okur, ButtonGroup yazar; üçü aynı modülü
// paylaşınca döngüsel import doğmaz.
//
// NEDEN CONTEXT (CSS ya da cloneElement DEĞİL):
//   • CSS ile `.btn-group.sm > button` yazmak `button.sm`in dolgusunu
//     kopyalar ve telefondaki `button.sm, button.xs { min-height: 36px }`
//     kuralını (G0, v0.9.1013) ıskalar — o kural sınıfın BUTONUN
//     ÜSTÜNDE olmasına bakıyor.
//   • cloneElement Fragment'ların ve koşullu çocukların içine inemez
//     (Trace araç çubuğu, AnomaliesPage dizileri).
// Rung'lar Button ile IconButton'ın ORTAK kümesi; açık `size` prop'u
// her zaman kazanır.
export type ButtonGroupSize = 'xs' | 'sm' | 'md';

export const ButtonGroupSizeContext = createContext<ButtonGroupSize | null>(null);
