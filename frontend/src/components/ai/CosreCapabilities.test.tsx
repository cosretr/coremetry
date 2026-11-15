// @vitest-environment jsdom
//
// v0.10.1128 (operatör) — boş CoSRE sohbetinin "neler yapabilirim" ipucu:
//   (1) saf: wiki satırı yalnız `wiki` bayrağıyla ve en başta; `|` imleç işareti;
//   (2) EN/TR kataloglarında her anahtar var, metinler dile göre (EN gelecekteki
//       sohbet-geneli i18n için katalogda durur);
//   (3) çekmecede: ipucu boş sohbette görünür; wiki kapalıyken wiki satırı ve
//       wiki çipi YOK, açıkken VAR; satıra tıklamak composer'ı doldurur ama
//       GÖNDERMEZ (copilotChat çağrılmaz); wiki çipi "@wiki " doldurur (v0.10.1378);
//   (4) prod hatası (v0.10.1130): sohbet yüzeyi Türkçe-öncelikli — UI dili EN
//       iken de ipuçları, title'ı, wiki çipi ve doldurulan örnekler TÜRKÇE.
//   /cosre sayfası aynı bileşeni çizer — pages/CoSRE.test.tsx.
//   (5) v0.10.1390 (operatör: "filo geneli ve böyle birçok servis yazmasın,
//       search kısmı çıksa yeter") — servis/operasyon satırlarında çip ve
//       "filo geneli" yok; her zaman görünen arama kutusu, seçim HEMEN gönderir.
//   (6) v0.10.1402 — Takımım kartı her zaman EN BAŞTA ('team'), deploy satırı
//       yok (ayrıntı: CosreTeamCard.test.tsx).
//   (7) v0.10.1404 (operatör: "P1 yerine en son exception'lara yönlendirsin.")
//       — P1 kartı yok; yerinde "Son exception gruplarına bakabilirim": grup
//       çipi TEK bir sohbet içi açıklama turu gönderir (explainAsk {kind:
//       'exception', id: <parmak izi>}), "Tüm son exception'lar" takım
//       exception sorusunu (LLM'siz yönlenir: TestStarterQuestionsRoute).
//   (8) v0.10.1515 (operatör 2026-10-24) — "Nereye bakman gerektiğini…" satırı
//       YOK; exceptions kartında "filo geneli" / grup çipleri / "Tüm son
//       exception'lar" YOK (sunucu eski şekilde grup gönderse bile); yerinde
//       kullanıcının takımıyla dolu TeamPicker; takım seçmek sohbet turu
//       GÖNDERMEDEN /problems?owner=<takım> (ya da sre) adresine gider.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

import { api } from '@/lib/api';
import { setUserLang, t } from '@/lib/i18n';
import { AuthProvider } from '@/components/AuthProvider';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { CopilotChat } from '@/components/CopilotChat';
import { __resetCopilotEnabledCache } from './useCopilotEnabled';
import {
  capabilityItems, splitCaret, tCosre, COSRE_LANG, serviceHealthQuestion, operationErrorQuestion,
} from './capabilityHints';
import { readExceptionsTeamFilter } from '@/lib/exceptionsTeamHref';
import type { CopilotStarterGroup } from '@/lib/types';

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  __resetCopilotEnabledCache();
  // Dil seçimi localStorage'da; Node'un yerleşik localStorage'ı jsdom'unkini
  // gölgeliyor (traceAskCosre.contract.test.tsx emsali). Marka isteği ağa çıkmaz.
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, String(v)); },
    removeItem: (k: string) => { mem.delete(k); },
  });
  vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')));
  vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.test', role: 'viewer', firstName: 'Ada' } as never);
  vi.spyOn(api, 'problemsCount').mockResolvedValue({ count: 0 } as never);
  vi.spyOn(api, 'problems').mockResolvedValue({ items: [], total: 0, truncated: false } as never);
  vi.spyOn(api, 'aiConversations').mockResolvedValue([] as never);
  vi.spyOn(api, 'copilotStarters').mockResolvedValue({ groups: [] } as never);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  setUserLang(null);
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('capabilityHints — saf', () => {
  it('wiki satırı yalnız bayrakla ve en başta', () => {
    // v0.10.1515 — navigate satırı yok; exceptions satırı koşulsuz, en sonda.
    expect(capabilityItems(false).map(i => i.kind)).toEqual(['team', 'service', 'operation', 'exceptions']);
    expect(capabilityItems(true).map(i => i.kind)).toEqual(['team', 'wiki', 'service', 'operation', 'exceptions']);
  });

  it('splitCaret: | imleç yeri, işaretsiz metinde imleç sonda', () => {
    expect(splitCaret('@| svc')).toEqual({ text: '@ svc', caret: 1 });
    expect(splitCaret('| op')).toEqual({ text: ' op', caret: 0 });
    expect(splitCaret('wikide ')).toEqual({ text: 'wikide ', caret: 7 });
  });

  it('EN/TR: her anahtar iki katalogda da var ve dile göre farklı', () => {
    const keys = capabilityItems(true).flatMap(i => [i.labelKey, i.promptKey])
      .concat(['cosre.cap.aria', 'cosre.cap.tryHint', 'cosre.chip.wiki']);
    for (const k of keys) {
      expect(t(k, 'en'), k).not.toBe(k);
      expect(t(k, 'tr'), k).not.toBe(k);
      expect(t(k, 'tr'), k).not.toBe(t(k, 'en'));
    }
    expect(t('cosre.cap.wiki', 'tr')).toContain('wiki');
    expect(t('cosre.cap.service', 'tr')).toContain('servis adını yaz');
    expect(t('cosre.cap.service', 'en')).toContain('service name');
    // v0.10.1378 — çipin metni kapsam belirteci: iki dilde aynı `@wiki `.
    expect(t('cosre.chip.wiki.prompt', 'tr')).toBe('@wiki ');
    expect(t('cosre.chip.wiki.prompt', 'en')).toBe('@wiki ');
  });

  it('tCosre: UI dilinden bağımsız, karşılamanın dili (TR)', () => {
    expect(COSRE_LANG).toBe('tr');
    setUserLang('en');
    expect(tCosre('cosre.cap.wiki')).toBe(t('cosre.cap.wiki', 'tr'));
    expect(tCosre('cosre.cap.tryHint')).toBe('Kutuya örnek bir soru yazar — düzenleyip gönder');
    expect(tCosre('cosre.chip.wiki.prompt')).toBe('@wiki ');
  });
});

// v0.10.1382 — gruplu başlangıç seçenekleri (sentetik adlar). v0.10.1515 —
// sunucu artık grup üretmez; ESKİ şekilde gelen yanıt (önbellek / sürüm geçişi)
// da çip çizdirmez: bu grup o durumun fikstürü.
const ORA_FP = 'ora:00000000000000a1';
const LEGACY_GROUPS: CopilotStarterGroup[] = [
  { kind: 'exceptions', fleet: true, items: [
    { chip: 'SQLIntegrityConstraintViolationException · orders-api', question: 'q1', kind: 'exception', explainAsk: { kind: 'exception', id: ORA_FP, src: 'starter' } },
    { chip: '503 · batch-manager', question: 'q2', kind: 'exception', explainAsk: { kind: 'exception', id: 'fp-batch-0001', src: 'starter' } },
  ] },
];

describe('capabilityHints — başlangıç cümleleri (saf)', () => {
  it('v0.10.1390 — arama kutularının cümleleri (LLM\'siz yönlenir: TestStarterQuestionsRoute)', () => {
    expect(serviceHealthQuestion('orders-api')).toBe('orders-api sağlığı nasıl?');
    expect(operationErrorQuestion('INSURANCE_DOC_PDF')).toBe('INSURANCE_DOC_PDF operasyonu neden hata veriyor?');
    // Şablon satırının doldurduğu kalıpla aynı.
    expect(splitCaret(tCosre('cosre.cap.operation.prompt')).text).toBe(operationErrorQuestion(''));
  });

  it('istemci URL\'si sorgu dizesini ? ile ayırır; v0.10.1390 — range_s yok', async () => {
    const f = vi.fn().mockResolvedValue(new Response(JSON.stringify({ groups: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', f);
    vi.mocked(api.copilotStarters).mockRestore(); // beforeEach casusu: gerçek istemci
    await api.copilotStarters('prod');
    const url = String(f.mock.calls[0][0]);
    expect(url).toContain('/api/copilot/starters?');
    expect(url).toContain('env=prod');
    expect(url).not.toContain('range_s');
  });

  it('v0.10.1390 — operasyon araması istemcisi', async () => {
    const f = vi.fn().mockResolvedValue(new Response(JSON.stringify({ names: [], total: 0 }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', f);
    await api.copilotOperationSearch('INSU');
    const url = String(f.mock.calls[0][0]);
    expect(url).toContain('/api/copilot/operation-search?');
    expect(url).toContain('q=INSU');
    expect(url).toContain('limit=20');
  });

  it('anahtarlar iki katalogda da var; kaldırılan anahtarlar yok', () => {
    for (const k of ['cosre.cap.exceptions', 'cosre.cap.exceptions.prompt', 'cosre.cap.exceptions.searchTitle',
      'cosre.cap.choices', 'cosre.cap.search.service', 'cosre.cap.search.operation', 'cosre.cap.search.team']) {
      expect(t(k, 'en'), k).not.toBe(k);
      expect(t(k, 'tr'), k).not.toBe(k);
      expect(t(k, 'tr'), k).not.toBe(t(k, 'en'));
    }
    expect(tCosre('cosre.cap.search.service')).toBe('Servis ara…');
    expect(tCosre('cosre.cap.search.operation')).toBe('Operasyon ara…');
    expect(tCosre('cosre.cap.exceptions')).toBe('Takımının son exception\'larına bakabilirim'); // v0.10.1472
    for (const gone of ['cosre.cap.pickService', 'cosre.cap.pickService.placeholder', 'cosre.cap.deploy', 'cosre.cap.deploy.prompt',
      'cosre.cap.p1', 'cosre.cap.p1.prompt',
      // v0.10.1515 — navigate satırı ve exceptions kartının filo çipleri
      'cosre.cap.navigate', 'cosre.cap.navigate.prompt', 'cosre.cap.exceptions.all', 'cosre.cap.fleet', 'cosre.cap.fleet.title']) {
      expect(t(gone, 'tr'), gone).toBe(gone);
      expect(t(gone, 'en'), gone).toBe(gone);
    }
  });
});

// v0.10.1515 — gezinme gözlemcisi (takım seçimi → Exceptions sayfası).
let here = '';
function Where() {
  const l = useLocation();
  here = l.pathname + l.search;
  return null;
}

function shell() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <MemoryRouter initialEntries={['/services']}>
      <Where />
      <QueryClientProvider client={qc}>
        <AuthProvider>
          <ConfirmProvider>
            <CopilotChat />
          </ConfirmProvider>
        </AuthProvider>
      </QueryClientProvider>
    </MemoryRouter>
  );
}

async function openDrawer() {
  await act(async () => { root.render(shell()); });
  await act(async () => { await new Promise(r => setTimeout(r, 10)); });
  const fab = document.querySelector<HTMLButtonElement>('.cm-ai-fab');
  if (!fab) throw new Error('FAB çizilmedi');
  await act(async () => { fab.click(); });
  await act(async () => { await new Promise(r => setTimeout(r, 10)); });
}

const rows = () => Array.from(document.body.querySelectorAll<HTMLButtonElement>('button[data-cap]'));
const textarea = () => document.body.querySelector<HTMLTextAreaElement>('textarea');

describe('CopilotChat çekmecesi — yetenek ipucu', () => {
  it('wiki kapalı: üç satır, wiki satırı/çipi yok', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm' });
    setUserLang('tr');
    await openDrawer();
    expect(rows().map(b => b.dataset.cap)).toEqual(['team', 'service', 'operation', 'exceptions']);
    expect(document.body.querySelector('[data-chip="wiki"]')).toBeNull();
    expect(document.body.textContent).toContain('Hata veren teknik operasyonları inceleyebilirim');
    expect(document.body.textContent).not.toContain('Nereye bakman gerektiğini'); // v0.10.1515
  });

  it('wiki açık: wiki satırı + çip; tıklama doldurur, GÖNDERMEZ', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm', wiki: true });
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    setUserLang('tr');
    await openDrawer();
    expect(rows().map(b => b.dataset.cap)).toEqual(['team', 'wiki', 'service', 'operation', 'exceptions']);
    expect(document.body.textContent).toContain('Kurum wiki’sinde arayabilirim');

    const wikiRow = rows().find(b => b.dataset.cap === 'wiki')!;
    await act(async () => { wikiRow.click(); });
    expect(textarea()?.value).toBe('@wiki payments cache refresh en sık karşılaşılan hatalar nelerdir?');

    const svcRow = rows().find(b => b.dataset.cap === 'service')!;
    await act(async () => { svcRow.click(); });
    expect(textarea()?.value.startsWith('@ servisinin')).toBe(true);

    const chip = document.body.querySelector<HTMLButtonElement>('[data-chip="wiki"]');
    expect(chip?.textContent).toContain('Wikide ara');
    await act(async () => { chip!.click(); });
    expect(textarea()?.value).toBe('@wiki ');
    expect(send).not.toHaveBeenCalled();
    // Hâlâ boş sohbet: ipucu yerinde (gönderilmedi).
    expect(rows().length).toBe(5);
  });

  it('UI dili EN: ipuçları, title, wiki çipi ve örnekler yine TÜRKÇE (karşılamayla aynı dil)', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm', wiki: true });
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    setUserLang('en');
    await openDrawer();
    const text = document.body.textContent ?? '';
    expect(text).toContain('Sana nasıl yardımcı olabilirim?');
    expect(text).toContain('Kurum wiki’sinde arayabilirim');
    expect(text).toContain('Hata veren teknik operasyonları inceleyebilirim');
    expect(text).not.toContain('I can search the company wiki');
    expect(text).not.toContain('type the operation name');
    for (const b of rows()) expect(b.title).toBe('Kutuya örnek bir soru yazar — düzenleyip gönder');
    expect(document.body.querySelector('[role="list"].cosre-cap')?.getAttribute('aria-label')).toBe('CoSRE neler yapabilir');

    await act(async () => { rows().find(b => b.dataset.cap === 'wiki')!.click(); });
    expect(textarea()?.value).toBe('@wiki payments cache refresh en sık karşılaşılan hatalar nelerdir?');
    await act(async () => { rows().find(b => b.dataset.cap === 'operation')!.click(); });
    expect(textarea()?.value).toBe(' operasyonu neden hata veriyor?');
    await act(async () => { rows().find(b => b.dataset.cap === 'exceptions')!.click(); });
    expect(textarea()?.value).toBe('Takımımın exception\'ları?');

    const chip = document.body.querySelector<HTMLButtonElement>('[data-chip="wiki"]');
    expect(chip?.textContent).toContain('Wikide ara');
    expect(chip?.textContent).not.toContain('Search the wiki');
    await act(async () => { chip!.click(); });
    expect(textarea()?.value).toBe('@wiki ');
    expect(send).not.toHaveBeenCalled();
  });
});

describe('CopilotChat çekmecesi — v0.10.1382 / v0.10.1390 başlangıçlar', () => {
  const choices = (kind: string) => Array.from(document.body.querySelectorAll<HTMLButtonElement>(`[data-cap-choices="${kind}"] button`));
  const search = (kind: string) => document.body.querySelector<HTMLInputElement>(`[data-cap-search="${kind}"] input`);
  const setVal = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
  const type = async (input: HTMLInputElement, v: string) => {
    await act(async () => {
      setVal.call(input, v);
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
  };
  const key = async (input: HTMLInputElement, k: string) => {
    await act(async () => { input.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true })); });
  };
  // Debounce (180 ms) + cevap + setTimeout(onEnter) için bekleme.
  const settle = async (ms = 260) => { await act(async () => { await new Promise(r => setTimeout(r, ms)); }); };
  const lastSent = (send: ReturnType<typeof vi.spyOn>) => {
    const msgs = (send.mock.calls[send.mock.calls.length - 1] as unknown[])[0] as { role: string; text: string }[];
    return msgs[msgs.length - 1].text;
  };

  it('v0.10.1515 — exceptions kartı: filo çipleri YOK (eski şekilli yanıtta bile), takım seçici kullanıcının takımıyla dolu', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm' });
    vi.spyOn(api, 'copilotStarters').mockResolvedValue({ groups: LEGACY_GROUPS, team: 'orders-team' } as never);
    const meta = vi.spyOn(api, 'servicesMetadata').mockResolvedValue({} as never);
    setUserLang('en');
    await openDrawer();
    expect(rows().map(b => b.dataset.cap)).toEqual(['team', 'service', 'operation', 'exceptions']);
    const body = document.body.textContent ?? '';
    expect(body).toContain('Takımının son exception\'larına bakabilirim');
    for (const gone of ['filo geneli', 'SQLIntegrityConstraintViolationException', '503 · batch-manager', 'Tüm son exception\'lar',
      'Nereye bakman gerektiğini', 'P1 problemlerinin', 'başka bir servis', 'teknik hataları']) {
      expect(body, gone).not.toContain(gone);
    }
    expect(document.body.querySelector('[data-starter]')).toBeNull();
    expect(document.body.querySelector('[data-cap-choices="exceptions"]')).toBeNull();
    expect(document.body.querySelector('[data-cap-choices="service"]')).toBeNull();
    expect(document.body.querySelector('[data-cap-choices="operation"]')).toBeNull();
    // UI dili EN iken de sohbetin dili (TR); Takımım kartıyla aynı seçici.
    expect(search('service')?.placeholder).toBe('Servis ara…');
    expect(search('operation')?.placeholder).toBe('Operasyon ara…');
    const exc = search('exceptions')!;
    expect(exc.placeholder).toBe('Takım ara…');
    expect(exc.value).toBe('orders-team');
    expect(search('team')?.value).toBe('orders-team');
    // Kart açılırken servis kataloğu ÇEKİLMEZ (yalnız seçimde).
    expect(meta).not.toHaveBeenCalled();
  });

  it('v0.10.1515 — takım seçimi Exceptions sayfasına takım süzgeçli GİDER, sohbet turu göndermez; sayfa okuyucusu takımı geri okur', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm' });
    vi.spyOn(api, 'copilotStarters').mockResolvedValue({ groups: [], team: 'orders-team' } as never);
    const names = vi.spyOn(api, 'copilotScopeNames').mockResolvedValue({ names: ['Platform-SRE'] } as never);
    vi.spyOn(api, 'servicesMetadata').mockResolvedValue({
      'orders-api': { service: 'orders-api', ownerTeam: 'orders-team', sreTeam: 'platform-sre' },
    } as never);
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    await openDrawer();
    expect(here).toBe('/services');
    const input = search('exceptions')!;
    await type(input, 'plat');
    await settle();
    expect(names).toHaveBeenCalledWith('team', 'plat');
    await key(input, 'ArrowDown');
    await key(input, 'Enter');
    await settle(40);
    expect(send).not.toHaveBeenCalled();
    // Yalnız SRE ekseninde geçen takım → ?sre=, katalogdaki yazımla.
    expect(here).toBe('/problems?sre=platform-sre&s_exception-inbox-v2=lastSeen.desc');
    const sp = new URLSearchParams(here.slice(here.indexOf('?') + 1));
    expect(readExceptionsTeamFilter(sp)).toEqual({ owner: '', sre: 'platform-sre' });
  });

  it('v0.10.1515 — önseçili takımda Enter: sunucuda birebir doğrulanınca ?owner= ile gider', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm' });
    vi.spyOn(api, 'copilotStarters').mockResolvedValue({ groups: [], team: 'orders-team' } as never);
    vi.spyOn(api, 'copilotScopeNames').mockResolvedValue({ names: ['orders-team'] } as never);
    vi.spyOn(api, 'servicesMetadata').mockResolvedValue({
      'orders-api': { service: 'orders-api', ownerTeam: 'orders-team' },
    } as never);
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    await openDrawer();
    const input = search('exceptions')!;
    await key(input, 'Enter');
    await settle(40);
    expect(send).not.toHaveBeenCalled();
    expect(here).toBe('/problems?owner=orders-team&s_exception-inbox-v2=lastSeen.desc');
    expect(readExceptionsTeamFilter(new URL(here, 'http://x').searchParams).owner).toBe('orders-team');
  });

  it('servis kutusu: listeden seçim HEMEN gönderir "<svc> sağlığı nasıl?"', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm' });
    const names = vi.spyOn(api, 'serviceNames').mockResolvedValue({ names: ['orders-api'], total: 1, hasMore: false } as never);
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    await openDrawer();
    const input = search('service')!;
    await type(input, 'ord');
    await settle();
    expect(names).toHaveBeenCalledWith('ord', 200);
    await key(input, 'ArrowDown');
    await key(input, 'Enter');
    await settle(20);
    expect(send).toHaveBeenCalledTimes(1);
    expect(lastSent(send)).toBe('orders-api sağlığı nasıl?');
  });

  it('servis kutusu: Enter — birebir ad sunucuda doğrulanınca gönderir, değilse cümleyi DOLDURUR', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm' });
    vi.spyOn(api, 'serviceNames').mockResolvedValue({ names: ['orders-api', 'orders-worker'], total: 2, hasMore: false } as never);
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    await openDrawer();
    let input = search('service')!;
    await type(input, 'orders');
    await key(input, 'Enter');
    await settle(20);
    expect(send).not.toHaveBeenCalled();
    expect(textarea()?.value).toBe('orders sağlığı nasıl?');
    expect(input.value).toBe('');

    input = search('service')!;
    await type(input, 'orders-api');
    await key(input, 'Enter');
    await settle(20);
    expect(send).toHaveBeenCalledTimes(1);
    expect(lastSent(send)).toBe('orders-api sağlığı nasıl?');
  });

  it('operasyon kutusu: <2 karakter istek atmaz; seçim HEMEN gönderir "<OP> operasyonu neden hata veriyor?"', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm' });
    const opSearch = vi.spyOn(api, 'copilotOperationSearch').mockResolvedValue({ names: ['INSURANCE_DOC_PDF'], total: 1 });
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    await openDrawer();
    const input = search('operation')!;
    await settle();
    expect(opSearch).not.toHaveBeenCalled(); // boş alan: liste yok, istek yok
    await type(input, 'I');
    await settle();
    expect(opSearch).not.toHaveBeenCalled();
    await type(input, 'INSU');
    await settle();
    expect(opSearch).toHaveBeenCalledWith('INSU');
    await key(input, 'ArrowDown');
    await key(input, 'Enter');
    await settle(20);
    expect(send).toHaveBeenCalledTimes(1);
    expect(lastSent(send)).toBe('INSURANCE_DOC_PDF operasyonu neden hata veriyor?');
  });

  it('başlık hâlâ şablonu doldurur (göndermez); istek yalnız env taşır', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm' });
    const st = vi.spyOn(api, 'copilotStarters').mockResolvedValue({ groups: [] } as never);
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    await openDrawer();
    expect(st).toHaveBeenCalled();
    expect(typeof st.mock.calls[0][0]).toBe('string');
    expect(rows().map(b => b.dataset.cap)).toEqual(['team', 'service', 'operation', 'exceptions']);
    expect(search('service')).not.toBeNull();
    expect(search('operation')).not.toBeNull();
    await act(async () => { rows().find(b => b.dataset.cap === 'operation')!.click(); });
    expect(textarea()?.value).toBe(' operasyonu neden hata veriyor?');
    expect(send).not.toHaveBeenCalled();
  });
});
