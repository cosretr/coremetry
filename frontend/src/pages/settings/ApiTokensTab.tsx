import { useEffect, useState } from 'react';
import { Spinner, Empty } from '@/components/Spinner';
import { Button, useConfirm } from '@/components/ui';
import { api } from '@/lib/api';
import { copyToClipboard } from '@/lib/clipboard';
import type { APIToken } from '@/lib/types';
import { Field2, FlashBox, Row } from './shared';
import { tsLong } from '@/lib/utils';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, type ColumnDef } from '@/components/ui/DataTable';

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

  const dt = useDataTable<APIToken>({
    storageKey: 'settings-api-tokens', columns: TOKEN_COLS, rows: tokens ?? [],
    initialSort: { id: 'createdAt', dir: 'desc' },
  });

  const load = () => {
    api.listAPITokens().then(r => setTokens(r.tokens ?? [])).catch(() => setTokens(null));
  };
  useEffect(load, []);

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

  return (
    <div style={{ maxWidth: 720 }}>
      <h2 style={{ fontSize: 14, fontWeight: 600, marginBottom: 6 }}>API Tokens</h2>
      <p style={{ color: 'var(--text2)', fontSize: 13, marginBottom: 16 }}>
        Harici sistemlerin (GenAI Studio agent'ları, otomasyonlar) Coremetry
        API'sine ve MCP'ye (<code>/api/mcp/sse</code>) kimlikle bağlanması için
        uzun ömürlü token'lar. <code>Authorization: Bearer cmk_…</code> ile
        kullanılır; agent'lar için <b>viewer</b> rolü önerilir (tüm MCP
        tool'ları salt okuma).
      </p>

      <Row>
        <Field2 label="Ad" small hint="ör. genai-studio-sre-agent">
          <input value={name} onChange={e => setName(e.target.value)} style={{ width: '100%' }} />
        </Field2>
        <Field2 label="Rol" small>
          <select value={role} onChange={e => setRole(e.target.value)} style={{ width: '100%' }}>
            <option value="viewer">viewer (önerilen)</option>
            <option value="editor">editor</option>
            <option value="admin">admin</option>
          </select>
        </Field2>
        <Button variant="primary" onClick={() => { void create(); }} disabled={!name.trim()} style={{ marginTop: 18 }} loading={busy}>
          + Token üret
        </Button>
      </Row>

      {fresh && (
        <div style={{
          margin: '12px 0', padding: '10px 12px', borderRadius: 6,
          background: 'color-mix(in srgb, var(--warn) 8%, transparent)',
          border: '1px solid color-mix(in srgb, var(--warn) 35%, transparent)',
          fontSize: 12,
        }}>
          <b>Token'ı ŞİMDİ kopyala — bir daha gösterilmeyecek:</b>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 6 }}>
            <code className="mono" style={{
              padding: '4px 8px', background: 'var(--bg2)', borderRadius: 4,
              overflowWrap: 'anywhere', flex: 1,
            }}>{fresh}</code>
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
            <div style={{ marginTop: 6, color: 'var(--err)' }}>
              Kopyalanamadı — token'ı yukarıdan elle seçip kopyala.
            </div>
          )}
        </div>
      )}

      {tokens === undefined && <Spinner />}
      {tokens === null && <Empty icon="⚠" title="Token listesi yüklenemedi" />}
      {tokens && tokens.length === 0 && (
        <Empty icon="🔑" title="Henüz token yok">
          Yukarıdan bir ad verip üret — GenAI Studio agent'ının MCP bağlantısına yapıştır.
        </Empty>
      )}
      {tokens && tokens.length > 0 && (
        <div className="table-wrap">
          <table {...dt.tableProps}>
            <DataTableColgroup dt={dt} />
            <DataTableHead dt={dt} />
            <tbody>
              {dt.sortedRows.map(t => (
                <tr key={t.id} style={{ opacity: t.revoked ? 0.55 : 1 }}>
                  <DataTableCell dt={dt} col="name" row={t} value={t.name} className="cell-strong" />
                  <DataTableCell dt={dt} col="prefix" row={t} value={t.prefix} />
                  <DataTableCell dt={dt} col="role" row={t}><span className="badge b-gray">{t.role}</span></DataTableCell>
                  <DataTableCell dt={dt} col="createdBy" row={t} value={t.createdBy} />
                  {/* v0.10.942 — zaman damgası mono kalır: S2 yalnız sayı hücresini kapsar. */}
                  <DataTableCell dt={dt} col="createdAt" row={t} value={tsLong(t.createdAt)} className="mono" />
                  <DataTableCell dt={dt} col="revoked" row={t}>{t.revoked
                    ? <span className="badge b-gray">REVOKED</span>
                    : <span className="badge b-gray">ACTIVE</span>}</DataTableCell>
                  <DataTableCell dt={dt} col="actions" row={t}>
                    {!t.revoked && (
                      <Button variant="danger" size="sm"
                        onClick={async () => {
                          if (!await confirm({
                            title: 'Token iptal edilsin mi?',
                            body: <><b>{t.name}</b> iptal edilecek. Bu token’ı kullanan
                              sistemler <b>ANINDA</b> erişimi kaybeder; iptal geri alınamaz.</>,
                            confirmLabel: 'Token’ı iptal et',
                            danger: true,
                          })) return;
                          try { await api.revokeAPIToken(t.id); load(); }
                          catch (e) { setMsg({ kind: 'err', text: e instanceof Error ? e.message : String(e) }); }
                        }}>
                        İptal et
                      </Button>
                    )}
                  </DataTableCell>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}
    </div>
  );
}
