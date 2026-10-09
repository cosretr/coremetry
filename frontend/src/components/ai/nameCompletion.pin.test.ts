import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.687 — D4 ad tamamlama bağlanma pini: CopilotChat girişi hook'u
// çağırır, popup çizer, klavye dalı gönderim kuralından ÖNCE gelir
// (Enter açık listede seçer, gönder değil), a11y nitelikleri var; hook boş
// sorguda istek atmaz ve sunucu aramasını (api.serviceNames) kullanır —
// istemcide katalog süzme YOK (picker kuralı).
//
// v0.10.1145 — textarea ortak composer'a (ChatComposer) taşındı: combobox
// nitelikleri `comboboxProps` ile geçer, klavye dalı CopilotChat'in
// `onBeforeKey`inde ve composer onu kısayollardan / liste sürdürmeden /
// gönderimden ÖNCE çağırır (popup gezinmesi açıkken kazanır).
const chat = readFileSync(resolve(__dirname, '../CopilotChat.tsx'), 'utf8');
const composer = readFileSync(resolve(__dirname, 'ChatComposer.tsx'), 'utf8');
const hook = readFileSync(resolve(__dirname, 'useNameCompletion.ts'), 'utf8');

describe('D4 ad tamamlama (v0.10.687)', () => {
  it('CopilotChat: hook + popup + a11y', () => {
    expect(chat).toContain('useNameCompletion(input, caret)');
    expect(chat).toContain('<NameCompletionPopup');
    expect(chat).toContain("'aria-autocomplete': 'list'");
    expect(chat).toContain("'aria-activedescendant':");
    expect(composer).toContain('{...comboboxProps}');
  });
  it('klavye dalı gönderimden önce', () => {
    const i = chat.indexOf('onBeforeKey={e => {');
    const j = chat.indexOf('actions={', i);
    expect(i).toBeGreaterThan(-1);
    expect(j).toBeGreaterThan(i);
    const block = chat.slice(i, j);
    expect(block).toContain('if (!completion.open) return false;');
    for (const k of ["'ArrowDown'", "'ArrowUp'", "'Enter' || e.key === 'Tab'", "'Escape'"]) expect(block).toContain(k);
    // Composer: IME → popup → kısayol → liste/çit → gönderim sırası.
    const ime = composer.indexOf('e.nativeEvent.isComposing');
    const pop = composer.indexOf('if (onBeforeKey?.(e)) return;');
    const sc = composer.indexOf('composerShortcut(e)', pop);
    const send = composer.indexOf('if (chatInputSubmitKey(e))', sc);
    expect(ime).toBeGreaterThan(-1);
    expect(pop).toBeGreaterThan(ime);
    expect(sc).toBeGreaterThan(pop);
    expect(send).toBeGreaterThan(sc);
  });
  it('hook: sunucu araması, boşta istek yok, 180 ms debounce', () => {
    expect(hook).toContain('api.serviceNames(dq, 20)');
    expect(hook).toContain('enabled: dq.length > 0');
    expect(hook).toContain('180)');
  });
});
