// @vitest-environment jsdom
//
// v0.10.1346 (operatör-bildirimli, /cosre ekran görüntüsü) — yüklenen belge
// satırları tablosu (internal/api/chat_doc_rows.go docRowsMarkdown) balonda:
// kısa başlıklı kolonlarda "LOGIN_14300.aspx" L/O/GI/N_/…, "1526" 152/6,
// "AKTİF" AKTİ/F diye kırılıyordu. Kısa değer hücresi `.cm-td-nw` taşır,
// uzun kimlik yalnız ayraç sonrası <wbr> alır, sayı kolonu `.num`.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router';
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

const DOC = [
  'Yüklenen belgelerde **login** için 2 satır bulundu:',
  '',
  '**Fonksiyon Envanteri-061026_v3.xlsx › Sheet1**',
  '',
  '| Satır | Ad | Banka Fonksiyon ID | Fonksiyon ID | Host Fonksiyon Kodu | Fonksiyon Adı | Statü | SLA Kanal Adı |',
  '|---|---|---|---|---|---|---|---|',
  '| 12 | LOGIN_14300.aspx | 1526 | 14300 | LGN01 | Müşteri internet şubesine kullanıcı adı ve şifre ile giriş yapar | AKTİF | İnternet Şube |',
  '| 13 | LOGIN_14301.aspx | 1527 | 14301 | LGN02 | **Giriş** | KAPALI | Mobil |',
  '',
].join('\n');

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); });

async function mount(text: string) {
  await act(async () => {
    root.render(
      <MemoryRouter>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <ChatBubble turn={{ role: 'assistant', text }} />
        </QueryClientProvider>
      </MemoryRouter>,
    );
  });
}

const cell = (txt: string) =>
  Array.from(host.querySelectorAll('table.cm-md-table tbody td')).find(td => td.textContent === txt) as HTMLElement | undefined;

describe('belge satırları tablosu — kısa değer bölünmez (v0.10.1346)', () => {
  it('"AKTİF", "KAPALI", "1526" hücreleri nowrap sınıfı taşır', async () => {
    await mount(DOC);
    for (const v of ['AKTİF', 'KAPALI', '1526', '12', 'LGN01', 'İnternet Şube']) {
      const td = cell(v);
      expect(td, v).toBeTruthy();
      expect(td!.classList.contains('cm-td-nw'), v).toBe(true);
      expect(td!.querySelector('wbr'), v).toBeNull();
    }
  });

  it('"LOGIN_14300.aspx" yalnız ayraç sonrası <wbr> alır, metin aynen kalır', async () => {
    await mount(DOC);
    const td = cell('LOGIN_14300.aspx')!;
    expect(td).toBeTruthy();
    expect(td.querySelectorAll('wbr').length).toBe(2);
    expect(td.innerHTML).toContain('LOGIN_<wbr>14300.<wbr>aspx');
  });

  it('uzun düzyazı sarar (nowrap yok); işaretli hücre yine kalın çizilir', async () => {
    await mount(DOC);
    const prose = cell('Müşteri internet şubesine kullanıcı adı ve şifre ile giriş yapar')!;
    expect(prose.classList.contains('cm-td-nw')).toBe(false);
    const bold = cell('Giriş')!;
    expect(bold.querySelector('b, strong')).toBeTruthy();
    expect(bold.classList.contains('cm-td-nw')).toBe(true);
  });

  it('sayı kolonu (Satır, Banka Fonksiyon ID) başlığıyla birlikte `.num`; metin kolonu değil', async () => {
    await mount(DOC);
    const th = Array.from(host.querySelectorAll('table.cm-md-table thead th'));
    expect(th[0].classList.contains('num')).toBe(true);
    expect(th[2].classList.contains('num')).toBe(true);
    expect(th[1].classList.contains('num')).toBe(false);
    expect(cell('1526')!.classList.contains('num')).toBe(true);
    expect(cell('AKTİF')!.classList.contains('num')).toBe(false);
  });

  it('tablo yatay kaydırma kabında', async () => {
    await mount(DOC);
    expect(host.querySelector('.table-wrap.cm-md-tw > table.cm-md-table')).toBeTruthy();
  });
});
