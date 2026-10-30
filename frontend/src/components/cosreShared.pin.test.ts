// cosreShared.pin — v0.10.1648 (CoSRE v2 Faz 2b B2a, plan §10). v1 karşılaması,
// composer'ı ve başlık pini PAYLAŞILAN parçalardan çizilir; v2 aynılarını
// kullanır ("v2 asla v1'den yoksul görünmez"). DOM eşitliğini v1'in kendi
// render testleri taşır (CosreCapabilities, codeRead, modelPicker.composer,
// chatContextScope, explainTurn …); bu pin yalnız kablonun tek olduğunu söyler.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const v1 = readFileSync(resolve(__dirname, 'CopilotChat.tsx'), 'utf8');
const hooks = readFileSync(resolve(__dirname, 'ai/cosreAsk.ts'), 'utf8');
const count = (s: string, needle: string) => s.split(needle).length - 1;

describe('v1 ↔ v2 paylaşılan sohbet parçaları', () => {
  it('v1 gövdesi paylaşılan bileşenleri birer kez çizer', () => {
    expect(count(v1, '<CosreHeaderContext ')).toBe(1);
    expect(count(v1, '<CosreWelcome ')).toBe(1);
    expect(count(v1, '<CosreComposerForm ')).toBe(1);
    expect(v1).toContain('const askCtx = useCosreAskContext();');
    expect(v1).toContain('const composerInput = useCosreComposerInput(conversationId, page, chipTrace);');
    expect(v1).toContain('const welcome = useCosreWelcomeData(enabled === true && open && turns.length === 0, env);');
  });
  it('kancalar dosyası JSX ve ui/ atomu taşımaz (ratchet tavanları dosya sayar)', () => {
    expect(hooks).not.toMatch(/from '@\/components\/ui/);
    expect(hooks).not.toMatch(/return \(?\s*</);
  });
});
