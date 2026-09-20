package chstore

// mv_target_repair_test.go — v0.10.835: hedef uuid uyuşmazlığının ONARIMI.
//
// Semptom (v0.10.833'te GERÇEK CH 24.8 üzerinde ölçüldü): MV gizli iç
// tablosunu ADLA değil `TO INNER UUID` ile çözer. Uyuşmazlığın İKİ gözlenebilir
// sonucu var ve ayrımı yalnız davranış verir — şekil-1'de düğüm-yerel okuma
// kod 60 alır (ingest düşer, spool büyür), şekil-2'de hedef BAŞKA ADLI var olan
// bir tabloya çözülür ve MV TOPLAR. Onarım YALNIZ şekil-1'e aittir; şekil-2'de
// aynı eylem CANLI veriyi siler.
//
// Bu dosyanın çividiği sözleşmeler (hepsi DAVRANIŞSAL, sahte bağlantı üstünde
// gerçek sıra koşar — [[feedback-tested-but-unreachable]]):
//
//   - uygunluk FAIL-CLOSED: `nil` (ölçülmedi) ASLA yeterli değil, `true`
//     (çözülüyor) KESİN ret, kapsaması `ok` olmayan satır RET (satır başına
//     tek yıkıcı eylem);
//   - "BOŞ" tek sorguyla kanıtlanmaz: motor ailesi + DETACHED parçalar +
//     nesnenin BAŞKA bir MV'nin hedefi olmaması da şart, ölçüm düşerse RET;
//   - DROP ön-denetlenebilir HER okumanın ARDINDA koşar (hazırlık önce);
//   - kanonik dal o adı HİÇ kullanmaz: ne ölçer ne düşürür, dolu ad onu
//     engellemez;
//   - doğrulama İKİ ŞARTLI ve 1. şart TERS YÖNDEN (uuid → ad) okunur.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2"
)

const (
	// wrongObjUUID — `.inner_id.<view uuid>` adını TAŞIYAN tablonun kendi
	// nesne uuid'si. MV bunu değil peerObjUUID'yi hedefler: şekil-1 budur.
	wrongObjUUID = "cccccccc-dddd-4eee-8fff-000000000001"
	// otherMVObjUUID — BAŞKA bir MV'nin (ölçülmüş) hedefi.
	otherMVObjUUID = "dddddddd-eeee-4fff-8aaa-000000000002"
	mvInnerEngine  = "ReplicatedAggregatingMergeTree"
)

// targetRow — şekil-1 hücresi (fixture'lar mv_inner_peer_test.go ile ORTAK:
// aynı view uuid'si, aynı nesne uuid'si, aynı ZK yolu — iki dosya ayrışırsa
// eşten kurulum merdiveni iki farklı gerçeği anlatırdı).
func targetRow() *MVHostState {
	no := false
	return &MVHostState{
		Host: "ch-03", Addr: "ch-03:9000", View: "db_summary_5m_local", State: MVStateOK,
		UUID: peerViewUUID, InnerUUID: wrongObjUUID, TargetUUID: peerObjUUID,
		Target: MVTargetMismatch, TargetResolves: &no,
		PeerHost: "ch-04", PeerAddr: "ch-04:9000",
	}
}

// measuredTargets — probe ÖLÇTÜ ve bu uuid'ler bir MV'nin hedefi. Harita
// unexported olduğu için küme yalnız probe'un ürettiği yoldan kurulabilir;
// test de aynı kapıdan geçer.
func measuredTargets(us ...string) MVTargetSet {
	set := MVTargetSet{Measured: true, uuids: map[innerObjectUUID]bool{}}
	for _, u := range us {
		set.uuids[innerObjectUUID(strings.ToLower(u))] = true
	}
	return set
}

// ── 1. uygunluk kapısı (SAF) ─────────────────────────────────────────

// TestMVTargetRepairEligible — ÜÇ EKSEN: ölçüm (nil/true/false) × şekil
// (target + durum + uuid) × guarded. Tek bir hücre uygundur ve o hücre
// KANITLANMIŞ şekil-1'dir; gerisi RET.
func TestMVTargetRepairEligible(t *testing.T) {
	yes, no := true, false
	base := func(mut func(*MVHostState)) *MVHostState {
		c := targetRow()
		mut(c)
		return c
	}
	cases := []struct {
		name    string
		cell    *MVHostState
		guarded bool
		ok      bool
		wantIn  string // ret metninde geçmeli (operatör sebebi görmeli)
	}{
		{name: "şekil-1: ölçüldü ve ÇÖZÜLMÜYOR → UYGUN", cell: targetRow(), ok: true},
		{
			name: "ÖLÇÜLMEDİ (nil) → RET: bilmiyoruz ile bozuk aynı kova değil",
			cell: base(func(c *MVHostState) { c.TargetResolves = nil }), wantIn: "ÖLÇÜLMEDİ",
		},
		{
			name: "şekil-2 (çözülüyor) → RET: MV topluyor, eylem canlı veriyi siler",
			cell: base(func(c *MVHostState) { c.TargetResolves = &yes }), wantIn: "ÇÖZÜYOR",
		},
		{
			name: "hedef kararı mismatch değil (sarkan) → RET: o satırın onarımı başka düğme",
			cell: base(func(c *MVHostState) {
				c.Target, c.State, c.TargetResolves = MVTargetUnmeasured, MVStateDangling, &no
			}),
			wantIn: "Yeniden kur",
		},
		{
			name:   "hedef kararı byname → RET (CH hedefi ADLA çözer, bulgu değil)",
			cell:   base(func(c *MVHostState) { c.Target = MVTargetByName }),
			wantIn: "mismatch",
		},
		// v0.10.835 incelemesi: `plain + mismatch` hücresi onarım tablosunda
		// ZATEN "Yeniden kur" taşıyor. İkinci bir yıkıcı düğme satır başına
		// tek eylem duruşunu bozardı ve hangisinin koştuğu belirsizleşirdi.
		{
			name:   "durum `plain` → RET: satırın yıkıcı eylemi üstteki tabloda",
			cell:   base(func(c *MVHostState) { c.State = MVStatePlain }),
			wantIn: "TEK yıkıcı eylem",
		},
		{
			name:   "hedef uuid'si geçersiz → RET: kurulacak nesnenin uuid'si TAHMİN EDİLEMEZ",
			cell:   base(func(c *MVHostState) { c.TargetUUID = "" }),
			wantIn: "TAHMİN EDİLEMEZ",
		},
		{
			name:   "hedef uuid'si sıfır uuid → RET (validUUID sözleşmesi)",
			cell:   base(func(c *MVHostState) { c.TargetUUID = zeroUUID }),
			wantIn: "TAHMİN EDİLEMEZ",
		},
		{name: "guarded MV → RET: bu kurulumda bilerek kurulmuyor", cell: targetRow(), guarded: true, wantIn: "bilerek kurulmuyor"},
		{name: "satır yok → RET", cell: nil, wantIn: "yeniden Ölç"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mvTargetRepairEligible(c.cell, c.guarded)
			if c.ok {
				if got != "" {
					t.Fatalf("uygun olmalıydı, ret: %s", got)
				}
				return
			}
			if got == "" {
				t.Fatal("RET bekleniyordu, kapı AÇIK geldi — fail-closed sözleşmesi kırık")
			}
			if c.wantIn != "" && !strings.Contains(got, c.wantIn) {
				t.Errorf("ret metni %q içermeli: %s", c.wantIn, got)
			}
		})
	}
	// Eksen çarpımı: guarded HER ölçümde reddeder, ölçüm HER şekilde karar
	// verir, durum `ok` DEĞİLSE hiçbir şekil uygun değildir.
	for _, probe := range []*bool{nil, &yes, &no} {
		for _, target := range []string{MVTargetOK, MVTargetMismatch, MVTargetByName, MVTargetUnmeasured} {
			for _, state := range []string{MVStateOK, MVStatePlain, MVStateDangling, MVStateMissing} {
				c := targetRow()
				c.TargetResolves, c.Target, c.State = probe, target, state
				eligible := mvTargetRepairEligible(c, false) == ""
				want := probe != nil && !*probe && target == MVTargetMismatch && state == MVStateOK
				if eligible != want {
					t.Errorf("ölçüm %v + hedef %q + durum %q: uygun %v, beklenen %v", probe, target, state, eligible, want)
				}
				if mvTargetRepairEligible(c, true) == "" {
					t.Errorf("guarded MV (ölçüm %v, hedef %q, durum %q) uygun sayıldı", probe, target, state)
				}
			}
		}
	}
}

// TestMVCoverageNeverEmitsGuardedRows — v0.10.835 incelemesi (KÜÇÜK): guarded
// kolu RepairMVTargetOnHost'tan ULAŞILAMAZ ve ASIL SÖZLEŞME budur. Kanonik
// eksen guarded MV'leri eler, yani o MV için hiç HÜCRE doğmaz ve çağıran daha
// önce "kapsama raporunda yok" der. Eleme kalkarsa (a) burası kırmızıya döner,
// (b) uygunluk kapısının guarded kolu ikinci katman olarak devreye girer.
func TestMVCoverageNeverEmitsGuardedRows(t *testing.T) {
	const guarded, open = "operation_group_summary_5m", "db_summary_5m"
	rows := []mvTableRow{combinedMVRow("ch-01", guarded), combinedMVRow("ch-01", open)}
	// mvCoverageReport kanonik listeyi guarded adları ELEYEREK kurar; testin
	// girdisi o sözleşmenin çıktısıdır.
	got := mvCoverageFromRows(rows, []string{open}, []string{"ch-01"}, true, map[string]int{"ch-01": 1})
	for _, c := range got {
		if c.View == guarded {
			t.Fatalf("guarded MV kapsama ekseninde satır üretti: %+v", c)
		}
	}
	// Elemenin GÖVDEDE durduğunu da çivile: kanonik listeyi kuran döngü.
	fn := funcBody(t, "mv_coverage.go", "func (s *Store) mvCoverageReport(")
	if !strings.Contains(fn, "s.mvGuardedOff(n)") {
		t.Error("mvCoverageReport guarded MV'leri kanonik eksenden ELEMELİ — eleme kalkarsa guarded hücreler yıkıcı düğme kapısına girer")
	}
}

// ── 2. adı tutan tablo kapısı (SAF) ──────────────────────────────────

// TestMVTargetOccupancyGate — "boş" TEK BİR SORGUYLA kanıtlanmaz (v0.10.835
// incelemesi, KRİTİK): `system.parts` DETACHED parçaları göstermez ve parça
// tutmayan bir motor orada hiç satır üretmez; ikisinde de aggregate 0 döner.
// Üstüne, boş bir tablo BAŞKA bir MV'nin canlı hedefi olabilir.
func TestMVTargetOccupancyGate(t *testing.T) {
	const inner = ".inner_id." + peerViewUUID
	full := innerNameState{Exists: true, Engine: mvInnerEngine, UUID: wrongObjUUID}
	with := func(mut func(*innerNameState)) innerNameState {
		st := full
		mut(&st)
		return st
	}
	targets := measuredTargets(peerObjUUID, otherMVObjUUID)
	cases := []struct {
		name                          string
		needsName, dropEmpty, allowed bool
		st                            innerNameState
		targets                       MVTargetSet
		wantIn                        string
	}{
		{name: "ad boşta → geç", needsName: true, st: innerNameState{}, targets: targets, allowed: true},
		{name: "boş + onay → geç", needsName: true, dropEmpty: true, st: full, targets: targets, allowed: true},
		{name: "boş ama onay YOK → RET", needsName: true, st: full, targets: targets, wantIn: "AYRI bir onaydır"},
		{
			name: "DOLU → RET", needsName: true, dropEmpty: true, targets: targets,
			st: with(func(s *innerNameState) { s.Rows = 42 }), wantIn: "42 satır",
		},
		// (a) DETACHED parçalar system.parts'ta YOKTUR: aktif satır 0 olsa da
		// tabloda veri vardır ve DROP `detached/` dizinini de götürür.
		{
			name: "DETACHED parça var → RET (aktif satır 0 olsa bile)", needsName: true, dropEmpty: true, targets: targets,
			st: with(func(s *innerNameState) { s.Detached = 3 }), wantIn: "3 DETACHED parça",
		},
		// (b) parça tutmayan motor: "0 satır" doluluk kanıtı DEĞİLDİR.
		{
			name: "motor MergeTree ailesi değil → RET", needsName: true, dropEmpty: true, targets: targets,
			st: with(func(s *innerNameState) { s.Engine = "Memory" }), wantIn: "parça (part) üretmez",
		},
		{
			name: "motor Log → RET", needsName: true, dropEmpty: true, targets: targets,
			st: with(func(s *innerNameState) { s.Engine = "StripeLog" }), wantIn: "StripeLog",
		},
		// Boş ama BAŞKA bir MV'nin ölçülmüş hedefi → o MV'nin toplaması.
		{
			name: "nesne uuid'si BAŞKA bir MV'nin hedefi → RET", needsName: true, dropEmpty: true, targets: targets,
			st: with(func(s *innerNameState) { s.UUID = otherMVObjUUID }), wantIn: "BİR MV'NİN HEDEFİ",
		},
		{
			name: "hedef kümesi ÖLÇÜLMEDİ → RET (fail-closed)", needsName: true, dropEmpty: true,
			st: full, targets: MVTargetSet{}, wantIn: "ÖLÇÜLEMEDİ",
		},
		{
			name: "adı tutan tablonun KENDİ uuid'si okunamadı → RET", needsName: true, dropEmpty: true, targets: targets,
			st: with(func(s *innerNameState) { s.UUID = "" }), wantIn: "kanıtlanamıyor",
		},
		// KANONİK DAL: o adı HİÇ kullanmaz (yeni view uuid'si yeni ad doğurur).
		// Kapı orada hiç çalışmaz — dolu bir ad onarımı ENGELLEMEZ.
		{
			name: "kanonik dal + DOLU ad → geç (o ad hiç kullanılmaz)", needsName: false, targets: targets,
			st: with(func(s *innerNameState) { s.Rows = 1e6; s.Detached = 5 }), allowed: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mvTargetOccupancyGate(inner, c.needsName, c.st, c.dropEmpty, c.targets)
			if c.allowed {
				if got != "" {
					t.Fatalf("geçmeliydi, ret: %s", got)
				}
				return
			}
			if got == "" {
				t.Fatal("RET bekleniyordu — yanlış ölçülmüş bir 'boş' DROP'u geri dönülmez")
			}
			if !strings.Contains(got, c.wantIn) {
				t.Errorf("ret metni %q içermeli: %s", c.wantIn, got)
			}
			if !strings.Contains(got, inner) && !strings.Contains(got, "kümesi") {
				t.Errorf("ret metni hangi tabloyu konuştuğunu SÖYLEMELİ: %s", got)
			}
		})
	}
	// Onay bir NİYET, doluluk bir OLGU: dropEmpty hiçbir olgu kolunu açmaz.
	for _, st := range []innerNameState{
		with(func(s *innerNameState) { s.Rows = 1 }),
		with(func(s *innerNameState) { s.Detached = 1 }),
		with(func(s *innerNameState) { s.Engine = "Memory" }),
		with(func(s *innerNameState) { s.UUID = otherMVObjUUID }),
	} {
		if mvTargetOccupancyGate(inner, true, st, true, targets) == "" {
			t.Errorf("dropEmpty bir OLGU kapısını açtı: %+v", st)
		}
	}
}

// ── 3. onarım merdiveni (DAVRANIŞ) ───────────────────────────────────

// localTargetScript — eşten dalının YEREL betiği; sıra GERÇEK sıradır:
// ölç(3) → hazırla(1) → uygula(2) → doğrula(3).
func localTargetScript(exists bool, rows, detached uint64) []scriptStep {
	steps := []scriptStep{measureStep(exists, wrongObjUUID)}
	if exists {
		steps = append(steps,
			scriptStep{match: "toUInt64(sum(rows)) FROM system.parts", vals: []any{rows}},
			scriptStep{match: "FROM system.detached_parts", vals: []any{detached}},
		)
	}
	return append(steps,
		// prepareInnerFromPeer: yerel MV'nin hedefi (SALT OKUMA, DROP'tan ÖNCE)
		scriptStep{match: "SELECT create_table_query", vals: []any{localDDL(peerObjUUID)}},
		// applyInnerFromPeer: kurulan satırın uuid'si + ZK yolu
		scriptStep{match: "SELECT toString(uuid) FROM system.tables", vals: []any{peerObjUUID}},
		scriptStep{match: "zookeeper_path", vals: []any{peerZK}},
		// verifyMVTargetRepair: hedef uuid + view uuid + TERS YÖN (uuid → ad)
		scriptStep{match: "SELECT create_table_query", vals: []any{localDDL(peerObjUUID)}},
		scriptStep{match: "SELECT toString(uuid) FROM system.tables", vals: []any{peerViewUUID}},
		scriptStep{match: "SELECT any(name) FROM system.tables", vals: []any{innerTableName(peerViewUUID)}},
	)
}

// measureStep — varlık + motor + nesne uuid'si TEK aggregate'te.
func measureStep(exists bool, uuid string) scriptStep {
	n, u, e := uint64(0), "", ""
	if exists {
		n, u, e = 1, uuid, mvInnerEngine
	}
	return scriptStep{match: "any(toString(uuid)), any(engine)", vals: []any{n, u, e}}
}

func peerTargetScript() []scriptStep {
	return []scriptStep{
		{match: "SELECT toString(uuid), create_table_query", vals: []any{peerObjUUID, peerInnerDDL(peerObjUUID)}},
		{match: "zookeeper_path", vals: []any{peerZK}},
	}
}

func okTargets() MVTargetSet { return measuredTargets(peerObjUUID) }

func TestRepairMVTargetOnBehaviour(t *testing.T) {
	inner := innerTableName(peerViewUUID)
	run := func(local, peer *scriptConn, dropEmpty bool, row *MVHostState, targets MVTargetSet) ([]string, error) {
		var pc *scriptConn
		if peer != nil {
			pc = peer
		}
		if pc == nil {
			return (&Store{}).repairMVTargetOn(context.Background(), local, nil, row, dropEmpty, targets)
		}
		return (&Store{}).repairMVTargetOn(context.Background(), local, pc, row, dropEmpty, targets)
	}

	t.Run("mutlu yol: boş ad düşer, iç tablo MV'nin NESNE uuid'siyle doğar, iki şart da tutar", func(t *testing.T) {
		local := &scriptConn{name: "yerel", steps: localTargetScript(true, 0, 0)}
		peer := &scriptConn{name: "eş", steps: peerTargetScript()}
		steps, err := run(local, peer, true, targetRow(), okTargets())
		if err != nil {
			t.Fatalf("mutlu yol düştü: %v (%v)", err, steps)
		}
		if len(local.execs) != 3 {
			t.Fatalf("üç ifade beklenir (DROP · CREATE · yoklama): %v", local.execs)
		}
		drop, create, probe := local.execs[0], local.execs[1], local.execs[2]
		if !strings.HasPrefix(drop, "DROP TABLE IF EXISTS `"+inner+"` SYNC") {
			t.Errorf("önce BOŞ ad düşmeli: %s", drop)
		}
		// v0.10.835 incelemesi (KRİTİK): purgeGuard `max_table_size_to_drop`
		// sınırını KALDIRIR. Gerçekten boş bir tablo o eşiğe zaten takılmaz;
		// eşik SADECE ölçümümüz yanlışken ısırır — yani son fren.
		if strings.Contains(drop, "max_table_size_to_drop") {
			t.Errorf("bu DROP purgeGuard TAŞIMAMALI: ölçüm yanlışken CH'nin reddi tam olarak istediğimiz şey: %s", drop)
		}
		if !strings.Contains(create, "`"+inner+"` UUID '"+peerObjUUID+"'") {
			t.Errorf("ad VIEW uuid'sinden, nesne uuid'si MV'nin hedefinden olmalı: %s", create)
		}
		if strings.Contains(create, "UUID '"+peerViewUUID+"'") {
			t.Errorf("ADIN uuid'si nesne uuid'si olarak gömülmüş (v0.10.780 hatası): %s", create)
		}
		for _, st := range local.execs {
			if strings.Contains(st, "ON CLUSTER") {
				t.Errorf("yalnız bu host: %s", st)
			}
		}
		if !strings.Contains(probe, "LIMIT 0") || !strings.Contains(probe, "db_summary_5m_local") {
			t.Errorf("2. şart DAVRANIŞI ölçmeli (`SELECT 1 FROM <view> LIMIT 0`): %s", probe)
		}
		if len(peer.execs) != 0 {
			t.Errorf("eşe DDL koşulmamalı: %v", peer.execs)
		}
		joined := strings.Join(steps, "\n")
		if !strings.Contains(joined, "İKİ ŞART") {
			t.Errorf("doğrulama adımı iki şartı da SÖYLEMELİ: %v", steps)
		}
	})

	// v0.10.835 incelemesi (ÖNEMLİ): eşten kurulumun ilk üç işi SAF OKUMAdır
	// ve eskiden DROP onlardan ÖNCE koşuyordu. Okuma düştüğünde sonuç
	// `[DROP …]`tan ibaret kalıyordu: ad boş, tablo yok, MV hâlâ hedefini
	// çözemiyor. Üç ret yolu da AYNI şeyi ister: hiçbir DDL koşmamış olmalı.
	t.Run("hazırlık düşerse DROP KOŞMAZ", func(t *testing.T) {
		prep := []struct {
			name  string
			local []scriptStep
			peer  []scriptStep
			want  string
		}{
			{
				name:  "yerel hedef uuid okuması düştü",
				local: append(localTargetScript(true, 0, 0)[:3], scriptStep{match: "SELECT create_table_query", err: errors.New("read: i/o timeout")}),
				peer:  peerTargetScript(),
				want:  "nesne uuid'si okunamadı",
			},
			{
				name:  "eşin DDL okuması düştü",
				local: localTargetScript(true, 0, 0),
				peer:  []scriptStep{{match: "SELECT toString(uuid), create_table_query", err: errors.New("dial tcp: connection refused")}},
				want:  "eş replikadan DDL",
			},
			{
				name:  "eşin nesne uuid'si YEREL MV'nin hedefiyle TUTMUYOR",
				local: localTargetScript(true, 0, 0),
				peer:  []scriptStep{{match: "SELECT toString(uuid), create_table_query", vals: []any{otherMVObjUUID, peerInnerDDL(otherMVObjUUID)}}},
				want:  "BULAMAZ",
			},
		}
		for _, c := range prep {
			t.Run(c.name, func(t *testing.T) {
				local := &scriptConn{name: "yerel", steps: c.local}
				peer := &scriptConn{name: "eş", steps: c.peer}
				_, err := run(local, peer, true, targetRow(), okTargets())
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Fatalf("hazırlık reddi bekleniyordu (%q): %v", c.want, err)
				}
				if len(local.execs) != 0 {
					t.Fatalf("hazırlık düşerken DROP koşmuş — ad boşaldı, tablo kurulmadı: %v", local.execs)
				}
			})
		}
	})

	t.Run("ad DOLU → RET, hiçbir DDL koşmaz", func(t *testing.T) {
		local := &scriptConn{name: "yerel", steps: localTargetScript(true, 17, 0)}
		peer := &scriptConn{name: "eş", steps: peerTargetScript()}
		_, err := run(local, peer, true, targetRow(), okTargets())
		if err == nil {
			t.Fatal("dolu bir tablonun üstüne onarım koşmamalı")
		}
		if !strings.Contains(err.Error(), "17 satır") {
			t.Errorf("ret ÖLÇÜLEN satır sayısını söylemeli: %v", err)
		}
		if len(local.execs) != 0 || len(peer.execs) != 0 {
			t.Fatalf("hiçbir DDL koşmamalıydı: %v / %v", local.execs, peer.execs)
		}
	})

	t.Run("DETACHED parça var → RET, hiçbir DDL koşmaz", func(t *testing.T) {
		local := &scriptConn{name: "yerel", steps: localTargetScript(true, 0, 4)}
		_, err := run(local, &scriptConn{name: "eş", steps: peerTargetScript()}, true, targetRow(), okTargets())
		if err == nil || !strings.Contains(err.Error(), "4 DETACHED parça") {
			t.Fatalf("detached parça DROP'u engellemeli (system.parts onları göstermez): %v", err)
		}
		if len(local.execs) != 0 {
			t.Fatalf("hiçbir DDL koşmamalıydı: %v", local.execs)
		}
	})

	t.Run("ad boş ama onay YOK → RET, hiçbir DDL koşmaz", func(t *testing.T) {
		local := &scriptConn{name: "yerel", steps: localTargetScript(true, 0, 0)}
		_, err := run(local, &scriptConn{name: "eş"}, false, targetRow(), okTargets())
		if err == nil || !strings.Contains(err.Error(), "AYRI bir onaydır") {
			t.Fatalf("DROP ayrı onay ister: %v", err)
		}
		if len(local.execs) != 0 {
			t.Fatalf("hiçbir DDL koşmamalıydı: %v", local.execs)
		}
	})

	t.Run("satır sayısı ÖLÇÜLEMEDİ → RET (sessiz 0 kapıyı kandırırdı)", func(t *testing.T) {
		local := &scriptConn{name: "yerel", steps: []scriptStep{
			measureStep(true, wrongObjUUID),
			{match: "toUInt64(sum(rows)) FROM system.parts", err: errors.New("read: i/o timeout")},
		}}
		_, err := run(local, &scriptConn{name: "eş"}, true, targetRow(), okTargets())
		if err == nil || !strings.Contains(err.Error(), "ÖLÇMEDEN") {
			t.Fatalf("ölçülemeyen bir sayım 0 sayılmamalı: %v", err)
		}
		if len(local.execs) != 0 {
			t.Fatalf("hiçbir DDL koşmamalıydı: %v", local.execs)
		}
	})

	t.Run("detached okuması ÖLÇÜLEMEDİ → RET", func(t *testing.T) {
		local := &scriptConn{name: "yerel", steps: []scriptStep{
			measureStep(true, wrongObjUUID),
			{match: "toUInt64(sum(rows)) FROM system.parts", vals: []any{uint64(0)}},
			{match: "FROM system.detached_parts", err: errors.New("read: i/o timeout")},
		}}
		_, err := run(local, &scriptConn{name: "eş"}, true, targetRow(), okTargets())
		if err == nil || !strings.Contains(err.Error(), "ÖLÇMEDEN") {
			t.Fatalf("detached okuması düşerse de reddedilir: %v", err)
		}
		if len(local.execs) != 0 {
			t.Fatalf("hiçbir DDL koşmamalıydı: %v", local.execs)
		}
	})

	t.Run("ad BOŞTA (tablo yok) → DROP koşmaz, doğrudan kurulur", func(t *testing.T) {
		local := &scriptConn{name: "yerel", steps: localTargetScript(false, 0, 0)}
		peer := &scriptConn{name: "eş", steps: peerTargetScript()}
		if _, err := run(local, peer, false, targetRow(), okTargets()); err != nil {
			t.Fatalf("ad boştayken onay gerekmez: %v", err)
		}
		if len(local.execs) != 2 {
			t.Fatalf("iki ifade beklenir (CREATE · yoklama): %v", local.execs)
		}
		if strings.Contains(local.execs[0], "DROP") {
			t.Errorf("var olmayan bir tablo düşürülmez: %s", local.execs[0])
		}
	})

	// ── doğrulamanın İKİ ŞARTI ───────────────────────────────────────
	// 1. şart TERS YÖNDEN okunur (uuid → ad): merdiven kurduğu tabloyu ADIYLA
	// doğruluyor, aynı yönü tekrar okumak kendi iddiasının tekrarı olurdu.

	t.Run("1. şart tutmaz (hedef HİÇBİR tabloya çözülmüyor) → YARIM", func(t *testing.T) {
		steps := localTargetScript(true, 0, 0)
		steps[len(steps)-1] = scriptStep{match: "SELECT any(name) FROM system.tables", vals: []any{""}}
		local := &scriptConn{name: "yerel", steps: steps}
		_, err := run(local, &scriptConn{name: "eş", steps: peerTargetScript()}, true, targetRow(), okTargets())
		if err == nil {
			t.Fatal("hedefi çözülmeyen bir kurulum YEŞİL sayılamaz")
		}
		if !strings.Contains(err.Error(), "YARIM") || !strings.Contains(err.Error(), "HİÇBİR tabloya") {
			t.Errorf("YARIM + şekil-1 sürüyor denmeli: %v", err)
		}
	})

	t.Run("1. şart tutmaz (hedef BAŞKA ADA çözülüyor) → YARIM, şekil-2 uyarısı", func(t *testing.T) {
		steps := localTargetScript(true, 0, 0)
		steps[len(steps)-1] = scriptStep{match: "SELECT any(name) FROM system.tables", vals: []any{".inner_id." + wrongObjUUID}}
		local := &scriptConn{name: "yerel", steps: steps}
		_, err := run(local, &scriptConn{name: "eş", steps: peerTargetScript()}, true, targetRow(), okTargets())
		if err == nil || !strings.Contains(err.Error(), "YARIM") || !strings.Contains(err.Error(), "şekil-2") {
			t.Fatalf("başka ada çözülen hedef YARIM + şekil-2 uyarısı vermeli: %v", err)
		}
	})

	t.Run("2. şart tutmaz (düğüm-yerel okuma kod 60) → YARIM, 1. şart tutsa bile", func(t *testing.T) {
		local := &scriptConn{name: "yerel", steps: localTargetScript(true, 0, 0), execRules: []scriptExec{
			{match: "LIMIT 0", err: &clickhouse.Exception{Code: 60, Message: "Table doesn't exist"}},
		}}
		_, err := run(local, &scriptConn{name: "eş", steps: peerTargetScript()}, true, targetRow(), okTargets())
		if err == nil {
			t.Fatal("metadata tutuyor diye MV'nin hedefini ÇÖZDÜĞÜ varsayılamaz — 2. şart ölçülmeli")
		}
		if !strings.Contains(err.Error(), "YARIM") || !strings.Contains(err.Error(), "2. şart") {
			t.Errorf("sonuç YARIM ve hangi şartın tutmadığı yazmalı: %v", err)
		}
	})

	t.Run("doğrulama okuması düşerse de YARIM (yeşil denmez)", func(t *testing.T) {
		steps := localTargetScript(true, 0, 0)
		steps[len(steps)-2] = scriptStep{match: "SELECT toString(uuid) FROM system.tables", err: errors.New("read: connection reset")}
		local := &scriptConn{name: "yerel", steps: steps}
		_, err := run(local, &scriptConn{name: "eş", steps: peerTargetScript()}, true, targetRow(), okTargets())
		if err == nil || !strings.Contains(err.Error(), "YARIM") {
			t.Fatalf("doğrulanamayan bir kurulum yeşil sayılamaz: %v", err)
		}
	})

	// YARIM bir onarımın KOŞAN adımları hata yolunda da DÖNER: audit + ekran
	// için tek kanıt odur (v0.10.835 incelemesi).
	t.Run("YARIM sonuç KOŞAN adımları taşır", func(t *testing.T) {
		local := &scriptConn{name: "yerel", steps: localTargetScript(true, 0, 0), execRules: []scriptExec{
			{match: "LIMIT 0", err: &clickhouse.Exception{Code: 60}},
		}}
		steps, err := run(local, &scriptConn{name: "eş", steps: peerTargetScript()}, true, targetRow(), okTargets())
		if err == nil {
			t.Fatal("YARIM bekleniyordu")
		}
		joined := strings.Join(steps, "\n")
		for _, want := range []string{"DROP TABLE IF EXISTS", "CREATE TABLE"} {
			if !strings.Contains(joined, want) {
				t.Errorf("hata yolunda %q adımı kaybolmuş — operatörün tek kanıtı bu: %v", want, steps)
			}
		}
	})

	t.Run("view uuid'si sıfır (Ordinary DB) → RET, ölçüm bile koşmaz", func(t *testing.T) {
		row := targetRow()
		row.UUID = zeroUUID
		local := &scriptConn{name: "yerel"}
		if _, err := run(local, &scriptConn{name: "eş"}, true, row, okTargets()); err == nil {
			t.Fatal("Ordinary DB'de iç tablo `.inner.<ad>`'dır — bu onarım orada tanımsız")
		}
		if len(local.queries) != 0 || len(local.execs) != 0 {
			t.Fatalf("hiçbir okuma/DDL koşmamalıydı: %v / %v", local.queries, local.execs)
		}
	})
}

// TestRepairMVTargetCanonicalBranchIgnoresOldName — v0.10.835 incelemesi
// (ÖNEMLİ): kanonik dal view'ı yeniden kurar, view YENİ bir uuid alır ve iç
// tablonun adı ondan doğar — yani `.inner_id.<ESKİ view uuid>` o dalda HİÇ
// kullanılmaz. Eski kod o adı ölçüp düşürüyordu ("kod 57 alırız" gerekçesiyle)
// ve ad DOLU olduğunda ingest'i düşmüş host'u onarımsız bırakıyordu.
func TestRepairMVTargetCanonicalBranchIgnoresOldName(t *testing.T) {
	row := targetRow()
	row.PeerHost, row.PeerAddr = "", ""
	const newViewUUID = "99999999-8888-4777-8666-555555555555"
	const newObjUUID = "77777777-6666-4555-8444-333333333333"
	cq := "CREATE MATERIALIZED VIEW coremetry." + row.View + " UUID '" + newViewUUID +
		"' TO INNER UUID '" + newObjUUID + "' (`x` Int) ENGINE = AggregatingMergeTree AS SELECT 1"
	local := &scriptConn{name: "yerel", steps: []scriptStep{
		// verifyMVRebuild: view doğdu mu + iç tablosu var mı (tek düğüm)
		{match: "toString(uuid), substring(create_table_query", vals: []any{newViewUUID, cq}},
		{match: "SELECT engine FROM system.tables", vals: []any{"AggregatingMergeTree"}},
		// verifyMVTargetRepair: hedef uuid + view uuid + TERS YÖN
		{match: "SELECT create_table_query", vals: []any{cq}},
		{match: "SELECT toString(uuid) FROM system.tables", vals: []any{newViewUUID}},
		{match: "SELECT any(name) FROM system.tables", vals: []any{innerTableName(newViewUUID)}},
	}}
	// Ad DOLU ve DETACHED parçalı olsa bile kanonik dal ETKİLENMEZ: o adı hiç
	// kullanmaz. Kapı çalışsaydı bu çağrı reddedilirdi.
	steps, err := (&Store{}).repairMVTargetOn(context.Background(), local, nil, row, false, okTargets())
	if err != nil {
		t.Fatalf("kanonik dal düştü: %v (%v)", err, steps)
	}
	for _, q := range local.queries {
		if strings.Contains(q, "system.parts") || strings.Contains(q, "system.detached_parts") {
			t.Errorf("kanonik dal eski adı ÖLÇMEMELİ (o adı hiç kullanmaz): %s", q)
		}
	}
	for _, st := range local.execs {
		if strings.Contains(st, "DROP TABLE IF EXISTS `"+innerTableName(peerViewUUID)+"`") {
			t.Errorf("kanonik dal eski adı DÜŞÜRMEMELİ — öksüz olarak v0.10.830'un kartına düşer: %s", st)
		}
		if strings.Contains(st, "ON CLUSTER") {
			t.Errorf("yalnız bu host: %s", st)
		}
	}
	if len(local.execs) < 2 {
		t.Fatalf("kanonik dal DROP view + CREATE koşmalı: %v", local.execs)
	}
	if !strings.HasPrefix(local.execs[0], "DROP TABLE IF EXISTS `"+row.View+"` SYNC") {
		t.Errorf("ilk ifade view'ın kendisini düşürmeli: %s", local.execs[0])
	}
}

// ── 4. kapılar ÇAĞRILDIĞI YERDE ──────────────────────────────────────

// TestRepairMVTargetOnHostGatesBeforeDDL — saf çekirdek yeşil olup çağrıldığı
// yer pinlenmemişse düzeltme kendini sessizce iptal eder (v0.10.832 dersi).
// Host yolu canlı küme I/O'su ister; ölçülebilen şey SIRA'dır: taze ölçüm ve
// uygunluk kapısı onarım çağrısından ÖNCE.
func TestRepairMVTargetOnHostGatesBeforeDDL(t *testing.T) {
	// YORUMLAR SÖKÜLÜR: muhafız KENDİ gerekçe metnini ısırır — "purgeGuard
	// BİLEREK YOK" diyen bir yorum, "purgeGuard geçmesin" kapısını kırmızı
	// yapıyordu ([[feedback-gate-matches-its-own-text]]).
	fn := stripGoComments(funcBody(t, "mv_target_repair.go", "func (s *Store) RepairMVTargetOnHost("))
	for _, want := range []string{
		"s.mvCoverageReport(ctx, false)",        // kapaklı süpürme ATLANIR
		"s.mvHardenCell(ctx, row, rep.Cluster)", // kararı ÖLÇ, varsayma
		"mvTargetRepairEligible(row, guarded)",  // fail-closed kapı
		"s.mvGuardedOff(base)",                  // guarded MV düğme almaz
		`row.Addr == ""`,                        // adres yoksa düğüm-yerel iş YOK
		"rep.Targets",                           // öksüz kararı için hedef kümesi taşınır
	} {
		if !strings.Contains(fn, want) {
			t.Errorf("RepairMVTargetOnHost içinde eksik: %s", want)
		}
	}
	if strings.Contains(fn, "s.MVCoverage(ctx)") {
		t.Error("eylem yolu 126 hücrelik süpürmeyi koşmamalı (v0.10.833 D)")
	}
	repair := strings.Index(fn, "s.repairMVTargetOn(")
	if repair < 0 {
		t.Fatal("onarım çağrısı bulunamadı — gövde adı değişmiş, kapı KÖR")
	}
	for _, gate := range []string{"s.mvHardenCell(", "mvTargetRepairEligible("} {
		if i := strings.Index(fn, gate); i < 0 || i > repair {
			t.Errorf("%s onarım çağrısından ÖNCE olmalı", gate)
		}
	}
	// Merdiven TEK GÖVDE ve YIKICI ADIM HAZIRLIĞIN ARDINDA.
	body := stripGoComments(funcBody(t, "mv_target_repair.go", "func (s *Store) repairMVTargetOn("))
	if !strings.Contains(body, "s.prepareInnerFromPeer(") || !strings.Contains(body, "s.applyInnerFromPeer(") {
		t.Error("eşten kurulum v0.10.832'nin gövdesini ÇAĞIRMALI (hazırla + uygula) — ikinci merdiven ayrışır")
	}
	if strings.Contains(body, "injectTableUUID(") {
		t.Error("uuid gömme merdivenin İÇİNDE kalmalı (prepareInnerFromPeer); burada ikinci kopya doğuyor")
	}
	prep, drop := strings.Index(body, "s.prepareInnerFromPeer("), strings.Index(body, `"DROP TABLE IF EXISTS`)
	if prep < 0 || drop < 0 || prep > drop {
		t.Error("HAZIRLIK yıkıcı adımdan ÖNCE olmalı — hazırlık düşerse ad boşalmış ve tablo kurulmamış kalır")
	}
	if strings.Contains(body, "purgeGuard") {
		t.Error("bu DROP purgeGuard TAŞIMAMALI: max_table_size_to_drop yanlış ölçümün SON FRENİ (v0.10.835)")
	}
}
