// textIdMap — v0.10.1618 (CoSRE v2 Faz 2a): transport'un parça akışını saran
// TransformStream. Parçaları DEĞİŞTİRMEDEN geçirir; yalnız messageId başına
// `text-start id → text parçası sırası` haritasını tutar.
//
// Neden: ai@5.0.276'da TextUIPart'ın id'si yok — `data-reset {textId}` hedefi
// useChat'in mesajından çözülemez (scratchpad aiparse/REPORT.md bulgu 4).
// SDK her `text-start` için mesaja YENİ bir text parçası ekler; dolayısıyla
// "bu mesajda kaçıncı text-start" = "message.parts içinde kaçıncı text
// parçası". Reset uygulaması burada DEĞİL (resetReducer.ts).
//
// `active`: o an akan mesajın id'si (`start.messageId`, sunucu ULID'i). textId
// yalnız mesaj içinde tekil (her mesaj t1'den başlar), yani çözüm etkin
// mesaja kapsanır. Sarmalayıcı aşağı akıştan birkaç parça önde olabilir
// (TransformStream backpressure) — harita yalnız büyür ve bir akışta tek
// `start` var, dolayısıyla öndeyken de çözüm doğru.
import type { UIMessageChunk } from 'ai';

export interface TextIdMap {
  wrap(stream: ReadableStream<UIMessageChunk>): ReadableStream<UIMessageChunk>;
  /** Etkin (akan) mesajda textId'nin sırası. */
  resolve(textId: string): { messageId: string; ordinal: number } | undefined;
  ordinalOf(messageId: string, textId: string): number | undefined;
}

export function createTextIdMap(): TextIdMap {
  const byMessage = new Map<string, { ids: Map<string, number>; n: number }>();
  let active = '';
  const entry = (id: string) => {
    let e = byMessage.get(id);
    if (!e) byMessage.set(id, (e = { ids: new Map(), n: 0 }));
    return e;
  };
  return {
    wrap: stream => stream.pipeThrough(new TransformStream<UIMessageChunk, UIMessageChunk>({
      transform(chunk, ctl) {
        if (chunk.type === 'start' && chunk.messageId) active = chunk.messageId;
        else if (chunk.type === 'text-start') {
          const e = entry(active);
          e.ids.set(chunk.id, e.n++);
        }
        ctl.enqueue(chunk);
      },
    })),
    resolve(textId) {
      const ordinal = byMessage.get(active)?.ids.get(textId);
      return ordinal === undefined ? undefined : { messageId: active, ordinal };
    },
    ordinalOf: (messageId, textId) => byMessage.get(messageId)?.ids.get(textId),
  };
}
