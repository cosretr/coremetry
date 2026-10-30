// CosreWelcomeV2 — CoSRE v2 boş sohbet karşılaması, mockup B "Düz akış + Tabs"
// (operatör onayı 2026-10-31, "güzel mockup devam"). v1 karşılaması
// (CopilotChat CosreWelcome) AYNEN kalır; bu yalnız v2 Sheet'inde çizilir.
//   • "Merhaba" + tek açılış cümlesi
//   • brifing yuvası (BriefingFeed) — kırmızı hata hapının yerine; veri
//     kaynağı yoksa hiçbir şey; takım bilinmiyorsa "hangi takımdasın?" +
//     sunucu-aramalı takım Command'ı (seçim v1 Takımım yolu: takım saklanır ve
//     "<takım> takımının servisleri nasıl?" HEMEN gönderilir — sunucunun
//     data-scope no_team "Hangi takım?" sorusunun cevabı)
//   • hazır sorular shadcn Tabs (Durum · Araştır · Bul · Öğren), sekme başına
//     ≤4 outline çip; anlamlar welcomeSuggestions.ts'te (send / fill / pick /
//     explain / navigate — v1 ile aynı). Satır içi Service/Operation/Team
//     seçici YOK: "pick" çipi sunucu-aramalı Command popover'ı açar.
//   • takım + zaman kapsamı YALNIZ composer bağlam satırında (CosreTeamScopeChip).
import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { Pencil, Search, Sparkles, Users, X } from 'lucide-react';
import { api } from '@/lib/api';
import { keys } from '@/lib/queries';
import type { ChatExplainAsk, ServiceMetadata } from '@/lib/types';
import type { ChatContextStarter } from '@/lib/chatContext';
import { exceptionsTeamAxis, exceptionsTeamHref } from '@/lib/exceptionsTeamHref';
import { Badge } from '@/components/shadcn/badge';
import { Button } from '@/components/shadcn/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/coremetry/overlay';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/shadcn/tabs';
import { greetHello } from '@/components/ai/greeting';
import type { ChatSendOpts } from '@/components/ai/useChatThread';
import {
  operationErrorQuestion, serviceHealthQuestion, splitCaret, tCosre as t, teamPickQuestion,
} from '@/components/ai/capabilityHints';
import { BriefingFeed, type BriefingFeedLine, type BriefingView } from './BriefingFeed';
import { CosreScopeSearch } from './CosreScopeSearch';
import type { CosreTeamScope } from './useCosreTeamScope';
import { welcomeTabs, type WelcomeSuggestion, type WelcomeTabId } from './welcomeSuggestions';

export interface CosreWelcomeV2Props {
  firstName?: string;
  scope: CosreTeamScope;
  wiki: boolean;
  ctxStarter: ChatContextStarter | null;
  /** Brifing görünümü; null = veri kaynağı yok (yuva çizilmez). */
  briefing: BriefingView | null;
  onPrefill: (text: string, caret: number) => void;
  onAsk: (text: string, so?: ChatSendOpts) => void;
  onAskExplain: (a: ChatExplainAsk) => void;
}

const chipClass = 'h-7 max-w-full rounded-full px-2.5 text-[12.5px] font-normal';

function SuggestionChip({ s, onRun, onPick }: {
  s: WelcomeSuggestion;
  onRun: (s: WelcomeSuggestion) => void;
  onPick: (kind: 'service' | 'operation', name: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const label = 'labelKey' in s && s.labelKey ? t(s.labelKey) : ('label' in s ? s.label ?? '' : '');
  const title = s.mode === 'send' ? s.question
    : s.mode === 'fill' ? t('cosre.welcome.s.fillTitle')
      : s.mode === 'pick' ? t('cosre.welcome.s.pickTitle')
        : s.mode === 'navigate' ? t('cosre.cap.exceptions.searchTitle')
          : s.starter.question;
  const icon = s.mode === 'fill' ? <Pencil aria-hidden="true" />
    : s.mode === 'pick' ? <Search aria-hidden="true" />
      : s.mode === 'explain' || s.id === 'ctx' ? <Sparkles aria-hidden="true" /> : null;
  const chip = (
    <Button variant="outline" size="xs" className={chipClass} title={title} data-welcome-suggestion={s.id}
      data-mode={s.mode} onClick={s.mode === 'pick' ? undefined : () => onRun(s)}>
      {icon}<span className="truncate">{label}</span>
    </Button>
  );
  if (s.mode !== 'pick') return chip;
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>{chip}</PopoverTrigger>
      <PopoverContent align="start" className="w-80 p-0">
        <CosreScopeSearch kind={s.pick} autoFocus className="rounded-md"
          onPick={name => { setOpen(false); onPick(s.pick, name); }} />
      </PopoverContent>
    </Popover>
  );
}

export function CosreWelcomeV2({ firstName, scope, wiki, ctxStarter, briefing, onPrefill, onAsk, onAskExplain }: CosreWelcomeV2Props) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const tabs = welcomeTabs({ team: scope.team, wiki, ctxStarter });
  const [tab, setTab] = useState<WelcomeTabId | null>(null);
  const active = tabs.find(x => x.id === tab)?.id ?? tabs[0]?.id;

  // v1 CosreCapabilities goExceptions ile aynı: katalog yalnız tıklamada (RQ önbelleği ya da tek istek).
  const goExceptions = (team: string) => {
    void qc.fetchQuery({
      queryKey: keys.services.metadata,
      queryFn: async () => (await api.servicesMetadata()) ?? {},
      staleTime: 60_000,
    })
      .catch(() => undefined)
      .then(cat => {
        const r = exceptionsTeamAxis(cat as Record<string, ServiceMetadata> | undefined, team);
        navigate(exceptionsTeamHref(r.name, r.axis));
      });
  };
  const run = (s: WelcomeSuggestion) => {
    switch (s.mode) {
      case 'send': onAsk(s.question); return;
      case 'fill': { const p = splitCaret(t(s.promptKey)); onPrefill(p.text, p.caret); return; }
      case 'explain': if (s.starter.explainAsk) onAskExplain(s.starter.explainAsk); else onAsk(s.starter.question); return;
      case 'navigate': goExceptions(s.team); return;
      case 'pick': return;
    }
  };
  const pick = (kind: 'service' | 'operation', name: string) =>
    onAsk(kind === 'service' ? serviceHealthQuestion(name) : operationErrorQuestion(name));
  const chooseTeam = (name: string) => { scope.choose(name); onAsk(teamPickQuestion(name)); };
  const askLine = (l: BriefingFeedLine) => onPrefill(l.ask, l.ask.length);

  const view: BriefingView | null = scope.unknown ? { state: 'unknownTeam' } : briefing;
  const lead = scope.unknown ? t('cosre.welcome.lead.noTeam')
    : briefing && briefing.state !== 'unknownTeam' && scope.team
      ? t('cosre.welcome.lead.briefing').replace('{window}', briefing.windowLabel).replace('{team}', scope.team)
      : t('cosre.welcome.lead.default');

  return (
    <div className="cosre-welcome flex w-full flex-col gap-4 py-4" data-cosre-welcome="">
      <div>
        <div className="cosre-welcome__hello text-[15px] font-semibold">{greetHello(firstName)}</div>
        <p className="m-0 mt-0.5 text-[13px] text-muted-foreground">{lead}</p>
      </div>
      <BriefingFeed view={view} onAskLine={askLine}
        teamPicker={<CosreScopeSearch kind="team" onPick={chooseTeam} />} />
      {active && (
        <Tabs value={active} onValueChange={v => setTab(v as WelcomeTabId)} className="gap-2.5">
          <TabsList aria-label={t('cosre.welcome.tabs.aria')}>
            {tabs.map(x => <TabsTrigger key={x.id} value={x.id} data-welcome-tab={x.id}>{t(x.labelKey)}</TabsTrigger>)}
          </TabsList>
          {tabs.map(x => (
            <TabsContent key={x.id} value={x.id} className="flex flex-wrap gap-1.5">
              {x.items.map(s => <SuggestionChip key={s.id} s={s} onRun={run} onPick={pick} />)}
            </TabsContent>
          ))}
        </Tabs>
      )}
    </div>
  );
}

/** Composer bağlam satırının takım çipi: takım varsa Badge secondary + ✕
 *  (kapsamı kaldırır → takım soruları gizlenir), yoksa kesik kenarlı
 *  "Takım seç" (sunucu-aramalı Command popover; seçim yalnız kapsamı kurar). */
export function CosreTeamScopeChip({ scope }: { scope: CosreTeamScope }) {
  const [open, setOpen] = useState(false);
  if (scope.team) {
    return (
      <Badge variant="secondary" className="h-6 gap-1 pr-0.5 pl-2 font-normal" data-team-scope={scope.team}
        title={t('cosre.welcome.scope.teamTitle')}>
        <Users aria-hidden="true" />
        <span className="max-w-48 truncate font-mono">{scope.team}</span>
        <Button variant="ghost" size="icon-xs" className="size-5 rounded-full text-muted-foreground"
          aria-label={t('cosre.welcome.scope.teamRemove')} onClick={scope.clear}><X aria-hidden="true" /></Button>
      </Badge>
    );
  }
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button variant="outline" size="xs" className="h-6 rounded-full border-dashed font-normal text-muted-foreground"
          data-team-scope="">
          <Users aria-hidden="true" />{t('cosre.welcome.scope.teamPick')}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-72 p-0">
        <CosreScopeSearch kind="team" autoFocus className="rounded-md"
          onPick={name => { setOpen(false); scope.choose(name); }} />
      </PopoverContent>
    </Popover>
  );
}
