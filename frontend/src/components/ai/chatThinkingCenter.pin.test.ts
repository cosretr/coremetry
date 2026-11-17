// v0.10.1389 (operatör: "Düşünüyor yazarken telemetry logosu çakmasına gerek
// yok ve ortalı olsun") — ilk token öncesi bekleyişte marka etiketi (logo +
// "CoSRE") çizilmez; AiThinking ortalı kapta durur.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const src = readFileSync(resolve(__dirname, 'ChatBubble.tsx'), 'utf8');
const css = readFileSync(resolve(__dirname, '../../styles/globals.css'), 'utf8');

describe('sohbet bekleyişi: logosuz + ortalı', () => {
  it('marka etiketi yalnız bekleyiş bitince', () => {
    const who = src.indexOf('className="cm-msg-ai__who"');
    expect(who).toBeGreaterThan(0);
    expect(src.slice(Math.max(0, who - 120), who)).toContain('{!thinkingOnly && (');
    expect(src).toContain('const thinkingOnly = !isUser && turn.pending && !turn.text && !turn.error;');
  });
  it('gösterge ortalı kapta', () => {
    expect(src).toContain('{thinkingOnly && <div className="cm-wait-center"><AiThinking variant="row" label={turn.statusLine} /></div>}');
    expect(css).toMatch(/\.cm-wait-center\s*\{[^}]*justify-content:\s*center/);
  });
});
