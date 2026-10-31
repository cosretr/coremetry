// actWait — sabit uykulu "settle" yerine KOŞULA bekleyen act döngüsü.
//
// Neden: `await act(async () => { await sleep(30) })` sorgunun 30 ms'de
// çözülüp çizileceğini VARSAYAR. Tam paket paralel koşarken (CPU doygun)
// React Query bildirimi + render zinciri 30 ms'yi aşar ve test ya yanlış
// DOM'u ölçer ya da bir sonraki adımı hiç gelmeyecek bir tetikleyiciyle
// bekler. Bu yardımcı her turda act kapsamını kapatır (React kuyruğu
// boşalır), koşulu denetler, koşul gerçekleşene dek bekler. Tavan aşılırsa
// koşulun ADIYLA düşer — sessiz zaman aşımı değil.
import { act } from 'react';

export async function settleUntil(cond: () => boolean, label: string, timeoutMs = 4000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    await act(async () => { await new Promise(r => setTimeout(r, 10)); });
    if (cond()) return;
    if (Date.now() > deadline) throw new Error(`settleUntil: ${label} ${timeoutMs} ms içinde gerçekleşmedi`);
  }
}
