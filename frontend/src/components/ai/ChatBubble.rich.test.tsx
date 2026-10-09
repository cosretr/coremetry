// @vitest-environment jsdom
//
// v0.10.1137 — balonun "Claude gibi" çizim sözleşmesi, gerçek DOM ile:
// güvenli linkler (doğrulanmış / doğrulanmamış / göreli / javascript:),
// [n] atıf hapı ↔ kaynak eşlemesi, Kaynaklar listesi (>3 katlanır),
// "Nasıl cevapladım" açılırı (akarken açık, bitince kapanır), kopyala (düz
// metin), "Düşünüyor…", uyarı kutusu / details / dosya bloğu XSS'i, katlama
// eşiği, diff renkleri, satır numarası metne karışmaz.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
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
import type { ChatTurn, RagSource } from '@/lib/types';

let host: HTMLDivElement;
let root: Root;
const q = <T extends Element>(sel: string) => host.querySelector<T>(sel);
const qa = <T extends Element = HTMLElement>(sel: string) => Array.from(host.querySelectorAll<T>(sel));
const btn = (label: string) => qa<HTMLButtonElement>('button').find(b => (b.textContent ?? '').includes(label) || b.getAttribute('aria-label') === label);

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

const WIKI = 'https://devops.example.test/wiki?pagePath=%2FRunbook';
const JENKINS = 'https://jenkins.example.test/job/orders/';

describe('linkler — güvenlik', () => {
  it('allowedLinks\'teki çıplak URL tıklanır: yeni sekme, noopener noreferrer, tam adres metni', async () => {
    await mount(asst(`Pipeline: ${JENKINS}.`, { allowedLinks: [JENKINS] }));
    const a = q<HTMLAnchorElement>('a.cm-md-a')!;
    expect(a.getAttribute('href')).toBe(JENKINS);
    expect(a.getAttribute('target')).toBe('_blank');
    expect(a.getAttribute('rel')).toBe('noopener noreferrer');
    expect(a.textContent).toBe(JENKINS);
  });
  it('markdown [metin](url) izinliyse metin + host ipucu', async () => {
    await mount(asst(`[Jenkins işi](${JENKINS})`, { allowedLinks: [JENKINS] }));
    const a = q<HTMLAnchorElement>('a.cm-md-a')!;
    expect(a.textContent).toBe('Jenkins işi');
    expect(a.title).toContain('jenkins.example.test');
  });
  it('doğrulanmamış adres: link YOK, tam adres + "doğrulanmamış bağlantı"', async () => {
    await mount(asst('[tıkla](https://attacker.example.test/?q=gizli) ve https://other.example.test/x', { allowedLinks: [JENKINS] }));
    expect(qa('a').length).toBe(0);
    const spans = qa('.cm-md-unverified');
    expect(spans.map(s => s.textContent)).toEqual(['tıkla (https://attacker.example.test/?q=gizli)', 'https://other.example.test/x']);
    expect(spans.every(s => s.getAttribute('title') === 'doğrulanmamış bağlantı')).toBe(true);
  });
  it('kaynak host\'u allowlist; javascript:/data: asla link', async () => {
    await mount(asst('[sayfa](https://devops.example.test/x) [x](javascript:alert(1)) [y](data:text/html,z)', {
      sources: [{ doc: 'Wiki · Runbook', ref: WIKI, chunk: 1, score: 0.9 }],
    }));
    const as = qa<HTMLAnchorElement>('a.cm-md-a');
    expect(as.map(a => a.getAttribute('href'))).toEqual(['https://devops.example.test/x']);
    expect(host.innerHTML).not.toContain('javascript:');
    expect(host.innerHTML).not.toContain('href="data:');
  });
  it('göreli yol SPA linki (data-nav); görsel çizilmez', async () => {
    await mount(asst('[servis](/service?service=a) ![g](https://x.example.test/p.png)'));
    const a = q<HTMLAnchorElement>('a[data-nav]')!;
    expect(a.getAttribute('href')).toBe('/service?service=a');
    expect(q('img:not([src="/favicon.svg"])')).toBeNull();
  });
});

// v0.10.1137 inceleme (HIGH) — aynı-köken kılığında dış adres SPA linki olmaz.
describe('linkler — aynı-köken exfil', () => {
  const O = window.location.origin;
  const UP = O.replace(/\/\/([^/]+)/, (_m, h: string) => '//' + h.toUpperCase());
  it.each([
    `${O}//evil.example.com/?q=s`,
    `${UP}//evil.example.com/?q=s`,
    `${O}/\\evil.example.com`,
  ])('%s → tıklanmaz', async u => {
    await mount(asst(`bkz ${u} ve [tık](${u})`));
    expect(qa('a').length).toBe(0);
    expect(qa('.cm-md-unverified').length).toBeGreaterThan(0);
  });
  it.each(['/%5Cevil.example.com', '/%2F%2Fevil.example.com', '//evil.example.com'])('göreli %s → link yok, yalnız etiket', async u => {
    await mount(asst(`[tık](${u})`));
    expect(qa('a').length).toBe(0);
    expect(host.textContent).toContain('tık');
  });
  it('güvenli aynı köken mutlak adres SPA yoluna iner; yeni sekmede noreferrer', async () => {
    await mount(asst(`[problemler](${O}/problems?x=1)`));
    expect(q('a[data-nav]')?.getAttribute('href')).toBe('/problems?x=1');
  });
});

describe('linkler — yalnız host eşleşmesi', () => {
  it('etiket adresi gizlemez: host görünür, tam adres ipucunda; tam eşleşme etiketi korur', async () => {
    await mount(asst(`[rehber](https://devops.example.test/other/page) ve [runbook](${WIKI})`, {
      sources: [{ doc: 'Wiki · Runbook', ref: WIKI, chunk: 1, score: 0.9 }],
    }));
    const [hostOnly, exact] = qa<HTMLAnchorElement>('a.cm-md-a');
    expect(hostOnly.textContent).toBe('rehber (devops.example.test)');
    expect(hostOnly.title).toBe('https://devops.example.test/other/page');
    expect(exact.textContent).toBe('runbook');
  });
});

describe('dayanıklılık', () => {
  it("'>'.repeat(3000), 2000 kademeli liste ve '<details>' × 5000 atmadan çizilir", async () => {
    await mount(asst('>'.repeat(3000) + ' derin\n'));
    expect(host.textContent).toContain('derin');
    await mount(asst(Array.from({ length: 2000 }, (_, i) => `${'  '.repeat(i)}- m${i}`).join('\n') + '\n'));
    expect(host.textContent).toContain('m1999');
    await mount(asst('<details>\n'.repeat(5000) + 'son'));
    expect(q('details')).toBeTruthy();
  });
  it('çizimde hata → o balon düz metin (sohbet sökülmez)', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    await mount(asst('**kalın** metin', { sources: [null as unknown as RagSource] }));
    expect(q('[data-fallback="1"]')?.textContent).toBe('**kalın** metin');
  });
});

describe('atıf + Kaynaklar', () => {
  const sources = [
    { doc: 'Wiki · Runbook', ref: WIKI, chunk: 1, score: 0.9 },
    { doc: 'Wiki · Pipeline', ref: 'https://devops.example.test/wiki?pagePath=%2FPipeline', chunk: 1, score: 0.8 },
    { doc: 'kanal.pdf', chunk: 2, score: 0.5 },
    { doc: 'Wiki · Dört', ref: 'https://devops.example.test/4', chunk: 1, score: 0.4 },
  ];
  it('[n] → üst simge hap; ipucu kaynak başlığı + host, tık kaynağı yeni sekmede', async () => {
    await mount(asst('Yeniden başlat [1] ve izle [3]. Uydurma [9].', { sources }));
    const pills = qa('sup.cm-cite');
    expect(pills.length).toBe(2);
    const a = pills[0].querySelector('a')!;
    expect(a.getAttribute('href')).toBe(WIKI);
    expect(a.getAttribute('target')).toBe('_blank');
    expect(a.getAttribute('data-tip')).toBe('Runbook · devops.example.test');
    // href'siz kaynak: odaklanabilir span (ipucu klavyeyle de)
    const s = pills[1].querySelector('span')!;
    expect(s.getAttribute('tabindex')).toBe('0');
    expect(s.getAttribute('data-tip')).toBe('kanal.pdf');
    expect(host.textContent).toContain('[9]');
  });
  it('Kaynaklar listesi numaralı; >3 katlanır', async () => {
    await mount(asst('x', { sources }));
    const items = () => qa('.cm-sources__list li');
    expect(items().length).toBe(3);
    expect(items()[0].textContent).toContain('Runbook');
    expect(items()[0].textContent).toContain('devops.example.test');
    await act(async () => { btn('1 kaynak daha')!.click(); });
    expect(items().length).toBe(4);
    expect(host.textContent).not.toContain('📄 Kaynak');
  });
});

describe('Nasıl cevapladım', () => {
  const steps = { steps: ["kurum wiki'si", 'wiki_select'] };
  it('akarken AÇIK, bitince kendiliğinden KAPALI; elle açılır', async () => {
    await mount(asst('yazı', { ...steps, pending: true }));
    expect(btn('Nasıl cevapladım')!.getAttribute('aria-expanded')).toBe('true');
    expect(host.textContent).toContain('⚙ wiki_select');
    expect(btn('Nasıl cevapladım')!.textContent).toContain('2 adım');
    await mount(asst('yazı', { ...steps, pending: false }));
    expect(btn('Nasıl cevapladım')!.getAttribute('aria-expanded')).toBe('false');
    expect(host.textContent).not.toContain('⚙ wiki_select');
    await act(async () => { btn('Nasıl cevapladım')!.click(); });
    expect(host.textContent).toContain('⚙ wiki_select');
  });
});

describe('akış + eylemler', () => {
  it('ilk token öncesi "Düşünüyor…"; metin gelince imleç metnin içinde', async () => {
    await mount(asst('', { pending: true }));
    expect(q('.cm-thinking')?.textContent).toBe('Düşünüyor…');
    await mount(asst('merhaba', { pending: true }));
    expect(q('.cm-thinking')).toBeNull();
    expect(q('.cm-md-p .cm-ai-cursor')).toBeTruthy();
  });
  it('Kopyala düz metin yazar (işaretsiz, link adresiyle)', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    await mount(asst('**Özet:** tamam\n\n[Jenkins](https://ci.example.test/a) `make`', { exchangeId: 'x1' }));
    await act(async () => { btn('Cevabı kopyala')!.click(); });
    expect(writeText).toHaveBeenCalledWith('Özet:\ntamam\n\nJenkins (https://ci.example.test/a) make');
    expect(q('.cm-msg-actions')).toBeTruthy();
  });
});

describe('zengin bloklar — XSS metin kalır', () => {
  const X = '<img src=x onerror=alert(1)>';
  it('uyarı kutusu: ikon + tür + başlık (başlıkta HTML metin)', async () => {
    await mount(asst(`> [!UYARI] ${X}\n> dikkat et\n`));
    const c = q('.cm-callout.is-warning')!;
    expect(c.textContent).toContain('Uyarı');
    expect(c.textContent).toContain(X);
    expect(q('.cm-callout img')).toBeNull();
  });
  it('özet kutusu en üstte', async () => {
    await mount(asst('**Özet:** kısa cevap\n\nuzun açıklama'));
    expect(q('.cm-callout.is-summary')?.textContent).toContain('kısa cevap');
  });
  it('<details>: yalnız iki etiket; özetteki HTML metin', async () => {
    await mount(asst(`<details>\n<summary>${X}</summary>\n\n<b>gövde</b>\n</details>\n`));
    const d = q<HTMLDetailsElement>('details.cm-md-details')!;
    expect(d.querySelector('summary')!.textContent).toBe(X);
    expect(d.querySelector('img, b')).toBeNull();
    expect(d.textContent).toContain('<b>gövde</b>');
  });
  it('dosya bloğu: başlık metin, İndir + Kopyala; satır no metne karışmaz', async () => {
    await mount(asst('```yaml title="<img onerror=x>/app.yaml"\na: 1\nb: 2\n```\n'));
    expect(q('.cm-md-code-file')?.textContent).toBe('<img onerror=x>/app.yaml');
    expect(q('.cm-md-code img')).toBeNull();
    expect(btn('Kod bloğunu indir')).toBeTruthy();
    expect(q('.cm-md-code pre')?.textContent).toBe('a: 1\nb: 2');
    expect(q('.cm-md-code pre.is-num')).toBeTruthy();
  });
  it('26 satır katlanır "Tümünü göster (26 satır)"; 25 katlanmaz', async () => {
    const body = (n: number) => Array.from({ length: n }, (_, i) => `l${i}`).join('\n');
    await mount(asst('```\n' + body(26) + '\n```\n'));
    expect(qa('.cm-cl').length).toBe(25);
    await act(async () => { btn('Tümünü göster (26 satır)')!.click(); });
    expect(qa('.cm-cl').length).toBe(26);
    await mount(asst('```\n' + body(25) + '\n```\n'));
    expect(btn('Tümünü göster')).toBeUndefined();
  });
  it('diff renkleri + sar düğmesi', async () => {
    await mount(asst('```diff\n-eski\n+yeni\n```\n'));
    expect(qa('.cm-cl.is-del').length).toBe(1);
    expect(qa('.cm-cl.is-add').length).toBe(1);
    const wrap = qa<HTMLButtonElement>('.cm-md-code-acts button')[0];
    await act(async () => { wrap.click(); });
    expect(q('.cm-md-code pre.is-wrap')).toBeTruthy();
  });
  it('görev listesi salt-okunur kutular; [[Ctrl+C]] kbd; ~~üstü~~ del; tanım listesi', async () => {
    await mount(asst('- [x] bitti\n- [ ] kaldı\n\n[[Ctrl+C]] ~~eski~~\n\n**Servis:** checkout\n**Durum:** iyi\n'));
    const boxes = qa<HTMLInputElement>('input.cm-task');
    expect(boxes.map(b => b.checked)).toEqual([true, false]);
    expect(boxes.every(b => b.disabled)).toBe(true);
    expect(q('kbd.cm-kbd')?.textContent).toBe('Ctrl+C');
    expect(q('del')?.textContent).toBe('eski');
    expect(qa('dl.cm-md-dl dt').map(e => e.textContent)).toEqual(['Servis', 'Durum']);
  });
  it('iç içe liste ve 3 ile başlayan sıralı liste', async () => {
    await mount(asst('3. üç\n   - alt\n4. dört\n'));
    const ol = q<HTMLOListElement>('ol.cm-md-list')!;
    expect(ol.getAttribute('start')).toBe('3');
    expect(ol.querySelector('li ul.cm-md-list li')?.textContent).toBe('alt');
  });
});
