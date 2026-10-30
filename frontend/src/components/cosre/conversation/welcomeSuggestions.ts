// welcomeSuggestions — CoSRE v2 karşılaması (mockup B "Düz akış + Tabs",
// operatör onayı 2026-10-31). Hazır sorular dört niyet sekmesinde (Durum ·
// Araştır · Bul · Öğren), sekme başına ≤4 çip. İçerik ve DAVRANIŞ v1
// karşılamasıyla aynı kaynaktan (capabilityHints + i18n cosre.cap.*):
//   send     — soruyu HEMEN gönderir (eski Takımım çipleri, bağlam çipi)
//   fill     — composer'ı şablonla DOLDURUR, göndermez (eski satır başlıkları,
//              "…/yol" ve wiki çipleri; `|` imleç yeri)
//   pick     — sunucu-aramalı Command açar; seçim cümleyi HEMEN gönderir
//              (eski satır içi Service/OperationPicker'ın yolu)
//   explain  — ekrandaki trace'i açıklama turu (ctxStarter.explainAsk)
//   navigate — Exceptions sayfasına takımla süzülü gider (eski exceptions kartı)
// Takım kapsamlı çipler (Durum) yalnız takım biliniyorken; boş sekme çizilmez.
import type { ChatContextStarter } from '@/lib/chatContext';
import { TEAM_ASKS, teamCardQuestion, type TeamAsk } from '@/components/ai/capabilityHints';

export type WelcomeTabId = 'status' | 'investigate' | 'find' | 'learn';
export type WelcomePickKind = 'service' | 'operation';

export type WelcomeSuggestion =
  | { id: string; mode: 'send'; labelKey?: string; label?: string; question: string }
  | { id: string; mode: 'fill'; labelKey: string; promptKey: string }
  | { id: string; mode: 'pick'; labelKey: string; pick: WelcomePickKind }
  | { id: string; mode: 'explain'; label: string; starter: ChatContextStarter }
  | { id: string; mode: 'navigate'; labelKey: string; team: string };

export interface WelcomeTab {
  id: WelcomeTabId;
  labelKey: string;
  items: WelcomeSuggestion[];
}

/** Sekme başına görünen çip tavanı (mockup: ≤4). */
export const WELCOME_TAB_MAX = 4;

const TEAM_ASK_LABEL: Record<TeamAsk, string> = {
  services: 'cosre.team.ask.services',
  exceptions: 'cosre.team.ask.exceptions',
  topErrors: 'cosre.team.ask.topErrors',
};

/** SAF — görünen sekmeler (sırası sabit), boş sekme düşer, her sekme ≤4 çip. */
export function welcomeTabs({ team, wiki, ctxStarter }: {
  team: string;
  wiki: boolean;
  ctxStarter: ChatContextStarter | null;
}): WelcomeTab[] {
  const tm = team.trim();
  const status: WelcomeSuggestion[] = tm ? [
    ...TEAM_ASKS.map(a => ({ id: `team-${a.kind}`, mode: 'send' as const, labelKey: TEAM_ASK_LABEL[a.kind], question: teamCardQuestion(a.kind, tm) })),
    { id: 'team-exceptions-page', mode: 'navigate', labelKey: 'cosre.welcome.s.exceptionsPage', team: tm },
  ] : [];
  const investigate: WelcomeSuggestion[] = [];
  if (ctxStarter) {
    investigate.push(ctxStarter.explainAsk
      ? { id: 'ctx', mode: 'explain', label: ctxStarter.chip, starter: ctxStarter }
      : { id: 'ctx', mode: 'send', label: ctxStarter.chip, question: ctxStarter.question });
  }
  investigate.push(
    { id: 'pick-service', mode: 'pick', labelKey: 'cosre.welcome.s.pickService', pick: 'service' },
    { id: 'pick-operation', mode: 'pick', labelKey: 'cosre.welcome.s.pickOperation', pick: 'operation' },
  );
  const find: WelcomeSuggestion[] = [
    { id: 'fill-path', mode: 'fill', labelKey: 'cosre.welcome.s.path', promptKey: 'cosre.welcome.s.path.prompt' },
    { id: 'fill-service', mode: 'fill', labelKey: 'cosre.welcome.s.service', promptKey: 'cosre.cap.service.prompt' },
    { id: 'fill-operation', mode: 'fill', labelKey: 'cosre.welcome.s.operation', promptKey: 'cosre.cap.operation.prompt' },
  ];
  const learn: WelcomeSuggestion[] = wiki
    ? [{ id: 'fill-wiki', mode: 'fill', labelKey: 'cosre.chip.wiki', promptKey: 'cosre.chip.wiki.prompt' }]
    : [];
  const tabs: WelcomeTab[] = [
    { id: 'status', labelKey: 'cosre.welcome.tab.status', items: status },
    { id: 'investigate', labelKey: 'cosre.welcome.tab.investigate', items: investigate },
    { id: 'find', labelKey: 'cosre.welcome.tab.find', items: find },
    { id: 'learn', labelKey: 'cosre.welcome.tab.learn', items: learn },
  ];
  return tabs
    .map(t => ({ ...t, items: t.items.slice(0, WELCOME_TAB_MAX) }))
    .filter(t => t.items.length > 0);
}
