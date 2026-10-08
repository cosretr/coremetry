import { BookOpen, Compass, Search, Workflow, type LucideIcon } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { capabilityItems, splitCaret, tCosre as t, type CapabilityKind } from './capabilityHints';

// CosreCapabilities — v0.10.1128: boş CoSRE sohbetinde "neler yapabilirim"
// satırları (çekmece + /cosre aynı bileşen: CopilotChat). Tıklama composer'ı
// örnek soruyla doldurur, GÖNDERMEZ (onPrefill). Kurallar capabilityHints.ts.
// Metinler sohbetin dilinde (tCosre, sabit TR) — UI dilinde DEĞİL; gerekçe orada.
const ICON: Record<CapabilityKind, LucideIcon> = {
  wiki: BookOpen,
  service: Search,
  operation: Workflow,
  navigate: Compass,
};

export function CosreCapabilities({ wiki, onPrefill }: {
  wiki: boolean;
  onPrefill: (text: string, caret: number) => void;
}) {
  return (
    <div className="cosre-cap" role="list" aria-label={t('cosre.cap.aria')}>
      {capabilityItems(wiki).map(it => {
        const Icon = ICON[it.kind];
        return (
          <div role="listitem" key={it.kind}>
            <Button variant="ghost" size="sm" className="cosre-cap__row"
              data-cap={it.kind}
              title={t('cosre.cap.tryHint')}
              leftIcon={<Icon size={13} aria-hidden="true" className="cosre-cap__icon" />}
              onClick={() => { const p = splitCaret(t(it.promptKey)); onPrefill(p.text, p.caret); }}>
              <span>{t(it.labelKey)}</span>
            </Button>
          </div>
        );
      })}
    </div>
  );
}
