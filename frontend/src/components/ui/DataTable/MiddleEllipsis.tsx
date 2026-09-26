// MiddleEllipsis — v0.10.939 (tablo standardı T11): kimliği ORTADAN kırpar.
//
// `service.operation`, yol, pod adı, trace/span kimliği gibi değerlerde
// ayırt edici kısım SONDADIR (`checkout-api-7c8977f965-7hrqz` → son ek pod'u
// ayırır); sondan kırpmak tam o kısmı siler. İki span, ölçüm YOK (saf CSS):
//   • baş  — inline-block, taşarsa "…" (`.mid-ellipsis__head`); genişliği
//            `100% - --mid-tail-w` ile sınırlı
//   • kuyruk — son `tail` karakter, inline, hiç kırpılmaz (`.mid-ellipsis__tail`)
//
// v0.10.939 (tablo standardı T11, inceleme) — kutular SATIR İÇİ düzeyde
// (dış kap blok + nowrap, baş inline-block, kuyruk inline). Önceki `display:
// flex` iki span'i BLOKLAŞTIRIYORDU: kopyala-yapıştır ve innerText değeri
// "checkout-api-7c8977f965-\n7hrqz" diye iki satıra bölüyordu, ekran okuyucu
// iki ayrı parça okuyordu. Satır içi kutular tek dizge olarak kopyalanır ve
// okunur (columnFlags.contract CSS çivisi: `.mid-ellipsis` flex DEĞİL).
//
// Tam değer: metnin tamamı DOM'da (tek dizge — ekran okuyucu onu okur) ve
// kökün `title`ı (varsayılan `text`; hücre dışı kullanımda da ipucu var).
// <DataTableCell> hücrenin title'ını (sayfanınki ya da cellProps'unki) buraya
// da geçirir: iç title sayfanın hücreye verdiği ipucunu EZMEZ. aria-label
// YOK: ARIA düz `span`e (generic) ad vermeyi yasaklar ve okuyucular onu
// yok sayar; satır içi kutularla metnin kendisi zaten tek dizge.
//
// `--mid-tail-w` kuyruğun karakter sayısı kadar `ch` (satır içi stil): baş
// kuyruğa yer bırakır. Kuyruk yoksa 0 — kısa metin boşuna kırpılmaz.
// Hücrenin TEK içeriği olarak tasarlandı: satır-link (`.row-link`, blok) ya
// da `<td>` doğrudan kabı olur.

import type { CSSProperties } from 'react';
import { MIDDLE_ELLIPSIS_TAIL, splitMiddle } from './middleSplit';

export interface MiddleEllipsisProps {
  text: string;
  /** Hiç kırpılmayan son ek uzunluğu (karakter). Varsayılan 8. */
  tail?: number;
  /** Tam değer ipucu; varsayılan `text`. */
  title?: string;
  className?: string;
}

export function MiddleEllipsis({ text, tail = MIDDLE_ELLIPSIS_TAIL, title, className }: MiddleEllipsisProps) {
  const [head, end] = splitMiddle(text, tail);
  const style = { '--mid-tail-w': `${end.length}ch` } as CSSProperties;
  return (
    <span className={['mid-ellipsis', className].filter(Boolean).join(' ')} style={style} title={title ?? text}>
      <span className="mid-ellipsis__head">{head}</span>
      {end && <span className="mid-ellipsis__tail">{end}</span>}
    </span>
  );
}
