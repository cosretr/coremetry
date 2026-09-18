// v0.10.784 — servis sayfası dikkat şeridi pinleri.
//
// Operatör (2026-09-18): "SLO üstte yazmasın; varsa problem veya
// exception gözüksün; tıklanarak gidebilsin; daha az yükseklik."
// Şerit Inbox'tan okur, satır = bağlantı, SLO dipnot. Bu pinler
// "saf çekirdek yeşil ama ekranda yok" sınıfını kapatır
// (feedback-tested-but-unreachable).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const read = (rel: string) => readFileSync(resolve(__dirname, rel), 'utf8');

describe('v0.10.784 — service attention strip', () => {
  const page = read('./Service.tsx');
  const strip = read('../components/ServiceAttentionStrip.tsx');
  const inbox = read('./Inbox.tsx');

  it('Service.tsx renders the strip and no longer draws the SLO-card callout', () => {
    expect(page).toContain('<ServiceAttentionStrip service={svc} fallbackProblems={problems} />');
    expect(page).not.toContain('open problem{openProbs.length');
    expect(page).not.toContain('Red PROBLEM CALLOUT');
    // SLO dipnotunun "↓" çıpası sayfanın altındaki SLO bloğu.
    expect(page).toContain('id="svc-slos"');
  });

  it('strip reads the Inbox (same ranking) and links rows via the shared helper', () => {
    expect(strip).toContain("api.inbox({ service, status: 'open', limit: 20 })");
    expect(strip).toContain('attentionRows(items)');
    expect(strip).toContain('inboxItemHref(it)');
    expect(strip).toContain('href="#svc-slos"');
    // Polling ≥ 10 sn ve RQ (gizli sekmede durur) — ham setInterval yok.
    expect(strip).toContain('refetchInterval: 30_000');
    expect(strip).not.toContain('setInterval');
  });

  it('Inbox.tsx navigates through the same helper (no second copy of the URLs)', () => {
    expect(inbox).toContain("from '@/lib/inboxHref'");
    expect(inbox).not.toContain('navigate(`/anomalies?event=');
    expect(inbox).not.toContain('navigate(`/incident?id=');
    expect(inbox).not.toContain('navigate(`/problems?exc=');
    // "Kaynağı aç" için exception satırı listede GENİŞLETİR (tab=open&exception=)
    // — bu davranış değişmedi; helper'ın detay hedefinden ayrı kalır.
    expect(inbox).toContain('/problems?tab=open&exception=');
  });
});
