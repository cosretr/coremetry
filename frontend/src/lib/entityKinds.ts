// entityKinds — v0.10.2488 (entity tutarlılık denetimi dilim 4,
// docs/frontend/entity-coherence-audit.md §3.2, §3.5, §3.7).
//
// Tür kaydı TEK yerde: tür → ad anahtarı (TR/EN katalog), ikon adı
// (lucide, §3.2 tablosu) ve hedef rota üreticisi. `EntityLink`, hover kartı
// ve (dilim 5–6) EntityHeader / `/e/:kind/*` kodeği yalnız bu kayda bakar.
//
// Rota üreticileri bu dosyanın ARKASINDADIR: denetimin saydığı yedi üretici
// (serviceHref, entityHref, podDetailPath, endpointDetailHref,
// databaseDetailHref, messagingTopicHref, nodeDetailHref) bileşenlerden
// doğrudan çağrılmaz, `entityTargetHref` üzerinden çağrılır. Bu dilimde
// hedef rotalar DEĞİŞMEZ (bugünkü `/service?name=`, `/pod?…`, `/entity?id=`);
// dilim 6'da `/e/<tür>/<kimlik>` geçişi yalnız `entityTargetHref`'in
// gövdesini değiştirir, çağıranlar dokunulmaz.
//
// Kimlik modeli (duran kural): pod / servis / workload / cluster bağı entity
// kaydından (`EntityLinkRecord`, `id`) ya da sayfanın kendi kimlik üçlüsünden
// gelir — ad eşleştirmesiyle kimlik UYDURULMAZ. Hedef üretilemiyorsa
// (`nodeDetailHref` null) bağlantı çizilmez, düz metin çizilir.
//
// Paket: yalnız saf üreticiler; ikon BİLEŞENİ değil ikon ADI tutulur (lucide
// bileşenleri yalnız tembel hover kartında yüklenir).
import { serviceHref, type ServiceHrefOpts } from './serviceHref';
import { entityHref, type EntityHrefOpts, type EntityLinkRecord } from './entityHref';
import { podDetailPath } from '@/pages/service/podDetailPath';
import { endpointDetailHref, type EndpointRef, type EndpointPageScope } from '@/pages/endpoints/endpointParam';
import { databaseDetailHref, type DatabaseRef, type DatabasePageScope } from '@/pages/databases/databaseParam';
import { messagingTopicHref } from '@/pages/messaging/topicHref';
import { nodeDetailHref } from '@/components/topology/nodeDetailHref';

/** §3.2 tür listesi (entity modelinin türleri + endpoint). */
export type EntityKind =
  | 'service' | 'endpoint' | 'database' | 'queue' | 'cluster' | 'node'
  | 'namespace' | 'workload' | 'pod' | 'container' | 'external' | 'host';

/** lucide-react ikon adı (bileşen değil — paket kuralı). §3.2 tablosu. */
export type EntityIconName =
  | 'Boxes' | 'Webhook' | 'Database' | 'MessageSquare' | 'Server' | 'Cpu'
  | 'Layers' | 'Package' | 'Box' | 'Globe' | 'HardDrive';

export interface EntityKindSpec {
  /** entity kimlik öneki (`internal/entity/identity.go`); endpoint modelde yok. */
  prefix: string | null;
  /** i18n anahtarı — tür adı (TR/EN katalog). */
  labelKey: string;
  icon: EntityIconName;
}

export const ENTITY_KINDS: Record<EntityKind, EntityKindSpec> = {
  service:   { prefix: 'svc',     labelKey: 'ek.service',   icon: 'Boxes' },
  endpoint:  { prefix: null,      labelKey: 'ek.endpoint',  icon: 'Webhook' },
  database:  { prefix: 'db',      labelKey: 'ek.database',  icon: 'Database' },
  queue:     { prefix: 'queue',   labelKey: 'ek.queue',     icon: 'MessageSquare' },
  cluster:   { prefix: 'cluster', labelKey: 'ek.cluster',   icon: 'Server' },
  node:      { prefix: 'node',    labelKey: 'ek.node',      icon: 'Cpu' },
  namespace: { prefix: 'ns',      labelKey: 'ek.namespace', icon: 'Layers' },
  workload:  { prefix: 'wl',      labelKey: 'ek.workload',  icon: 'Package' },
  pod:       { prefix: 'pod',     labelKey: 'ek.pod',       icon: 'Box' },
  container: { prefix: 'ctr',     labelKey: 'ek.container', icon: 'Box' },
  external:  { prefix: 'ext',     labelKey: 'ek.external',  icon: 'Globe' },
  host:      { prefix: 'host',    labelKey: 'ek.host',      icon: 'HardDrive' },
};

type PodPathOpts = Parameters<typeof podDetailPath>[0];
type TopicHrefArgs = Parameters<typeof messagingTopicHref>[0];
type GraphNodeArg = Parameters<typeof nodeDetailHref>[0];

/**
 * Bağlantı hedefi — türe göre, bugünkü üreticinin argümanlarıyla birebir.
 * `record` ve `graphNode` iki KAYNAK biçimidir (entity modeli kaydı, topoloji
 * düğümü); türleri kayıttan/düğümden okunur.
 */
export type EntityTarget =
  | { kind: 'service'; name: string; opts?: ServiceHrefOpts }
  | { kind: 'pod'; ref: PodPathOpts }
  | { kind: 'endpoint'; ref: EndpointRef; scope?: EndpointPageScope }
  | { kind: 'database'; ref: DatabaseRef; scope?: DatabasePageScope }
  | { kind: 'queue'; ref: TopicHrefArgs }
  | { kind: 'record'; rec: EntityLinkRecord; opts?: EntityHrefOpts }
  | { kind: 'graphNode'; node: GraphNodeArg; scope?: DatabasePageScope };

const RECORD_KIND: Partial<Record<string, EntityKind>> = {
  cluster: 'cluster', node: 'node', namespace: 'namespace', workload: 'workload',
  pod: 'pod', container: 'container', service: 'service', database: 'database',
  queue: 'queue', external: 'external', host: 'host',
};
const GRAPH_KIND: Partial<Record<string, EntityKind>> = {
  service: 'service', db: 'database', database: 'database', queue: 'queue',
  messaging: 'queue', external: 'external',
};

/** Hedefin entity türü (ikon + tür adı için). */
export function entityTargetKind(t: EntityTarget): EntityKind {
  switch (t.kind) {
    case 'record': return RECORD_KIND[t.rec.type] ?? 'service';
    case 'graphNode': return GRAPH_KIND[String(t.node.kind)] ?? 'external';
    default: return t.kind;
  }
}

/** Hedefin görünen adı (etiket verilmezse). */
export function entityTargetName(t: EntityTarget): string {
  switch (t.kind) {
    case 'service': return t.name;
    case 'pod': return t.ref.pod;
    case 'endpoint': return t.ref.method ? `${t.ref.method} ${t.ref.path}` : t.ref.path;
    case 'database': return t.ref.dbName || t.ref.instance;
    case 'queue': return t.ref.destination;
    case 'record': return t.rec.name;
    case 'graphNode': return t.node.name;
  }
}

/**
 * Bugünkü rotayı kuran TEK yer (dilim 6'da `/e/<tür>/<kimlik>` buraya gelir).
 * null = gidilecek sayfa yok → çağıran düz metin çizer.
 */
export function entityTargetHref(t: EntityTarget): string | null {
  switch (t.kind) {
    case 'service': return serviceHref(t.name, t.opts);
    case 'pod': return podDetailPath(t.ref);
    case 'endpoint': return endpointDetailHref(t.ref, t.scope);
    case 'database': return databaseDetailHref(t.ref, t.scope);
    case 'queue': return messagingTopicHref(t.ref);
    case 'record': return entityHref(t.rec, t.opts);
    case 'graphNode': return nodeDetailHref(t.node, t.scope);
  }
}

export interface EntityPivot { key: 'logs' | 'metrics'; href: string }

/**
 * Hover kartının derin linkleri (Trace/Log/Metrik). Saf URL kurulumu — istek
 * yok. v1'de yalnız servis sekmeleri (Log = `logs`, Metrik = `details`);
 * hedefin kapsamı (range, env, params) aynen taşınır, yalnız `tab` değişir.
 */
export function entityPivots(t: EntityTarget): EntityPivot[] {
  if (t.kind !== 'service') return [];
  const base = { ...t.opts, hash: null };
  return [
    { key: 'logs', href: serviceHref(t.name, { ...base, tab: 'logs' }) },
    { key: 'metrics', href: serviceHref(t.name, { ...base, tab: 'details' }) },
  ];
}
