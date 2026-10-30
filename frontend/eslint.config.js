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
//   {/* eslint-disable-next-line ui/no-raw-element -- <neden> */}
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
// v0.10.1614 (shadcn Faz 1 R6, plan §3.3) — kural `ui/no-raw-element`
// oldu: aynı ziyaretçi <input>, <select>, <textarea>'yı da yakalar. Mevcut
// ihlaller eslint-suppressions.json'da dosya başına sayılı (R6 tabanı),
// yenisi kırmızı; vitest ikizi rawElementRatchet.test.ts. <input> yalnız
// LİTERAL `type` şu kümedeyse serbest: file (kalıcı muaf, §12 Q3),
// checkbox / radio (shadcn checkbox / radio-group kurulana dek — o sürüm
// muafiyeti kaldırır), hidden / range / color (kurulum kümesinde karşılığı
// yok). components/coremetry MUAF DEĞİL: bileşikler shadcn'i birleştirir.
// Faz 2 dilim 1 R2 (G10, operatör Q-IA7): shadcn checkbox kuruldu →
// `checkbox` muafiyetten çıktı; ham kutu kendi iletisiyle raporlanır,
// vitest ikizinde ayrı tavan (MAX_RAW_CHECKBOX). radio muaf kalır.
const ALLOWED_INPUT_TYPES = new Set(['file', 'radio', 'hidden', 'range', 'color']);
const literalType = (node) => {
  const a = node.attributes.find((x) => x.type === 'JSXAttribute' && x.name.type === 'JSXIdentifier' && x.name.name === 'type');
  if (!a || !a.value) return null;
  if (a.value.type === 'Literal') return a.value.value;
  if (a.value.type === 'JSXExpressionContainer') {
    const e = a.value.expression;
    if (e.type === 'Literal') return e.value;
    if (e.type === 'TemplateLiteral' && e.expressions.length === 0) return e.quasis[0]?.value.cooked;
  }
  return null;
};
const noRawElement = {
  meta: {
    type: 'problem',
    schema: [],
    messages: {
      raw: 'Ham <button> yerine atom kullan (ui: Button / IconButton / Chip / LinkButton / SegmentedControl / ButtonGroup; geçirilmiş sayfada shadcn Button).',
      role: 'role="button" taklidi yerine gerçek düğme atomu kullan (Button / IconButton; görünmez düğme için btn-bare).',
      input: 'Ham <input> yerine Field bileşiği / shadcn input kullan (serbest: literal type file, radio, hidden, range, color).',
      checkbox: 'Ham <input type="checkbox"> yerine coremetry CheckboxField / shadcn Checkbox kullan.',
      select: 'Ham <select> yerine shadcn native-select (veri formu varsayılanı) ya da select kullan.',
      textarea: 'Ham <textarea> yerine shadcn textarea kullan.',
    },
  },
  create(context) {
    return {
      JSXOpeningElement(node) {
        if (node.name.type !== 'JSXIdentifier') return;
        const n = node.name.name;
        if (n === 'button') context.report({ node, messageId: 'raw' });
        else if (n === 'select' || n === 'textarea') context.report({ node, messageId: n });
        else if (n === 'input' && literalType(node) === 'checkbox') context.report({ node, messageId: 'checkbox' });
        else if (n === 'input' && !ALLOWED_INPUT_TYPES.has(literalType(node))) context.report({ node, messageId: 'input' });
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
    // v0.10.919 — ui/no-raw-button, R6'dan beri ui/no-raw-element (gerekçe yukarıda). Atomların evi ve
    // test fixture'ları kapsam dışı. v0.10.1613 (shadcn Faz 1 R5, plan
    // §3.3): üretilen components/shadcn ve CoSRE v2'nin ai-elements dizini
    // de muaf (karar 8); components/coremetry MUAF DEĞİL.
    files: ['src/**/*.tsx'],
    ignores: ['src/components/ui/**', 'src/components/shadcn/**', 'src/components/ai-elements/**', 'src/**/*.test.tsx'],
    plugins: { ui: { rules: { 'no-raw-element': noRawElement } } },
    rules: { 'ui/no-raw-element': 'error' },
  },
  {
    // v0.10.1615 — shadcn Faz 1 R7 (plan §7.2, karar d): Radix overlay'leri
    // YALNIZ components/coremetry/overlay sarmalayıcılarıyla — onlar Esc'i
    // lib/escLayer yığınına bağlar (escBridge.ts). Çıplak shadcn overlay
    // dosyası ya da radix-ui importu Esc'i yığının dışında kapatır
    // (escLayer.ts :4-10 hata #1/#2). shadcn/ (üretilen) ve ai-elements/
    // (karar 8) muaf. Kabuk dosyaları aşağıdaki DAHA SIKI blokla eşleşir
    // (flat config: sonraki blok aynı kuralı ezer).
    // v0.10.1619 (R10): pages/design/catalog.tsx muaf — YALNIZ geliştirme
    // kataloğu (App.tsx DEV kapısı, Design.pin) select + command'ı
    // sarmalayıcıları gelmeden shadcn dosyasından çizer; tek dosya, glob değil.
    files: ['src/**/*.{ts,tsx}'],
    ignores: ['src/components/coremetry/**', 'src/components/shadcn/**', 'src/components/ai-elements/**', 'src/pages/design/catalog.tsx'],
    rules: {
      'no-restricted-imports': ['error', {
        patterns: [{
          group: [
            ...['dialog', 'alert-dialog', 'sheet', 'popover', 'dropdown-menu', 'context-menu', 'tooltip', 'command', 'select'].map((n) => `**/shadcn/${n}`),
            'radix-ui', 'radix-ui/**', '@radix-ui/*',
          ],
          message: 'Radix overlays only via @/components/coremetry/overlay (Esc stack, lib/escLayer.ts) — Radix katmanları yalnız coremetry/overlay sarmalayıcılarıyla.',
        }],
      }],
    },
  },
  {
    // v0.10.1609 — shadcn Faz 1 R2 (plan §1.2): kabuk (bugünkü eager
    // closure) Radix'e statik bağlanamaz; bağlanırsa Radix/cmdk/sonner
    // index.html modulepreload'ına biner. Bugün no-op (kabuk yalnız
    // components/ui import ediyor). Geçişli kenarları yalnız
    // scripts/bundle-budget.mjs görür — bu blok satır-hassas hızlı yarısı.
    files: [
      'src/App.tsx',
      'src/main.tsx',
      'src/components/AppShell.tsx',
      'src/components/Topbar.tsx',
      'src/components/Sidebar.tsx',
      'src/components/CommandPalette.tsx',
      'src/components/TimeRangePicker.tsx',
      'src/components/ContextBar/**',
      'src/components/CopilotChatGate.tsx',
      'src/components/Toaster.tsx',
      // Faz 2 dilim 1 R3: onay kapısı eager (App.tsx) — çizicisi tembel.
      'src/components/coremetry/Confirm/ConfirmGate.tsx',
      'src/components/coremetry/Confirm/context.ts',
      'src/components/GlobalShortcuts.tsx',
      'src/components/AnnouncementBanner.tsx',
    ],
    ignores: ['src/**/*.test.{ts,tsx}'],
    rules: {
      'no-restricted-imports': ['error', {
        patterns: [{
          group: [
            '**/components/shadcn', '**/components/shadcn/**', './shadcn/**',
            '**/coremetry/overlay', '**/coremetry/overlay/**',
            'radix-ui', 'radix-ui/**', '@radix-ui/*', 'cmdk', 'sonner',
          ],
          message: 'Kabuk Radix/shadcn overlay import edemez (ilk yüke biner, plan §1.2). Sayfa chunk\'ında kullan ya da tembel kapı (CopilotChatGate deseni).',
        }, {
          // v0.10.1617 (R9): coremetry BARREL'ı overlay'i yeniden dışa aktarır;
          // kabuk PageShell/PageControls'ü dosya yolundan alır. regex: glob
          // (gitignore) deseninde dizin eşleşmesi altını da kapsar, geri
          // açılamaz.
          regex: '(^|/)components/coremetry(/index)?$|^\\.{1,2}/coremetry(/index)?$',
          message: 'Kabuk coremetry barrel\'ını import edemez (overlay → Radix ilk yüke biner). Dosya yolundan al: @/components/coremetry/PageShell.',
        }],
      }],
    },
  },
);
