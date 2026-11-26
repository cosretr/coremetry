import { savedViewTooLarge } from '@/lib/savedViewLimits';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { useLocation, useNavigate } from 'react-router';
import { useAuth } from '@/components/AuthProvider';
import { Button } from '@/components/ui/Button';
import { LinkButton } from '@/components/ui/LinkButton';
import { MenuItem } from '@/components/ui/Menu';
import { Popover } from '@/components/ui/Popover';
import { useShortcuts, type Shortcut } from '@/lib/keyboard';
import { api } from '@/lib/api';
import { toast } from '@/lib/toast';
import type { SavedView } from '@/lib/types';
import { useConfirm } from '@/components/ui/ConfirmDialog';
import { t as tl, useLang } from '@/lib/i18n'; // v0.10.2317 — silme onayı arayüz dilinde (TR aynı)

// Normalise a URL query string so two semantically-equal forms
// compare equal: strip leading `?`, parse → sort by key → re-emit.
// `?b=2&a=1` and `?a=1&b=2` collapse to the same key. Empty
// string when there's nothing to compare (no params). v0.5.453.
function normaliseQS(qs: string): string {
  const q = qs.replace(/^\?/, '');
  if (!q) return '';
  const pairs = q.split('&').filter(Boolean);
  pairs.sort();
  return pairs.join('&');
}

// SavedViewsBar lives at the top of filter-heavy pages (/traces,
// /logs, /anomalies) and gives the operator a one-click way to
// stash the current URL filter combo and recall it later. The
// query state is the page's existing URL search string — saving
// = remembering window.location.search, applying = restoring it.
// No coupling between server schema and SPA filter shape.
//
// Permissions:
//   - Anyone signed in can save personal views.
//   - Admins can flip "shared" so everyone on the team sees it.
//   - You can only delete your own views (admins can delete any).
export function SavedViewsBar({ page, right, variant = 'bar', size, viewQuery }: {
  page: string;
  // v0.10.2481 — kayıtlı sorgunun bu sayfadaki hali (uygula + "etkin görünüm"
  // karşılaştırması). Problems › Sinyaller eski Anomalies kayıtlarına ?tab=
  // ekler (lib/inboxTabs withSignalsTab); verilmezse sorgu aynen.
  viewQuery?: (qs: string) => string;
  // v0.9.304 — trailing slot, right-aligned on the same line. /traces
  // parks Reset + CSV here: they are page-level actions, and the strip
  // they used to sit in was removed as a whole row (operator).
  right?: ReactNode;
  // v0.10.1498 (/logs Kibana-Discover yeniden tasarımı) — 'compact': şerit
  // yerine sorgu çubuğunda tek "Saved ▾" düğmesi + menü (uygula / sil /
  // "＋ Save current view…" diyaloğu). Durum, kısayollar (1-9), "● modified"
  // ve izinler şeritle AYNI çekirdekten; varsayılan 'bar' diğer sayfalarda
  // BAYT BAYT eski hâl.
  variant?: 'bar' | 'compact';
  // v0.10.1524 — compact tetikleyicinin boyu. /traces grafik başlığında Reset /
  // CSV ile aynı `sm` rung'u; verilmezse atomun varsayılanı (/logs md, aynen).
  size?: 'sm';
}) {
  const lang = useLang();
  const confirm = useConfirm();
  const navigate = useNavigate();
  const location = useLocation();
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';

  const [views, setViews] = useState<SavedView[] | undefined>(undefined);
  const menuAnchorRef = useRef<HTMLButtonElement>(null);
  const [menuOpen, setMenuOpen] = useState(false);
  const [showSaver, setShowSaver] = useState(false);
  const [name, setName] = useState('');
  const [shared, setShared] = useState(false);
  // v0.5.453 — lastAppliedViewId tracks which view the operator
  // most recently applied (by clicking a chip or pressing 1-9).
  // Persisted in sessionStorage per page so it survives in-app
  // navigation but resets on a hard reload. When this is set AND
  // the current URL has drifted away from the view's queryString,
  // we surface a "● modified" badge + ↺ revert affordance so the
  // operator sees "I'm on the Errors-only view but with extra
  // filters added".
  const sessionKey = `coremetry-active-view:${page}`;
  const [lastAppliedViewId, setLastAppliedViewId] = useState<string | null>(() => {
    try { return sessionStorage.getItem(sessionKey); } catch { return null; }
  });

  useEffect(() => {
    api.savedViews(page).then(v => setViews(v ?? [])).catch(() => setViews([]));
  }, [page]);

  // Recompute "is current URL identical to view V?" on every nav.
  // Exact match (key set + values equal, order-insensitive) sets
  // lastAppliedViewId to V. If nothing matches and lastAppliedViewId
  // was previously set, we KEEP it so the modified-from indicator
  // still has a target to revert to.
  const currentQS = useMemo(() => normaliseQS(location.search), [location.search]);
  const viewQs = useCallback((qs: string) => (viewQuery ? viewQuery(qs) : qs), [viewQuery]);
  useEffect(() => {
    if (!views) return;
    const match = views.find(v => normaliseQS(viewQs(v.queryString)) === currentQS);
    if (match) {
      setLastAppliedViewId(match.id);
      try { sessionStorage.setItem(sessionKey, match.id); } catch { /* noop */ }
    }
  }, [currentQS, views, sessionKey, viewQs]);

  const activeViewId = useMemo(() => {
    if (!views) return null;
    return views.find(v => normaliseQS(viewQs(v.queryString)) === currentQS)?.id ?? null;
  }, [views, currentQS, viewQs]);

  const apply = (v: SavedView) => {
    const qs = viewQs(v.queryString);
    const target = window.location.pathname + (qs ? '?' + qs : '');
    setLastAppliedViewId(v.id);
    try { sessionStorage.setItem(sessionKey, v.id); } catch { /* noop */ }
    navigate(target);
  };

  // Keyboard 1-9 → first-nine saved views. Datadog "favourites"
  // shortcut equivalent. Order matches what the user sees: any
  // "shared" / starred views float first, then the personal
  // ones; the 1-9 keys map to that visible ordering. Bindings
  // are skipped while typing in inputs (useShortcuts already
  // handles that) so saving "1" in a name field doesn't warp
  // the page.
  const ordered = useMemo<SavedView[]>(() => {
    if (!views) return [];
    return [...views].sort((a, b) => {
      const aShared = a.ownerId === '' ? 1 : 0;
      const bShared = b.ownerId === '' ? 1 : 0;
      if (aShared !== bShared) return bShared - aShared;
      return a.name.localeCompare(b.name);
    });
  }, [views]);
  const numericShortcuts = useMemo<Shortcut[]>(() => {
    return ordered.slice(0, 9).map((v, i) => ({
      keys: String(i + 1),
      label: `Saved view: ${v.name}`,
      group: 'Saved views',
      handler: () => apply(v),
    }));
  // The handler closes over `navigate` (stable) and `v` (per
  // entry); deps include the ordered list reference so a
  // re-ordering or rename re-registers the right handlers.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ordered]);
  useShortcuts(numericShortcuts, [numericShortcuts]);

  const save = async () => {
    const trimmed = name.trim();
    if (!trimmed) return;
    // Strip leading '?' so the server stores a stable search string.
    const qs = window.location.search.replace(/^\?/, '');
    const tooLarge = savedViewTooLarge(qs); // v0.10.1236 — sunucu tavanıyla aynı
    if (tooLarge) { toast.error(tooLarge); return; }
    try {
      await api.createSavedView({ name: trimmed, page, queryString: qs, shared });
      setName('');
      setShared(false);
      setShowSaver(false);
      api.savedViews(page).then(v => setViews(v ?? []));
      toast.success(`Saved view "${trimmed}"`);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      toast.error(`Failed to save view: ${msg}`);
    }
  };

  const remove = async (v: SavedView) => {
    if (!await confirm({
      title: tl('svb.delTitle', lang),
      body: <><b>{v.name}</b>{tl('svb.delBody', lang)}</>,
      confirmLabel: tl('svb.delConfirm', lang),
      danger: true,
    })) return;
    try {
      await api.deleteSavedView(v.id);
      api.savedViews(page).then(v => setViews(v ?? []));
      toast.success(`Deleted view "${v.name}"`);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      toast.error(`Failed to delete view: ${msg}`);
    }
  };

  if (variant === 'compact') {
    const activeView = ordered.find(v => v.id === activeViewId);
    const modifiedView = !activeView && lastAppliedViewId
      ? ordered.find(v => v.id === lastAppliedViewId) : undefined;
    return (
      <>
        <Button ref={menuAnchorRef} variant="secondary" size={size} aria-haspopup="menu" aria-expanded={menuOpen}
          disabled={views === undefined}
          title={activeView ? `Saved view: ${activeView.name}`
            : modifiedView ? `Drifted from saved view "${modifiedView.name}" — open to restore`
            : 'Saved views — apply, save or delete a filter combo'}
          onClick={() => { setShowSaver(false); setMenuOpen(o => !o); }}>
          <span className="ld-saved-label">
            {activeView ? `✓ ${activeView.name}` : modifiedView ? `● ${modifiedView.name}` : 'Saved'}
          </span> ▾
        </Button>
        <Popover anchorRef={menuAnchorRef} open={menuOpen} onClose={() => setMenuOpen(false)} kind="menu"
          ariaLabel="Saved views" width={300}>
          {ordered.length === 0 && (
            <div className="ld-saved-empty">(none yet — Save current view to pin a filter combo)</div>
          )}
          {ordered.map((v, i) => {
            const isActive = activeViewId === v.id;
            const isModified = !isActive && lastAppliedViewId === v.id;
            return (
              <div key={v.id} className="ld-saved-row">
                <MenuItem className="ld-saved-apply"
                  icon={isActive ? '✓' : isModified ? '●' : v.ownerId === '' ? '★' : ''}
                  onClick={() => { setMenuOpen(false); apply(v); }}
                  title={`${v.ownerId === '' ? 'Team-shared view' : 'Your view'}${isModified ? ' · drifted; click to restore' : ''}${i < 9 ? ` · press ${i + 1}` : ''}`}>
                  {v.name}
                  {i < 9 && <kbd className="kbd">{i + 1}</kbd>}
                </MenuItem>
                {(v.ownerId === user?.id || isAdmin) && (
                  <MenuItem danger className="ld-saved-del" aria-label={`Delete view ${v.name}`} title="Delete"
                    onClick={() => { setMenuOpen(false); void remove(v); }}>×</MenuItem>
                )}
              </div>
            );
          })}
          <div className="ld-menu-sep" role="separator" />
          <MenuItem onClick={() => { setMenuOpen(false); setShowSaver(true); }}>＋ Save current view…</MenuItem>
        </Popover>
        <Popover anchorRef={menuAnchorRef} open={showSaver} onClose={() => setShowSaver(false)} kind="dialog"
          ariaLabel="Save current view" width={300}>
          <input
            placeholder="Name…"
            aria-label="Saved view name"
            value={name}
            onChange={e => setName(e.target.value)}
            onKeyDown={e => { if (e.key === 'Enter') void save(); }} />
          {isAdmin && (
            <label className="ld-saved-share">
              <input type="checkbox" checked={shared}
                onChange={e => setShared(e.target.checked)} />
              Share with team
            </label>
          )}
          <Button variant="primary" size="sm" onClick={() => void save()} disabled={!name.trim()}>
            Save
          </Button>
        </Popover>
      </>
    );
  }

  // v0.10.24xx (CLS) — yüklenirken satırın yeri ayrılır (Save düğmesi satırı 24 px);
  // null dönüp sonra belirmesi /endpoints'te altındaki her şeyi kaydırıyordu.
  if (views === undefined) return <div aria-hidden style={{ minHeight: 24, marginBottom: 8 }} />;

  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 6, marginBottom: 8,
      flexWrap: 'wrap', fontSize: 11,
    }}>
      <span style={{ color: 'var(--text3)', marginRight: 2 }}>Saved:</span>
      {views.length === 0 && (
        <span style={{ color: 'var(--text3)', fontStyle: 'italic' }}>
          (none yet — Save current view to pin a filter combo)
        </span>
      )}
      {ordered.map((v, i) => {
        const isActive = activeViewId === v.id;
        // "Modified": operator applied THIS view earlier in the
        // session, then the URL drifted away (extra filters typed,
        // toggle flipped). Active + modified are mutually
        // exclusive — exact-match wins.
        const isModified = !isActive && lastAppliedViewId === v.id;
        return (
        <span key={v.id} style={{
          display: 'inline-flex', alignItems: 'center', gap: 4,
          padding: '2px 8px', borderRadius: 3,
          // v0.10.929 (K5) — uygulanan görünüm bir SEÇİM: yeşil literal yerine vurgu dili.
          background: isActive
            ? 'var(--accent-bg)'
            : isModified
              ? 'rgba(187,128,9,.14)'  // amber when drifted
              : v.ownerId === '' ? 'color-mix(in srgb, var(--accent) 10%, transparent)' : 'var(--bg3)',
          border: isActive
            ? '1px solid var(--accent)'
            : isModified
              ? '1px solid rgba(187,128,9,.55)'
              : v.ownerId === '' ? '1px solid color-mix(in srgb, var(--accent) 35%, transparent)' : '1px solid var(--border)',
        }}>
          {/* v0.10.924 — buton bütünlüğü Faz 2: elle şeffaflaştırılmış
              ham düğme yerine LinkButton (hap zaten görsel düğme; etiket
              metin gibi durur). Glif boşluğu artık atomun `gap`i. */}
          <LinkButton tone="muted" underline="none" onClick={() => apply(v)}
            title={(() => {
              const base = v.ownerId === '' ? 'Team-shared view' : 'Your view';
              const shortcut = i < 9 ? ` · press ${i + 1}` : '';
              if (isActive) return `${base} · current filter${shortcut}`;
              if (isModified) return `${base} · drifted; click to restore${shortcut}`;
              return base + shortcut;
            })()}>
            {isActive && <span style={{ fontSize: 9, color: 'var(--accent2)' }}>✓</span>}
            {isModified && <span style={{ fontSize: 9, color: 'rgb(187,128,9)' }}>●</span>}
            {!isActive && !isModified && v.ownerId === '' && <span style={{ fontSize: 9 }}>★</span>}
            {v.name}
            {i < 9 && (
              <span style={{
                fontSize: 9, color: 'var(--text3)',
                marginLeft: 2, padding: '0 4px',
                border: '1px solid var(--border)', borderRadius: 2,
                fontFamily: 'var(--font-mono)',
              }}>{i + 1}</span>
            )}
          </LinkButton>
          {isModified && (
            <Button variant="ghost" size="sm" onClick={() => apply(v)}
              title="Revert to saved filter"
              style={{ color: 'rgb(187,128,9)' }}>↺</Button>
          )}
          {(v.ownerId === user?.id || isAdmin) && (
            <Button variant="ghost-danger" size="sm" onClick={() => void remove(v)}
              title="Delete">×</Button>
          )}
        </span>
        );
      })}
      <Button variant="secondary" size="sm"
        onClick={() => setShowSaver(s => !s)}>
        {showSaver ? '✕ Cancel' : '＋ Save current view'}
      </Button>

      {showSaver && (
        <span style={{
          display: 'inline-flex', alignItems: 'center', gap: 6,
          padding: '4px 8px', borderRadius: 3,
          background: 'var(--bg2)', border: '1px solid var(--border)',
        }}>
          <input autoFocus
            placeholder="Name…"
            value={name}
            onChange={e => setName(e.target.value)}
            onKeyDown={e => {
              if (e.key === 'Enter') save();
              else if (e.key === 'Escape') setShowSaver(false);
            }}
            style={{ width: 200, fontSize: 11 }} />
          {isAdmin && (
            <label style={{ display: 'flex', alignItems: 'center', gap: 4, color: 'var(--text2)' }}>
              <input type="checkbox" checked={shared}
                onChange={e => setShared(e.target.checked)} />
              Share with team
            </label>
          )}
          <Button variant="primary" size="sm" onClick={save}>
            Save
          </Button>
        </span>
      )}
      {right && (
        <span style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          {right}
        </span>
      )}
    </div>
  );
}
