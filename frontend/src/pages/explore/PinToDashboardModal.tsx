import { useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router';
import { api } from '@/lib/api';
import { Modal } from '@/components/ui';
import { Button } from '@/components/ui/Button';
import { Field, SelectField } from '@/components/ui/Field';
import { Spinner } from '@/components/Spinner';
import { dashboardHref } from '@/lib/dashboardHref';
import { useT } from '@/lib/i18n';
import type { DashboardPanelSkip, Panel } from '@/lib/types';

// PinToDashboardModal (v0.8.419, DE4) — target picker for an Explore
// query pinned as a live dashboard panel. Fetch-on-open only (the
// dashboard list loads when the modal mounts, never speculatively
// from the toolbar).
//
// v0.10.1443 (sohbetten dashboard, R1) — `panels[]`: tek panel (Explore pini,
// sohbet grafiğinin "Dashboard'a ekle"si) ya da bir cevabın bütün grafikleri
// ("Hepsini dashboard yap"). Var olan panoya ekleme artık istemcide GET+PUT
// değil, sunucuda: POST /api/dashboards/{id}/panels — sunucu tam satırı
// mevcut kayıttan taşır (değişmez #4), tekrar eden paneli atlar ve
// skipped[]'i döndürür; modal eklenen/atlananı söyler. Yeni pano: `origin`
// verilmişse (sohbet) POST /api/dashboards/from-chat (ad çakışması "(2)",
// etiket "cosre", audit konuşma kimliğini taşır); Explore eskisi gibi
// POST /api/dashboards ('Pinned from Explore').
export function PinToDashboardModal({ panels, onClose, origin, defaultName }: {
  panels: Panel[];
  onClose: () => void;
  /** Sohbet kökeni — verilirse yeni pano from-chat ucundan kurulur ve audit konuşmayı taşır. */
  origin?: { conversation?: string; exchangeId?: string };
  /** Yeni pano adı önerisi (sohbet: soru). */
  defaultName?: string;
}) {
  const tr = useT();
  const qc = useQueryClient();
  const listQ = useQuery({
    queryKey: ['dashboards'],
    queryFn: () => api.listDashboards(),
    staleTime: 30_000,
  });
  const dashboards = useMemo(() => listQ.data ?? [], [listQ.data]);
  const single = panels.length === 1;

  const [target, setTarget] = useState<string>('new');
  const [newName, setNewName] = useState(defaultName ?? '');
  const [title, setTitle] = useState(panels[0]?.title ?? '');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const [done, setDone] = useState<{ id: string; name: string; added: number; skipped: DashboardPanelSkip[] } | null>(null);

  const pin = async () => {
    setBusy(true);
    setErr('');
    const ps: Panel[] = single
      ? [{ ...panels[0], title: title.trim() || panels[0].title }]
      : panels;
    try {
      if (target === 'new' && !origin) {
        const name = newName.trim() || 'Explore pins';
        const created = await api.createDashboard({
          name, description: 'Pinned from Explore', panels: ps,
        });
        setDone({ id: created.id, name, added: ps.length, skipped: [] });
      } else {
        const res = target === 'new'
          ? await api.createDashboardFromChat({ panels: ps, dashboard: { name: newName.trim() }, origin })
          : await api.appendDashboardPanels(target, { panels: ps, ...(origin ? { origin } : {}) });
        setDone({ id: res.id, name: res.name, added: res.added, skipped: res.skipped ?? [] });
      }
      void qc.invalidateQueries({ queryKey: ['dashboards'] });
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'Pin failed');
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal open onClose={onClose} title={origin ? tr('dash.addFromChat.title') : 'Pin to dashboard'} size="md"
      footer={done ? (
        // v0.9.1006 (M4/O6) — bir KAPATMA sayfanın en davetkâr öğesi
        // olmamalı; iş zaten bitti, burada birincil bir eylem yok.
        <Button variant="secondary" onClick={onClose}>Kapat</Button>
      ) : (
        <>
          <Button variant="secondary" onClick={onClose} disabled={busy}>Vazgeç</Button>
          <Button variant="primary" onClick={() => { void pin(); }} loading={busy}
            disabled={busy || panels.length === 0 || (target === 'new' && !newName.trim())}>
            {origin ? tr('dash.addFromChat.submit') : 'Pinle'}
          </Button>
        </>
      )}>
      {done ? (
        <div style={{ fontSize: 13, display: 'flex', flexDirection: 'column', gap: 8 }} data-testid="pin-done">
          <span>{tr('dash.addFromChat.added').replace('{n}', String(done.added))}: <b>{done.name}</b></span>
          {done.skipped.length > 0 && (
            <ul className="cell-hint" style={{ margin: 0, paddingLeft: 16 }} data-testid="pin-skipped">
              {done.skipped.map((s, i) => (
                <li key={i}>
                  {s.title || '—'} — {tr(`dash.addFromChat.skip.${s.reason}`)}{s.detail ? ` (${s.detail})` : ''}
                </li>
              ))}
            </ul>
          )}
          <Link to={dashboardHref(done.id)} style={{ color: 'var(--accent2)' }}>
            Dashboard&apos;u aç →
          </Link>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          {single ? (
            <Field label="Panel başlığı" value={title}
              onChange={e => setTitle(e.target.value)} />
          ) : (
            <div style={{ fontSize: 13 }} data-testid="pin-panel-list">
              <div className="cell-hint">{tr('dash.addFromChat.panels').replace('{n}', String(panels.length))}</div>
              <ul style={{ margin: '4px 0 0', paddingLeft: 16 }}>
                {panels.map(p => <li key={p.id}>{p.title}</li>)}
              </ul>
            </div>
          )}
          {listQ.isLoading ? <Spinner /> : (
            <SelectField label="Hedef dashboard" value={target}
              onChange={e => setTarget(e.target.value)}>
              <option value="new">+ Yeni dashboard…</option>
              {dashboards.map(d => (
                <option key={d.id} value={d.id}>{d.name}</option>
              ))}
            </SelectField>
          )}
          {target === 'new' && (
            <Field label="Yeni dashboard adı" value={newName}
              onChange={e => setNewName(e.target.value)}
              placeholder={origin ? undefined : 'Explore pins'} />
          )}
          {origin && <div className="cell-hint">{tr('dash.addFromChat.hint')}</div>}
          {err && <div style={{ fontSize: 12, color: 'var(--err)' }}>{err}</div>}
        </div>
      )}
    </Modal>
  );
}
