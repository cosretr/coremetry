// InvestigationQuality — CoSRE Faz B (araştırma kalitesi). InvestigationResult
// çağırır:
//   - InvestigationGateBanner: kanıt kapısı "yetersiz" + kip show iken
//     "Kanıt yetersiz" bandı (eksikler listesi). Kip shadow (VARSAYILAN) →
//     karar kayıtta, burada ÇİZİLMEZ.
//   - InvestigationQuality: oyun kitabı adı + kompakt hipotez ağacı
//     (desteklenen önce; elenenler katlı, gerekçesiyle) + erişim beyanı
//     (bakılan / bakılamayan kaynaklar). Durumsuz kayıt (eski / plain) → yok.
// Liste tek sütunlu durum akışı (tablo değil); renk yalnız sapan değerde.
import { useState } from 'react';
import { ChevronRight } from 'lucide-react';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { Button } from '@/components/shadcn/button';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/shadcn/collapsible';
import { t, tf, useLang } from '@/lib/i18n';
import type { SREHypothesis, SREInvestigation } from '@/lib/types';
import { fmtScore, hypLabel } from './hypothesisView';
import { OrigLang } from './OrigLang';
import {
  gapText, hypothesisTree, playbookLabel, reachSplit, reachStatusLabel, showGate, sourceLabel, stateLabel, stateTone,
} from './investigationQualityView';

const muted = { color: 'var(--text3)' } as const;

export function InvestigationGateBanner({ inv }: { inv: SREInvestigation }) {
  const lang = useLang();
  const g = inv.coverage;
  if (!showGate(g)) return null;
  return (
    <div className="cm-callout is-caution" role="status" data-testid="iq-gate" style={{ margin: 0 }}>
      <b>{t('iq.gate.head', lang)}</b> — {t('iq.gate.body', lang)}
      <ul style={{ margin: '4px 0 0', paddingLeft: 18 }}>
        {(g.missing ?? []).map((m, i) => <li key={`${m.code}:${m.source ?? ''}:${i}`}>{gapText(m, lang)}</li>)}
      </ul>
    </div>
  );
}

function TreeRow({ h }: { h: SREHypothesis }) {
  const lang = useLang();
  const refs = h.stateRefs?.length ?? 0;
  return (
    <li data-state={h.state} style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'baseline', gap: 6 }}>
      <span style={h.state === 'untested' || h.state === 'ruled_out' ? muted : undefined}>{hypLabel(h, lang)}</span>
      {h.state === 'supported' || h.state === 'tested'
        ? <StatusBadge status={stateTone(h.state)} size="sm">{stateLabel(h.state, lang)}</StatusBadge>
        : <span style={{ ...muted, fontSize: 12 }}>{stateLabel(h.state, lang)}</span>}
      <span className="num" style={{ ...muted, fontSize: 12 }}>{fmtScore(h.score)}</span>
      {refs > 0 && <span style={{ ...muted, fontSize: 12 }}>{tf('iq.tree.refs', lang, { n: refs })}</span>}
      {h.state === 'ruled_out' && h.stateReason && (
        <span style={{ ...muted, fontSize: 12, flexBasis: '100%', paddingLeft: 12 }}><OrigLang>{h.stateReason}</OrigLang></span>
      )}
    </li>
  );
}

export function InvestigationQuality({ inv }: { inv: SREInvestigation }) {
  const lang = useLang();
  const [showRuled, setShowRuled] = useState(false);
  const { open, ruledOut } = hypothesisTree(inv);
  const { checked, blind } = reachSplit(inv.reach);
  const pb = playbookLabel(inv, lang);
  if (open.length === 0 && ruledOut.length === 0 && checked.length === 0 && blind.length === 0) return null;
  const list = { margin: 0, paddingLeft: 0, listStyle: 'none', display: 'grid', gap: 2 } as const;
  return (
    <div style={{ display: 'grid', gap: 6 }} data-testid="iq">
      {(open.length > 0 || ruledOut.length > 0) && (
        <div>
          <div style={{ ...muted, fontSize: 12, marginBottom: 4 }}>{t('iq.tree.head', lang)}{pb ? ` · ${pb}` : ''}</div>
          <ul aria-label={t('iq.tree.aria', lang)} style={list}>
            {open.map(h => <TreeRow key={h.kind} h={h} />)}
          </ul>
          {ruledOut.length > 0 && (
            <Collapsible open={showRuled} onOpenChange={setShowRuled}>
              <CollapsibleTrigger asChild>
                <Button variant="ghost" size="xs" style={{ marginLeft: -8 }}>
                  <ChevronRight aria-hidden="true" className={showRuled ? 'rotate-90 transition-transform' : 'transition-transform'} />
                  {tf('iq.tree.ruledOut', lang, { n: ruledOut.length })}
                </Button>
              </CollapsibleTrigger>
              <CollapsibleContent>
                <ul style={list}>{ruledOut.map(h => <TreeRow key={h.kind} h={h} />)}</ul>
              </CollapsibleContent>
            </Collapsible>
          )}
        </div>
      )}
      {(checked.length > 0 || blind.length > 0) && (
        <div style={{ fontSize: 12 }} data-testid="iq-reach">
          <span style={muted}>{t('iq.reach.head', lang)} · {t('iq.reach.checked', lang)}: </span>
          {checked.length ? checked.map(r => sourceLabel(r.source, lang) + (r.status === 'partial' ? ` (${reachStatusLabel(r, lang)})` : '')).join(', ') : t('iq.reach.none', lang)}
          {blind.length > 0 && (
            <>
              <span style={muted}> · {t('iq.reach.blind', lang)}: </span>
              {blind.map((r, i) => (
                <span key={r.source} title={r.note}>
                  {i > 0 ? ', ' : ''}{sourceLabel(r.source, lang)} <span style={muted}>({reachStatusLabel(r, lang)})</span>
                </span>
              ))}
            </>
          )}
        </div>
      )}
    </div>
  );
}
