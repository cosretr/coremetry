import { useEffect, useState } from 'react';
import { Button } from '@/components/shadcn/button';
import { Badge } from '@/components/shadcn/badge';
import { Field, SelectField } from '@/components/coremetry/Field';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { useConfirm } from '@/components/coremetry/Confirm';
import { DataTable, type ColumnDef, type DataTableStateInput } from '@/components/coremetry/DataTable';
import { api } from '@/lib/api';
import { copyToClipboard } from '@/lib/clipboard';
import type { APIToken } from '@/lib/types';
import { FlashBox } from './shared';
import { tsLong } from '@/lib/utils';

// v0.9.872 (tutarlılık denetimi BT5) — `is-fit` sınıfı vardı, primitif
// yoktu. Kolon genişliği BEYAN EDİLMEDİĞİ için isFitTables.test.ts bu
// tabloyu ÖLÇEMİYOR ve UNMEASURED istisnasında bekliyordu; genişlikler
// beyan edildiği an ölçüme giriyor ve istisnadan çıkarılıyor.
// Toplam: 170+120+110+150+165+110 = 825 + eylem 120 = 945px < 1150 eşiği.
// v0.10.942 (tablo standardı dilim 3) — hücre görünümü kolon bayraklarında
// (11px ikincil metin → `tone: muted`, S3); eylem hücresi `kind: 'actions'`
// kolonu. `minWidth = width`: sığdırma onu eski sabit `trailing` gibi kilitler.
const TOKEN_COLS: ColumnDef<APIToken>[] = [
  { id: 'name',      label: 'Ad',     sortValue: t => t.name,            naturalDir: 'asc', width: 170 },
  { id: 'prefix',    label: 'Token',  sortValue: t => t.prefix,          naturalDir: 'asc', width: 120, mono: true, tone: () => 'muted' },
  { id: 'role',      label: 'Rol',    sortValue: t => t.role,            naturalDir: 'asc', width: 110 },
  { id: 'createdBy', label: 'Üreten', sortValue: t => t.createdBy ?? '', naturalDir: 'asc', width: 150, tone: () => 'muted' },
  { id: 'createdAt', label: 'Tarih',  sortValue: t => t.createdAt,       width: 165, tone: () => 'muted' },
  // Aktif token'lar üstte: iptal edilmişler arşiv, canlı olanlar envanter.
  { id: 'revoked',   label: 'Durum',  sortValue: t => (t.revoked ? 0 : 1), width: 110 },
  { id: 'actions',   label: 'Eylemler', kind: 'actions', width: 120, minWidth: 120 },
];

// ApiTokensTab — v0.8.444. Harici agent platformlarının (GenAI Studio
// vb.) MCP/REST erişimi için servis token'ları. Düz token create
// yanıtında BİR KEZ gösterilir (SMTP-secret sözleşmesi); listede yalnız
// prefix durur. Revoke tombstone'dur — kayıt audit izi olarak kalır.
// Faz 2 dilim 1 R10 — shadcn/coremetry bileşikleri (Field, DataTable,
// StatusBadge); kolonlar, metinler, sıralama ve storageKey aynı.

export function ApiTokensTab() {
  const confirm = useConfirm();
  const [tokens, setTokens] = useState<APIToken[] | null | undefined>(undefined);
  const [name, setName] = useState('');
  const [role, setRole] = useState('viewer');
  const [fresh, setFresh] = useState<string | null>(null); // tek seferlik düz token
  // v0.8.547 — kopyalama sonucu. null = henüz denenmedi. Bu token bir daha
  // gösterilmediği için sessiz başarısızlık = kalıcı kayıp: sonucu
  // göstermek burada opsiyonel bir cila değil, güvenlik ağı.
  const [copyOk, setCopyOk] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const load = () => {
    api.listAPITokens().then(r => setTokens(r.tokens ?? [])).catch(() => setTokens(null));
  };
  useEffect(load, []);

  // v0.10.954 — tablo standardı T12: yükleniyor / hata / boş tablonun
  // İÇİNDE, başlık durur (undefined / null / [] sırası aynı).
  const tableState: DataTableStateInput<APIToken> =
    tokens === undefined ? { kind: 'loading' }
    : tokens === null ? { kind: 'error' }
    : { kind: 'empty', message: 'Henüz token yok — yukarıdan bir ad verip üret, GenAI Studio agent\'ının MCP bağlantısına yapıştır.' };

  const create = async () => {
    setBusy(true); setMsg(null); setFresh(null);
    try {
      const r = await api.createAPIToken(name.trim(), role);
      setFresh(r.token);
      setName('');
      load();
    } catch (e) {
      setMsg({ kind: 'err', text: e instanceof Error ? e.message : String(e) });
    } finally { setBusy(false); }
  };

  const revoke = async (t: APIToken) => {
    if (!await confirm({
      title: 'Token iptal edilsin mi?',
      body: <><b>{t.name}</b> iptal edilecek. Bu token’ı kullanan
        sistemler <b>ANINDA</b> erişimi kaybeder; iptal geri alınamaz.</>,
      confirmLabel: 'Token’ı iptal et',
      danger: true,
    })) return;
    try { await api.revokeAPIToken(t.id); load(); }
    catch (e) { setMsg({ kind: 'err', text: e instanceof Error ? e.message : String(e) }); }
  };

  // R10 — iptal edilmiş satırın sönükleştirmesi satır içi opacity yerine
  // `data-revoked` işaretinden: tablo kabındaki `:has()` utility'si satırı
  // .55'e indirir (eski değer).
  const cell = (t: APIToken, col: ColumnDef<APIToken>) => {
    switch (col.id) {
      case 'name': return <span className="font-semibold" data-revoked={t.revoked ? '' : undefined}>{t.name}</span>;
      case 'prefix': return t.prefix;
      case 'role': return <Badge variant="secondary">{t.role}</Badge>;
      case 'createdBy': return t.createdBy;
      // v0.10.942 — zaman damgası mono kalır: S2 yalnız sayı hücresini kapsar.
      case 'createdAt': return <span className="font-mono">{tsLong(t.createdAt)}</span>;
      case 'revoked': return <StatusBadge status="unknown">{t.revoked ? 'REVOKED' : 'ACTIVE'}</StatusBadge>;
      case 'actions': return !t.revoked && (
        <Button variant="ghost-danger" size="sm" onClick={() => { void revoke(t); }}>İptal et</Button>
      );
      default: return null;
    }
  };

  return (
    <div className="max-w-[720px]">
      <h2 className="mb-1.5 text-[length:var(--fs-lg)] font-semibold">API Tokens</h2>
      <p className="mb-4 text-[length:var(--fs-md)] text-(--text2)">
        Harici sistemlerin (GenAI Studio agent'ları, otomasyonlar) Coremetry
        API'sine ve MCP'ye (<code>/api/mcp/sse</code>) kimlikle bağlanması için
        uzun ömürlü token'lar. <code>Authorization: Bearer cmk_…</code> ile
        kullanılır; agent'lar için <b>viewer</b> rolü önerilir (tüm MCP
        tool'ları salt okuma).
      </p>

      <div className="flex flex-wrap items-start gap-3">
        <Field label="Ad" small hint="ör. genai-studio-sre-agent" value={name} onChange={e => setName(e.target.value)} />
        <SelectField label="Rol" small value={role} onChange={e => setRole(e.target.value)}>
          <option value="viewer">viewer (önerilen)</option>
          <option value="editor">editor</option>
          <option value="admin">admin</option>
        </SelectField>
        <Button className="mt-5" size="sm" onClick={() => { void create(); }} disabled={!name.trim()} loading={busy}>
          + Token üret
        </Button>
      </div>

      {fresh && (
        <div className="my-3 rounded-md border border-(--warn)/35 bg-(--warn)/8 px-3 py-2.5 text-[length:var(--fs-sm)]">
          <b>Token'ı ŞİMDİ kopyala — bir daha gösterilmeyecek:</b>
          <div className="mt-1.5 flex items-center gap-2">
            <code className="mono flex-1 rounded-sm bg-(--bg2) px-2 py-1 [overflow-wrap:anywhere]">{fresh}</code>
            <Button variant="secondary" size="sm"
              onClick={async () => {
                const ok = await copyToClipboard(fresh);
                setCopyOk(ok);
                if (ok) setTimeout(() => setCopyOk(null), 2500);
              }}>
              {copyOk === true ? '✓ Kopyalandı' : 'Kopyala'}
            </Button>
          </div>
          {/* Başarısızlık SESSİZ kalamaz: token bir daha gösterilmiyor, ve
              düz HTTP'de (secure context yok) fallback de reddedilebilir.
              Operatör kopyaladığını sanıp sekmeyi kapatırsa token gider. */}
          {copyOk === false && (
            <div className="mt-1.5 text-(--err)">
              Kopyalanamadı — token'ı yukarıdan elle seçip kopyala.
            </div>
          )}
        </div>
      )}

      <div className="table-wrap [&_tr:has([data-revoked])]:opacity-55">
        <DataTable<APIToken> storageKey="settings-api-tokens" columns={TOKEN_COLS} rows={tokens ?? []}
          sort={{ initial: { id: 'createdAt', dir: 'desc' } }} state={tableState}
          getRowKey={t => t.id} renderCell={cell} />
      </div>
      {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}
    </div>
  );
}
