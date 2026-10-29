// uiStreamFixture.ts — v0.10.1598 (CoSRE v2 Faz 1, R8): UI Message Stream SSE
// metnini parçalara ayıran SAF ayrıştırıcı. Paylaşılan fixture testi ve Faz 2'nin
// parça→bileşen testi bunu kullanır (canlı akış Faz 2'de `ai` istemcisinden).
//
// SSE kuralları (eventsource-parser semantiği): boş satır çerçeveyi bitirir;
// `:` ile başlayan satır yorumdur (heartbeat `: ping`) ve atlanır; bir
// çerçevenin birden çok `data:` satırı `\n` ile birleşir.
import { DONE_MARKER, type UIStreamPart } from './protocol';

export interface ParsedUIStream {
  parts: UIStreamPart[];
  /** `data: [DONE]` görüldü mü. */
  done: boolean;
  /** [DONE]'dan sonra gelen data çerçevesi sayısı (protokol ihlali; 0 olmalı). */
  afterDone: number;
}

export function parseUIStreamSSE(text: string): ParsedUIStream {
  const out: ParsedUIStream = { parts: [], done: false, afterDone: 0 };
  const frames = text.replace(/\r\n/g, '\n').split('\n\n');
  for (const frame of frames) {
    const data: string[] = [];
    for (const line of frame.split('\n')) {
      if (line === '' || line.startsWith(':')) continue;
      if (line.startsWith('data:')) data.push(line.slice(5).replace(/^ /, ''));
    }
    if (data.length === 0) continue;
    const payload = data.join('\n');
    if (out.done) {
      out.afterDone++;
      continue;
    }
    if (payload === DONE_MARKER) {
      out.done = true;
      continue;
    }
    const part = JSON.parse(payload) as UIStreamPart;
    if (typeof part !== 'object' || part === null || typeof part.type !== 'string') {
      throw new Error(`parça değil: ${payload.slice(0, 80)}`);
    }
    out.parts.push(part);
  }
  return out;
}
