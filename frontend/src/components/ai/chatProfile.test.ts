// @vitest-environment jsdom
//
// chatProfile.test.ts — v0.10.183 kablolama kapısı (çoklu model dilim C):
// seçim UI → useChatThread({profile}) → api.copilotChat(..., contextProfile)
// → gövde {context:{profile}} → sunucu WithProfile. Saf codec yok; zincir
// kaynak kapısıyla pinli ([[feedback-tested-but-unreachable]]).
//
// v0.10.1138 — seçici HİÇ çizilmiyordu: useCopilotEnabled önbelleği
// profiles/defaultProfile'ı düşürüyordu (normalizeCopilotConfig testi
// aşağıda). Seçici artık ModelPicker (kompakt "model ▾" menüsü), seçim
// kullanıcı başına kalıcı (chatProfileStore).
import { describe, it, expect, beforeEach } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { normalizeCopilotConfig } from './useCopilotEnabled';
import { activeProfileModel, readChatProfile, readChatProfileChoice, parseChatProfileChoice, resolveChatProfile, writeChatProfile, chatProfileKey } from './chatProfileStore';

const read = (rel: string) => readFileSync(resolve(__dirname, rel), 'utf8').replace(/^\s*\/\/.*$/gm, '');

describe('sohbet model profili kablolaması (v0.10.183)', () => {
  it('api.copilotChat profile parametresini gövdeye yazar', () => {
    const src = read('../../lib/api.ts');
    expect(src).toMatch(/contextProfile\?: string/);
    // v0.10.1648 — bağlam gövdesi lib/copilotChatContext'te (v1/v2 ortak kurucu).
    expect(src).toContain('profile: contextProfile,');
    expect(read('../../lib/copilotChatContext.ts')).toMatch(/\.\.\.\(f\.profile \? \{ profile: f\.profile \} : \{\}\)/);
  });
  it('useChatThread opts.profile → copilotChat son argüman', () => {
    const src = read('./useChatThread.ts');
    expect(src).toMatch(/profile\?: string;/);
    // v0.10.478 — konuşma kimliği (sunucu bağlam state'i) profilin ARDINDA geçer; profil hâlâ son-öncesi argüman.
    // v0.10.539 — son argümanlar artık sayfa bağlamı (page, pinnedPage); profil ondan önce.
    // v0.10.1134 — wiki href'leri; v0.10.1138 — yapısal kapsam + komut; v0.10.1150 — Derin düşün; CoSRE Faz 1 — en sonda deneme beyanı.
    expect(src).toMatch(/o\.toMs \|\| undefined, o\.profile \|\| undefined,[\s\S]{0,140}convIdRef\.current \|\| undefined,[\s\S]{0,160}o\.page \|\| undefined, o\.pinnedPage \|\| undefined,[\s\S]{0,120}wikiRefs\.length > 0 \? wikiRefs : undefined,[\s\S]{0,80}parsed\.scope, parsed\.command,[\s\S]{0,120}o\.deep \|\| undefined,[\s\S]{0,120}so\?\.attempt,[\s\S]{0,120}so\?\.explainAsk,[\s\S]{0,80}\(o\.code \|\| codeLocked\) \|\| undefined,[\s\S]{0,160}codeLocked \|\| undefined\)/); // v0.10.1400 — açıklama niyeti en sonda; v0.10.1461 — ardından Kod hapı; v0.10.1465 — en sonda kod kilidi
  });
  it("iki yüzey de seçimi hook'a geçirir; seçici ModelPicker (yalnız >1 profilde menü)", () => {
    for (const rel of ['../CopilotChat.tsx']) { // v0.10.1407 — AIDrawerBody silindi
      const src = read(rel) + read('./cosreAsk.ts'); // v0.10.1648 — bağlam/composer/karşılama kancaları ai/cosreAsk.ts'e taşındı (v1/v2 ortak)
      expect(src).toMatch(/profile: profile \|\| undefined,/);
      expect(src).toContain('<ModelPicker');
      expect(src).toContain('useChatProfile(');
      expect(src).toContain("onProfileRejected: () => setProfile('')");
    }
    expect(read('./ModelPicker.tsx')).toMatch(/profiles\.length < 2/);
  });
});

describe('normalizeCopilotConfig — profiller önbellekte KALIR (v0.10.1138 kök neden)', () => {
  it('profiles + defaultProfile taşınır; id\'siz satır elenir', () => {
    const c = normalizeCopilotConfig({
      enabled: true, model: 'small', wiki: true, defaultProfile: 'fast',
      profiles: [{ id: 'fast', label: 'Hızlı', model: 'small', description: 'kısa cevap' }, { id: 'deep', model: 'big' }, { id: '' }],
    });
    expect(c).toEqual({
      enabled: true, model: 'small', wiki: true, defaultProfile: 'fast',
      profiles: [{ id: 'fast', label: 'Hızlı', model: 'small', description: 'kısa cevap' }, { id: 'deep', model: 'big' }],
    });
  });
  it('profilsiz/boş yanıt eski şekilde', () => {
    expect(normalizeCopilotConfig({ enabled: true, model: 'm' })).toEqual({ enabled: true, model: 'm', wiki: false });
    expect(normalizeCopilotConfig(null)).toEqual({ enabled: false });
  });
  it('useCopilotConfig önbelleği normalizeCopilotConfig\'ten geçer', () => {
    expect(read('./useCopilotEnabled.ts')).toContain('cached = normalizeCopilotConfig(c)');
  });
});

describe('chatProfileStore — kullanıcı başına kalıcılık', () => {
  const profiles = [{ id: 'fast', model: 'small' }, { id: 'deep', model: 'big', description: 'Derin' }];
  beforeEach(() => { try { window.localStorage.clear(); } catch { /* yok */ } });
  it('yazılan seçim aynı kullanıcıya geri gelir, başka kullanıcıya gelmez', () => {
    writeChatProfile('u-1', 'deep');
    expect(readChatProfile('u-1')).toBe('deep');
    expect(readChatProfile('u-2')).toBe('');
    expect(chatProfileKey('u-1')).toBe('cosre.chatProfile.u-1');
    writeChatProfile('u-1', '');
    expect(readChatProfile('u-1')).toBe('');
  });
  it('izinli listede olmayan kayıtlı seçim varsayılana düşer', () => {
    expect(resolveChatProfile('deep', profiles)).toBe('deep');
    expect(resolveChatProfile('gone', profiles)).toBe('');
    expect(resolveChatProfile('deep', [profiles[0]])).toBe(''); // tek seçenek: seçici yok
  });
  // v0.10.1448 (operatör-bildirimli) — admin varsayılanı değiştirince kayıtlı
  // seçim bayatlar: kullanıcı yeni varsayılana döner, eski model kalmaz.
  it('seçim o anki varsayılanla damgalanır; varsayılan değişince düşer', () => {
    writeChatProfile('u-3', 'deep', 'fast');
    expect(readChatProfileChoice('u-3')).toEqual({ id: 'deep', def: 'fast' });
    expect(parseChatProfileChoice(JSON.stringify({ id: 'deep', def: 'fast' }))).toEqual({ id: 'deep', def: 'fast' }); // depo biçimi
    expect(resolveChatProfile('deep', profiles, 'fast', 'fast')).toBe('deep'); // varsayılan aynı → seçim kalır
    expect(resolveChatProfile('deep', profiles, 'fast2', 'fast')).toBe('');   // varsayılan değişti → varsayılana dön
    expect(resolveChatProfile('deep', profiles, 'fast', undefined)).toBe(''); // damgasız (eski kayıt) → bayat
    expect(resolveChatProfile('deep', profiles, 'deep', 'fast')).toBe('');    // yeni varsayılan zaten seçilen → ''
    expect(resolveChatProfile('deep', profiles, undefined, 'fast')).toBe('deep'); // config'de varsayılan yok → eski kural
    expect(resolveChatProfile('', profiles, 'fast', 'fast')).toBe('');
  });
  it('eski düz-dize kayıt okunur ama damgasızdır; bozuk JSON varsayılana düşer', () => {
    const legacy = parseChatProfileChoice('deep'); // v0.10.1138 düz-dize kayıt
    expect(legacy).toEqual({ id: 'deep' });
    expect(resolveChatProfile(legacy.id, profiles, 'fast', legacy.def)).toBe('');
    expect(parseChatProfileChoice('{bozuk')).toEqual({ id: '' });
    expect(parseChatProfileChoice('{"id":7}')).toEqual({ id: '' });
    expect(parseChatProfileChoice(null)).toEqual({ id: '' });
    writeChatProfile('u-4', 'deep', 'fast');
    writeChatProfile('u-4', '', 'fast'); // varsayılan seçimi damgasız silinir
    expect(readChatProfileChoice('u-4')).toEqual({ id: '' });
    expect(readChatProfile('u-4')).toBe('');
  });
  it('CopilotChat seçimi config varsayılanıyla damgalar (useChatProfile defaultProfile alır)', () => {
    expect(read('../CopilotChat.tsx') + read('./cosreAsk.ts')).toMatch(/useChatProfile\(user\?\.id, profiles, defaultProfile, model\)/);
    expect(read('./chatProfileStore.ts')).toMatch(/writeChatProfile\(user, id, id \? defaultProfile : undefined\)/);
  });
  it('rozet etkin modeli gösterir (seçim > varsayılan profil > genel model)', () => {
    expect(activeProfileModel('deep', profiles, 'fast', 'x')).toBe('big');
    expect(activeProfileModel('', profiles, 'fast', 'x')).toBe('small');
    expect(activeProfileModel('', [], undefined, 'x')).toBe('x');
  });
});
