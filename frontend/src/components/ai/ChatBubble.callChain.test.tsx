// @vitest-environment jsdom
//
// v0.10.2374 — Operatör: "CoSRE trace açıklamasında gelen cevabı biraz daha
// görselleştirebilir miyiz? Servisler arası çağrı, mesaj highlight, çift
// tırnak gösterimleri daha güzel olabilir" + "hepsi düz siyah geliyor".
// İki çizici de pinli: sohbet balonu (renderNodes) ve ✨ Explain gövdesi
// (ExplainBody → Markdown.tsx `rich`; açıklama turunu ASIL çizen yüzey).
// Fixture'lar nötr adlarla (orders / customer-rel / com.example).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { renderToStaticMarkup } from 'react-dom/server';
import { act } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});
vi.mock('@/components/CosreChart', () => ({ CosreChart: () => <div>[grafik]</div> }));

import { ChatBubble } from './ChatBubble';
import { ExplainBody } from './ExplainBody';
import { RenderedMarkdown } from '@/components/Markdown';
import { chatPlainText } from './chatMarkdown';
import type { ChatTurn } from '@/lib/types';

let host: HTMLDivElement;
let root: Root;
const q = <T extends Element = HTMLElement>(sel: string) => host.querySelector<T>(sel);
const qa = <T extends Element = HTMLElement>(sel: string) => Array.from(host.querySelectorAll<T>(sel));
const texts = (sel: string) => qa(sel).map(e => e.textContent);

async function mount(turn: ChatTurn) {
  await act(async () => {
    root.render(
      <MemoryRouter>
        <QueryClientProvider client={new QueryClient()}>
          <ChatBubble turn={turn} />
        </QueryClientProvider>
      </MemoryRouter>,
    );
  });
}
const asst = (t: string, over: Partial<ChatTurn> = {}): ChatTurn => ({ role: 'assistant', text: t, ...over });

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); vi.restoreAllMocks(); });

const CHAIN = 'orders-api-prod, CUSTOMER_INFO_GET_RELATIONS için customer-rel-prod-grpc.customer.svc.cluster.local üzerinden BagService/callBagService\'i çağırdı → gRPC status error döndü → customer-rel-prod içinde RelationService.getRelations ve db0p.REL_TABLE_GER üzerinden aranan ilişki bulunamadı → com.example.core.exception.AppException: "Herhangi bir kayıt bulunamadı".';
const ITEM = 'Kritik hata noktası: orders-api-prod → customer-rel-prod-grpc.customer.svc.cluster.local · com.example.core.grpc.BagService/callBagService → status error + com.example.core.exception.AppException: "Herhangi bir kayıt bulunamadı"';
const KV = 'İş verisi: requestId=2d2fe61f-1c5b-478c-b32f-9545375bcbba · called=CUSTOMER_INFO_GET_RELATIONS · pilot request mbb=null, sube=null · fallback reason=Service gate closed';
const SLOW = 'En yavaş bileşen: orders-api-prod · SERVER · POST /WEB/orders/assessment/decision · 196 ms · öz 131 ms (kök süresinin %67\'si)';

describe('sohbet balonu — çağrı zinciri', () => {
  it('uyarı kutusu içindeki zincir: sıralı adımlar, çipler adımın içinde, hata adımı işaretli, metin oklarıyla durur', async () => {
    await mount(asst(`> [!NOT] Karar\n> ${CHAIN}\n`));
    const steps = qa('.cm-callout ol.cm-chain > li.cm-chain__s');
    expect(steps.length).toBe(4);
    expect(steps.map(s => s.classList.contains('cm-chain__s--err'))).toEqual([false, true, false, true]);
    expect(steps[0].querySelector('code.cm-hl-ent')?.textContent).toBe('orders-api-prod');
    expect(steps[1].querySelector('code.cm-hl-err')?.textContent).toBe('status error');
    expect(steps[3].querySelector('.cm-hl-msg')?.textContent).toBe('"Herhangi bir kayıt bulunamadı"');
    // ok metni DOM'da (seçip kopyalayınca özgün satır), ekran okuyucudan gizli
    expect(q('.cm-callout__b')?.textContent).toBe(CHAIN);
    expect(qa('.cm-chain__sep').every(s => s.getAttribute('aria-hidden') === 'true')).toBe(true);
  });

  it('liste maddesi: "Etiket:" akışın üstünde kalır, adımlar altında', async () => {
    await mount(asst(`## İşlem Akışı ve Veri Özeti\n\n- ${ITEM}\n- ${SLOW}\n`));
    const [li, slow] = qa('.cm-md-list > li');
    expect(li.querySelector(':scope > .cm-li-label')?.textContent).toBe('Kritik hata noktası:');
    expect(texts('.cm-md-list > li:first-child .cm-chain__s').length).toBe(3);
    expect(li.querySelectorAll('.cm-chain__s--err').length).toBe(1);
    expect(li.textContent).toBe(ITEM);
    // zincirsiz madde: etiket + düz satır (akış yok), süre ve yol vurgulu
    expect(slow.querySelector('.cm-chain')).toBeNull();
    expect(slow.querySelector(':scope > .cm-li-label')?.textContent).toBe('En yavaş bileşen:');
    expect(Array.from(slow.querySelectorAll('.cm-hl-dur')).map(e => e.textContent)).toEqual(['196 ms', '131 ms']);
    expect(slow.textContent).toBe(SLOW);
  });

  it('paragraf: çok satırlı metinde yalnız zincir satırı akışa döner', async () => {
    await mount(asst('Özet satırı.\na → b → c\nSon satır.'));
    expect(texts('.cm-chain__s')).toEqual(['a', ' → b', ' → c']);
    expect(texts('p.cm-md-p')).toEqual(['Özet satırı.', 'Son satır.']);
  });

  it('ikiden az ok: eskisi gibi tek paragraf / tek madde', async () => {
    await mount(asst('Window 19.11.2026 21:02:24.443 → 21:02:24.529 arası.\n\n- orders-api-prod → checkout-api-prod çağrısı\n'));
    expect(q('.cm-chain')).toBeNull();
    expect(texts('p.cm-md-p')).toEqual(['Window 19.11.2026 21:02:24.443 → 21:02:24.529 arası.']);
    expect(q('.cm-md-list > li')?.textContent).toBe('orders-api-prod → checkout-api-prod çağrısı');
    expect(q('p.cm-md-p')?.querySelector('code, .cm-hl-dur, .cm-hl-msg')).toBeNull();
  });

  it('akış sürerken yazılan son madde / paragraf akışa bölünmez; bitince bölünür', async () => {
    await mount(asst('- a → b → c', { pending: true }));
    expect(q('.cm-chain')).toBeNull();
    await mount(asst('a → b → c', { pending: true }));
    expect(q('.cm-chain')).toBeNull();
    await mount(asst('- a → b → c'));
    expect(texts('.cm-chain__s').length).toBe(3);
  });

  it('kopyala (düz metin) yolu özgün oklu metni verir', () => {
    expect(chatPlainText(`- ${ITEM}`)).toBe(`• ${ITEM}`);
    expect(chatPlainText(CHAIN)).toBe(CHAIN);
  });
});

describe('sohbet balonu — tırnaklı mesaj, anahtar=değer, kullanıcı metni', () => {
  it('tırnaklı mesaj ayrı stil (çip değil), tırnaklar metinde', async () => {
    await mount(asst('Servis “Kayıt yok” dedi; fallback reason="Service gate closed". `"kod içi tırnak"`'));
    expect(texts('.cm-hl-msg')).toEqual(['“Kayıt yok”', '"Service gate closed"']);
    expect(qa('.cm-hl-msg').every(e => e.tagName === 'SPAN')).toBe(true);
    expect(q('code:not(.cm-hl)')?.textContent).toBe('"kod içi tırnak"');
  });

  it('anahtar=değer: tek çip, anahtar ayrı (soluk) span', async () => {
    await mount(asst(`- ${KV}\n`));
    const kv = qa('code.cm-hl-kv');
    expect(kv.map(e => e.textContent)).toEqual([
      'requestId=2d2fe61f-1c5b-478c-b32f-9545375bcbba', 'called=CUSTOMER_INFO_GET_RELATIONS', 'mbb=null', 'sube=null', 'reason=Service',
    ]);
    expect(kv.map(e => e.querySelector('.cm-hl-k')?.textContent)).toEqual(['requestId=', 'called=', 'mbb=', 'sube=', 'reason=']);
    expect(q('.cm-li-label')?.textContent).toBe('İş verisi:');
    expect(q('.cm-md-list > li')?.textContent).toBe(KV);
  });

  it('servis-benzeri ad bağlantı DEĞİL (entity modeli kuralı): yalnız stil', async () => {
    await mount(asst('orders-api-prod yavaş.'));
    const e = q('code.cm-hl-ent')!;
    expect(e.textContent).toBe('orders-api-prod');
    expect(e.closest('a')).toBeNull();
    expect(e.querySelector('a')).toBeNull();
  });

  it('kullanıcı mesajı dokunulmaz: zincir, çip, mesaj stili yok', async () => {
    await mount({ role: 'user', text: `${CHAIN}\n\n- ${KV}` } as ChatTurn);
    expect(q('.cm-chain, .cm-hl, .cm-hl-msg, .cm-hl-dur, .cm-li-label')).toBeNull();
  });
});

// Açıklama turu (✨ Explain / CoSRE "trace açıkla") balonda ExplainBody ile
// çizilir: Karar şeridi düz string, gövde Markdown.tsx. Operatörün ekranı bu.
describe('ExplainBody — Karar şeridi ve gövde (Markdown.tsx rich)', () => {
  const TEXT = [
    '**Kök Neden ve Sonraki Adım**',
    `${CHAIN} Sonraki adım: kaydı doğrula.`,
    '',
    '**İşlem Akışı ve Veri Özeti**',
    '- Operasyon: orders-api-prod · POST /WEB/orders/assessment/decision',
    `- ${ITEM}`,
    `- ${KV}`,
    `- ${SLOW}`,
    '- Kaynak: `orders-api-prod → a → b` ve [belge](https://example.com/orders-api-prod)',
  ].join('\n');

  async function mountExplain(text: string, busy = false) {
    await act(async () => { root.render(<ExplainBody text={text} busy={busy} />); });
  }

  it('Karar: ≥2 oklu cümle adım akışı; etiket durur, çipler ve mesaj adımın içinde', async () => {
    await mountExplain(TEXT);
    const v = q('.cx-verdict')!;
    expect(v.querySelector('.cx-verdict-k')?.textContent).toBe('Karar');
    const steps = Array.from(v.querySelectorAll('ol.cm-chain > li.cm-chain__s'));
    expect(steps.length).toBe(4);
    expect(steps.map(s => s.classList.contains('cm-chain__s--err'))).toEqual([false, true, false, true]);
    expect(steps[0].querySelector('code.cm-hl-ent')?.textContent).toBe('orders-api-prod');
    expect(steps[2].querySelector('code.cm-hl:not(.cm-hl-ent)')?.textContent).toBe('RelationService.getRelations');
    expect(steps[3].querySelector('.cm-hl-msg')?.textContent).toBe('"Herhangi bir kayıt bulunamadı"');
    expect(v.textContent).toBe(`Karar${CHAIN}`);
  });

  it('Karar: zincir değilse tek satır, yine vurgulu', async () => {
    await mountExplain('**Kök Neden ve Sonraki Adım**\norders-api-prod HTTP 503 döndü. Sonraki adım: havuzu büyüt.');
    const v = q('.cx-verdict')!;
    expect(v.querySelector('.cm-chain')).toBeNull();
    expect(v.querySelector('code.cm-hl-err')?.textContent).toBe('HTTP 503');
    expect(v.textContent).toBe('Kararorders-api-prod HTTP 503 döndü.');
  });

  it('gövde: madde etiketi, zincir, anahtar=değer, süre; kod ve bağlantı içine girilmez', async () => {
    await mountExplain(TEXT);
    const items = qa('.cm-md-wrap ul > li');
    expect(items.map(li => li.querySelector(':scope > .cm-li-label')?.textContent)).toEqual([
      'Operasyon:', 'Kritik hata noktası:', 'İş verisi:', 'En yavaş bileşen:', 'Kaynak:',
    ]);
    expect(items[0].querySelector('code.cm-hl-ent')?.textContent).toBe('orders-api-prod');
    expect(Array.from(items[0].querySelectorAll('code.cm-hl')).map(e => e.textContent)).toContain('POST /WEB/orders/assessment/decision');
    expect(items[1].querySelectorAll('ol.cm-chain > li').length).toBe(3);
    expect(items[1].textContent).toBe(ITEM);
    expect(items[2].querySelectorAll('code.cm-hl-kv').length).toBe(5);
    expect(Array.from(items[3].querySelectorAll('.cm-hl-dur')).map(e => e.textContent)).toEqual(['196 ms', '131 ms']);
    // model `kod`u ve bağlantı: zincir yok, içlerinde çip yok
    expect(items[4].querySelector('.cm-chain')).toBeNull();
    expect(items[4].querySelector('code:not(.cm-hl)')?.textContent).toBe('orders-api-prod → a → b');
    expect(items[4].querySelector('a')?.querySelector('.cm-hl')).toBeNull();
    expect(items[4].querySelectorAll('.cm-hl').length).toBe(0);
  });

  it('akış sürerken: yarım son satır akışa bölünmez, Karar yok; hiçbir kısmi girdi fırlatmaz', async () => {
    const line = `- ${ITEM}`;
    for (let n = 0; n <= line.length; n += 7) await mountExplain(line.slice(0, n), true);
    await mountExplain(line, true);
    expect(q('.cm-chain')).toBeNull();
    await mountExplain(`${line}\n`, true);
    expect(qa('.cm-chain__s').length).toBe(3);
  });
});

describe('RenderedMarkdown — rich yalnız açıkça istenince', () => {
  it('rich yok (runbook / not / diğer çağıranlar): çıktı eskisi gibi, vurgu ve akış yok', () => {
    const md = `- ${ITEM}\n\n${CHAIN}\n`;
    const plain = renderToStaticMarkup(<RenderedMarkdown text={md} />);
    expect(plain).not.toContain('cm-hl');
    expect(plain).not.toContain('cm-chain');
    expect(plain).not.toContain('cm-li-label');
    const rich = renderToStaticMarkup(<RenderedMarkdown text={md} rich />);
    expect(rich).toContain('cm-chain__s cm-chain__s--err');
    expect(rich).toContain('<code class="cm-hl cm-hl-ent">orders-api-prod</code>');
  });
});
