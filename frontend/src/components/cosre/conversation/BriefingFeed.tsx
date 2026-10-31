// BriefingFeed — CoSRE v2 karşılamasının brifing yuvası (mockup B "Düz akış",
// operatör onayı 2026-10-31). SUNUMSAL: veri prop'tan gelir, istek atmaz.
// Kırmızı hata hapının (greetLine) yerini alır. Üç hâl:
//   findings    — eyebrow "Brifing · <pencere> · N bulgu", hairline ayraçlı
//                 satırlar: önem noktası · nötr kalem etiketi · metin (renk
//                 YALNIZ sapan sayıda) · pivot linki. Faydalı/gürültü geri
//                 bildirimi YOK (operatör 2026-10-31: gerek yok).
//   silence     — tek ok-noktalı satır ("dikkat çeken bir şey yok")
//   unknownTeam — "Ekibin tanımlı değil — hangi takımdasın?" + takım seçici
//                 (çağıranın slotu: sunucu-aramalı Command)
// `view` null → hiçbir şey çizilmez (veri kaynağı yok / henüz bilinmiyor).
// Satır metni tıklaması soruyu composer'a DOLDURUR (onAskLine; göndermez).
import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { ArrowUpRight } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import { Separator } from '@/components/shadcn/separator';
import { tCosre as t } from '@/components/ai/capabilityHints';
import { cn } from '@/lib/cn';

export type BriefingSeverity = 'critical' | 'warn' | 'info' | 'muted';

/** Satır metninin parçası: `subject` = servis/özne adı (mono), `num` = sapan
 *  sayı (satırın önem rengiyle), `muted` = ikincil not; tonsuz = düz metin. */
export interface BriefingPart {
  text: string;
  tone?: 'subject' | 'num' | 'muted';
}

export interface BriefingFeedLine {
  id: string;
  /** Kalem etiketi (RED, EXC, LOG, ROLLOUT, PROBLEM …) — nötr çizilir. */
  kind: string;
  severity: BriefingSeverity;
  parts: readonly BriefingPart[];
  /** Satıra tıklanınca composer'a dolan soru. */
  ask: string;
  /** v0.10.1716 (dilim 7) — "Sor" isteğinin context.briefingId + briefingLine'ı. */
  ref?: BriefingLineRef;
  pivot?: { label: string; href: string };
  /** v0.10.1718 — örnek trace çipi (yalnız güvenli uygulama yolu). */
  trace?: { label: string; href: string };
}

/** Kartın kimliği (ULID) + satırın sırası — sunucu satırı kart yükünden yeniden okur. */
export interface BriefingLineRef {
  briefingId: string;
  line: number;
}

export type BriefingView =
  | { state: 'findings'; windowLabel: string; agoLabel?: string; lines: readonly BriefingFeedLine[] }
  | { state: 'silence'; windowLabel: string; agoLabel?: string; team: string; serviceCount?: number }
  | { state: 'unknownTeam'; windowLabel?: string };

const DOT: Record<BriefingSeverity | 'ok', string> = {
  critical: 'bg-status-critical',
  warn: 'bg-status-warn',
  info: 'bg-status-info',
  muted: 'bg-muted-foreground/60',
  ok: 'bg-status-ok',
};
const NUM: Record<BriefingSeverity, string> = {
  critical: 'text-status-critical',
  warn: 'text-status-warn',
  info: 'text-status-info',
  muted: 'text-muted-foreground',
};

function Dot({ tone }: { tone: BriefingSeverity | 'ok' }) {
  return <span aria-hidden="true" data-dot={tone} className={cn('mt-1.5 size-1.5 shrink-0 rounded-full', DOT[tone])} />;
}

function Eyebrow({ children, aside }: { children: ReactNode; aside?: ReactNode }) {
  return (
    <div className="flex items-center gap-2">
      <span className="text-[11px] font-medium tracking-wide text-muted-foreground uppercase">{children}</span>
      <span className="flex-1" />
      {aside && <span className="text-[11px] text-muted-foreground">{aside}</span>}
    </div>
  );
}

function LineText({ line }: { line: BriefingFeedLine }) {
  return (<>
    {line.parts.map((p, i) => (
      <span key={i} className={cn(
        p.tone === 'subject' && 'font-mono font-semibold',
        p.tone === 'num' && cn('font-semibold tabular-nums', NUM[line.severity]),
        p.tone === 'muted' && 'text-muted-foreground',
      )}>{p.text}</span>
    ))}
  </>);
}

function FeedLine({ line, onAsk }: { line: BriefingFeedLine; onAsk: (l: BriefingFeedLine) => void }) {
  return (
    <li className="flex items-start gap-2 py-2" data-briefing-line={line.id}>
      <Dot tone={line.severity} />
      <span className="mt-px w-14 shrink-0 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase tabular-nums">{line.kind}</span>
      <Button variant="link" size="xs" title={t('cosre.welcome.briefing.askTitle')}
        className="h-auto min-w-0 flex-1 justify-start p-0 text-left text-[13px] leading-snug font-normal whitespace-normal text-foreground decoration-muted-foreground"
        onClick={() => onAsk(line)}>
        <span><LineText line={line} /></span>
      </Button>
      {line.trace && (
        <Button asChild variant="ghost" size="xs" className="shrink-0 text-muted-foreground" data-briefing-trace="">
          <Link to={line.trace.href} title={line.trace.label}>{line.trace.label}<ArrowUpRight aria-hidden="true" /></Link>
        </Button>
      )}
      {line.pivot && (
        <Button asChild variant="ghost" size="xs" className="shrink-0 text-muted-foreground">
          <Link to={line.pivot.href} title={line.pivot.label}>{line.pivot.label}<ArrowUpRight aria-hidden="true" /></Link>
        </Button>
      )}
    </li>
  );
}

const fill = (s: string, vars: Record<string, string | number>) =>
  s.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k]) : m));

export function BriefingFeed({ view, onAskLine, teamPicker }: {
  view: BriefingView | null;
  onAskLine: (line: BriefingFeedLine) => void;
  /** unknownTeam hâlinde çizilen takım seçici (sunucu-aramalı Command). */
  teamPicker?: ReactNode;
}) {
  if (!view) return null;
  if (view.state === 'unknownTeam') {
    return (
      <section className="flex flex-col gap-2.5" data-briefing="unknownTeam" aria-label={t('cosre.welcome.briefing.aria')}>
        {view.windowLabel && <Eyebrow>{fill(t('cosre.welcome.briefing.eyebrow'), { window: view.windowLabel })}</Eyebrow>}
        <p className="m-0 flex items-start gap-2 text-[13px]">
          <Dot tone="muted" />
          <span>
            <b className="font-semibold">{t('cosre.welcome.noTeam.title')}</b>{' — '}{t('cosre.welcome.noTeam.ask')}{' '}
            <span className="text-muted-foreground">{t('cosre.welcome.noTeam.hint')}</span>
          </span>
        </p>
        {teamPicker}
      </section>
    );
  }
  const eyebrow = view.state === 'findings'
    ? fill(t('cosre.welcome.briefing.eyebrowN'), { window: view.windowLabel, n: view.lines.length })
    : fill(t('cosre.welcome.briefing.eyebrow'), { window: view.windowLabel });
  return (
    <section className="flex flex-col" data-briefing={view.state} aria-label={t('cosre.welcome.briefing.aria')}>
      <Eyebrow aside={view.agoLabel}>{eyebrow}</Eyebrow>
      <Separator className="mt-1" />
      {view.state === 'silence' ? (
        <p className="m-0 flex items-start gap-2 py-2 text-[13px]">
          <Dot tone="ok" />
          <span>
            {fill(t('cosre.welcome.briefing.silence'), { window: view.windowLabel })}{' '}
            <b className="font-mono font-semibold">{view.team}</b>{' '}{t('cosre.welcome.briefing.silenceTail')}
            {view.serviceCount !== undefined && (
              <span className="text-muted-foreground">{' '}{fill(t('cosre.welcome.briefing.watched'), { n: view.serviceCount })}</span>
            )}
          </span>
        </p>
      ) : (
        <ul className="m-0 list-none divide-y divide-border p-0">
          {view.lines.map(l => <FeedLine key={l.id} line={l} onAsk={onAskLine} />)}
        </ul>
      )}
      <Separator />
    </section>
  );
}
