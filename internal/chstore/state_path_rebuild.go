package chstore

// state_path_rebuild.go — v0.10.965 — State tablolarını BİRLEŞİK ZK yolunda
// yeniden kurma sihirbazı (Admin → ClickHouse → "Replika tutarlılığı").
//
// OPERATÖR KARARLARI (2026-09-27):
//   - Denetim VE düzeltme sihirbaza ("sihirbaza ekle … düzeltmesi ve kontrolü").
//   - Düzeltme on tablonun HEPSİ için DROP + CREATE, birleşik yolda:
//     ingest_ledger veri kaybı kabul ("2 için önerin a"), ai_eval_runs veri
//     kaybı kabul ("3 için de data kaybı önemsiz"), sekiz Rollouts v2
//     tablosu boş. VERİ TAŞIMA YOK.
//   - Boot kuralı değişikliği (kural 3: hiç var olmayan yeni state tablosu
//     her zaman birleşik yola) ONAYLANMADI — useUnifiedStatePath'e dokunulmaz.
//
// SIRA ÖNEMLİ. Önce seçilen tabloların HEPSİ düşer, her host'ta gittiği
// yoklanır, SONRA hepsi kurulur, sonra doğrulanır. Neden: kural 3. Bazı
// tablolar düşmüş, bazıları hâlâ eski yoldayken açılan bir pod eksik olanları
// ESKİ yola kurar. Onu hepsi gittikten sonra açılan pod kural 4'e ulaşır ve
// sihirbazın ifadesinin AYNISIYLA birleşik yola kurar — o yarış zararsız.
//
// ÖLÇÜM KATI: her okuma ana bağlantıda (s.conn), veritabanı `database = ?`
// ile bağlı, skip_unavailable_shards YOK (erişilemeyen host okumayı düşürür →
// kapı reddeder; ON CLUSTER DROP o host'ta kuyrukta bekler ve karışık durum
// kalırdı), her okuma zaman tavanlı, satır döndüren okumalar LIMIT'li.
//
// DDL TEK KAYNAKTAN. Sekiz Rollouts v2 tablosu 0015'in ifadesiyle
// (rolloutV2LayerStatements); ingest_ledger ve ai_eval_runs kanonik katalogdan,
// TAZE bir gölge Store'un tüm-birleşik gözlemiyle render edilir. Pod'un boot
// anındaki gözlemi (stateObs) ASLA kullanılmaz: o gözlem kural 2/3 ile eski
// yolu verir. statePathRenderCheck birleşik yolu ve replika adını doğrular.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/cilcenk/coremetry/internal/config"
)

// ───────────────────────── 2.1 izin listesi ─────────────────────────

// Sınıflar — v0.10.965: izin listesindeki her tablonun veri politikası.
const (
	statePathClassDerived           = "derived"            // türev: yeniden dolar
	statePathClassOperatorException = "operator_exception" // korunan sınıf, operatör kaybı kabul etti
	statePathClassEmptyOnly         = "empty_only"         // yalnız BOŞKEN
)

// Eylem ve durum adları (StatePathRebuildTable.Action / State).
const (
	statePathActRebuild = "rebuild"
	statePathActCreate  = "create"
	statePathActSkip    = "skip"
	statePathActBlocked = "blocked"

	statePathStUnified        = "unified"
	statePathStAbsent         = "absent"
	statePathStUnifiedPartial = "unified_partial"
	statePathStLegacy         = "legacy"
	statePathStLegacyPartial  = "legacy_partial"
	statePathStMixed          = "mixed"
	statePathStUnknown        = "unknown"
)

type statePathEntry struct{ Name, Class, Note string }

const statePathEmptyOnlyNote = "Rollouts v2 state tablosu (0015): yalnız BOŞKEN yeniden kurulur — yazıcı yok (rollouts.source=v1, argocd kapalı)"

// statePathAllowlist — v0.10.965 — SABİT liste, bu sırayla (operatör kararı
// 2026-09-27; prod sorgusunun döndürdüğü on tablo). Kullanıcı girdisi değil:
// adlar okumalara satır içi girer (rolloutV2NameList emsali).
var statePathAllowlist = []statePathEntry{
	{"ingest_ledger", statePathClassDerived, "türev sayaç defteri (telemetryPurgeTables): en fazla 30 günlük filo defteri geçmişi gider; ingest pod'ları dakikalık yazımla yeniden doldurur — operatör kararı 2026-09-27 (öneri a)"},
	{"ai_eval_runs", statePathClassOperatorException, "evalset koşu geçmişi — normalde korunan sınıf (configPreserveTables); operatör kararı 2026-09-27: veri kaybı kabul. Silinen skorlar yeniden üretilemez"},
	{"rollout_events", statePathClassEmptyOnly, statePathEmptyOnlyNote},
	{"rollout_workload_state", statePathClassEmptyOnly, statePathEmptyOnlyNote},
	{"argocd_app_status", statePathClassEmptyOnly, statePathEmptyOnlyNote},
	{"argocd_sync_events", statePathClassEmptyOnly, statePathEmptyOnlyNote + "; korunan sınıf — satır varsa asla"},
	{"argocd_app_mapping", statePathClassEmptyOnly, statePathEmptyOnlyNote},
	{"rollout_classification", statePathClassEmptyOnly, statePathEmptyOnlyNote},
	{"ado_commit_enrichment", statePathClassEmptyOnly, statePathEmptyOnlyNote},
	{"rollout_worker_runs", statePathClassEmptyOnly, statePathEmptyOnlyNote},
}

// StatePathRebuildAllowed — v0.10.965 — ad izin listesinde mi (API girdi
// doğrulaması: liste dışı ad hiçbir okumadan ÖNCE 400).
func StatePathRebuildAllowed(name string) bool {
	_, ok := statePathEntryFor(name)
	return ok
}

// StatePathEvalFresh — v0.10.965 — "çalışan evalset koşusu" penceresi. API
// testi bunun api.evalRunStaleAfter'a EŞİT olduğunu pinler: 15 dk sessiz bir
// "running" satırı ürün tarafında zaten terk edilmiş sayılır.
const StatePathEvalFresh = 15 * time.Minute

// statePathRebuildBusy — v0.10.965 — pod başına tek çalıştırma (Store'a alan
// eklenmez). İki api pod'u aynı anda koşarsa yakınsar: ikisi de yalnız
// birleşik yolu hedefler, DROP IF EXISTS / CREATE IF NOT EXISTS eşgüçlü.
var statePathRebuildBusy atomic.Bool

func statePathEntryFor(name string) (statePathEntry, bool) {
	for _, e := range statePathAllowlist {
		if e.Name == name {
			return e, true
		}
	}
	return statePathEntry{}, false
}

// statePathAllowIndex — izin listesi sırası; listede yoksa listenin boyu.
func statePathAllowIndex(name string) int {
	for i, e := range statePathAllowlist {
		if e.Name == name {
			return i
		}
	}
	return len(statePathAllowlist)
}

func statePathNameList() string {
	q := make([]string, len(statePathAllowlist))
	for i, e := range statePathAllowlist {
		q[i] = "'" + e.Name + "'" // sabit izin listesinden, kullanıcı girdisi değil
	}
	return strings.Join(q, ", ")
}

func statePathIsEight(name string) bool {
	e, ok := statePathEntryFor(name)
	return ok && e.Class == statePathClassEmptyOnly
}

// statePathRepairReject — v0.10.965 — SAF: izin listesindeki bir state
// tablosu birleşik yolda DEĞİLKEN (eski / karışık satır ya da denetimin
// "birleşik yolda eksik" satırı) replika onarımı ve ilk replika ESKİ yolu
// çoğaltır — Onar eşin literal yolunu klonlar, ilk replika boot gözlemiyle
// adaptDDL'e gider (kural 2/3 → {shard}) — ve yeniden kurulumu geri alır.
// O tablonun tek eylemi "State tablolarının ZK yolu" bloğudur. kind: satırın
// StatePath'i, yoksa denetim listesindeki türü. İzin listesi dışı eski
// tablolarda onarım olduğu gibi kalır (sihirbaz onları kurmaz). "" = geçer.
func statePathRepairReject(table, kind string) string {
	if kind == "" || !StatePathRebuildAllowed(table) {
		return ""
	}
	where := map[string]string{
		statePathLegacy: "eski (shard'lı) ZK yolunda",
		statePathMixed:  "karışık ZK yolunda (birleşik + eski)",
		statePathAbsent: "birleşik yolda eksik",
	}[kind]
	if where == "" {
		where = kind + " durumunda"
	}
	return fmt.Sprintf("%s state tablosu %s — onarım eski yolu yeniden kurar; \"State tablolarının ZK yolu\" bloğundan birleşik yola yeniden kur", table, where)
}

// statePathListedKind — v0.10.965 — SAF: denetimin (rapor.StatePaths)
// tablo için listelediği tür; listede yoksa "".
func statePathListedKind(chk *StatePathCheck, table string) string {
	if chk == nil {
		return ""
	}
	for _, l := range chk.Legacy {
		if l.Table == table {
			return l.Kind
		}
	}
	return ""
}

// ───────────────────────── tipler ─────────────────────────

// StatePathRebuildTable — v0.10.965 — plan / sonuç satırı.
type StatePathRebuildTable struct {
	Table     string           `json:"table"`
	State     string           `json:"state"`
	Action    string           `json:"action"`
	Rows      uint64           `json:"rows"`
	Groups    []StatePathGroup `json:"groups"`
	Class     string           `json:"class"`
	ClassNote string           `json:"classNote"`
	Detail    string           `json:"detail,omitempty"`
	After     string           `json:"after,omitempty"` // yalnız apply
	Verified  bool             `json:"verified"`        // yalnız apply

	pending []string // drop_wait: tabloyu hâlâ tutan host'lar / znode sahipleri
	created bool     // CREATE ifadesi koştu (ya da kuyruğa alındı)
}

// StatePathRebuildPlan — v0.10.965 — salt okuma plan.
type StatePathRebuildPlan struct {
	Cluster          string                  `json:"cluster"`
	Database         string                  `json:"database"`
	ZKPrefix         string                  `json:"zkPrefix"`
	Hosts            int                     `json:"hosts"`
	MeasuredAt       int64                   `json:"measuredAt"` // unix ms — onayın parmak izi
	Tables           []StatePathRebuildTable `json:"tables"`
	Drops            []string                `json:"drops"`
	Creates          []string                `json:"creates"`
	LockOpensAfter   bool                    `json:"lockOpensAfter"`
	StillLegacyAfter []string                `json:"stillLegacyAfter"`
	Blocked          []string                `json:"blocked"`
	Checks           []string                `json:"checks"`
	Warnings         []string                `json:"warnings"`
}

// StatePathAckTable — v0.10.965 — operatörün plan ekranında onayladığı durum + satır.
type StatePathAckTable struct {
	Table string `json:"table"`
	State string `json:"state"`
	Rows  uint64 `json:"rows"`
}

// StatePathRebuildAck — v0.10.965 — onay: planın ölçüm anı + tablo başına durum/satır.
type StatePathRebuildAck struct {
	MeasuredAt int64               `json:"measuredAt"`
	Tables     []StatePathAckTable `json:"tables"`
}

// StatePathRebuildRequest — v0.10.965 — apply isteği.
type StatePathRebuildRequest struct {
	Cluster   string               `json:"cluster"`
	Tables    []string             `json:"tables"`
	Ack       *StatePathRebuildAck `json:"ack"`
	PartialOK bool                 `json:"partialOK"`
}

// StatePathRebuildResult — v0.10.965 — apply sonucu. OK=false iken de DDL
// koşmuş olabilir: Statements ve Resume bunu taşır.
type StatePathRebuildResult struct {
	OK          bool                    `json:"ok"`
	Phase       string                  `json:"phase"` // "drop" | "drop_wait" | "create" | "verify" | "done"
	Tables      []StatePathRebuildTable `json:"tables"`
	Statements  []RollupStmtResult      `json:"statements"`
	LockOpen    bool                    `json:"lockOpen"`
	LockReason  string                  `json:"lockReason"`
	StillLegacy []string                `json:"stillLegacy"`
	Resume      string                  `json:"resume"`
	Note        string                  `json:"note"`
}

// StatePathGateError — v0.10.965 — kapı reddi: HİÇBİR ifade koşmadı.
type StatePathGateError struct{ Blocked []string }

func (e *StatePathGateError) Error() string { return strings.Join(e.Blocked, " · ") }

// statePathReplica — bir tablonun bir host'taki system.replicas satırı.
type statePathReplica struct {
	Path        string
	ReplicaName string
	Total       int
	Active      int
	ReadOnly    bool
}

// ───────────────────────── 2.2 sınıflandırma (SAF) ─────────────────────────

// classifyStatePathTable — v0.10.965 — SAF: bir izin listesi tablosunun
// küme genelindeki hâli ve sihirbazın eylemi. hosts: erişilebilir host'lar
// (N), engines: host → motor (system.tables), reps: host → system.replicas.
func classifyStatePathTable(name string, hosts []string, engines map[string]string,
	reps map[string]statePathReplica, zkPrefix string) (state, action, detail string) {
	n := len(hosts)
	want := unifiedStatePath(zkPrefix, name)
	present, uni, leg := 0, 0, 0
	var unknown []string
	activeShort, totalOff := false, false
	for _, h := range hosts {
		eng := engines[h]
		r, hasRep := reps[h]
		if eng == "" && !hasRep {
			continue
		}
		present++
		switch {
		case eng != "" && !strings.HasPrefix(eng, "Replicated"):
			unknown = append(unknown, fmt.Sprintf("%s'te motor %s (Replicated değil)", h, eng))
			continue
		case !hasRep:
			unknown = append(unknown, fmt.Sprintf("%s'te Replicated ama system.replicas satırı yok — yeniden ölç", h))
			continue
		case r.ReadOnly:
			unknown = append(unknown, fmt.Sprintf("%s'te replika readonly", h))
			continue
		}
		switch {
		case r.Path == want:
			uni++
			if r.Active < n {
				activeShort = true
			}
			if r.Total != n {
				totalOff = true
			}
		case legacyStatePath(zkPrefix, name, r.Path):
			leg++
		default:
			unknown = append(unknown, fmt.Sprintf("%s'te tanınmayan ZK yolu %s (ne birleşik ne eski biçim)", h, r.Path))
		}
	}
	if len(unknown) > 0 {
		return statePathStUnknown, statePathActBlocked, strings.Join(unknown, "; ")
	}
	switch {
	case present == 0:
		return statePathStAbsent, statePathActCreate, ""
	case uni > 0 && leg > 0:
		return statePathStMixed, statePathActRebuild, ""
	case leg > 0 && present == n:
		return statePathStLegacy, statePathActRebuild, ""
	case leg > 0:
		return statePathStLegacyPartial, statePathActRebuild, "önceki DROP yarım"
	case present < n:
		return statePathStUnifiedPartial, statePathActCreate, ""
	case activeShort:
		return statePathStUnknown, statePathActBlocked, "birleşik ama aktif replika eksik — Replika tutarlılığı satırına bak"
	case totalOff:
		return statePathStUnknown, statePathActBlocked, fmt.Sprintf("birleşik yolda kayıtlı replika sayısı %d host'la uyuşmuyor — yol paylaşılıyor olabilir, Replika tutarlılığı satırına bak", n)
	}
	return statePathStUnified, statePathActSkip, ""
}

// statePathVerified — v0.10.965 — SAF: kurulan tablo her host'ta birleşik
// yolda, Replicated, benzersiz replika adıyla, total == active == N ve
// readonly değil mi?
func statePathVerified(name string, hosts []string, engines map[string]string,
	reps map[string]statePathReplica, zkPrefix string) bool {
	n := len(hosts)
	if n == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, h := range hosts {
		r, ok := reps[h]
		if !ok || !strings.HasPrefix(engines[h], "Replicated") {
			return false
		}
		if r.Path != unifiedStatePath(zkPrefix, name) || r.ReadOnly || r.Total != n || r.Active != n {
			return false
		}
		if r.ReplicaName == "" || seen[r.ReplicaName] {
			return false
		}
		seen[r.ReplicaName] = true
	}
	return true
}

// ───────────────────────── 2.4 kapılar (SAF) ─────────────────────────

// statePathAckMaxAge — onayın en büyük yaşı: ölçümden 30 dk sonra bayat.
const statePathAckMaxAge = 30 * time.Minute

// statePathStateLabel — v0.10.965 — SAF: durum adının operatör metni
// (statePaths.ts STATE_LABEL aynası). Ret ve devam metinleri operatörün az
// önce onayladığı modalın diliyle konuşur; JSON alanları ve audit HAM adı
// taşır. Tanınmayan değer (onaydaki durum istemciden gelir) aynen döner.
func statePathStateLabel(s string) string {
	switch s {
	case statePathStLegacy:
		return "eski yol"
	case statePathStAbsent:
		return "yok"
	case statePathStUnified:
		return "birleşik"
	case statePathStLegacyPartial:
		return "eski (bazı host'larda yok)"
	case statePathStUnifiedPartial:
		return "birleşik (bazı host'larda yok)"
	case statePathStMixed:
		return "karışık (birleşik + eski)"
	case statePathStUnknown:
		return "tanınmayan"
	}
	return s
}

// statePathAckAge — v0.10.965 — SAF: onayın yaşı [0, 30 dk] içinde mi. Yaş
// tam dakika (Go süre metni "31m5s" Türkçe "dk" ile karışmasın); gelecek
// tarihli ölçüm eksi süre yerine açıkça söylenir.
func statePathAckAge(measuredAt int64, now time.Time) string {
	age := now.Sub(time.UnixMilli(measuredAt))
	switch {
	case age < 0:
		return "onay bayat (ölçüm zamanı gelecekte; en çok 30 dk) — yeniden planla"
	case age > statePathAckMaxAge:
		return fmt.Sprintf("onay bayat (%d dk önce ölçüldü; en çok 30 dk) — yeniden planla", int(age/time.Minute))
	}
	return ""
}

// statePathAckGate — v0.10.965 — SAF: bir tablonun şimdiki durumu/satırı
// operatörün onayladığıyla uyuşuyor mu. "" = geçer.
//
//   - durum değiştiyse (onayda legacy, şimdi başka) → yeniden planla
//   - empty_only: şimdi satır varsa ASLA (bir v2 yazıcısı çalışıyor olabilir)
//   - şimdi 0 satır: onay gerekmez (silinecek veri yok)
//   - satır var, onay yok → reddet
//   - derived (ingest_ledger): şimdi ≤ onay + max(onay/20, 10000) — ~30 dk
//     defter büyümesi; TTL/merge azalması serbest
//   - operator_exception (ai_eval_runs): şimdi ≤ onay — onaydan sonra yeni
//     bir koşu yazıldıysa reddet
func statePathAckGate(entry statePathEntry, state string, rows uint64, ack *StatePathAckTable) string {
	// v0.10.965 — metinler modalın diliyle: durum etiketi, satırlar noktalı binlik (fmtCount).
	if ack != nil && ack.State != state {
		return fmt.Sprintf("%s: durum değişti (onay: %s, şimdi: %s) — yeniden planla", entry.Name, statePathStateLabel(ack.State), statePathStateLabel(state))
	}
	if entry.Class == statePathClassEmptyOnly && rows > 0 {
		return fmt.Sprintf("%s: yalnız BOŞKEN yeniden kurulur, şimdi %s satır — bir Rollouts v2 yazıcısı çalışıyor olabilir", entry.Name, fmtCount(rows))
	}
	if rows == 0 {
		return ""
	}
	if ack == nil {
		return fmt.Sprintf("%s: %s satır var ama onay yok — önce planla ve satır sayısını onayla", entry.Name, fmtCount(rows))
	}
	limit := ack.Rows
	if entry.Class == statePathClassDerived {
		limit += max(ack.Rows/20, 10000)
	}
	if rows > limit {
		return fmt.Sprintf("%s: onaylanan %s, şimdi %s satır — yeniden planla", entry.Name, fmtCount(ack.Rows), fmtCount(rows))
	}
	return ""
}

// statePathEvalAckGate — v0.10.965 — SAF: ai_eval_runs için MANTIKSAL onay
// kapısı. Satır kapısı (şimdi ≤ onay) FİZİKSEL satırı sayar: tablo
// ReplacingMergeTree(version) ORDER BY id, her koşu vaka başına yeni sürüm
// yazar; plan anında birleşmemiş U sürüm varken plan→apply arasında başlayıp
// biten bir koşu, birleşmeler (ve TTL) sayımı küçülttüğü için gizlenebilirdi.
// Bu yüzden en son yazımın zamanı onayın ölçüm anıyla karşılaştırılır:
// ölçümde ya da sonrasında yazım varsa operatör onu hiç görmedi → reddet.
// Saat kayması güvenli yöne (ret) düşer. evalChecked=false (R8 koşmadı ya da
// okunamadı; okunamama zaten engeldir) ya da onay yoksa "".
func statePathEvalAckGate(evalChecked bool, lastWriteMs int64, ack *StatePathRebuildAck) string {
	if !evalChecked || ack == nil || lastWriteMs < ack.MeasuredAt {
		return ""
	}
	ts := func(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04:05 UTC") }
	return fmt.Sprintf("ai_eval_runs: plandan sonra bir evalset koşusu yazıldı (onay ölçümü %s, son yazım %s) — yeniden planla", ts(ack.MeasuredAt), ts(lastWriteMs))
}

// statePathAckAll — v0.10.965 — SAF: onay kapısı plan tablolarının hepsine.
// Onayda olmayan ATLA satırı sınanmaz (veri silinmez).
func statePathAckAll(tables []StatePathRebuildTable, ack *StatePathRebuildAck, now time.Time) []string {
	if ack == nil {
		return []string{"ack zorunlu — önce planla"}
	}
	var out []string
	if msg := statePathAckAge(ack.MeasuredAt, now); msg != "" {
		out = append(out, msg)
	}
	byName := map[string]*StatePathAckTable{}
	for i := range ack.Tables {
		byName[ack.Tables[i].Table] = &ack.Tables[i]
	}
	for _, t := range tables {
		e, ok := statePathEntryFor(t.Table)
		if !ok {
			continue
		}
		a := byName[t.Table]
		if a == nil && t.Action == statePathActSkip {
			continue
		}
		// v0.10.965 — plan anında ATLA olan (onaya girmeyen) tablo şimdi
		// düşürülecek/kurulacaksa operatör onu hiç onaylamadı: 0 satırda bile
		// reddet (satır varsa aşağıdaki "onay yok" metni daha açıklayıcı).
		if a == nil && t.Rows == 0 {
			out = append(out, fmt.Sprintf("%s: plan onayında yok (şimdi: %s) — yeniden planla", t.Table, statePathStateLabel(t.State)))
			continue
		}
		if msg := statePathAckGate(e, t.State, t.Rows, a); msg != "" {
			out = append(out, msg)
		}
	}
	return out
}

// statePathWriterBlobs — R9'un iki blob'unun YEREL şekli. internal/rollout ve
// internal/argocd İTHAL EDİLMEZ (ikisi başka iş akışlarında değişiyor);
// yalnız kararın okuduğu iki alan.
type statePathRolloutsBlob struct {
	Source string `json:"source"`
}
type statePathArgocdBlob struct {
	Enabled bool `json:"enabled"`
}

// statePathWritersGate — v0.10.965 — SAF: sekiz Rollouts v2 tablosunun
// yazıcıları kapalı mı? source ∈ {"", "v1"} ve argocd.enabled=false.
// Okuma ya da çözme hatası REDDEDER (emin değilsek düşürmeyiz).
func statePathWritersGate(rolloutsJSON, argocdJSON []byte, err error) string {
	if err != nil {
		return fmt.Sprintf("Rollouts v2 yazıcı ayarları okunamadı (%v) — sekiz tablo yalnız yazıcıların kapalı olduğu doğrulanınca yeniden kurulur", err)
	}
	var ro statePathRolloutsBlob
	var ar statePathArgocdBlob
	if len(rolloutsJSON) > 0 {
		if jerr := json.Unmarshal(rolloutsJSON, &ro); jerr != nil {
			return fmt.Sprintf("Rollouts v2 yazıcı ayarları çözülemedi (rollouts: %v) — sekiz tablo yalnız yazıcıların kapalı olduğu doğrulanınca yeniden kurulur", jerr)
		}
	}
	if len(argocdJSON) > 0 {
		if jerr := json.Unmarshal(argocdJSON, &ar); jerr != nil {
			return fmt.Sprintf("Rollouts v2 yazıcı ayarları çözülemedi (argocd: %v) — sekiz tablo yalnız yazıcıların kapalı olduğu doğrulanınca yeniden kurulur", jerr)
		}
	}
	if (ro.Source != "" && ro.Source != "v1") || ar.Enabled {
		return fmt.Sprintf("Rollouts v2 yazıcıları açık olabilir (rollouts.source=%q, argocd.enabled=%v) — sekiz tablo yalnız yazıcılar kapalıyken yeniden kurulur", ro.Source, ar.Enabled)
	}
	return ""
}

// unifiedZKGate — v0.10.965 — SAF: birleşik znode'un sahipleri.
//
//   - ZNONODE ya da çocuksuz: sahipsiz → geçer
//   - tabloda canlı birleşik replika varsa (unified_partial / mixed) sahipler
//     CANLI birleşik replikaların alt kümesi olmalı. Bu kümenin beklenen adı
//     olup canlı tablosu OLMAYAN sahip BAYAT kayıttır (PV değişimi, SYNC'siz
//     DROP, yarım CREATE): CREATE o host'ta 253 REPLICA_ALREADY_EXISTS ile
//     düşer, karışık tabloda DROP sonrası znode boşalmaz — DROP'tan ÖNCE reddet
//   - aksi hâlde başka biri tutuyor → reddet (yol paylaşılıyor olabilir)
//   - başka okuma hatası → reddet: yalnız sahipsiz olduğu DOĞRULANMIŞ yola kurulur
func unifiedZKGate(table, path string, owners []string, err error, liveUnifiedNames, expectedNames []string) string {
	switch {
	case err != nil && zkNoNode(err):
		return ""
	case err != nil:
		return fmt.Sprintf("%s: birleşik ZK yolu okunamadı (%v) — yalnız sahipsiz olduğu doğrulanmış yola kurulur", table, err)
	case len(owners) == 0:
		return ""
	}
	if len(liveUnifiedNames) > 0 {
		live := map[string]bool{}
		for _, n := range liveUnifiedNames {
			live[n] = true
		}
		exp := map[string]bool{}
		for _, n := range expectedNames {
			exp[n] = true
		}
		var stale []string
		foreign := false
		for _, o := range owners {
			switch {
			case live[o]:
			case exp[o]:
				stale = append(stale, o)
			default:
				foreign = true
			}
		}
		if !foreign && len(stale) == 0 {
			return ""
		}
		if !foreign {
			sort.Strings(stale)
			return fmt.Sprintf("%s: birleşik ZK yolu %s'de tablosu olmayan host'un replika kaydı kalmış (%s) — CREATE o host'ta REPLICA_ALREADY_EXISTS ile düşer; elle incele — SYSTEM DROP REPLICA '<ad>' FROM ZKPATH '%s' otomatik koşmaz",
				table, path, strings.Join(stale, ", "), path)
		}
	}
	sorted := append([]string(nil), owners...)
	sort.Strings(sorted)
	return fmt.Sprintf("%s: birleşik ZK yolu %s başka replikalarca tutuluyor (%s) — yol paylaşılıyor olabilir (aynı önek başka kurulum); elle incele — SYSTEM DROP REPLICA … FROM ZKPATH otomatik koşmaz",
		table, path, strings.Join(sorted, ", "))
}

// statePathReplicaNames — v0.10.965 — SAF: her host'un birleşik yoldaki
// replika adı ({shard}-{replica} makrolarla). Çözülemeyen makro ya da iki
// host'ta aynı ad → engel (REPLICA_ALREADY_EXISTS / yanlış yol).
func statePathReplicaNames(hosts []string, macros map[string]map[string]string) (names map[string]string, blocked []string) {
	names = map[string]string{}
	byName := map[string][]string{}
	for _, h := range hosts {
		n, err := expandMacros(stateReplicaName, macros[h])
		if err != nil {
			blocked = append(blocked, fmt.Sprintf("%s: makro çözülemedi (%v)", h, err))
			continue
		}
		names[h] = n
		byName[n] = append(byName[n], h)
	}
	dups := make([]string, 0)
	for n, hs := range byName {
		if len(hs) > 1 {
			dups = append(dups, n)
		}
	}
	sort.Strings(dups)
	for _, n := range dups {
		hs := byName[n]
		sort.Strings(hs)
		blocked = append(blocked, fmt.Sprintf("replika adı çakışması: {shard}-{replica} %s host'larında %q", strings.Join(hs, ", "), n))
	}
	return names, blocked
}

// statePathLockAfter — v0.10.965 — SAF: çalıştırmadan SONRA kilit. Seçilen
// her tablo birleşik yoluyla değiştirilir, sonra boot'un kuralı
// (useUnifiedStatePath(obs, önek, "")). still: eski yolda kalan state
// tabloları (izin listesi dışındakiler dahil), sıralı.
func statePathLockAfter(currentPaths map[string]string, selected []string, zkPrefix string) (open bool, reason string, still []string) {
	paths := make(map[string]string, len(currentPaths)+len(selected))
	for t, p := range currentPaths {
		paths[t] = p
	}
	for _, t := range selected {
		paths[t] = unifiedStatePath(zkPrefix, t)
	}
	open, reason = useUnifiedStatePath(stateObservation{ok: true, paths: paths}, zkPrefix, "")
	return open, reason, statePathStillLegacy(paths, zkPrefix)
}

// statePathStillLegacy — SAF: birleşik yolda olmayan state tabloları, sıralı, [] (null değil).
func statePathStillLegacy(paths map[string]string, zkPrefix string) []string {
	out := []string{}
	for t, p := range paths {
		if p != unifiedStatePath(zkPrefix, t) {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// statePathObserved — SAF: R3 satırlarından boot'un gözlemi (stateProbeTable
// süzgeci + mergeObservedStatePath). Sıra deterministik.
func statePathObserved(reps map[string]map[string]statePathReplica, zkPrefix string) map[string]string {
	paths := map[string]string{}
	tables := make([]string, 0, len(reps))
	for t := range reps {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	for _, t := range tables {
		if !stateProbeTable(t) {
			continue
		}
		hosts := make([]string, 0, len(reps[t]))
		for h := range reps[t] {
			hosts = append(hosts, h)
		}
		sort.Strings(hosts)
		for _, h := range hosts {
			mergeObservedStatePath(paths, t, reps[t][h].Path, zkPrefix)
		}
	}
	return paths
}

// statePathSelectionError — v0.10.965 — SAF: istek seçimi izin listesinin
// tekrarsız alt kümesi mi? Hiçbir okumadan ÖNCE koşar.
func statePathSelectionError(tables []string) string {
	seen := map[string]bool{}
	for _, t := range tables {
		n := strings.TrimSpace(t)
		if !StatePathRebuildAllowed(n) {
			return fmt.Sprintf("geçersiz tablo: %s — izin listesinde değil", n)
		}
		if seen[n] {
			return fmt.Sprintf("yinelenen tablo: %s", n)
		}
		seen[n] = true
	}
	return ""
}

// statePathGateInput — statePathGate'in girdileri (ölçüm + render sonuçları).
type statePathGateInput struct {
	ClusterMode bool
	Cluster     string
	CfgCluster  string
	RosterSize  int
	Hosts       []string // cevap veren host'lar
	ReachErr    error

	Queue    DDLQueueHealth
	QueueErr error

	DBEngines map[string]string // host → veritabanı motoru

	NothingToDo bool
	Tables      []StatePathRebuildTable // seçilen (izin listesi sırası)

	ReplicaBlocked []string
	ZKBlocked      []string
	RenderBlocked  []string

	ZKPrefix     string
	EightTouched bool // sekizden biri rebuild ya da create

	WritersChecked bool
	WritersBlocked string

	EvalChecked bool
	EvalRunning uint64
	EvalErr     error
	// EvalByParts — v0.10.965: tablo bazı host'larda yok, sayım parça
	// yoklamasından (statePathEvalPartsSQL): EvalRunning taze PARÇA sayısıdır.
	EvalByParts bool
}

// statePathGate — v0.10.965 — SAF: engel nedenleri, bu SIRAYLA. İlk üç
// (küme kipi, küme, erişim) ölçümü anlamsız kılar: sonrası sınanmaz.
// Onay ve kilit-sonrası kapıları apply'a özgüdür (statePathAckAll,
// statePathLockAfter) ve bu listenin ARDINA eklenir.
func statePathGate(in statePathGateInput) []string {
	if !in.ClusterMode {
		return []string{"küme kipi değil"}
	}
	if !validRolloutLayerCluster(in.Cluster) || in.Cluster != strings.TrimSpace(in.CfgCluster) || in.RosterSize == 0 {
		return []string{fmt.Sprintf("küme %q uygulamanın kümesi değil (%q) / system.clusters'ta yok", in.Cluster, strings.TrimSpace(in.CfgCluster))}
	}
	if in.ReachErr != nil || len(in.Hosts) < in.RosterSize {
		// v0.10.965 — okuma hatası mesaja eklenir: tek ölü host da tüm sorguyu
		// düşürür ("0 cevap"), yetki/eşzamanlılık hatası da buraya düşer —
		// operatör gerçek nedeni görmeli, ölü host aramaya gitmemeli.
		why := ""
		if in.ReachErr != nil {
			why = fmt.Sprintf(" (system.one okuma hatası: %v)", in.ReachErr)
		}
		return []string{fmt.Sprintf("erişilemeyen host: küme tanımında %d host, %d cevap verdi%s (skip_unavailable_shards YOK) — ON CLUSTER DROP o host'ta kuyrukta bekler, karışık durum kalırdı", in.RosterSize, len(in.Hosts), why)}
	}
	var out []string
	switch {
	case in.QueueErr != nil:
		out = append(out, fmt.Sprintf("dağıtık DDL kuyruğu sağlıklı değil (%s): %s", "okunamadı", in.QueueErr.Error()))
	case in.Queue.Verdict != "healthy":
		out = append(out, fmt.Sprintf("dağıtık DDL kuyruğu sağlıklı değil (%s): %s", in.Queue.Verdict, in.Queue.Detail))
	}
	for _, h := range in.Hosts {
		if eng := in.DBEngines[h]; eng != "Atomic" {
			if eng == "" {
				eng = "okunamadı"
			}
			out = append(out, fmt.Sprintf("%s: veritabanı motoru %s (Atomic değil)", h, eng))
		}
	}
	if in.NothingToDo {
		out = append(out, "yapılacak iş yok: seçilen tabloların hepsi zaten birleşik yolda")
	}
	for _, t := range in.Tables {
		if t.State == statePathStUnknown {
			out = append(out, fmt.Sprintf("%s: %s", t.Table, t.Detail))
		}
	}
	out = append(out, in.ReplicaBlocked...)
	out = append(out, in.ZKBlocked...)
	out = append(out, in.RenderBlocked...)
	if in.EightTouched && in.ZKPrefix != rolloutV2ZKPrefix {
		out = append(out, fmt.Sprintf("özel ZK öneki (%s): Rollouts v2 tabloları 0015 metniyle SABİT /clickhouse/tables/state/<ad> yoluna kurulur — bu sekiz tablo burada kurulmaz", in.ZKPrefix))
	}
	if in.WritersChecked && in.WritersBlocked != "" {
		out = append(out, in.WritersBlocked)
	}
	if in.EvalChecked {
		// v0.10.965 — legacy_partial'a özel "geçici" dalı YOK: tablo bir yerde
		// eksikse sayım artık parça yoklamasından gelir (UNKNOWN_TABLE
		// beklenmez); her okuma hatası gerçek bir engeldir.
		switch {
		case in.EvalErr != nil:
			out = append(out, fmt.Sprintf("ai_eval_runs: çalışan koşu sayımı okunamadı (%v) — koşu olmadığı doğrulanmadan düşürülmez", in.EvalErr))
		case in.EvalRunning > 0 && in.EvalByParts:
			out = append(out, fmt.Sprintf("ai_eval_runs: son 15 dk içinde %d taze parça (tablo bazı host'larda yok, koşular tek tek sayılamıyor) — bir evalset koşusu çalışıyor olabilir, bitmesini bekle", in.EvalRunning))
		case in.EvalRunning > 0:
			out = append(out, fmt.Sprintf("ai_eval_runs: %d evalset koşusu şu an çalışıyor (son 15 dk) — bitmesini bekle", in.EvalRunning))
		}
	}
	return out
}

// ───────────────────────── 2.5 DDL (tek kaynak) ─────────────────────────

// statePathDropDDL — v0.10.965 — SAF: küme geneli, SYNC (znode hemen
// temizlensin; birleşik yol ardından sahipsiz doğrulanır) + boyut
// muhafızı kapalı (purgeGuard: 50 GB tavanı düşürmeyi engellemesin).
func statePathDropDDL(name, onCluster string) string {
	return "DROP TABLE IF EXISTS " + name + onCluster + " SYNC" + purgeGuard
}

// statePathCreateDDL — v0.10.965 — tablonun birleşik yoldaki CREATE'i.
//
//   - sekiz Rollouts v2 tablosu: 0015'in ifadesi (rolloutV2LayerStatements),
//     ada göre (ddlCreatesObject) — operatörün kararıyla 0015.
//   - ingest_ledger / ai_eval_runs: kanonik katalog, TAZE gölge Store'un
//     tüm-birleşik gözlemiyle render. Kopya DEĞİL (Store atomic.Pointer
//     taşır); pod'un boot gözlemi kullanılmaz.
//
// İkisi de statePathRenderCheck'ten geçer.
func (s *Store) statePathCreateDDL(name, cluster string) (string, error) {
	e, ok := statePathEntryFor(name)
	if !ok {
		return "", fmt.Errorf("%s: izin listesinde değil", name)
	}
	var stmt string
	if e.Class == statePathClassEmptyOnly {
		stmts, err := rolloutV2LayerStatements(cluster)
		if err != nil {
			return "", fmt.Errorf("%s: birleşik yola render edilemedi (%v)", name, err)
		}
		for _, st := range stmts {
			if n, ok := ddlCreatesObject(st); ok && n == name {
				stmt = st
				break
			}
		}
		if stmt == "" {
			return "", fmt.Errorf("%s: birleşik yola render edilemedi (0015'te ifade yok)", name)
		}
	} else {
		ddl := tableDDLByName(s.canonicalTableDDL, name)
		if ddl == "" {
			return "", fmt.Errorf("%s: birleşik yola render edilemedi (kanonik katalog yüklenmemiş / tanım yok)", name)
		}
		shadow := &Store{
			cfg:      config.CHConfig{ClusterName: s.cfg.ClusterName, ReplicaPath: s.cfg.ReplicaPath},
			stateObs: stateObservation{ok: true, paths: map[string]string{}},
		}
		frags := shadow.adaptDDL(ddl)
		if len(frags) != 1 {
			return "", fmt.Errorf("%s: birleşik yola render edilemedi (%d ifade, 1 bekleniyordu)", name, len(frags))
		}
		stmt = frags[0]
	}
	if err := statePathRenderCheck(stmt, name, s.zkPrefix()); err != nil {
		return "", err
	}
	return stmt, nil
}

// statePathRenderCheck — v0.10.965 — SAF: ifade tabloyu ON CLUSTER, birleşik
// yolda ve {shard}-{replica} replika adıyla mı kuruyor?
func statePathRenderCheck(stmt, name, zkPrefix string) error {
	var why string
	_, path, rep, ok := replicatedEngineArgs(stmt)
	n, created := ddlCreatesObject(stmt)
	switch {
	case !created || n != name:
		why = fmt.Sprintf("ifade %q kuruyor", n)
	case !strings.Contains(stmt, " ON CLUSTER "):
		why = "ON CLUSTER yok"
	case !ok:
		why = "Replicated motor argümanı yok"
	case path != unifiedStatePath(zkPrefix, name):
		why = fmt.Sprintf("yol %s, beklenen %s", path, unifiedStatePath(zkPrefix, name))
	case rep != stateReplicaName:
		why = fmt.Sprintf("replika %s, beklenen %s", rep, stateReplicaName)
	}
	if why != "" {
		return fmt.Errorf("%s: birleşik yola render edilemedi (%s)", name, why)
	}
	return nil
}

// ───────────────────────── 2.3 katı ölçüm ─────────────────────────

// statePathQueueHealth — test dikişi (R10).
var statePathQueueHealth = func(ctx context.Context, s *Store) (DDLQueueHealth, error) { return s.GetDDLQueueHealth(ctx) }

// statePathPoll — test dikişi: yoklama aralığı ve bütçeler. Deneme sayısı
// 1 + bütçe/aralık (aralık 0 → tek deneme).
var statePathPoll = struct {
	Every  time.Duration
	Drop   time.Duration
	Verify time.Duration
}{Every: 2 * time.Second, Drop: 90 * time.Second, Verify: 120 * time.Second}

type statePathLive struct {
	reps    map[string]map[string]statePathReplica // tablo → host → replika (TÜM tablolar)
	engines map[string]map[string]string           // tablo → host → motor (on ad)
}

type statePathMeasure struct {
	cluster, db string
	roster      int
	hosts       []string // cevap veren hostName()'ler, sıralı
	reachErr    error
	statePathLive
	rows       map[string]map[string]uint64 // tablo → host → aktif satır (on ad)
	dbEngines  map[string]string            // host → veritabanı motoru
	macros     map[string]map[string]string // host → makro → değer
	isLocal    map[string]uint32
	isLocalErr error
}

func (m *statePathMeasure) reachable() bool {
	return m.reachErr == nil && m.roster > 0 && len(m.hosts) >= m.roster
}

// Okuma metinleri — küme adı validRolloutLayerCluster'dan geçmiş olmalı.
func statePathReachSQL(cluster string) string {
	return fmt.Sprintf("SELECT hostName() FROM clusterAllReplicas('%s', system.one) LIMIT 1000 SETTINGS max_execution_time = 10", cluster)
}

func statePathReplicasSQL(cluster string) string {
	return fmt.Sprintf("SELECT hostName(), table, zookeeper_path, replica_name, toUInt32(total_replicas), toUInt32(active_replicas), toUInt8(is_readonly) "+
		"FROM clusterAllReplicas('%s', system.replicas) WHERE database = ? LIMIT 20000 SETTINGS max_execution_time = 15", cluster)
}

func statePathTablesSQL(cluster string) string {
	return fmt.Sprintf("SELECT hostName(), name, engine FROM clusterAllReplicas('%s', system.tables) "+
		"WHERE database = ? AND name IN (%s) LIMIT 1000 SETTINGS max_execution_time = 10", cluster, statePathNameList())
}

func statePathPartsSQL(cluster string) string {
	return fmt.Sprintf("SELECT hostName(), table, toUInt64(sum(rows)) FROM clusterAllReplicas('%s', system.parts) "+
		"WHERE database = ? AND active AND table IN (%s) GROUP BY hostName(), table LIMIT 1000 SETTINGS max_execution_time = 15", cluster, statePathNameList())
}

func statePathDatabasesSQL(cluster string) string {
	return fmt.Sprintf("SELECT hostName(), engine FROM clusterAllReplicas('%s', system.databases) WHERE name = ? LIMIT 100 SETTINGS max_execution_time = 10", cluster)
}

func statePathMacrosSQL(cluster string) string {
	return fmt.Sprintf("SELECT hostName(), macro, substitution FROM clusterAllReplicas('%s', system.macros) LIMIT 1000 SETTINGS max_execution_time = 10", cluster)
}

func statePathIsLocalSQL(cluster string) string {
	return fmt.Sprintf("SELECT hostName(), toUInt32(countIf(is_local)) FROM clusterAllReplicas('%s', system.clusters) "+
		"WHERE cluster = ? GROUP BY hostName() LIMIT 1000 SETTINGS max_execution_time = 10", cluster)
}

// statePathEvalSQL — R8: son StatePathEvalFresh içinde güncellenmiş
// "running" koşu sayısı + en son yazımın zamanı (unix ms; boş tabloda 0).
// db chObjRe'den geçmiş olmalı. v0.10.965 — ikinci kolon onay kapısının
// MANTIKSAL denetimi (statePathEvalAckGate): fiziksel satır sayımı
// birleşmelerle küçülüp plan sonrası bir koşuyu gizleyebilir.
func statePathEvalSQL(cluster, db string) string {
	return fmt.Sprintf("SELECT toUInt64(countIf(st = 'running' AND upd >= now64(9) - INTERVAL %d MINUTE)), toInt64(toUnixTimestamp64Milli(max(upd))) "+
		"FROM (SELECT id, argMax(status, version) AS st, argMax(updated_at, version) AS upd "+
		"FROM clusterAllReplicas('%s', `%s`.`ai_eval_runs`) GROUP BY id) "+
		"LIMIT 1 SETTINGS max_execution_time = 10",
		int(StatePathEvalFresh/time.Minute), cluster, db)
}

// statePathEvalPartsSQL — v0.10.965 — R8'in KATI yedeği. Tablo bazı
// erişilebilir host'larda YOKSA (legacy_partial ya da eksik host'lu mixed)
// tablonun kendisini okumak UNKNOWN_TABLE ile düşer; o host'taki ON CLUSTER
// DROP hata ile bittiyse (DDL girdisi yeniden denenmez) durum kalıcıdır ve
// sihirbaz ai_eval_runs'ı HİÇ düşüremezdi. system.parts her host'ta var:
// son StatePathEvalFresh içinde yazılmış aktif parça sayısı + en yeni
// parçanın zamanı (ms). Çalışan koşu vaka başına yazar → taze parça bırakır.
// Birleşme de taze parça üretir: kısa süreli yanlış ret, güvenli yön.
// level=0 süzgeci YOK: birleşmiş yeni bir yazım kaçardı.
func statePathEvalPartsSQL(cluster string) string {
	return fmt.Sprintf("SELECT toUInt64(countIf(modification_time >= now() - INTERVAL %d MINUTE)), toInt64(toUnixTimestamp(max(modification_time))) * 1000 "+
		"FROM clusterAllReplicas('%s', system.parts) WHERE database = ? AND table = 'ai_eval_runs' AND active "+
		"LIMIT 1 SETTINGS max_execution_time = 10",
		int(StatePathEvalFresh/time.Minute), cluster)
}

// statePathReach — R2: cevap veren host'lar (sıralı, tekil).
func (s *Store) statePathReach(ctx context.Context, cluster string) ([]string, error) {
	rows, err := s.conn.Query(ctx, statePathReachSQL(cluster))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := map[string]bool{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		set[h] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}

// statePathReadLive — R3 + R4 (katı). Yoklamalar da bunu okur.
func (s *Store) statePathReadLive(ctx context.Context, cluster, db string) (statePathLive, error) {
	live := statePathLive{reps: map[string]map[string]statePathReplica{}, engines: map[string]map[string]string{}}
	rrows, err := s.conn.Query(ctx, statePathReplicasSQL(cluster), db)
	if err != nil {
		return live, fmt.Errorf("system.replicas: %w", err)
	}
	for rrows.Next() {
		var host, table string
		var r statePathReplica
		var total, active uint32
		var ro uint8
		if err := rrows.Scan(&host, &table, &r.Path, &r.ReplicaName, &total, &active, &ro); err != nil {
			rrows.Close()
			return live, fmt.Errorf("system.replicas: %w", err)
		}
		r.Total, r.Active, r.ReadOnly = int(total), int(active), ro == 1
		if live.reps[table] == nil {
			live.reps[table] = map[string]statePathReplica{}
		}
		live.reps[table][host] = r
	}
	rrows.Close()
	if err := rrows.Err(); err != nil {
		return live, fmt.Errorf("system.replicas: %w", err)
	}
	trows, err := s.conn.Query(ctx, statePathTablesSQL(cluster), db)
	if err != nil {
		return live, fmt.Errorf("system.tables: %w", err)
	}
	for trows.Next() {
		var host, name, engine string
		if err := trows.Scan(&host, &name, &engine); err != nil {
			trows.Close()
			return live, fmt.Errorf("system.tables: %w", err)
		}
		if live.engines[name] == nil {
			live.engines[name] = map[string]string{}
		}
		live.engines[name][host] = engine
	}
	trows.Close()
	if err := trows.Err(); err != nil {
		return live, fmt.Errorf("system.tables: %w", err)
	}
	return live, nil
}

// measureStatePaths — v0.10.965 — R1–R7 (+ is_local uyarısı), KATI. Erişim
// eksikse (R2) sonraki okumalar yapılmaz: skip'siz clusterAllReplicas zaten
// düşerdi; kapı erişim mesajıyla reddeder.
func (s *Store) measureStatePaths(ctx context.Context, cluster, db string) (*statePathMeasure, error) {
	m := &statePathMeasure{cluster: cluster, db: db}
	roster, err := s.clusterHostRowsFor(ctx, cluster) // R1
	if err != nil {
		return nil, fmt.Errorf("system.clusters: %w", err)
	}
	m.roster = len(roster)
	if m.roster == 0 {
		return m, nil
	}
	m.hosts, m.reachErr = s.statePathReach(ctx, cluster) // R2
	if !m.reachable() {
		return m, nil
	}
	if m.statePathLive, err = s.statePathReadLive(ctx, cluster, db); err != nil { // R3 + R4
		return nil, err
	}
	m.rows = map[string]map[string]uint64{} // R5
	prows, err := s.conn.Query(ctx, statePathPartsSQL(cluster), db)
	if err != nil {
		return nil, fmt.Errorf("system.parts: %w", err)
	}
	for prows.Next() {
		var host, table string
		var n uint64
		if err := prows.Scan(&host, &table, &n); err != nil {
			prows.Close()
			return nil, fmt.Errorf("system.parts: %w", err)
		}
		if m.rows[table] == nil {
			m.rows[table] = map[string]uint64{}
		}
		m.rows[table][host] = n
	}
	prows.Close()
	if err := prows.Err(); err != nil {
		return nil, fmt.Errorf("system.parts: %w", err)
	}
	m.dbEngines = map[string]string{} // R6
	drows, err := s.conn.Query(ctx, statePathDatabasesSQL(cluster), db)
	if err != nil {
		return nil, fmt.Errorf("system.databases: %w", err)
	}
	for drows.Next() {
		var host, engine string
		if err := drows.Scan(&host, &engine); err != nil {
			drows.Close()
			return nil, fmt.Errorf("system.databases: %w", err)
		}
		m.dbEngines[host] = engine
	}
	drows.Close()
	if err := drows.Err(); err != nil {
		return nil, fmt.Errorf("system.databases: %w", err)
	}
	// R7 — KATI: clusterMacrosByHost erişilemeyen shard'ı atlar, burada atlanmaz.
	m.macros = map[string]map[string]string{}
	mrows, err := s.conn.Query(ctx, statePathMacrosSQL(cluster))
	if err != nil {
		return nil, fmt.Errorf("system.macros: %w", err)
	}
	for mrows.Next() {
		var host, macro, sub string
		if err := mrows.Scan(&host, &macro, &sub); err != nil {
			mrows.Close()
			return nil, fmt.Errorf("system.macros: %w", err)
		}
		if m.macros[host] == nil {
			m.macros[host] = map[string]string{}
		}
		m.macros[host][macro] = sub
	}
	mrows.Close()
	if err := mrows.Err(); err != nil {
		return nil, fmt.Errorf("system.macros: %w", err)
	}
	// is_local — yalnız UYARI (blindHosts); hatası engel değil.
	m.isLocal = map[string]uint32{}
	lrows, lerr := s.conn.Query(ctx, statePathIsLocalSQL(cluster), cluster)
	if lerr != nil {
		m.isLocalErr = lerr
		return m, nil
	}
	for lrows.Next() {
		var host string
		var n uint32
		if err := lrows.Scan(&host, &n); err != nil {
			m.isLocalErr = err
			break
		}
		m.isLocal[host] = n
	}
	lrows.Close()
	if m.isLocalErr == nil {
		m.isLocalErr = lrows.Err()
	}
	return m, nil
}

// groupsFor — plan satırının grupları (R3 yolu + R5 satırı).
func (m *statePathMeasure) groupsFor(table, zkPrefix string) ([]StatePathGroup, uint64) {
	var rs []ReplicaState
	for h, r := range m.reps[table] {
		rs = append(rs, ReplicaState{Host: h, ZKPath: r.Path, TotalRows: m.rows[table][h]})
	}
	return statePathGroups(rs, zkPrefix, table)
}

// ───────────────────────── 2.6 plan ─────────────────────────

// statePathPlanned — plan + apply'ın ihtiyaç duyduğu ölçüm.
type statePathPlanned struct {
	plan     *StatePathRebuildPlan
	m        *statePathMeasure
	measured bool     // tablolar sınıflandırıldı (erişim tamam)
	selected []string // izin listesi sırasıyla
	// v0.10.965 — R8 hatasız okundu mu + ai_eval_runs'a en son yazım (unix
	// ms). Plan JSON'una girmez; apply'da onayın ölçüm anıyla karşılaştırılır.
	evalRead        bool
	evalLastWriteMs int64
}

// PlanStatePathRebuild — v0.10.965 — SALT OKUMA: ölç, sınıflandır, kapıları
// (onay hariç) koştur, ifadeleri render et. Engelli plan da 200'dür.
func (s *Store) PlanStatePathRebuild(ctx context.Context, tables []string) (*StatePathRebuildPlan, error) {
	pl, err := s.planStatePathRebuild(ctx, "", tables)
	if err != nil {
		return nil, err
	}
	return pl.plan, nil
}

func (s *Store) planStatePathRebuild(ctx context.Context, cluster string, tables []string) (*statePathPlanned, error) {
	prefix := s.zkPrefix()
	p := &StatePathRebuildPlan{
		ZKPrefix: prefix, MeasuredAt: time.Now().UnixMilli(),
		Tables: []StatePathRebuildTable{}, Drops: []string{}, Creates: []string{},
		StillLegacyAfter: []string{}, Blocked: []string{}, Checks: []string{}, Warnings: []string{},
	}
	out := &statePathPlanned{plan: p}
	if bad := statePathSelectionError(tables); bad != "" {
		p.Blocked = append(p.Blocked, bad)
		return out, nil
	}
	c := strings.TrimSpace(cluster)
	if c == "" {
		c = strings.TrimSpace(s.cfg.ClusterName)
	}
	p.Cluster = c
	in := statePathGateInput{ClusterMode: s.clusterMode(), Cluster: c, CfgCluster: s.cfg.ClusterName, ZKPrefix: prefix}
	// Küme adı SQL'e girmeden ÖNCE doğrulanır (kapının ilk iki adımı).
	if !in.ClusterMode || !validRolloutLayerCluster(c) || c != strings.TrimSpace(s.cfg.ClusterName) {
		p.Blocked = append(p.Blocked, statePathGate(in)...)
		return out, nil
	}
	db, err := s.currentDatabaseName(ctx)
	if err != nil {
		return nil, err
	}
	if !chObjRe.MatchString(db) {
		return nil, fmt.Errorf("veritabanı adı beklenmeyen biçimde: %q", db)
	}
	p.Database = db
	m, err := s.measureStatePaths(ctx, c, db)
	if err != nil {
		return nil, err
	}
	out.m = m
	p.Hosts = m.roster
	in.RosterSize, in.Hosts, in.ReachErr = m.roster, m.hosts, m.reachErr
	if !m.reachable() {
		p.Blocked = append(p.Blocked, statePathGate(in)...)
		return out, nil
	}
	out.measured = true

	// Sınıflandır (izin listesi sırası) + seçimi çöz.
	want := map[string]bool{}
	for _, t := range tables {
		want[strings.TrimSpace(t)] = true
	}
	for _, e := range statePathAllowlist {
		st, act, detail := classifyStatePathTable(e.Name, m.hosts, m.engines[e.Name], m.reps[e.Name], prefix)
		if len(want) > 0 && !want[e.Name] {
			continue
		}
		if len(want) == 0 && st == statePathStUnified {
			continue // boş seçim = birleşik OLMAYAN her izinli tablo
		}
		groups, rows := m.groupsFor(e.Name, prefix)
		p.Tables = append(p.Tables, StatePathRebuildTable{
			Table: e.Name, State: st, Action: act, Rows: rows, Groups: groups,
			Class: e.Class, ClassNote: e.Note, Detail: detail,
		})
		out.selected = append(out.selected, e.Name)
	}
	in.Tables = p.Tables
	in.NothingToDo = true
	var liveWork []string // rebuild + create
	for _, t := range p.Tables {
		if t.Action == statePathActRebuild || t.Action == statePathActCreate {
			in.NothingToDo = false
			liveWork = append(liveWork, t.Table)
			if statePathIsEight(t.Table) {
				in.EightTouched = true
			}
		}
		if t.Action == statePathActBlocked {
			in.NothingToDo = false
		}
	}
	p.Checks = append(p.Checks, fmt.Sprintf("%d/%d host erişilebilir", len(m.hosts), m.roster))

	// R10 — DDL kuyruğu.
	in.Queue, in.QueueErr = statePathQueueHealth(ctx, s)
	if in.QueueErr == nil && in.Queue.Verdict == "healthy" {
		p.Checks = append(p.Checks, "DDL kuyruğu sağlıklı")
	}
	in.DBEngines = m.dbEngines
	atomicHosts := 0
	for _, h := range m.hosts {
		if m.dbEngines[h] == "Atomic" {
			atomicHosts++
		}
	}
	if atomicHosts == len(m.hosts) {
		p.Checks = append(p.Checks, fmt.Sprintf("veritabanı motoru Atomic (%d host)", atomicHosts))
	}

	// Replika adları.
	names, rblocked := statePathReplicaNames(m.hosts, m.macros)
	in.ReplicaBlocked = rblocked
	if len(rblocked) == 0 {
		list := make([]string, 0, len(names))
		for _, h := range m.hosts {
			list = append(list, names[h])
		}
		sort.Strings(list)
		p.Checks = append(p.Checks, fmt.Sprintf("replika adları benzersiz (%s)", strings.Join(list, ", ")))
	}
	expected := make([]string, 0, len(names))
	for _, n := range names {
		expected = append(expected, n)
	}

	// R11 — birleşik znode sahipleri + render.
	var ownerless []string
	for _, t := range liveWork {
		path := unifiedStatePath(prefix, t)
		owners, zerr := zkChildren(ctx, s.conn, path+"/replicas")
		var liveUni []string
		for _, r := range m.reps[t] {
			if r.Path == path {
				liveUni = append(liveUni, r.ReplicaName)
			}
		}
		if msg := unifiedZKGate(t, path, owners, zerr, liveUni, expected); msg != "" {
			in.ZKBlocked = append(in.ZKBlocked, msg)
		} else {
			ownerless = append(ownerless, t)
		}
	}
	if len(ownerless) > 0 {
		p.Checks = append(p.Checks, "birleşik znode sahipsiz ya da yalnız bu kümenin: "+strings.Join(ownerless, ", "))
	}
	for _, t := range p.Tables {
		if t.Action == statePathActRebuild {
			p.Drops = append(p.Drops, statePathDropDDL(t.Table, s.onCluster()))
		}
		if t.Action == statePathActRebuild || t.Action == statePathActCreate {
			stmt, rerr := s.statePathCreateDDL(t.Table, c)
			if rerr != nil {
				in.RenderBlocked = append(in.RenderBlocked, rerr.Error())
				continue
			}
			p.Creates = append(p.Creates, stmt)
		}
	}
	if len(in.RenderBlocked) == 0 && len(p.Creates) > 0 {
		p.Checks = append(p.Checks, fmt.Sprintf("%d CREATE birleşik yola render edildi (%s/state/<ad>, replika %s)", len(p.Creates), prefix, stateReplicaName))
	}

	// R9 — sekizin yazıcıları (yalnız sekizden biri kurulacaksa).
	if in.EightTouched {
		in.WritersChecked = true
		ro, err1 := s.GetSetting(ctx, "rollouts")
		ar, err2 := s.GetSetting(ctx, "argocd")
		in.WritersBlocked = statePathWritersGate(ro, ar, errors.Join(err1, err2))
		if in.WritersBlocked == "" {
			p.Checks = append(p.Checks, "Rollouts v2 yazıcıları kapalı (rollouts.source v1/boş, argocd kapalı)")
		}
	}
	// R8 — çalışan evalset koşusu (yalnız ai_eval_runs düşecekse).
	for _, t := range p.Tables {
		if t.Table == "ai_eval_runs" && t.Action == statePathActRebuild {
			in.EvalChecked = true
			var lastMs int64
			// v0.10.965 — tablo erişilebilir bir host'ta bile YOKSA kendisi
			// okunamaz (UNKNOWN_TABLE): katı parça yoklaması (system.parts).
			if len(m.engines["ai_eval_runs"]) < len(m.hosts) {
				in.EvalByParts = true
				in.EvalErr = s.conn.QueryRow(ctx, statePathEvalPartsSQL(c), db).Scan(&in.EvalRunning, &lastMs)
			} else {
				in.EvalErr = s.conn.QueryRow(ctx, statePathEvalSQL(c, db)).Scan(&in.EvalRunning, &lastMs)
			}
			if in.EvalErr == nil {
				out.evalRead, out.evalLastWriteMs = true, lastMs
				if in.EvalRunning == 0 {
					if in.EvalByParts {
						p.Checks = append(p.Checks, "ai_eval_runs'a son 15 dk'da yazım yok (parça yoklaması; tablo bazı host'larda yok)")
					} else {
						p.Checks = append(p.Checks, "çalışan evalset koşusu yok (son 15 dk)")
					}
				}
			}
		}
	}
	p.Blocked = append(p.Blocked, statePathGate(in)...)

	// Kilit — çalıştırmadan sonra. v0.10.965 — kilit uyarısı plan uyarısına
	// YAZILMAZ: arayüz lockOpensAfter/stillLegacyAfter'dan kendi satırını ve
	// onay kutusunu çizer ("partialOK" bir istek alanıdır, operatör metni değil);
	// apply'daki ret (partialOK olmadan koşmaz) aynen durur.
	var still []string
	p.LockOpensAfter, _, still = statePathLockAfter(statePathObserved(m.reps, prefix), out.selected, prefix)
	p.StillLegacyAfter = still
	// Uyarılar (engel değil).
	if m.isLocalErr != nil {
		p.Warnings = append(p.Warnings, "is_local denetimi okunamadı: "+m.isLocalErr.Error())
	} else {
		zero, absent := blindHosts(m.hosts, m.isLocal)
		if len(zero) > 0 {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s: küme tanımında kendini is_local görmüyor — ON CLUSTER DDL bu host'ta UYGULANMAZ; düşürme doğrulaması orada zaman aşımına uğrar", strings.Join(zero, ", ")))
		}
		if len(absent) > 0 {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s: küme tanımı bu host'ta '%s' kümesini içermiyor — ON CLUSTER DDL burada UYGULANMAZ", strings.Join(absent, ", "), c))
		}
	}
	// v0.10.965 — "rollout/pod restart yapma" uyarısı plana YAZILMAZ: modalın
	// sabit satırı aynı uyarıyı nedeniyle birlikte taşır (iki kez görünüyordu).
	return out, nil
}

// ───────────────────────── 2.7 apply ─────────────────────────

// statePathSuccessNote — başarı notu (çalışan pod'lar boot gözlemini tutar).
const statePathSuccessNote = "Çalışan pod'lar boot anındaki yol gözlemini tutar: kilit her pod'un bir sonraki açılışında açılır (sonraki deploy ya da rolling restart). Doğrulama: boot log satırı '(N birleşik, 0 eski) — yeni state tabloları BİRLEŞİK yola kurulacak'. Rollouts kartındaki 0015 ön kontrolü artık çakışma göstermez."

// statePathPendingErr — kuyruğa alınmış (zaman aşımı) ifadenin kaydı.
const statePathPendingErr = "kuyrukta — durum yoklamasıyla doğrulanıyor"

// ApplyStatePathRebuild — v0.10.965 — DROP hepsi → her host'ta gitti mi
// (katı yoklama, birleşik znode'da replika kalmadı mı) → CREATE hepsi →
// katı yoklamayla doğrula → son kilit. İstemciye GÜVENİLMEZ: taze plan +
// onay kapısı; herhangi bir engel *StatePathGateError döner ve hiçbir ifade
// koşmaz. İlk hatada durur; tablo başına After/Verified ve devam metni.
func (s *Store) ApplyStatePathRebuild(ctx context.Context, req StatePathRebuildRequest) (*StatePathRebuildResult, error) {
	if !statePathRebuildBusy.CompareAndSwap(false, true) {
		return nil, &StatePathGateError{Blocked: []string{"başka bir yeniden kurulum bu pod'da sürüyor"}}
	}
	defer statePathRebuildBusy.Store(false)
	if bad := statePathSelectionError(req.Tables); bad != "" {
		return nil, &StatePathGateError{Blocked: []string{bad}}
	}
	pl, err := s.planStatePathRebuild(ctx, req.Cluster, req.Tables)
	if err != nil {
		return nil, &StatePathGateError{Blocked: []string{"ölçüm başarısız: " + err.Error()}}
	}
	plan := pl.plan
	blocked := append([]string{}, plan.Blocked...)
	if pl.measured {
		blocked = append(blocked, statePathAckAll(plan.Tables, req.Ack, time.Now())...)
		if msg := statePathEvalAckGate(pl.evalRead, pl.evalLastWriteMs, req.Ack); msg != "" {
			blocked = append(blocked, msg)
		}
		if !plan.LockOpensAfter && !req.PartialOK {
			blocked = append(blocked, fmt.Sprintf("seçim kilidi açmıyor (%s eski yolda kalır) — partialOK olmadan koşmaz", strings.Join(plan.StillLegacyAfter, ", ")))
		}
	}
	if len(blocked) > 0 {
		return nil, &StatePathGateError{Blocked: blocked}
	}

	m, prefix := pl.m, plan.ZKPrefix
	res := &StatePathRebuildResult{
		Tables:      append([]StatePathRebuildTable(nil), plan.Tables...),
		Statements:  []RollupStmtResult{},
		StillLegacy: []string{},
	}
	var rebuild, creates []string
	for _, t := range res.Tables {
		if t.Action == statePathActRebuild {
			rebuild = append(rebuild, t.Table)
		}
		if t.Action == statePathActRebuild || t.Action == statePathActCreate {
			creates = append(creates, t.Table)
		}
	}

	// 3. DROP — düz s.conn.Exec (dropRemovedTable emsali, eşzamanlı yol).
	res.Phase = "drop"
	for _, stmt := range plan.Drops {
		r, stop := statePathExec(ctx, s.conn, stmt)
		res.Statements = append(res.Statements, r)
		if stop {
			s.statePathFinish(ctx, m, prefix, res)
			return res, nil
		}
	}

	// 4. drop_wait — hepsi her host'tan gitti mi, birleşik znode'da replika kaldı mı?
	res.Phase = "drop_wait"
	if len(rebuild) > 0 {
		var pending map[string][]string
		ok := statePathPollLoop(ctx, statePathPoll.Every, statePathPoll.Drop, func() bool {
			live, lerr := s.statePathReadLive(ctx, m.cluster, m.db)
			if lerr != nil {
				pending = map[string][]string{rebuild[0]: {"ölçülemedi: " + lerr.Error()}}
				return false
			}
			pending = statePathDropPending(ctx, s.conn, live, rebuild, prefix)
			return len(pending) == 0
		})
		if !ok {
			for i := range res.Tables {
				res.Tables[i].pending = pending[res.Tables[i].Table]
			}
			s.statePathFinish(ctx, m, prefix, res)
			return res, nil
		}
	}

	// 5. CREATE — aynı zaman aşımı kuralı.
	res.Phase = "create"
	for i, stmt := range plan.Creates {
		r, stop := statePathExec(ctx, s.conn, stmt)
		res.Statements = append(res.Statements, r)
		if stop {
			s.statePathFinish(ctx, m, prefix, res)
			return res, nil
		}
		for j := range res.Tables {
			if res.Tables[j].Table == creates[i] {
				res.Tables[j].created = true
			}
		}
	}

	// 6. verify — katı yoklama.
	res.Phase = "verify"
	ok := statePathPollLoop(ctx, statePathPoll.Every, statePathPoll.Verify, func() bool {
		live, lerr := s.statePathReadLive(ctx, m.cluster, m.db)
		if lerr != nil {
			return false
		}
		for _, t := range creates {
			if !statePathVerified(t, m.hosts, live.engines[t], live.reps[t], prefix) {
				return false
			}
		}
		return true
	})
	if ok {
		res.Phase = "done"
	}
	s.statePathFinish(ctx, m, prefix, res)
	return res, nil
}

// statePathUnsentErr — v0.10.965 — süre bütçesi dolduğu için HİÇ gönderilmeyen ifadenin kaydı.
const statePathUnsentErr = "gönderilmedi — süre bütçesi doldu"

// statePathExec — tek ifade. Zaman aşımı (isCHTimeout / dağıtık DDL kuyrukta)
// başarısızlık DEĞİL: kaydedilir, yoklama doğrular. Başka hata → dur.
//
// v0.10.965 — YEREL bağlam süresi (12 dk bütçe) sunucu tarafı kuyruk DEĞİLDİR:
// sürücü ölü ctx'te bağlantı almadan context.DeadlineExceeded döner ve
// isCHTimeout onu da "kuyrukta" sayardı — hiç gönderilmemiş ifadeler kuyrukta
// görünür, CREATE'ler "kuruldu" işaretlenir, devam metni yanlış nedeni
// söylerdi. ctx denetimi isCHTimeout'tan ÖNCE: bütçe dolmuşsa gönderme, dur;
// ifade sırasında dolduysa gönderilmiş olabilir (kuyrukta) ama yine DUR.
func statePathExec(ctx context.Context, conn driver.Conn, stmt string) (RollupStmtResult, bool) {
	r := RollupStmtResult{Head: stmtHead(stmt)}
	if err := ctx.Err(); err != nil {
		r.Err = statePathUnsentErr + " (" + err.Error() + ")"
		return r, true
	}
	err := conn.Exec(ctx, stmt)
	switch {
	case err == nil:
		r.OK = true
	case ctx.Err() != nil:
		r.Err = statePathPendingErr + " (süre bütçesi doldu; gönderildiği kesin değil)"
		return r, true
	case isCHTimeout(err) || isDistributedDDLQueued(err):
		r.Err = statePathPendingErr
	default:
		r.Err = err.Error()
		return r, true
	}
	return r, false
}

// statePathPollLoop — 1 + bütçe/aralık deneme; ctx biterse durur.
func statePathPollLoop(ctx context.Context, every, budget time.Duration, check func() bool) bool {
	tries := 1
	if every > 0 {
		tries += int(budget / every)
	}
	for i := 0; i < tries; i++ {
		if i > 0 {
			t := time.NewTimer(every)
			select {
			case <-ctx.Done():
				t.Stop()
				return false
			case <-t.C:
			}
		}
		if check() {
			return true
		}
	}
	return false
}

// statePathDropPending — tablo → onu hâlâ tutan host'lar ve birleşik znode'da
// kalan replika adları. Boş harita = hepsi gitti.
func statePathDropPending(ctx context.Context, conn driver.Conn, live statePathLive, tables []string, prefix string) map[string][]string {
	out := map[string][]string{}
	for _, t := range tables {
		set := map[string]bool{}
		for h := range live.engines[t] {
			set[h] = true
		}
		for h := range live.reps[t] {
			set[h] = true
		}
		hosts := make([]string, 0, len(set))
		for h := range set {
			hosts = append(hosts, h)
		}
		sort.Strings(hosts)
		owners, zerr := zkChildren(ctx, conn, unifiedStatePath(prefix, t)+"/replicas")
		switch {
		case zerr != nil && !zkNoNode(zerr):
			hosts = append(hosts, "znode okunamadı: "+zerr.Error())
		case zerr == nil && len(owners) > 0:
			sort.Strings(owners)
			hosts = append(hosts, "birleşik znode'da replika: "+strings.Join(owners, ", "))
		}
		if len(hosts) > 0 {
			out[t] = hosts
		}
	}
	return out
}

// statePathFinish — son katı ölçüm: tablo başına After/Verified, son kilit,
// eski yolda kalanlar, OK, devam metni ve not.
func (s *Store) statePathFinish(ctx context.Context, m *statePathMeasure, prefix string, res *StatePathRebuildResult) {
	// v0.10.965 — son ölçüm TAZE bağlamda: bütçe dolduysa ölü ctx ile okuma
	// "son ölçüm okunamadı" der, After boş kalır ve devam metni yanlış nedeni
	// (bir pod eski yola kurdu) söylerdi. Sonuç gerçek bir ölçümden gelmeli.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	live, err := s.statePathReadLive(fctx, m.cluster, m.db)
	if err != nil {
		res.LockReason = "son ölçüm okunamadı: " + err.Error()
	} else {
		for i := range res.Tables {
			t := &res.Tables[i]
			t.After, _, _ = classifyStatePathTable(t.Table, m.hosts, live.engines[t.Table], live.reps[t.Table], prefix)
			if t.Action == statePathActRebuild || t.Action == statePathActCreate {
				t.Verified = statePathVerified(t.Table, m.hosts, live.engines[t.Table], live.reps[t.Table], prefix)
			}
		}
		paths := statePathObserved(live.reps, prefix)
		res.LockOpen, res.LockReason = useUnifiedStatePath(stateObservation{ok: true, paths: paths}, prefix, "")
		res.StillLegacy = statePathStillLegacy(paths, prefix)
	}
	res.OK = res.Phase == "done"
	for _, t := range res.Tables {
		if (t.Action == statePathActRebuild || t.Action == statePathActCreate) && !t.Verified {
			res.OK = false
		}
	}
	if res.OK {
		res.Note = statePathSuccessNote
		return
	}
	if res.Phase == "done" {
		res.Phase = "verify" // son ölçüm doğrulamayı bozduysa dürüst ol
	}
	res.Resume = statePathResumeHint(res.Phase, res.Tables)
	if ctx.Err() != nil { // v0.10.965 — kesilme nedeni açıkça
		res.Resume = "12 dk süre bütçesi doldu — " + res.Resume
	}
}

// statePathResumeHint — v0.10.965 — SAF: yarıda kalan çalıştırmanın devam
// metni. Devam güvenli: DROP IF EXISTS … SYNC ve CREATE IF NOT EXISTS
// eşgüçlü, sınıflandırma her istekte yeniden ölçülür.
func statePathResumeHint(phase string, tables []StatePathRebuildTable) string {
	switch phase {
	case "drop":
		return "DROP yarıda kaldı. Yeniden ölç → planla → çalıştır: düşürülmüş tablolar kurulur, kalan eski tablolar düşürülür."
	case "drop_wait":
		var parts []string
		for _, t := range tables {
			if len(t.pending) > 0 {
				parts = append(parts, fmt.Sprintf("%s (%s)", t.Table, strings.Join(t.pending, ", ")))
			}
		}
		return fmt.Sprintf("DROP şu host'larda henüz uygulanmadı: %s. CREATE koşmadı. Dağıtık DDL kuyruğu işleyince yeniden ölç ve çalıştır.", strings.Join(parts, "; "))
	case "create":
		n := 0
		for _, t := range tables {
			if t.created {
				n++
			}
		}
		return fmt.Sprintf("Kurma yarıda kaldı: %d tablo kuruldu. Yeniden çalıştır: birleşikler atlanır, eksikler kurulur.", n)
	case "verify":
		var names, now []string
		for _, t := range tables {
			if (t.Action == statePathActRebuild || t.Action == statePathActCreate) && !t.Verified {
				names = append(names, t.Table)
				now = append(now, statePathStateLabel(firstNonEmpty(t.After, "?"))) // v0.10.965 — modalın etiketiyle
			}
		}
		return fmt.Sprintf("%s doğrulanamadı (şimdi: %s). Eski yola yeniden kurulduysa bu arada bir pod açılmıştır — rollout/restart olmadığından emin ol, yeniden ölç ve çalıştır.", strings.Join(names, ", "), strings.Join(now, ", "))
	}
	return ""
}
