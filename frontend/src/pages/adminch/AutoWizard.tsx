// AutoWizard — v0.10.2410: kurulum/migration sihirbazlarının TEK kalıbı
// (operatör: "Sihirbazları tek tek çalıştırmayayım; ihtiyaç varsa
// çıksınlar, önkontrolleri kendileri yapsınlar").
//
//   • Kart yalnız probe bekleyen iş bulursa görünür (applicable && pending);
//     iş yoksa HİÇ render edilmez (boş kart yok). Probe okunamazsa kart
//     hatayla görünür (bitti olduğunu kanıtlayamıyoruz).
//   • Ön kontroller otomatik (mount'ta probe); operatör düğmeyle kontrol koşmaz.
//   • TEK "Çalıştır": adaptör bekleyen adımları sırayla koşar (aralarda yeniden
//     probe, ilk hatada durur, tekrar koşmak kaldığı yerden devam eder).
//   • Koşacak SQL / iş listesi katlanır bir listede.
//   • Ağır opsiyonel iş (MATERIALIZE vb.) varsayılan KAPALI bir onay kutusu.
//   • Arka plan işi (probe.running) sürerken 10 sn'de bir yeniden probe;
//     gizli sekmede durur.
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Button, Row } from '@/components/ui';
import { SelectField } from '@/components/ui';
import { CheckboxField } from '@/components/coremetry/Field';
import type { RollupStmtResult } from '@/lib/types';

export interface WizardCheck { name: string; ok: boolean; detail?: string; blocking?: boolean }
export interface WizardStep { id: string; title: string; state: string; detail?: string }
export interface WizardRunResult { ok: boolean; failedStep?: string; err?: string; log: RollupStmtResult[] }

export interface WizardProbe {
  /** false → bu kurulum şeklinde iş hiç yok (boot yönetiyor, tek düğüm…): kart yok. */
  applicable: boolean;
  /** true → bekleyen iş var: kart görünür. */
  pending: boolean;
  checks: WizardCheck[];
  steps?: WizardStep[];
  /** koşuyu engelleyen ilk neden ("" / undefined = koşulabilir). */
  blocker?: string;
  /** önerilen küme ('' = belirsiz → seçici görünür). */
  cluster?: string;
  clusters?: string[];
  /** koşacak işler (SQL ya da nesne listesi), başlık → satırlar. */
  plan: { title: string; lines: string[] }[];
  summary?: ReactNode;
  /** sunucu tarafı arka plan işi sürüyor. */
  running?: boolean;
  /** sunucunun son koşu kaydı (arka plan işli sihirbazlar). */
  lastRun?: WizardRunResult | null;
}

export interface AutoWizardProps {
  title: string;
  intro: ReactNode;
  probe: () => Promise<WizardProbe>;
  /** bekleyen adımları koşar; null = arka plan işi başladı (probe.running izlenir). */
  run: (opts: { cluster: string; heavy: boolean; probe: WizardProbe }) => Promise<WizardRunResult | null>;
  confirmText: ReactNode;
  heavy?: { label: string; hint: string };
}

const POLL_MS = 10_000;

export function AutoWizard({ title, intro, probe, run, confirmText, heavy }: AutoWizardProps) {
  const [p, setP] = useState<WizardProbe | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [cluster, setCluster] = useState('');
  const [withHeavy, setWithHeavy] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<WizardRunResult | null>(null);
  const [ran, setRan] = useState(false);
  const probeRef = useRef(probe);
  probeRef.current = probe;

  const load = async () => {
    try {
      const r = await probeRef.current();
      setP(r); setErr(null);
      setCluster(c => c || r.cluster || '');
    } catch (e: unknown) { setErr(e instanceof Error ? e.message : String(e)); }
  };
  useEffect(() => { void load(); }, []);
  const running = !!p?.running;
  useEffect(() => {
    if (!running) return;
    const id = window.setInterval(() => { if (!document.hidden) void load(); }, POLL_MS);
    return () => window.clearInterval(id);
  }, [running]);

  if (!p && !err) return null; // probe sürüyor — iş olduğu bilinmeden kart yok
  if (p && !p.applicable) return null;
  if (p && !p.pending && !running && !ran) return null; // iş yok → görünmez

  const doRun = async () => {
    if (!p) return;
    setConfirm(false); setBusy(true); setResult(null); setRan(true);
    try {
      const r = await run({ cluster, heavy: withHeavy, probe: p });
      if (r) setResult(r);
    } catch (e: unknown) {
      setResult({ ok: false, err: e instanceof Error ? e.message : String(e), log: [] });
    } finally { setBusy(false); void load(); }
  };
  const shown = result ?? (ran ? p?.lastRun ?? null : null) ?? (running ? p?.lastRun ?? null : null);
  const needCluster = !!p && (p.clusters?.length ?? 0) > 1 && !p.cluster;
  const blocker = p?.blocker || (needCluster && !cluster ? 'birden çok küme var ve hangisinin kullanıldığı okunamadı — küme seçin' : '');
  const done = !!p && !p.pending && !running;

  return (
    <div style={{ marginBottom: 24 }}>
      <h3 style={{ fontSize: 13, fontWeight: 700, marginBottom: 8 }}>{title}</h3>
      <p style={{ fontSize: 'var(--fs-sm)', color: 'var(--text2)', margin: '0 0 var(--sp-5)', lineHeight: 1.55 }}>{intro}</p>
      {err && <div role="alert" style={{ color: 'var(--err)', fontSize: 'var(--fs-sm)', marginBottom: 'var(--sp-4)' }}>Durum okunamadı: {err}</div>}
      {p && (
        <>
          {p.summary && <div style={{ fontSize: 'var(--fs-sm)', color: 'var(--text3)', marginBottom: 'var(--sp-3)' }}>{p.summary}</div>}
          {done && <div style={{ fontSize: 'var(--fs-sm)', marginBottom: 'var(--sp-4)' }}><span className="badge b-gray">TAMAMLANDI</span> bekleyen iş kalmadı — kart bir sonraki açılışta görünmez.</div>}
          <WizardChecks checks={p.checks} />
          {p.steps && p.steps.length > 0 && <WizardSteps steps={p.steps} />}
          {p.plan.some(g => g.lines.length > 0) && (
            <details style={{ marginBottom: 'var(--sp-5)', fontSize: 'var(--fs-sm)' }}>
              <summary style={{ cursor: 'pointer', color: 'var(--text2)' }}>Koşacak işler ({p.plan.reduce((n, g) => n + g.lines.length, 0)})</summary>
              {p.plan.filter(g => g.lines.length > 0).map(g => (
                <div key={g.title} style={{ marginTop: 'var(--sp-3)' }}>
                  <div style={{ color: 'var(--text3)' }}>{g.title}</div>
                  <ol style={{ margin: 0, paddingLeft: 18 }}>
                    {g.lines.map((l, i) => <li key={i} className="mono" style={{ fontSize: 'var(--fs-xs)', wordBreak: 'break-all' }}>{l}</li>)}
                  </ol>
                </div>
              ))}
            </details>
          )}
          {!done && (
            <Row gap={2} wrap style={{ alignItems: 'flex-end' }}>
              {needCluster && (
                <SelectField label="Küme" value={cluster} onChange={e => setCluster(e.target.value)} disabled={busy || running}>
                  <option value="">—</option>
                  {(p.clusters ?? []).map(c => <option key={c} value={c}>{c}</option>)}
                </SelectField>
              )}
              {heavy && (
                <CheckboxField label={heavy.label} hint={heavy.hint} checked={withHeavy} disabled={busy || running} onCheckedChange={setWithHeavy} />
              )}
              {!confirm ? (
                <Button variant="primary" size="sm" loading={busy || running} disabled={!!blocker || busy || running} onClick={() => setConfirm(true)}>Çalıştır</Button>
              ) : (
                <>
                  <span role="alert" style={{ fontSize: 'var(--fs-sm)' }}>
                    {confirmText}{withHeavy && heavy ? <> <strong>{heavy.hint}</strong></> : null} Emin misin?
                  </span>
                  <Button variant="primary" size="sm" loading={busy} onClick={() => void doRun()}>Evet</Button>
                  <Button variant="ghost" size="sm" disabled={busy} autoFocus onClick={() => setConfirm(false)}>Vazgeç</Button>
                </>
              )}
              {blocker && <span style={{ color: 'var(--warn)', fontSize: 'var(--fs-sm)' }}>{blocker}</span>}
            </Row>
          )}
        </>
      )}
      {!p && err && <Button variant="ghost" size="sm" onClick={() => void load()}>Yeniden dene</Button>}
      {shown && <WizardResult res={shown} />}
    </div>
  );
}

function WizardChecks({ checks }: { checks: WizardCheck[] }) {
  if (checks.length === 0) return null;
  return (
    <ul style={{ listStyle: 'none', margin: '0 0 var(--sp-4)', padding: 0, fontSize: 'var(--fs-sm)' }}>
      {checks.map(c => (
        <li key={c.name}>
          <span style={{ color: c.ok ? 'var(--text2)' : c.blocking ? 'var(--err)' : 'var(--warn)' }}>{c.ok ? '✓' : '✗'}</span>{' '}
          {c.name}{c.detail ? <span style={{ color: 'var(--text3)' }}> — {c.detail}</span> : null}
        </li>
      ))}
    </ul>
  );
}

const STEP_BADGE: Record<string, string> = { done: 'b-gray', optional: 'b-gray', pending: 'b-warn', partial: 'b-warn', unknown: 'b-warn' };
const STEP_LABEL: Record<string, string> = { done: 'TAMAM', optional: 'OPSİYONEL', pending: 'BEKLİYOR', partial: 'KISMİ', unknown: 'OKUNAMADI' };

function WizardSteps({ steps }: { steps: WizardStep[] }) {
  return (
    <ul style={{ listStyle: 'none', margin: '0 0 var(--sp-4)', padding: 0, fontSize: 'var(--fs-sm)' }}>
      {steps.map(s => (
        <li key={s.id} style={{ marginBottom: 2 }}>
          <span className={`badge ${STEP_BADGE[s.state] ?? 'b-warn'}`}>{STEP_LABEL[s.state] ?? s.state}</span>{' '}
          {s.title}{s.detail ? <span style={{ color: 'var(--text3)' }}> — {s.detail}</span> : null}
        </li>
      ))}
    </ul>
  );
}

function WizardResult({ res }: { res: WizardRunResult }) {
  return (
    <div style={{ marginTop: 'var(--sp-5)' }}>
      <div style={{ fontSize: 'var(--fs-sm)', marginBottom: 'var(--sp-3)' }}>
        Son koşu: {res.ok ? <span className="badge b-ok">TAMAM</span> : <span className="badge b-err">DURDU</span>}
        {res.failedStep && <> · adım <span className="mono">{res.failedStep}</span></>}
        {res.err && <span style={{ color: 'var(--err)' }}> — {res.err}</span>}
      </div>
      {res.log.length > 0 && (
        <ul style={{ margin: 0, paddingLeft: 18, fontSize: 'var(--fs-xs)' }}>
          {res.log.map((st, i) => (
            <li key={i} className="mono" style={{ color: st.ok ? 'var(--text2)' : 'var(--err)' }}>{st.head}{st.err ? ` — ${st.err}` : ''}</li>
          ))}
        </ul>
      )}
    </div>
  );
}
