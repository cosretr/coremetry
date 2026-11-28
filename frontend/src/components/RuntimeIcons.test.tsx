// v0.10.2490 (operatör-raporlu) — dil logoları: tanınır işaret, `size`
// prop'u, role="img" + ad, bilinmeyen dilde genel ikon.
import { describe, it, expect } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { LanguageIcon, logoKeyForLanguage } from './RuntimeIcons';

describe('LanguageIcon', () => {
  it('her bilinen dil bir logoya eşlenir', () => {
    const cases: Record<string, string> = {
      java: 'openjdk', kotlin: 'kotlin', dotnet: 'dotnet', csharp: 'dotnet',
      go: 'go', rust: 'rust', nodejs: 'nodejs', webjs: 'nodejs',
      python: 'python', ruby: 'ruby', php: 'php', erlang: 'erlang', swift: 'swift',
    };
    for (const [lang, key] of Object.entries(cases)) {
      expect(logoKeyForLanguage(lang)).toBe(key);
      expect(renderToStaticMarkup(<LanguageIcon lang={lang} />)).toContain(`data-logo="${key}"`);
    }
  });

  it('varsayılan 14 px; size prop 24 px çizer, viewBox sabit', () => {
    expect(renderToStaticMarkup(<LanguageIcon lang="go" />)).toContain('width="14"');
    const big = renderToStaticMarkup(<LanguageIcon lang="go" size={24} />);
    expect(big).toContain('width="24"');
    expect(big).toContain('height="24"');
    expect(big).toContain('viewBox="0 0 24 24"');
  });

  it('role="img" ve dil+sürüm adı', () => {
    const html = renderToStaticMarkup(<LanguageIcon lang="java" label="Java 21" />);
    expect(html).toContain('role="img"');
    expect(html).toContain('aria-label="Java 21"');
    expect(renderToStaticMarkup(<LanguageIcon lang="dotnet" />)).toContain('aria-label=".NET"');
  });

  it('bilinmeyen dil genel ikona düşer', () => {
    const html = renderToStaticMarkup(<LanguageIcon lang="cobol" />);
    expect(html).not.toContain('data-logo');
    expect(html).toContain('<circle');
    expect(html).toContain('aria-label="cobol"');
  });
});
