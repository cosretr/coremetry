// EntityLink — v0.10.2488 (entity tutarlılık denetimi dilim 4, §3.5).
//
// Bir entity'nin adını çizen TEK bağlantı. href'i tür kaydı kurar
// (`lib/entityKinds.ts` `entityTargetHref`); çağıran üreticiyi doğrudan
// çağırmaz, hedefi (`to`) bugünkü üreticinin argümanlarıyla verir — böylece
// taşıdığı sorgu parametreleri (range, env, tab, cluster, params …) birebir
// korunur. Hedef rota bu dilimde DEĞİŞMEZ.
//
// `to.kind === 'subject'` — eski `SubjectLink` buraya katlandı (Problem /
// Inbox satırının öznesi): servis öznesi `/service?name=` linki; servis
// OLMAYAN özne (db / external) bağlantısız etiket + rozet + nedenini söyleyen
// title (`components/SubjectLink.tsx:47-63` dalı aynen). Ayrı bileşen
// kalmadı; tek tüketici sözleşmesi tek yerde.
//
// Hover kartı (Karar A5): v1 İSTEK ATMAZ — yalnız satırın elindeki veri
// (`card`) + tür kaydı + saf pivot URL'leri. Kart gövdesi tembel parça
// (`EntityHoverCard`), ilk hover'da 400 ms sonra yüklenir; fare ayrılınca
// zamanlayıcı iptal. Satır başına önden istek YOK.
//
// Paket: bu dosya yalnız `<Link>` + zamanlayıcı; giriş paketine girmez
// (sayfa parçalarında kalır), kart `lazy`.
import { lazy, Suspense, useEffect, useRef, useState, type MouseEvent, type ReactNode } from 'react';
import { Link, type LinkProps } from 'react-router';
import {
  entityTargetHref, entityTargetKind, entityTargetName, type EntityKind, type EntityTarget,
} from '@/lib/entityKinds';
import { subjectIsLinkable, subjectKind, subjectLabel, subjectTitle } from '@/lib/problemSubject';
import type { ServiceHrefOpts } from '@/lib/serviceHref';
import { useLang } from '@/lib/i18n';
import type { HealthLevel } from '../HealthDot';

const EntityHoverCard = lazy(() => import('./EntityHoverCard'));

/** Satırın zaten taşıdığı veri — kart bundan başka bir şey göstermez. */
export interface EntityCardData {
  health?: HealthLevel;
  healthTitle?: string;
  /** En çok 3 ölçü (fazlası kesilir). */
  metrics?: Array<{ label: string; value: ReactNode }>;
}

/** Problem / Inbox öznesi (eski SubjectLink). */
export interface SubjectTarget {
  kind: 'subject';
  /** Öznenin kendisi — Problem.service / InboxItem.service. */
  service: string;
  /** Öznenin TÜRÜ (Problem.kind / InboxItem.subjectKind) — InboxItem.kind DEĞİL. */
  subjectKind?: string;
  opts?: ServiceHrefOpts;
}

type Props = Omit<LinkProps, 'to'> & {
  to: EntityTarget | SubjectTarget;
  /** Özne boşken basılacak içerik (yalnız `subject`). */
  emptyFallback?: ReactNode;
  /** false = kart yok (ör. zaten kart/çekmece içindeki bağlantı). */
  card?: EntityCardData | false;
};

export const HOVER_DELAY_MS = 400;
const CLOSE_GRACE_MS = 150;

export function EntityLink({ to, children, emptyFallback, card, onMouseEnter, onMouseLeave, ...rest }: Props) {
  const lang = useLang();
  const anchor = useRef<HTMLAnchorElement>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [open, setOpen] = useState(false);
  useEffect(() => () => { if (timer.current) clearTimeout(timer.current); }, []);

  let target: EntityTarget;
  let kind: EntityKind;
  let text: ReactNode = children;
  if (to.kind === 'subject') {
    const { service, subjectKind: sk, opts } = to;
    if (!service && emptyFallback !== undefined) {
      return <span style={rest.style} className={rest.className}>{emptyFallback}</span>;
    }
    if (!subjectIsLinkable(service, sk)) {
      // Çalışmayan bir bağlantı, bağlantı olmamasından KÖTÜDÜR (SubjectLink
      // v0.9.1339). Rozet, düz metnin "bu satır bozuk" gibi okunmasını engeller.
      const sub = subjectKind(service, sk);
      return (
        <span style={rest.style} className={rest.className} title={subjectTitle(service, lang)}>
          {sub === 'db' && <span className="badge b-gray" style={{ fontSize: 9, marginRight: 4 }}>DB</span>}
          {sub === 'external' && <span className="badge b-gray" style={{ fontSize: 9, marginRight: 4 }}>EXT</span>}
          {subjectLabel(service)}
        </span>
      );
    }
    target = { kind: 'service', name: service, opts };
    kind = 'service';
    text = text ?? subjectLabel(service);
  } else {
    target = to;
    kind = entityTargetKind(to);
  }

  const href = entityTargetHref(target);
  const label = text ?? entityTargetName(target);
  if (href === null) {
    return <span style={rest.style} className={rest.className} title={rest.title}>{label}</span>;
  }

  const clear = () => { if (timer.current) { clearTimeout(timer.current); timer.current = null; } };
  const enter = (e: MouseEvent<HTMLAnchorElement>) => {
    onMouseEnter?.(e);
    if (card === false) return;
    clear();
    if (!open) timer.current = setTimeout(() => setOpen(true), HOVER_DELAY_MS);
  };
  const leave = (e: MouseEvent<HTMLAnchorElement>) => {
    onMouseLeave?.(e);
    clear();
    if (open) timer.current = setTimeout(() => setOpen(false), CLOSE_GRACE_MS);
  };

  // Yapı sabit (Link hep ilk çocuk): kart açılınca bağlantı yeniden
  // bağlanmaz, odak / hover kaybolmaz.
  return (
    <>
      <Link ref={anchor} to={href} onMouseEnter={enter} onMouseLeave={leave} {...rest}>{label}</Link>
      {open && card !== false && (
        <Suspense fallback={null}>
          <EntityHoverCard anchor={anchor} kind={kind} target={target} name={entityTargetName(target)}
            data={card} onHold={clear} onClose={() => { clear(); setOpen(false); }}
            onLeave={() => { clear(); timer.current = setTimeout(() => setOpen(false), CLOSE_GRACE_MS); }} />
        </Suspense>
      )}
    </>
  );
}
