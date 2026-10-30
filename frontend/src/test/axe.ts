// axe matcher — v0.10.1619 (shadcn Faz 1 R10, plan §8.2). axe-core
// doğrudan (devDependency, tam sürüm): jest-axe kendi axe kopyasını getirir.
// color-contrast jsdom'da KAPALI — yerleşim/boya olmadan hesaplanamaz;
// karşıtlık token düzeyinde styles/contrastTokens.test.ts'te çivili.
// region KAPALI: bileşen düzeyinde koşuyoruz; Radix portalları <body>'ye
// iner ve her sayfada landmark dışındadır — landmark yapısı kabuğun işi.
import axe from 'axe-core';

export async function axeViolations(node: Element | Document = document): Promise<string[]> {
  const res = await axe.run(node, { rules: { 'color-contrast': { enabled: false }, region: { enabled: false } } });
  return res.violations.map(v => `${v.id}: ${v.help} — ${v.nodes.map(n => n.target.join(' ')).slice(0, 3).join(', ')}`);
}

export async function expectNoAxeViolations(node: Element | Document = document): Promise<void> {
  const v = await axeViolations(node);
  if (v.length) throw new Error(`axe: ${v.length} ihlal\n  ${v.join('\n  ')}`);
}
