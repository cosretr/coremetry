import { useEffect, useRef, useState, type MutableRefObject } from 'react';
import type { PickerStatus } from '@/components/ui/PickerPopover';

// usePickerSearch — v0.10.1089 (seçici açılır listesi: ortak popover).
//
// Üç sunucu-taraflı seçicinin (Service / Operation / MetricName) AYNI
// debounce'lu arama katmanı; v0.5.180-181'den beri üç kez satır satır
// kopyalanmıştı. Davranış birebir korunuyor: yazılan değer 180 ms
// durulunca sunucuya gider, liste sunucunun cevabıdır (joker karakterler
// sunucuda çözülür), tam katalog ASLA önden çekilmez.
//
// Eklenen iki şey, popover'ın durum satırı için:
//   • status — istek bekliyorken 'loading' ("aranıyor…"), düşerse 'error'.
//   • sıra bekçisi — geç dönen ESKİ bir cevap yeni sorgunun listesini
//     ezemez (yazarken "pa" cevabı "pay" cevabından sonra gelebiliyordu;
//     artık durum satırı da yalan söylemiyor).
//
// `itemsRef` — seçicilerin seçim sezgiseli (shouldAutoCommit) onChange
// ANINDA en taze listeyi görmeli; durum güncellemesi o an henüz çizilmemiş
// olabilir (v0.7.27 sözleşmesi, ServicePicker'daki optsRef'in aynısı).

export interface PickerSearch<T> {
  items: T[];
  total: number;
  status: PickerStatus | undefined;
  /** `items`ın HANGİ sorgunun cevabı olduğu (ilk cevaptan önce null). */
  forQuery: string | null;
  itemsRef: MutableRefObject<T[]>;
}

export const PICKER_DEBOUNCE_MS = 180;

export function usePickerSearch<T>(
  query: string,
  scope: string,
  run: (q: string) => Promise<{ items: T[]; total: number }>,
): PickerSearch<T> {
  const [state, setState] = useState<{ items: T[]; total: number; status: PickerStatus | undefined; forQuery: string | null }>(
    { items: [], total: 0, status: 'loading', forQuery: null });
  const itemsRef = useRef<T[]>([]);
  const seqRef = useRef(0);
  const runRef = useRef(run);
  runRef.current = run;

  useEffect(() => {
    const seq = ++seqRef.current;
    setState(s => (s.status === 'loading' ? s : { ...s, status: 'loading' }));
    const t = setTimeout(() => {
      runRef.current(query)
        .then(r => {
          if (seq !== seqRef.current) return;
          itemsRef.current = r.items;
          setState({ items: r.items, total: r.total, status: undefined, forQuery: query });
        })
        .catch(() => {
          if (seq !== seqRef.current) return;
          itemsRef.current = [];
          setState({ items: [], total: 0, status: 'error', forQuery: query });
        });
    }, PICKER_DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [query, scope]);

  return { ...state, itemsRef };
}
