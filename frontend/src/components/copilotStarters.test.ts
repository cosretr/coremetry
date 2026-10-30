import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { TEAM_ASKS, teamCardQuestion } from './ai/capabilityHints';

// v0.9.652 — boş sohbetteki başlangıç çipleri (operatör isteği).
//
// v0.9.579'da operatör bunları "çıkar" demişti: SEKİZ sabit soru bir
// MENÜYDÜ, araç değil. Karar değişti ama gerekçe ölmedi — üç çip, ve
// üçü de operatörün adıyla istediği sorular.
//
// Bu testler v0.9.579'un dersini çiviliyor: liste büyürse menü hissi
// geri gelir.

const src = readFileSync(resolve(__dirname, './CopilotChat.tsx'), 'utf8')
  + readFileSync(resolve(__dirname, './ai/cosreAsk.ts'), 'utf8') // v0.10.1648
  + readFileSync(resolve(__dirname, './ai/drawerChat.ts'), 'utf8'); // v0.10.1660 — FOLLOWUPS taşındı; v0.10.1648 — bağlam/composer/karşılama kancaları ai/cosreAsk.ts'e taşındı (v1/v2 ortak)

function arrayLiteral(name: string): string[] {
  const i = src.indexOf(`const ${name} = [`);
  if (i < 0) return [];
  const body = src.slice(src.indexOf('[', i) + 1, src.indexOf('];', i));
  // SATIR bazlı: her eleman kendi satırında. Karakter bazlı bir regex,
  // "Takımımın exception'ları?" içindeki KESME İŞARETİNİ tırnak sanıp
  // çipi ikiye bölüyordu — testi yazarken bulundu.
  return body.split('\n')
    .map(l => l.trim())
    .filter(l => l && !l.startsWith('//'))
    .map(l => l.replace(/,\s*$/, ''))
    .filter(l => /^['"]/.test(l))
    .map(l => l.slice(1, -1));
}

// v0.10.1402 (operatör: "Alttaki takımımın servisleri exceptionları yukarıda
// olsun. Takım seçilebilsin.") — üç başlangıç sorusu gevşek çip satırından
// (CopilotChat STARTERS) Takımım kartına taşındı (capabilityHints TEAM_ASKS);
// aynı dersler orada çivili: takım seçilmemişken gönderilen cümleler.
describe('başlangıç çipleri (Takımım kartı)', () => {
  const starters = TEAM_ASKS.map(a => teamCardQuestion(a.kind, ''));

  it('gevşek satırda STARTERS listesi yok (tekrar yok)', () => {
    expect(src).not.toContain('const STARTERS = [');
  });

  // v0.9.579'un dersi: sekiz çip menüydü. Tavan BEŞ; her çip LLM'siz
  // yönlenir (copilot_starters_test.go / copilot_team_card_test.go pinler).
  it('en fazla BEŞ çip — menü hissi eşiği', () => {
    expect(starters.length).toBeGreaterThan(0);
    expect(starters.length).toBeLessThanOrEqual(5);
  });

  // v0.10.1249 (operatör) — "açık problemleri" yerine takım exception'ları;
  // filo geneli "son 1 saatteki log hataları" anlamsızdı. Geri gelmesin.
  it("takım exception'ları var; açık problemler ve filo log hataları yok", () => {
    const joined = starters.join(' | ').toLowerCase();
    expect(joined).toContain("exception'ları");
    expect(joined).not.toContain('açık problemleri');
    expect(joined).not.toContain('log hata');
  });

  // Operatörün adıyla istediği üç soru.
  it('operatörün istediği konuları kapsıyor', () => {
    expect(starters).toEqual([
      'Takımımın servisleri nasıl?',
      "Takımımın exception'ları?",
      'Takımımın en hatalı servisleri?',
    ]);
    expect(starters.join(' ').toLowerCase()).not.toContain('yavaş');
  });

  // Operatör: "son deploy etkisine gerek yok" (v0.9.652, v0.10.1402).
  it('deploy sorusu YOK', () => {
    expect(starters.join(' ').toLowerCase()).not.toContain('deploy');
  });
});

describe('follow-up listesi', () => {
  const followups = arrayLiteral('FOLLOWUPS');

  it('statik follow-up listesinden de deploy çıktı', () => {
    expect(followups.join(' ').toLowerCase()).not.toContain('deploy');
  });

  it('follow-up listesi boşalmadı', () => {
    expect(followups.length).toBeGreaterThan(0);
  });
});

// v0.10.1255 (operatör: "P1 yerine son 1 saatteki exception / hata sayısı") —
// karşılama /api/cosre/greeting'ten okur, yalnız karşılama ekrandayken; tıkı Exceptions.
describe('karşılama durum satırı', () => {
  it('cosreGreeting; P1 sorgusu yok; Exceptions linki', () => {
    expect(src).toContain("queryFn: () => api.cosreGreeting(env),");
    expect(src).not.toContain("prio: 'P1'");
    expect(src).toContain('<Link to={greetHref(env)}');
  });
  // v0.10.1278 — operatör 2026-10-17: CoSRE karşılaması tüm hatalı span'leri
  // sayıyordu ve global env'i yok sayıyordu. Env sorguya VE anahtara girer
  // (anahtarsız env = başka ortamın önbelleği), tanımlar title'da.
  it('global env sorguya ve queryKey\'e girer; tanım tooltip\'i', () => {
    expect(src).toContain("queryKey: ['cosre-greeting', env],");
    expect(src).toContain('title={greetTitle}');
    expect(src).not.toContain('errors1h');
  });
});
