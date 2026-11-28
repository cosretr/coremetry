import { Fragment, useEffect, useMemo, useRef, useState } from 'react';
import { Topbar } from '@/components/Topbar';
import { ServicePicker } from '@/components/ServicePicker';
import { Button, Chip, SegmentedControl, useConfirm } from '@/components/ui';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableState, type DataTableStateProps } from '@/components/ui/DataTable';
import type { DataTableColumn } from '@/lib/dataTable';
import { useAuth } from '@/components/AuthProvider';
import {
  useAlertRules, useAlertRuleProblemCounts,
  useCreateAlertRule, useUpdateAlertRule,
  useDeleteAlertRule, useEnableAlertRule, useDisableAlertRule,
} from '@/lib/queries';
import { api } from '@/lib/api';
import type { AlertRule } from '@/lib/types';
import { logsHref } from '@/lib/logsUrl';
import {
  METRICS, COMPARATORS, SEVERITIES, WINDOWS, emptyDraft, TEMPLATES,
  type UserPreset, DB_STMT_METRICS, isDbStmtMetric, targetMetrics, httpRouteUnit } from './alerts/constants';
import { ThresholdField } from './alerts/ThresholdField';
import { StatementPicker } from './alerts/StatementPicker';
import { NotifyTeamsField } from './alerts/NotifyTeamsField';
import { RuleSaveError, RunbookUrlHint } from './alerts/RuleSaveError'; // v0.10.1341
import { useRuleSaveError } from './alerts/useRuleSaveError'; // v0.10.1341
import { notifySummary } from './alerts/notifyTeams';
import { ConditionPreview } from './alerts/ConditionPreview';
import { NoisyRulesPanel } from './alerts/NoisyRulesPanel';
import { WatcherImportModal } from './alerts/WatcherImportModal';
import { Link } from 'react-router';
import { PageControls } from '@/components/coremetry/PageControls';
import { PageShell } from '@/components/coremetry/PageShell';
import { t, tf, useLang, useT, type Lang } from '@/lib/i18n';

// alertTypeLabel — Type hücresinin bastığı rozet metni. Sıralama accessor'ı
// buradan okuyor ki kolon GÖRÜNENE göre sıralansın (ham `metric` alanı
// 'log_query' / 'watcher' değerleriyle rozetlerden bambaşka bir düzen verir).
function alertTypeLabel(r: AlertRule): string {
  return r.target?.kind === 'http_route' ? 'HTTP ROUTE' // v0.10.705
    : r.target?.kind === 'kafka_client' ? 'KAFKA CLIENT' // v0.10.554
    : r.target ? 'DB STATEMENT'
    : r.metric === 'watcher' ? 'ES WATCHER'
    : r.metric === 'log_query' ? 'WATCHER'
    : r.builtIn ? 'BUILT-IN'
    : 'metric';
}

// v0.9.877 (tutarlılık denetimi BT3) — elle yazılmış <thead> paylaşılan
// primitife taşındı. Kolon kümesi, sıra, etiketler ve hücre içerikleri AYNEN
// korundu; kazanılan sıralama + yeniden boyutlandırma + kalıcı genişlik.
// Severity metin değil SIRA'ya göre sıralanıyor (info < warning < critical);
// alfabetik sıra critical'ı info'nun üstüne koyup ciddiyeti tersine çevirirdi.
// v0.9.1109 (Faz 5) — satır, kuralın ürettiği AÇIK problem sayısını
// taşır; alarm→veri yolu (satırdan Inbox'a) buradan açılır.
type AlertRuleRow = AlertRule & { openProblems: number };

const alertRuleCols = (lang: Lang): DataTableColumn<AlertRuleRow>[] => [
  { id: 'name',      label: t('alertsP.col.name', lang),      sortValue: r => r.name,                     naturalDir: 'asc', width: 220 },
  { id: 'service',   label: t('alertsP.col.service', lang),   sortValue: r => r.service ?? '',            naturalDir: 'asc', width: 160 },
  // Condition hücresi üç ayrı şekil basıyor (ES watcher / log watcher /
  // metrik); ortak eksen kuralın metriği, gruplamayı o veriyor.
  { id: 'condition', label: t('alertsP.col.condition', lang), sortValue: r => r.metric,                   naturalDir: 'asc', flex: true },
  { id: 'window',    label: t('alertsP.col.window', lang),    sortValue: r => r.windowSec,                numeric: true, width: 95 },
  { id: 'severity',  label: t('alertsP.col.severity', lang),  sortValue: r => SEVERITIES.indexOf(r.severity), width: 105 },
  { id: 'problems',  label: t('alertsP.col.problems', lang), sortValue: r => r.openProblems,         numeric: true, width: 125 },
  { id: 'enabled',   label: t('alertsP.col.enabled', lang),   sortValue: r => (r.enabled ? 1 : 0),        width: 95 },
  { id: 'type',      label: t('alertsP.col.type', lang),      sortValue: r => alertTypeLabel(r),          naturalDir: 'asc', width: 120 },
];

export default function AlertsPage() {
  const tn = useT(); // v0.10.2486 dilim 2 — Topbar başlığı katalogdan
  const lang = useLang();
  const ruleCols = useMemo(() => alertRuleCols(lang), [lang]);
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';
  // v0.8.424 — operator-reported: viewers saw + New rule / Edit /
  // Enable / Disable / Delete (the backend already 403s via
  // editorRoles, but the UI advertised write access). Invariant #7:
  // viewer SEES state read-only — rules render, mutations hide.
  const canEdit = user?.role === 'admin' || user?.role === 'editor';
  const [showForm, setShowForm] = useState(false);
  // ES Watcher import modal (Faz-1) — ephemeral by design, no URL
  // param: a transient paste-and-go flow, not a shareable view.
  const [showImport, setShowImport] = useState(false);
  const [draft, setDraft] = useState<Partial<AlertRule>>(emptyDraft);
  // User-saved presets (v0.5.157). Loaded once per mount; mutations
  // re-fetch eagerly so the strip stays accurate without polling.
  const [presets, setPresets] = useState<UserPreset[]>([]);
  const reloadPresets = () => {
    if (!user) { setPresets([]); return; }
    api.savedViews('alert-template')
      .then(rows => setPresets((rows ?? []).flatMap(v => {
        // queryString holds JSON.stringify(draft). Malformed
        // payloads (manual CH tampering, schema drift) collapse
        // to a dropped entry rather than breaking the whole strip.
        try {
          const draft = JSON.parse(v.queryString) as Partial<AlertRule>;
          return [{ id: v.id, name: v.name, shared: v.ownerId === '', draft }];
        } catch { return []; }
      })))
      .catch(() => setPresets([]));
  };
  useEffect(() => { reloadPresets(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [user]);
  const saveAsPreset = async () => {
    if (!user) {
      alert(tn('alertsP.preset.signIn'));
      return;
    }
    const name = window.prompt(tn('alertsP.preset.promptName'), draft.name || '');
    if (!name) return;
    const wantShared = isAdmin && await confirm({
      title: tn('alertsP.preset.shareTitle'),
      body: <>{tfNodes(tn('alertsP.preset.shareBody'), {
        name: <b>{name}</b>,
        shared: <b>{tn('alertsP.preset.teamOpen')}</b>,
        mine: <b>{tn('alertsP.preset.justMeInline')}</b>,
      })}</>,
      confirmLabel: tn('alertsP.preset.saveShared'),
      cancelLabel: tn('alertsP.preset.justMe'),
    });
    try {
      // Strip the per-rule `service` field so the preset is
      // reusable across services. The operator can re-pick a
      // service after loading. Everything else (thresholds,
      // dampening, severity, runbook) carries through.
      const { service: _svc, id: _id, builtIn: _bi, enabled: _en, ...templateDraft } = draft;
      void _svc; void _id; void _bi; void _en;
      await api.createSavedView({
        name,
        page: 'alert-template',
        queryString: JSON.stringify(templateDraft),
        shared: wantShared,
      });
      reloadPresets();
    } catch (e) {
      alert(tf('alertsP.preset.saveFailed', lang, { err: e instanceof Error ? e.message : String(e) }));
    }
  };
  const deletePreset = async (id: string, name: string) => {
    if (!await confirm({
      title: tn('alertsP.preset.deleteTitle'),
      body: <>{tfNodes(tn('alertsP.preset.deleteBody'), { name: <b>{name}</b> })}</>,
      confirmLabel: tn('alertsP.preset.deleteConfirm'),
      danger: true,
    })) return;
    try {
      await api.deleteSavedView(id);
      reloadPresets();
    } catch (e) {
      alert(tf('alertsP.preset.deleteFailed', lang, { err: e instanceof Error ? e.message : String(e) }));
    }
  };
  // Export / import (v0.5.172). Export bundles every preset
  // currently visible to the operator — personal + team-shared
  // (the server-side ListSavedViews already filters to "mine
  // OR shared") — into a JSON file. Import re-creates each
  // entry as a personal preset on the current user; the
  // operator can re-share via the save-with-shared path if
  // they have admin role.
  const exportPresets = () => {
    if (presets.length === 0) {
      alert(tn('alertsP.preset.noneToExport'));
      return;
    }
    const payload = {
      // Versioned envelope so a future format change can
      // refuse old files cleanly instead of producing broken
      // imports.
      schema: 'coremetry-alert-presets/v1',
      exportedAt: new Date().toISOString(),
      presets: presets.map(p => ({
        name: p.name,
        draft: p.draft,
        // shared flag is informational — re-importing on a
        // different install always starts as personal.
        shared: p.shared,
      })),
    };
    const blob = new Blob([JSON.stringify(payload, null, 2)], {
      type: 'application/json',
    });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `coremetry-alert-presets-${new Date().toISOString().slice(0, 10)}.json`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  };
  const importPresets = async (file: File) => {
    let text: string;
    try { text = await file.text(); }
    catch (e) { alert(tf('alertsP.import.readFailed', lang, { err: e instanceof Error ? e.message : String(e) })); return; }
    let parsed: unknown;
    try { parsed = JSON.parse(text); }
    catch { alert(tn('alertsP.import.badJson')); return; }
    // Loose shape match — we accept both the envelope above
    // AND a bare array (e.g. an operator hand-edits a
    // sub-set), so the export round-trip is forgiving.
    let entries: Array<{ name: string; draft: Partial<AlertRule> }> = [];
    const env = parsed as {
      schema?: string;
      presets?: Array<{ name?: string; draft?: Partial<AlertRule> }>;
    };
    if (env && Array.isArray(env.presets)) {
      entries = env.presets.flatMap(p => p.name && p.draft ? [{ name: p.name, draft: p.draft }] : []);
    } else if (Array.isArray(parsed)) {
      entries = (parsed as Array<{ name?: string; draft?: Partial<AlertRule> }>)
        .flatMap(p => p.name && p.draft ? [{ name: p.name, draft: p.draft }] : []);
    }
    if (entries.length === 0) {
      alert(tn('alertsP.import.noneValid'));
      return;
    }
    if (!await confirm({
      title: tn('alertsP.import.title'),
      body: <>{tfNodes(tn('alertsP.import.body'), { count: <b>{entries.length}</b> })}</>,
      confirmLabel: tf('alertsP.import.confirm', lang, { count: entries.length }),
    })) {
      return;
    }
    // Parallel create — the server-side endpoint dedups by
    // (name, page) per owner so re-importing is idempotent.
    const results = await Promise.allSettled(entries.map(e =>
      api.createSavedView({
        name: e.name,
        page: 'alert-template',
        queryString: JSON.stringify(e.draft),
      })
    ));
    const failed = results.filter(r => r.status === 'rejected').length;
    reloadPresets();
    if (failed > 0) {
      alert(tf('alertsP.import.partial', lang, { n: entries.length - failed, failed }));
    }
  };
  // Non-null while editing — `id` of the row we're editing. Drives the
  // form's "Update" vs "Save" copy and decides between PUT and POST on
  // submit.
  const [editingId, setEditingId] = useState<string | null>(null);

  // Rules query + 4 mutations. Each mutation auto-invalidates
  // the rules cache on success — no manual refresh() coordinator.
  const rulesQ = useAlertRules();
  // v0.9.858 (UX denetimi K6) — `?? []` hatayı BOŞ LİSTEYE çeviriyordu:
  // /api/alert-rules düştüğünde sayfa "No alert rules" + "+ New rule"
  // basıyor, operatör kurallarının silindiğini sanıyordu. null = hata.
  const rulesAll = rulesQ.isLoading ? undefined : rulesQ.isError ? null : rulesQ.data ?? [];
  // v0.5.305 — filter chip strip: All / Metric / Watcher.
  // Watchers = saved-search log alerts (metric='log_query') PLUS
  // imported ES Watcher definitions (metric='watcher', Faz-1);
  // operators asked to see them on this page alongside metric
  // rules with a clear visual scope.
  const [ruleKind, setRuleKind] = useState<'all' | 'metric' | 'watcher'>('all');
  const isWatcherRule = (r: AlertRule) => r.metric === 'log_query' || r.metric === 'watcher';
  const rules = rulesAll == null ? rulesAll : rulesAll.filter(r => {
    if (ruleKind === 'all') return true;
    return ruleKind === 'watcher' ? isWatcherRule(r) : !isWatcherRule(r);
  });
  const watcherCount = rulesAll?.filter(isWatcherRule).length ?? 0;
  const metricCount  = (rulesAll?.length ?? 0) - watcherCount;
  // initialSort YOK — bilinçli. Sunucu `ORDER BY created_at DESC` döndürüyor
  // ve created_at bu tabloda GÖRÜNÜR bir kolon değil; bir varsayılan sıra
  // uydurmak, denetimin dokunmaması gereken satır düzenini sessizce
  // değiştirirdi. id:null → satırlar sunucudan geldiği gibi kalıyor.
  // v0.9.1109 — kural başına açık problem sayısı; hata/boşta {} =
  // rozet görünmez, tablo asla bloklanmaz (soft dependency).
  const problemCounts = useAlertRuleProblemCounts().data ?? {};
  const dt = useDataTable<AlertRuleRow>({
    // 'alert-rules' ALINMIŞ — tarihsel bir isimle /problems tablosuna ait
    // (ProblemsSection.tsx); aynı anahtarı kullanmak iki tablonun sırasını
    // ve `s_alert-rules` URL paramını birbirine bağlardı.
    storageKey: 'alert-rules-list', columns: ruleCols,
    rows: (rules ?? []).map(r => ({ ...r, openProblems: problemCounts[r.id] ?? 0 })),
  });
  const createRule = useCreateAlertRule();
  const updateRule = useUpdateAlertRule();
  const deleteRule  = useDeleteAlertRule();
  const confirm = useConfirm();
  const enableRule  = useEnableAlertRule();
  const disableRule = useDisableAlertRule();

  // v0.10.967 — tablo standardı dilim 5 (T12 + P-1): yükleniyor / hata /
  // eşleşme yok / boş tablonun İÇİNDE, başlık durur. Sıra eskisiyle aynı;
  // hata eskisi gibi bayat satırları da gizler (rulesAll null → satır yok).
  // v0.9.858'in uyarısı ("boş kural kümesi değil, yerine yeni kural açma")
  // sunucu metniyle hata satırında, ↻ aynı refetch. "+ New rule" CTA'sı
  // silinmedi, dışarı park edilmedi: satırın detail yuvasında (P-1).
  // Tür çipi (Metric / Watcher) hepsini elediyse artık "eşleşme yok" +
  // Filtreleri temizle (= All çipi): eskiden kural VARKEN "No alert rules"
  // yazıyordu. Kaynak gerçekten boşsa çip seçili kalsa da boş durum.
  const newRuleCta = canEdit && (
    <Button variant="primary" onClick={() => setShowForm(true)}>{tn('alertsP.newRule')}</Button>
  );
  const rulesErr = rulesQ.error instanceof Error ? rulesQ.error.message : '';
  const tableState: Omit<DataTableStateProps<AlertRuleRow>, 'dt'> =
    rules === undefined ? { kind: 'loading' }
    : rules === null ? {
        kind: 'error',
        onRetry: () => void rulesQ.refetch(),
        message: tf('alertsP.loadError', lang, { err: rulesErr ? `: ${rulesErr}` : '' }),
      }
    : ruleKind !== 'all' && (rulesAll?.length ?? 0) > 0 ? {
        kind: 'no-match',
        message: ruleKind === 'watcher' ? tn('alertsP.noMatch.watcher') : tn('alertsP.noMatch.metric'),
        onClearFilters: () => setRuleKind('all'),
        detail: newRuleCta,
      }
    : {
        kind: 'empty',
        message: tn('alertsP.empty'),
        detail: newRuleCta,
      };

  // Open the edit form pre-filled from a noisy-rules suggestion.
  // NoisyRulesPanel owns the report + bulk-apply state; this single
  // callback is the one cross-cutting concern (it drives the parent's
  // form), so behaviour matches the pre-refactor inline applySuggestion.
  // v0.10.1319 — formun kabı (odak hedefi) ve açıldığı andaki taslak
  // (kirlilik ölçüsü: `draft` bundan farklıysa kaydedilmemiş düzenleme var).
  const formRef = useRef<HTMLDivElement>(null);
  const formBaseline = useRef<Partial<AlertRule>>(emptyDraft);
  const editFromSuggestion = (d: Partial<AlertRule>, ruleId: string) => {
    setDraft(d);
    formBaseline.current = d;
    setEditingId(ruleId);
    setShowForm(true);
    // Scroll the form into view so the operator sees the
    // suggested values immediately.
    setTimeout(() => {
      window.scrollTo({ top: 0, behavior: 'smooth' });
    }, 0);
  };

  const startEdit = (r: AlertRule) => {
    setDraft({ ...r });
    formBaseline.current = { ...r };
    setEditingId(r.id);
    setShowForm(true);
  };
  const cancelForm = () => {
    setShowForm(false);
    setEditingId(null);
    setDraft(emptyDraft);
    formBaseline.current = emptyDraft;
  };
  // v0.10.1319 K1 — "+ New alert rule" artık TEK YÖNLÜ: formu açar, asla
  // kapatmaz (eski Cancel/New etiket takası kalktı; iptal
  // formun kendi ActionRow'unda). Form ZATEN açık ve kaydedilmemiş
  // düzenleme varsa taslak SESSİZCE SİLİNMEZ — form açık kalır ve odak
  // forma taşınır; operatör bilinçli olarak Cancel'a basıp yeniden açar.
  // Açık ama temiz bir form (ör. değişiklik yapılmamış bir Edit) yeni
  // boş taslağa geçer.
  const focusForm = () => {
    const el = formRef.current;
    if (!el) return;
    el.scrollIntoView?.({ block: 'nearest', behavior: 'smooth' });
    el.querySelector<HTMLElement>('input:not([type="file"]), select, textarea')?.focus();
  };
  const startNew = () => {
    const dirty = showForm && JSON.stringify(draft) !== JSON.stringify(formBaseline.current);
    if (dirty) { focusForm(); return; }
    setDraft(emptyDraft);
    formBaseline.current = emptyDraft;
    setEditingId(null);
    setShowForm(true);
  };

  // ── v0.10.1341 — kaydetme reddi (400 runbookUrl …) formda satır içi; form açık kalır.
  const ruleSave = useRuleSaveError(showForm ? (editingId ?? 'new') : null);
  const save = async () => {
    if (!draft.name || !draft.metric) return;
    const ok = await ruleSave.run(() => editingId
      ? updateRule.mutateAsync({ id: editingId, patch: draft })
      : createRule.mutateAsync(draft));
    if (ok) cancelForm();
  };
  // ── /v0.10.1341
  // remove = hard delete (DELETE /api/alert-rules/{id} actually
  // removes the row now, per v0.5.175). `disable` is the soft
  // counterpart for the "I want to silence this but keep the
  // definition" case.
  const remove = async (id: string, name: string) => {
    if (!await confirm({
      title: tn('alertsP.remove.title'),
      body: <>{tfNodes(tn('alertsP.remove.body'), {
        name: <b>{name}</b>,
        disable: <b>{tn('alertsP.disable')}</b>,
      })}</>,
      confirmLabel: tn('alertsP.remove.confirm'),
      danger: true,
    })) return;
    await deleteRule.mutateAsync(id);
  };
  const disable = async (id: string) => {
    await disableRule.mutateAsync(id);
  };
  const enable = async (id: string) => {
    await enableRule.mutateAsync(id);
  };

  return (
    <>
      <Topbar title={tn('nav.alerts')} />
      <PageShell>
        {/* v0.10.1319 K1 — eylem yuvası: ikincil (Import) primary'nin solunda. */}
        <PageControls style={{ marginBottom: 14 }}
          actions={canEdit && (
            <>
              <Button variant="secondary" onClick={() => setShowImport(true)}
                      title={tn('alertsP.importEs.title')}>
                {tn('alertsP.importEs')}
              </Button>
              <Button variant="primary" onClick={startNew}>{tn('alertsP.newAlertRule')}</Button>
            </>
          )}>
          {/* v0.10.1069 — "Built-in rules ship pre-configured" cümlesi kalktı:
              yerleşikler artık varsayılan kapalı (tablonun üstündeki not). */}
          <span style={{ color: 'var(--text2)', fontSize: 12 }}>
            {tn('alertsP.evaluatorNote')}
          </span>
        </PageControls>

        {showImport && (
          <WatcherImportModal
            onClose={() => setShowImport(false)}
            onImported={() => rulesQ.refetch()} />
        )}

        {/* Noisy-rules report — surfaces rules that have opened
            problems most often in the last 24h with a one-click
            "Apply" affordance that pre-fills the edit form with
            the suggested dampening values. Self-hides when no rule
            has a suggestion. */}
        {/* v0.8.424 — editors only: the panel is pure mutation surface
            (Apply/Disable suggestions pre-filling the edit form a
            viewer doesn't have). */}
        {canEdit && (
          <NoisyRulesPanel rules={rules ?? undefined} onEditFromSuggestion={editFromSuggestion} />
        )}

        {showForm && (
          <div ref={formRef} style={{
            background: 'var(--bg1)', border: '1px solid var(--border)',
            borderRadius: 8, padding: 14, marginBottom: 14,
          }}>
            {/* Template strip — one-click pre-fill for the
                six scenarios operators wire up over and over.
                Picking a template populates the form below;
                the operator can still edit any field
                afterwards (service / threshold / window). */}
            {!editingId && (
              <div style={{ marginBottom: 12 }}>
                <div style={{
                  fontSize: 11, color: 'var(--text2)',
                  fontWeight: 600, letterSpacing: '0.5px',
                  textTransform: 'uppercase', marginBottom: 6,
                }}>
                  {tn('alertsP.fromTemplate')}
                </div>
                <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
                  {TEMPLATES.map(t => (
                    <Button key={t.id} variant="secondary" size="sm"
                      onClick={() => setDraft(t.draft)}
                      title={t.description}>
                      {t.label}
                    </Button>
                  ))}
                </div>
                <div style={{
                  display: 'flex', alignItems: 'baseline', gap: 10,
                  margin: '10px 0 6px',
                }}>
                  <div style={{
                    fontSize: 11, color: 'var(--text2)',
                    fontWeight: 600, letterSpacing: '0.5px',
                    textTransform: 'uppercase',
                  }}>
                    {tn('alertsP.myPresets')}
                  </div>
                  <span style={{ flex: 1 }} />
                  {presets.length > 0 && (
                    <Button variant="secondary" size="sm"
                      onClick={exportPresets}
                      title={tn('alertsP.export.title')}>
                      {tn('alertsP.export')}
                    </Button>
                  )}
                  <label className="sec"
                    style={{
                      fontSize: 11, padding: '3px 8px',
                      borderRadius: 6, border: '1px solid var(--border)',
                      cursor: 'pointer', background: 'var(--bg3)',
                    }}
                    title={tn('alertsP.import.fileTitle')}>
                    {tn('alertsP.import')}
                    <input type="file" accept="application/json,.json"
                      style={{ display: 'none' }}
                      onChange={e => {
                        const f = e.target.files?.[0];
                        if (f) { importPresets(f); e.target.value = ''; }
                      }} />
                  </label>
                </div>
                {presets.length > 0 ? (
                  <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
                    {presets.map(p => (
                      <Chip key={p.id}
                        onClick={() => setDraft({ ...emptyDraft, ...p.draft })}
                        title={tf('alertsP.preset.loadTitle', lang, { scope: p.shared ? tn('alertsP.preset.scopeShared') : tn('alertsP.preset.scopePersonal') })}
                        onRemove={() => void deletePreset(p.id, p.name)}
                        removeLabel={tf('alertsP.preset.deleteLabel', lang, { name: p.name })}>
                        {p.shared ? '◍ ' : '★ '}{p.name}
                      </Chip>
                    ))}
                  </div>
                ) : (
                  <div style={{ fontSize: 11, color: 'var(--text3)' }}>
                    {tn('alertsP.preset.none')}
                  </div>
                )}
              </div>
            )}
            <div className="grid-3" style={{ display: 'grid', gap: 10 }}>
              <Field label={tn('alertsP.col.name')}>
                <input value={draft.name ?? ''}
                  onChange={e => setDraft({ ...draft, name: e.target.value })}
                  placeholder={tn('alertsP.f.namePh')} />
              </Field>
              <Field label={draft.target ? tn('alertsP.col.service') : tn('alertsP.f.serviceAll')}>
                {draft.target?.kind === 'http_route'
                  ? <div className="mono" style={{ fontSize: 12, color: 'var(--text2)', padding: '6px 0' }}
                      title={tn('alertsP.f.routeTitle')}>
                      {draft.target.service} · {draft.target.route}
                    </div>
                  : draft.target?.kind === 'kafka_client'
                  ? <div className="mono" style={{ fontSize: 12, color: 'var(--text2)', padding: '6px 0' }}
                      title={tn('alertsP.f.kafkaTitle')}>
                      {draft.target.service}{` · ${draft.target.topic ? tf('alertsP.f.kafkaTopic', lang, { topic: draft.target.topic }) : tn('alertsP.f.kafkaAllTopics')}`}{draft.target.clientId ? ` · ${tf('alertsP.f.kafkaClient', lang, { id: draft.target.clientId })}` : ''}
                    </div>
                  : draft.target
                  ? <div style={{ fontSize: 12, color: 'var(--text3)', padding: '6px 0' }}>{tn('alertsP.f.allCallersStmt')}</div>
                  : <ServicePicker value={draft.service ?? ''} onChange={v => setDraft({ ...draft, service: v })}
                      placeholder={tn('alertsP.f.servicePh')} width="100%" />}
              </Field>
              <Field label={tn('alertsP.col.severity')}>
                <select value={draft.severity}
                  onChange={e => setDraft({ ...draft, severity: e.target.value })}>
                  {SEVERITIES.map(s => <option key={s} value={s}>{s}</option>)}
                </select>
              </Field>
              {/* v0.10.331 — hedef: belirli bir DB ifadesi (SQL arayıp seç). Seçilince
                  metrik ailesi db_stmt_* (p95 varsayılan), servis = tüm çağıranlar. */}
              {draft.target?.kind !== 'kafka_client' && draft.target?.kind !== 'http_route' && (
              <Field label={tn('alertsP.f.target')}>
                <StatementPicker value={draft.target} service={draft.service ?? ''} onChange={t => setDraft(d => ({
                  ...d, target: t, service: t ? '' : d.service,
                  metric: t ? (isDbStmtMetric(d.metric) ? d.metric : 'db_stmt_p95_ms') : (isDbStmtMetric(d.metric) ? 'error_rate' : d.metric),
                  threshold: t && !isDbStmtMetric(d.metric) ? 1000 : d.threshold,
                  windowSec: t && (d.windowSec ?? 0) < 300 ? 600 : d.windowSec,
                  name: t && !d.name ? `Slow SQL: ${(t.sample || '').replace(/\s+/g, ' ').slice(0, 60)}` : d.name,
                }))} />
              </Field>
              )}
              <Field label={tn('alertsP.f.metric')}>
                <select value={draft.metric}
                  onChange={e => setDraft({ ...draft, metric: e.target.value })}>
                  {targetMetrics(draft.target?.kind).map(m => <option key={m.v} value={m.v}>{m.label}</option>)}
                </select>
              </Field>
              <Field label={tn('alertsP.f.comparator')}>
                <select value={draft.comparator}
                  onChange={e => setDraft({ ...draft, comparator: e.target.value })}>
                  {COMPARATORS.map(c => <option key={c} value={c}>{c}</option>)}
                </select>
              </Field>
              <Field label={draft.target?.kind === 'http_route' ? tf('alertsP.f.thresholdUnit', lang, { unit: httpRouteUnit(draft.metric) }) : draft.target?.kind === 'kafka_client' ? tn('alertsP.f.threshold') : draft.target ? tf('alertsP.f.thresholdUnit', lang, { unit: 'ms' }) : tn('alertsP.f.threshold')}>
                {draft.target ? (
                  <input type="number" min={1} step={50} value={draft.threshold ?? 1000}
                    onChange={e => setDraft({ ...draft, threshold: Number(e.target.value) })} />
                ) : (
                <ThresholdField
                  value={draft.threshold ?? 0}
                  service={draft.service ?? ''}
                  metric={draft.metric ?? 'error_rate'}
                  comparator={draft.comparator ?? '>'}
                  windowSec={draft.windowSec}
                  onChange={v => setDraft({ ...draft, threshold: v })}
                  onApplySeverity={sev => setDraft(d => ({ ...d, severity: sev }))}
                />
                )}
              </Field>
              <Field label={tn('alertsP.col.window')}>
                <select value={draft.windowSec}
                  onChange={e => setDraft({ ...draft, windowSec: Number(e.target.value) })}>
                  {WINDOWS.map(w => <option key={w.v} value={w.v}>{w.label}</option>)}
                </select>
              </Field>
            </div>
            {/* Noise-dampening knobs (v0.5.127-129). All three
                default to 0 = legacy "fire immediately" behaviour.
                Operators tune per-rule when prod sends too many
                alerts:
                  • For — sustained breach gate (Prometheus `for:`)
                  • Min samples — sample-count floor (kills 1/1 = 100%)
                  • Cooldown — post-resolution silence (kills jitter) */}
            <div className="grid-3" style={{ marginTop: 10,
              display: 'grid', gap: 10 }}>
              <Field label={tn('alertsP.f.sustain')}>
                <input type="number" min={0} step={30}
                  value={draft.forSec ?? 0}
                  onChange={e => setDraft({ ...draft, forSec: Number(e.target.value) })}
                  placeholder={tn('alertsP.f.sustainPh')}
                  style={{ width: '100%' }} />
              </Field>
              <Field label={tn('alertsP.f.minSamples')}>
                <input type="number" min={0} step={10}
                  value={draft.minSamples ?? 0}
                  onChange={e => setDraft({ ...draft, minSamples: Number(e.target.value) })}
                  placeholder={tn('alertsP.f.minSamplesPh')}
                  style={{ width: '100%' }} />
              </Field>
              <Field label={tn('alertsP.f.cooldown')}>
                <input type="number" min={0} step={60}
                  value={draft.cooldownSec ?? 0}
                  onChange={e => setDraft({ ...draft, cooldownSec: Number(e.target.value) })}
                  placeholder={tn('alertsP.f.cooldownPh')}
                  style={{ width: '100%' }} />
              </Field>
            </div>
            {/* Runbook URL — optional. Surfaces on Problem
                detail when the rule fires so the oncall lands
                on the team's playbook in one click. */}
            <div style={{ marginTop: 10 }}>
              <Field label={tn('alertsP.f.runbook')}>
                <input value={draft.runbookUrl ?? ''}
                  onChange={e => setDraft({ ...draft, runbookUrl: e.target.value })} /* i18n-exempt: örnek URL */
                  placeholder="https://wiki.internal/runbook/high-error-rate"
                  style={{ width: '100%' }} />
                <RunbookUrlHint url={draft.runbookUrl} />{/* v0.10.1341 */}
              </Field>
            </div>
            {/* v0.10.519 — kural bazında ekip bildirimi: bu kural açılınca hangi
                ekip(ler) mail alır; boş = sahip + SRE (Settings → Team routing). */}
            <div style={{ marginTop: 10 }}>
              <Field label={tn('alertsP.f.notify')}>
                <NotifyTeamsField value={draft.notify} onChange={n => setDraft(d => ({ ...d, notify: n }))} />
              </Field>
            </div>
            {/* Live condition preview — the rule's metric over the last hour with
                the threshold line + a "would have fired N×" count, so the
                operator tunes the threshold against real data before saving. */}
            <ConditionPreview draft={draft} />
            {editingId && (
              <div style={{ marginTop: 10, fontSize: 11, color: 'var(--text3)' }}>
                {tfNodes(tn('alertsP.editing'), { id: <code>{editingId}</code> })}
                {draft.builtIn && (
                  <span style={{ marginLeft: 6, color: 'var(--text2)' }}>
                    {tn('alertsP.editingBuiltIn')}
                  </span>
                )}
              </div>
            )}
            {/* v0.10.1319 K1 — form footer'ı ActionRow: Save/Update EN SAĞDA
                (tek confirm yuvası), Cancel onun solunda, ★ preset en solda.
                Cancel eskiden barın etiket-takaslı primary'siydi. */}
            <RuleSaveError error={ruleSave.error} />{/* v0.10.1341 */}
            <ActionRow
              secondary={<>
                {!editingId && (
                  <Button variant="secondary" type="button" onClick={saveAsPreset}
                    disabled={!draft.metric}
                    title={isAdmin
                      ? tn('alertsP.saveAsPreset.adminTitle')
                      : tn('alertsP.saveAsPreset.title')}>
                    {tn('alertsP.saveAsPreset')}
                  </Button>
                )}
                <Button variant="secondary" type="button" onClick={cancelForm}>{tn('alertsP.cancel')}</Button>
              </>}
              confirm={<Button variant="primary" onClick={save}>{editingId ? tn('alertsP.updateRule') : tn('alertsP.saveRule')}</Button>} />
          </div>
        )}

        {/* v0.5.305 — kind filter chips so operators can scope
            the table to just their watchers (saved log alerts)
            without scrolling past 50+ metric rules. */}
        {rulesAll && rulesAll.length > 0 && (
          <div style={{ display: 'flex', gap: 6, marginBottom: 8, alignItems: 'center' }}>
            {/* v0.10.1342 (K3 dilim B) — All | Metric | Watcher tek seçim →
                SegmentedControl (eskiden seçili tür dolu birincildi). */}
            <SegmentedControl size="sm" aria-label={tn('alertsP.kind.aria')}
              value={ruleKind} onChange={setRuleKind}
              options={([
                { key: 'all',     label: tn('alertsP.kind.all'),     count: rulesAll.length },
                { key: 'metric',  label: tn('alertsP.kind.metric'),  count: metricCount },
                { key: 'watcher', label: tn('alertsP.kind.watcher'), count: watcherCount },
              ] as const).map(t => ({
                value: t.key,
                title: t.key === 'watcher'
                  ? tn('alertsP.kind.watcherTitle')
                  : t.key === 'metric'
                  ? tn('alertsP.kind.metricTitle')
                  : tn('alertsP.kind.allTitle'),
                label: <>
                  {t.label}
                  <span style={{
                    marginLeft: 6, fontSize: 10, color: 'var(--text3)',
                    fontFamily: 'var(--font-mono)',
                  }}>{t.count}</span>
                </>,
              }))} />
          </div>
        )}
        {/* v0.10.1069 — yerleşik kurallar varsayılan KAPALI (operatör: "çok
            false pozitif geliyor"). Tek satır not, yalnız listede yerleşik
            satır varken; satırlar BUILT-IN · OFF durur, Enable aynı tuş. */}
        {rulesAll?.some(r => r.builtIn) && (
          <div data-builtin-off-note
            style={{ fontSize: 12, color: 'var(--text2)', marginBottom: 6 }}>
            {tn('alertsP.builtInOff')}
          </div>
        )}
        <div className="table-wrap">
          <table {...dt.tableProps}>
            <DataTableColgroup dt={dt} trailing={[250]} />
            <DataTableHead dt={dt} trailing={<th />} />
            <tbody>
              {dt.sortedRows.length === 0 ? <DataTableState dt={dt} trailing={[250]} {...tableState} /> : dt.sortedRows.map(r => {
                // v0.5.305 — Watchers (Logs → Create watcher;
                // saved-search alerts) live in the same
                // alert_rules table with metric='log_query'.
                // Surface them with their own badge + render
                // the saved query in the Condition column so
                // the operator can tell at a glance which row
                // is a watcher vs a metric alert.
                // Faz-1 — imported ES Watcher definitions use
                // metric='watcher': same badge family, condition
                // rendered as the projected hits.total compare.
                const isWatcher = r.metric === 'log_query';
                const isEsWatcher = r.metric === 'watcher';
                // v0.10.331 — hedefli kural (DB ifadesi): örnek SQL + ölçü.
                const isTarget = !!r.target;
                const isRoute = r.target?.kind === 'http_route'; // v0.10.705
                return (
                <tr key={r.id} className="cv-row">
                  <td>
                    <b>{r.name}</b>
                    {/* v0.10.519 — kuralın ekip hedefi (boş = sahip + SRE). */}
                    {r.notify?.teams?.length ? (
                      <div className="mono" style={{ fontSize: 10, color: 'var(--text3)' }} title={tn('alertsP.notifyTitle')}>{notifySummary(r.notify)}</div>
                    ) : null}
                  </td>
                  <td className="mono">{isRoute ? r.target!.service : isTarget ? tn('alertsP.cell.allCallers') : (r.service || (isWatcher || isEsWatcher ? tn('alertsP.cell.logs') : tn('alertsP.cell.all')))}</td>
                  <td className="mono" style={{ maxWidth: 380 }}>
                    {isRoute ? (
                      <>
                        {/* i18n-exempt: hedef türü (http_route) */}
                        <span className="badge b-gray mono" style={{ fontSize: 10, marginRight: 6 }}>route</span>
                        <code className="mono cell-ellipsis" title={isRoute ? `${r.target!.service} ${r.target!.route}` : ''} style={{ maxWidth: 220, fontSize: 11, verticalAlign: 'middle' }}>{r.target!.route}</code>
                        <span style={{ color: 'var(--text3)', marginLeft: 6 }}>{r.metric.replace('http_route_', '').replace('_ms', '')} {r.comparator} {r.threshold} {httpRouteUnit(r.metric)}</span>
                      </>
                    ) : isTarget ? ( // i18n-exempt: JSX üçlüsü, metin değil
                      <>
                        <span className="badge b-gray mono" style={{ fontSize: 10, marginRight: 6 }}>{r.target!.dbSystem || 'db'}{r.target!.dbName && r.target!.dbName !== 'default' ? ` · ${r.target!.dbName}` : ''}</span>
                        <code className="mono cell-ellipsis" title={r.target!.sample || r.target!.stmtHash} style={{ maxWidth: 220, fontSize: 11, verticalAlign: 'middle' }}>{r.target!.sample || `#${r.target!.stmtHash}`}</code>
                        <span style={{ color: 'var(--text3)', marginLeft: 6 }}>{r.metric.replace('db_stmt_', '').replace('_ms', '')} {r.comparator} {r.threshold} ms</span>
                      </>
                    ) : isEsWatcher ? ( // i18n-exempt: JSX üçlüsü, metin değil
                      <span title={r.watcherJson}>
                        hits.total {r.comparator} {r.threshold}
                      </span>
                    ) : isWatcher ? ( // i18n-exempt: JSX üçlüsü, metin değil
                      <>
                        <code title={r.logQuery}
                          style={{
                            display: 'inline-block', maxWidth: '100%',
                            overflow: 'hidden', textOverflow: 'ellipsis',
                            whiteSpace: 'nowrap', verticalAlign: 'middle',
                            padding: '1px 4px', borderRadius: 3,
                            background: 'var(--bg3)', fontSize: 11,
                          }}>
                          {r.logQuery || tn('alertsP.cell.empty')}
                        </code>
                        <span style={{ color: 'var(--text3)', marginLeft: 6 }}>
                          count {r.comparator} {r.threshold}
                        </span>
                      </>
                    ) : (
                      <>{r.metric} {r.comparator} {r.threshold}</>
                    )}
                  </td>
                  <td>{tf('alertsP.cell.windowMin', lang, { n: r.windowSec / 60 })}</td>
                  <td><SeverityBadge s={r.severity} /></td>
                  <td>
                    {r.openProblems > 0
                      ? <Link className="badge b-err"
                          to={`/inbox?q=${encodeURIComponent(r.name)}`}
                          title={tf('alertsP.cell.openTitle', lang, { count: r.openProblems, page: tn('nav.inbox') })}
                          style={{ textDecoration: 'none' }}>
                          {tf('alertsP.cell.open', lang, { count: r.openProblems })}
                        </Link>
                      : <span style={{ color: 'var(--text3)' }}>—</span>}
                  </td>
                  <td>{r.enabled
                    ? <span className="badge b-gray">{tn('alertsP.cell.on')}</span>
                    : <span className="badge b-gray">{tn('alertsP.cell.off')}</span>}</td>
                  <td>
                    {isEsWatcher
                      ? <span className="badge b-info" title={tn('alertsP.type.esWatcherTitle')}>{tn('alertsP.type.esWatcher')}</span>
                      : isWatcher
                      ? <span className="badge b-info" title={tn('alertsP.type.watcherTitle')}>{tn('alertsP.type.watcher')}</span>
                      : r.builtIn
                        ? <span className="badge b-info">{tn('alertsP.type.builtIn')}</span>
                        : <span className="badge b-gray">{tn('alertsP.type.metric')}</span>}
                  </td>
                  <td><div className="cell-actions end">
                    {isWatcher && (
                      <Link className="sec"
                        to={logsHref({ window: null, q: r.logQuery || '' })}
                        title={tn('alertsP.logsTitle')}
                        style={{ textDecoration: 'none' }}>
                        {tn('alertsP.logs')}
                      </Link>
                    )}
                    {canEdit && (
                      <>
                        <Button variant="secondary" size="sm" onClick={() => startEdit(r)}>{tn('alertsP.edit')}</Button>
                        {r.enabled
                          ? <Button variant="secondary" size="sm" onClick={() => disable(r.id)}
                              title={tn('alertsP.disableTitle')}>
                              {tn('alertsP.disable')}
                            </Button>
                          : <Button variant="secondary" size="sm" onClick={() => enable(r.id)}>{tn('alertsP.enable')}</Button>}
                        <Button variant="ghost-danger" size="sm" loading={deleteRule.isPending}
                          onClick={() => void remove(r.id, r.name)}
                          title={tn('alertsP.deleteTitle')}>
                          {tn('alertsP.delete')}
                        </Button>
                      </>
                    )}
                  </div>
                  </td>
                </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </PageShell>
    </>
  );
}

// tfNodes — tf'nin JSX hâli: {ad} yer tutucularına düğüm (kalın ad / sayı) koyar.
function tfNodes(tpl: string, vars: Record<string, React.ReactNode>): React.ReactNode[] {
  return tpl.split(/(\{\w+\})/).map((part, i) => {
    const m = /^\{(\w+)\}$/.exec(part);
    return m && m[1] in vars ? <Fragment key={i}>{vars[m[1]]}</Fragment> : part;
  });
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 11, color: 'var(--text2)' }}>
      {label}
      {children}
    </label>
  );
}

function SeverityBadge({ s }: { s: string }) {
  const cls = s === 'critical' ? 'b-err' : s === 'warning' ? 'b-warn' : 'b-info';
  return <span className={`badge ${cls}`}>{s.toUpperCase()}</span>;
}
