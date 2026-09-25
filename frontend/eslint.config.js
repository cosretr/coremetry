import js from '@eslint/js';
import globals from 'globals';
import tseslint from 'typescript-eslint';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';

// ESLint (v0.7.49) — the frontend counterpart to the golangci-lint umbrella
// (v0.7.26). The noisiest rules are softened to 'warn'/'off' so the signal is
// the load-bearing stuff (rules-of-hooks, unreachable code, real unused
// bindings) rather than a wall of style nits. The CI step BLOCKS on errors
// since v0.10.626 (warnings pass; see .github/workflows/ci.yml).
//
// ui/no-raw-button — v0.10.919 (buton bütünlüğü, Seçenek B; operatör onayı
// 2026-09-25). `components/ui/` dışında ham `<button>` ve `role="button"`
// (span/div/tr üstünde düğme taklidi) yasak: Button / IconButton / Chip /
// LinkButton / SegmentedControl / ButtonGroup kullanılır. Gerekçe:
// operatör "bazı butonlar traces mesela farklılaşıyor" — kök sebep sayfaya
// özel ham düğmelerdi (v0.10.914).
//
// Mevcut ihlaller `eslint-suppressions.json`da SAYILI (dosya başına
// tavan); yenisi CI'da kırmızı. Bir ihlal düzeltilince aynı commit'te
// `npx eslint src --prune-suppressions` (aksi hâlde eslint çıkış kodu 2).
// Gerçekten gerekiyorsa satır istisnası GEREKÇEYLE:
//   {/* eslint-disable-next-line ui/no-raw-button -- <neden> */}
// Gerekçesiz istisnayı src/styles/eslintDisableReason.test.ts reddeder.
// Aynı sayım vitest'te de var (buttonUnityRatchet.test.ts) — tek başına
// eslint çalıştırmayan yol da kapıdan geçmesin.
// Koşullu rol de düğme taklididir: `role={x ? 'button' : undefined}`
// (AdminAudit'in tıklanabilir hücresi) ilk sürümde kaçıyordu.
const isButtonExpr = (e) => {
  if (!e) return false;
  if (e.type === 'Literal') return e.value === 'button';
  if (e.type === 'TemplateLiteral') return e.expressions.length === 0 && e.quasis[0]?.value.cooked === 'button';
  if (e.type === 'ConditionalExpression') return isButtonExpr(e.consequent) || isButtonExpr(e.alternate);
  if (e.type === 'LogicalExpression') return isButtonExpr(e.right);
  return false;
};
const isButtonRoleValue = (v) => {
  if (!v) return false;
  if (v.type === 'Literal') return v.value === 'button';
  if (v.type === 'JSXExpressionContainer') return isButtonExpr(v.expression);
  return false;
};
const noRawButton = {
  meta: {
    type: 'problem',
    schema: [],
    messages: {
      raw: 'Ham <button> yerine ui atomu kullan (Button / IconButton / Chip / LinkButton / SegmentedControl / ButtonGroup).',
      role: 'role="button" taklidi yerine gerçek düğme atomu kullan (Button / IconButton; görünmez düğme için btn-bare).',
    },
  },
  create(context) {
    return {
      JSXOpeningElement(node) {
        if (node.name.type === 'JSXIdentifier' && node.name.name === 'button') {
          context.report({ node, messageId: 'raw' });
        }
      },
      JSXAttribute(node) {
        if (node.name.type === 'JSXIdentifier' && node.name.name === 'role' && isButtonRoleValue(node.value)) {
          context.report({ node, messageId: 'role' });
        }
      },
    };
  },
};

export default tseslint.config(
  { ignores: ['dist', 'node_modules', 'src/**/*.test.ts', '*.config.js', '*.config.ts'] },
  {
    files: ['src/**/*.{ts,tsx}'],
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    languageOptions: {
      ecmaVersion: 2022,
      globals: { ...globals.browser },
    },
    // React rules set explicitly (not via the plugin's preset) so this config
    // is robust across react-hooks/react-refresh plugin versions.
    plugins: { 'react-hooks': reactHooks, 'react-refresh': reactRefresh },
    rules: {
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'warn',
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
      // Softened for the advisory baseline — intentional patterns in this
      // codebase (operator-controlled `any` at narrow OTel/attr boundaries,
      // empty catch on best-effort fetches, leading-underscore throwaways).
      '@typescript-eslint/no-explicit-any': 'off',
      '@typescript-eslint/no-unused-vars': ['warn', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
      'no-empty': ['warn', { allowEmptyCatch: true }],
      'prefer-const': 'warn',
    },
  },
  {
    // v0.10.919 — ui/no-raw-button (gerekçe yukarıda). Atomların evi ve
    // test fixture'ları kapsam dışı.
    files: ['src/**/*.tsx'],
    ignores: ['src/components/ui/**', 'src/**/*.test.tsx'],
    plugins: { ui: { rules: { 'no-raw-button': noRawButton } } },
    rules: { 'ui/no-raw-button': 'error' },
  },
);
