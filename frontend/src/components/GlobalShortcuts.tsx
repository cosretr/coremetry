import { useNavigate, useLocation } from 'react-router';
import { useShortcuts } from '@/lib/keyboard';
import { navHref } from '@/lib/navHref';
import { useIncidentsEnabled } from './AuthProvider';
import { incidentsNavVisible } from '@/lib/incidentsRetired'; // v0.10.1828
import { t, tf, useCatalogs, useLang } from '@/lib/i18n';

// GlobalShortcuts (v0.5.444) — registers app-wide power-user
// shortcuts: '/' to focus the page search input, and 'g <x>'
// two-key sequences for fast page navigation (Datadog / Linear
// muscle memory). Self-contained, mounted once at AppShell so
// every authenticated page inherits the bindings without
// per-page imports. Labels surface in the '?' help modal via
// the existing listShortcuts() registry walk.

// v0.8.525 — SINGLE 'g <x>' registry. Öncesinde AppShell.tsx ayrı bir
// useShortcuts bloğu daha kaydediyordu; ikisi 'g o' (Topology vs
// Monitors) ve 'g m' (Metrics vs Service Map) üzerinde ÇAKIŞIYORdu ve
// yarısı ölü/gizli rotaya (g h → silinmiş Home, g o → gizli Monitors)
// gidiyordu. Artık tek yer; her satır CANLI + görünür bir rota, harf
// başına tek anlam. 'g n' → Inbox eklendi (günlük iniş yüzeyi).
// v0.10.2486 dilim 2 — etiket = "Go to <menü adı>": nav.* anahtarından (tek ad;
// eski 'Go to Problems' Hatalar sayfasına, 'Go to Inbox' Problemler'e gidiyordu).
const PAGES: { key: string; navKey: string; to: string }[] = [
  { key: 'g n', navKey: 'nav.inbox',      to: '/inbox' },
  { key: 'g i', navKey: 'nav.incidents',  to: '/incidents' },
  { key: 'g p', navKey: 'nav.problems',   to: '/problems' },
  { key: 'g a', navKey: 'inbox.tab.signals', to: '/inbox?tab=signals' }, // v0.10.2481 — Problems › Sinyaller
  { key: 'g s', navKey: 'nav.services',   to: '/services' },
  { key: 'g o', navKey: 'nav.topology',   to: '/service-map' }, // 'o' = tOpology mnemonic (retired /topology → /service-map)
  { key: 'g t', navKey: 'nav.traces',     to: '/traces' },
  { key: 'g m', navKey: 'nav.metrics',    to: '/metrics' },
  { key: 'g l', navKey: 'nav.logs',       to: '/logs' },
  { key: 'g e', navKey: 'nav.explore',    to: '/explore' },
  { key: 'g r', navKey: 'nav.runbooks',   to: '/runbooks' },
  { key: 'g d', navKey: 'nav.dashboards', to: '/dashboards' },
  { key: 'g x', navKey: 'nav.alerts',     to: '/alerts' },
  { key: 'g c', navKey: 'nav.system',     to: '/system/stats' },
];

// focusPageSearch finds the page's search input and focuses+selects
// it. Resolution order:
//   1. The first element with `data-shortcut-search` — explicit
//      opt-in by the page so `/` lands where the operator expects
//      (e.g. the Service picker on /traces, not the trace-id
//      lookup which is the first DOM input).
//   2. The first visible text input or textarea on the page.
//      Fallback for pages that haven't been annotated yet.
// v0.5.454 — the fallback alone landed on the wrong field on
// /traces (Trace ID lookup is first in DOM, search picker is
// later) and missed /admin/sql entirely (textarea, not input).
function focusPageSearch(): void {
  const SELECT_VISIBLE = (el: HTMLElement) =>
    el.offsetParent !== null &&
    !(el as HTMLInputElement | HTMLTextAreaElement).disabled &&
    !(el as HTMLInputElement | HTMLTextAreaElement).readOnly;

  // 1. Explicit opt-in target wins.
  const opted = document.querySelectorAll<HTMLElement>('[data-shortcut-search]');
  for (const el of Array.from(opted)) {
    const target = el.matches('input, textarea')
      ? el
      : el.querySelector<HTMLElement>('input, textarea');
    if (target && SELECT_VISIBLE(target)) {
      target.focus();
      if ('select' in target && typeof (target as HTMLInputElement).select === 'function') {
        (target as HTMLInputElement).select();
      }
      return;
    }
  }

  // 2. Fallback — first visible text input or textarea.
  const inputs = document.querySelectorAll<HTMLElement>(
    'input[type="text"], input[type="search"], input:not([type]), textarea'
  );
  for (const el of Array.from(inputs)) {
    if (SELECT_VISIBLE(el)) {
      el.focus();
      if ('select' in el && typeof (el as HTMLInputElement).select === 'function') {
        (el as HTMLInputElement).select();
      }
      return;
    }
  }
}

export function GlobalShortcuts() {
  const navigate = useNavigate();
  // v0.9.932 (UX denetimi K2) — `g x` kısayolları da mutlak pencereyi
  // taşıyor. Bu yüzey en sinsi olanıydı: klavyeyle gezinen operatör
  // sinyaller arasında en hızlı geçen, yani pencereyi en çok kaybeden.
  const { search } = useLocation();
  // v0.10.1828 — Incidents emekliyken "g i" kaydı yok (gizli sayfaya kısayol olmaz).
  const incidentsOn = useIncidentsEnabled();
  const lang = useLang();
  const catalogVer = useCatalogs(); // katalog geç yüklenirse etiketler yenilensin

  useShortcuts(
    [
      {
        keys: '/',
        label: t('shortcut.focusSearch', lang),
        group: 'Navigation',
        // keyboard.ts already calls preventDefault before the
        // handler, so the browser's built-in quick-find on '/'
        // is suppressed automatically.
        handler: () => focusPageSearch(),
      },
      ...PAGES.filter(p => incidentsNavVisible(p.to, incidentsOn)).map(p => ({
        keys: p.key,
        label: tf('shortcut.goTo', lang, { page: t(p.navKey, lang) }),
        group: 'Navigation',
        handler: () => navigate(navHref(p.to, search)),
      })),
    ],
    // v0.9.932 — `search` DEP: navigate kimliği sabit ama handler artık
    // location'ı kapatıyor. Boş dep listesiyle binding ilk mount'taki
    // pencereyi sonsuza dek taşırdı — düzeltilen bug'ın daha sinsi hâli.
    [search, incidentsOn, lang, catalogVer],
  );

  return null;
}
