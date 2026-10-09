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
import { activeProfileModel, readChatProfile, resolveChatProfile, writeChatProfile, chatProfileKey } from './chatProfileStore';

const read = (rel: string) => readFileSync(resolve(__dirname, rel), 'utf8').replace(/^\s*\/\/.*$/gm, '');

describe('sohbet model profili kablolaması (v0.10.183)', () => {
  it('api.copilotChat profile parametresini gövdeye yazar', () => {
    const src = read('../../lib/api.ts');
    expect(src).toMatch(/contextProfile\?: string/);
    expect(src).toMatch(/\.\.\.\(contextProfile \? \{ profile: contextProfile \} : \{\}\)/);
  });
  it('useChatThread opts.profile → copilotChat son argüman', () => {
    const src = read('./useChatThread.ts');
    expect(src).toMatch(/profile\?: string;/);
    // v0.10.478 — konuşma kimliği (sunucu bağlam state'i) profilin ARDINDA geçer; profil hâlâ son-öncesi argüman.
    // v0.10.539 — son argümanlar artık sayfa bağlamı (page, pinnedPage); profil ondan önce.
    // v0.10.1134 — wiki href'leri; v0.10.1138 — en sonda yapısal kapsam + komut.
    expect(src).toMatch(/o\.toMs \|\| undefined, o\.profile \|\| undefined,[\s\S]{0,140}convIdRef\.current \|\| undefined,[\s\S]{0,160}o\.page \|\| undefined, o\.pinnedPage \|\| undefined,[\s\S]{0,120}wikiRefs\.length > 0 \? wikiRefs : undefined,[\s\S]{0,80}parsed\.scope, parsed\.command\)/);
  });
  it("iki yüzey de seçimi hook'a geçirir; seçici ModelPicker (yalnız >1 profilde menü)", () => {
    for (const rel of ['./AIDrawerBody.tsx', '../CopilotChat.tsx']) {
      const src = read(rel);
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
  it('rozet etkin modeli gösterir (seçim > varsayılan profil > genel model)', () => {
    expect(activeProfileModel('deep', profiles, 'fast', 'x')).toBe('big');
    expect(activeProfileModel('', profiles, 'fast', 'x')).toBe('small');
    expect(activeProfileModel('', [], undefined, 'x')).toBe('x');
  });
});
