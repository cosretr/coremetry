// Playwright — UI nightly (2026-11-21, operatör onayı; Aug-31 denetimi
// "Playwright yok" demişti, bu karar geri alındı). YALNIZ gece işi
// (.github/workflows/ui-nightly.yml) ve elle koşu için; scripts/gate.sh'in
// parçası DEĞİL. Canlı bir Coremetry ister (BASE_URL, varsayılan
// http://localhost:8088). Rapor-önce: bütçe/axe ihlali geceyi kırmaz,
// yalnız çökme kırar (nightly.pw.ts).
import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  testMatch: /.*\.pw\.ts$/,
  outputDir: './out/artifacts',
  fullyParallel: false, // ölçüm gürültüsü: tek işçi, sırayla
  workers: 1,
  retries: 0,
  timeout: 90_000,
  reporter: [['list'], ['json', { outputFile: './out/playwright.json' }]],
  use: {
    baseURL: process.env.BASE_URL || 'http://localhost:8088',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    viewport: { width: 1440, height: 900 },
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } } }],
});
