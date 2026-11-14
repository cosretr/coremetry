import type { ReactNode } from 'react';
import { ChevronRight } from 'lucide-react';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/shadcn/collapsible';
import { Button } from '@/components/shadcn/button';

// CollapsedSection — v0.10.2132 (servis sekmeleri sadeleştirme S1,
// docs/frontend/service-tabs-plan.md; mockup operatör onayı 2026-11-13).
// Sayfa içi bölümün aç/kapa bileşiği: KAPALIYKEN tek satır (başlık + tek
// cümle özet), AÇIKKEN SectionHead görünümünde başlık (rozetler, meta,
// eylemler) + gövde + bölüm altında küçük "kaynak" satırı (eski başlık
// rozeti — "bu sayı nereden" sorusu kaybolmaz, yalnız yer değiştirir).
//
// VERİ SÖZLEŞMESİ: gövde (children) yalnız AÇIKKEN monte edilir (Radix
// CollapsibleContent kapalıyken çocuk çizmez) — bölümün kendi okumaları
// açılana kadar YAPILMAZ. Özet çağıranın ZATEN yüklü verisinden kurulur;
// bu bileşen hiçbir şey çekmez.
//
// `id` başlık satırında durur: derin link (#dtl-*) kapalı bölümde de bir
// hedef bulur; açmak çağıranın işi (hash → onOpenChange(true)).
// Durum kontrollü (open + onOpenChange): kalıcılık çağıranda.
export interface CollapsedSectionProps {
  id?: string;
  title: ReactNode;
  /** Kapalı satırdaki tek cümle. */
  summary?: ReactNode;
  /** Okunan kaynak — açıkken bölüm altındaki "kaynak" satırı. */
  source?: ReactNode;
  /** Açıkken başlığın hemen sağı (kapsam rozetleri, kip düğmesi). */
  badges?: ReactNode;
  /** Açıkken sağ uç: sayı / tazelik. */
  meta?: ReactNode;
  /** Açıkken sağ uç: bağlantı / düğmeler. */
  actions?: ReactNode;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  children: ReactNode;
}

export function CollapsedSection({ id, title, summary, source, badges, meta, actions, open, onOpenChange, children }: CollapsedSectionProps) {
  return (
    <Collapsible open={open} onOpenChange={onOpenChange}>
      <div className="dtl-sech sec-head" id={id}>
        <CollapsibleTrigger asChild>
          <Button variant="ghost" size="xs" className="-ml-2 uppercase tracking-[.6px]">
            <ChevronRight aria-hidden="true" className={open ? 'rotate-90 transition-transform' : 'transition-transform'} />
            <span className="sec-head__title">{title}</span>
          </Button>
        </CollapsibleTrigger>
        {open ? (
          <>
            {badges}
            <span className="sec-head__rule" aria-hidden="true" />
            {meta && <span className="sec-head__meta">{meta}</span>}
            {actions && <span className="sec-head__actions">{actions}</span>}
          </>
        ) : (
          <>
            {summary && <span className="sec-head__meta csec-summary truncate">{summary}</span>}
            <span className="sec-head__rule" aria-hidden="true" />
          </>
        )}
      </div>
      <CollapsibleContent>
        {children}
        {source && (
          <p className="csec-source -mt-2 mb-3 text-[length:var(--fs-xs)] text-(--text3)">
            kaynak: {source}
          </p>
        )}
      </CollapsibleContent>
    </Collapsible>
  );
}
