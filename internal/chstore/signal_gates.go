package chstore

// signal_gates.go — v0.10.1523 (Problem standardı R1): sinyal kapıları G1–G7 ve
// güven seviyesi. SAF; okuma yok.
//
// Güven YALNIZ ölçülmüş kapılardan çıkar. Model (LLM), 👍/👎 oyları ve
// operatörün "gerçek problem / problem değil" kararları skora GİRMEZ
// (operatör: oylar zayıf sinyal; D4: triage işaretleri değerlendirme etiketi
// değil). Bu dosya o tiplerin hiçbirini içe almaz — pin
// TestConfidenceIgnoresVotesAndVerdicts kaynağı tarar.
//
// Bilinmiyor ≠ başarısız: okunamayan / sağlanmayan gerçek kapıyı "unknown"
// yapar ve unknown güveni HİÇBİR ZAMAN düşürmez, bildirimi de tutmaz
// (TestUnknownGateNeverLowers). Güveni yalnız ÖLÇÜLMÜŞ bir başarısızlık
// düşürür. Neden: kapılar R1'de gölge kipte ölçülüyor; bir CH hıçkırığının
// gerçek bir P1'i "düşük güven" diye sustur(acak)ması en kötü sonuç.
//
// Kapılar:
//
//	G1 trafik     — öznenin 5 dk kovasında ≥ StdMinCalls5m çağrı (trafik düşüşünde
//	                taban ≥ StdMinBaselineCalls5m); exception grubunda ≥ 5 oluşum
//	G2 sürdürme   — dedektörün kendi sürdürme kuralı (dwell) — "pass" dedektör
//	                tasarımından gelir; svc-slowdown tek kova tasarımı "na"
//	G3 VM karar   — RED metriklerinde ölçü VictoriaMetrics'ten mi; VM bu tik
//	                beklemedeyse ölçülmüş başarısızlık; span = bilinmiyor
//	G4 topoloji   — sinyal bir ilişki iddia ediyorsa (küme, paylaşılan
//	                bağımlılık, incident) topology_edges_5m doğrulaması
//	G5 deploy     — YALNIZ bağlam; hiçbir zaman güveni/önceliği değiştirmez
//	G6 takvim     — özel gün / ay sonu RED sinyalinde güveni bir basamak
//	                indirir (high → medium), ASLA bastırmaz
//	G7 batch      — bağlam (dedektörler batch yükünü zaten kapılıyor)
//
// Seviye:
//
//	low    = G1, G2 ya da G3'te ölçülmüş başarısızlık, ya da iddia edilen
//	         ilişki G4'te başarısız
//	high   = G1–G3 geçti (ya da bilinmiyor / uygulanmaz), G4 geçti ya da
//	         uygulanmaz/bilinmiyor, kaynak örneklenmemiş (D12), büyüklük ≥
//	         bigBreach, takvim işareti yok, etki doğrulanmamış değil
//	medium = diğer her şey

import (
	"math"
)

// Kapı durumları.
const (
	GatePass    = "pass"
	GateFail    = "fail"
	GateUnknown = "unknown"
	GateNA      = "na"
	GateContext = "context"
	GateFlag    = "flag"
)

// Güven seviyeleri.
const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
)

// Kapı tabanları (D6 ile aynı sayılar: ≥30 çağrı / 5 dk, düşüşte ≥100 taban).
const (
	StdMinCalls5m         = 30
	StdMinBaselineCalls5m = 100
	StdMinOccurrences     = 5
)

// SignalGate — tek kapının sonucu.
type SignalGate struct {
	ID     string `json:"id"` // G1 … G7
	Name   string `json:"name"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
	// NoteMsg — v0.10.2299 (S2): Note'un metin anahtarı (ek alan; Note aynen).
	NoteMsg *StdMsg `json:"noteMsg,omitempty"`
}

// GateInput — kapıların girdisi (Standardize* doldurur).
type GateInput struct {
	Family    string
	Traffic   bool   // G1 bu türe uygulanır
	VMDecides bool   // G3 bu türe uygulanır
	Dwell     string // G2: pass | na | unknown | fail
	Source    string
	VMHold    bool

	Floor        StdFloorMeasures
	TrafficKnown bool

	Occurrences      int64
	OccurrencesKnown bool

	RelationClaimed  bool
	RelationVerified *bool

	RecentDeploy string
	CalendarKind string
	Batch        bool
}

// ComputeSignalGates — SAF: G1–G7, her zaman yedi satır, sırayla.
func ComputeSignalGates(in GateInput) []SignalGate {
	return []SignalGate{
		gateTraffic(in),
		gateSustained(in),
		gateVMDecides(in),
		gateTopology(in),
		gateDeploy(in),
		gateCalendar(in),
		gateBatch(in),
	}
}

func gateTraffic(in GateInput) SignalGate {
	g := SignalGate{ID: "G1", Name: "trafik", Status: GateNA}
	switch {
	case in.OccurrencesKnown:
		if in.Occurrences >= StdMinOccurrences {
			g.Status = GatePass
		} else {
			g.Status, g.Note = GateFail, "oluşum tabanın altında"
			g.NoteMsg = stdM("gate.occBelow")
		}
	case in.Traffic && in.Family == StdFamilyTraffic && in.Floor.BaselineCalls5m != nil:
		if *in.Floor.BaselineCalls5m >= StdMinBaselineCalls5m {
			g.Status = GatePass
		} else {
			g.Status, g.Note = GateFail, "referans trafik tabanın altında"
			g.NoteMsg = stdM("gate.baseBelow")
		}
	case in.Traffic && in.Floor.Calls5m != nil:
		if *in.Floor.Calls5m >= StdMinCalls5m {
			g.Status = GatePass
		} else {
			g.Status, g.Note = GateFail, "5 dk'da çağrı tabanın altında"
			g.NoteMsg = stdM("gate.callsBelow")
		}
	case in.Traffic:
		g.Status, g.Note = GateUnknown, "trafik okunmadı"
		g.NoteMsg = stdM("gate.trafficUnread")
	}
	return g
}

func gateSustained(in GateInput) SignalGate {
	g := SignalGate{ID: "G2", Name: "sürdürme", Status: in.Dwell}
	switch in.Dwell {
	case GatePass:
		g.Note = "dedektörün sürdürme kuralı"
		g.NoteMsg = stdM("gate.dwellPass")
	case GateNA:
		g.Note = "tasarım gereği tek ölçüm"
		g.NoteMsg = stdM("gate.dwellNA")
	case GateFail:
		g.Note = "sürdürme sağlanmadı"
		g.NoteMsg = stdM("gate.dwellFail")
	default:
		g.Status, g.Note = GateUnknown, "sürdürme bilinmiyor"
		g.NoteMsg = stdM("gate.dwellUnknown")
	}
	return g
}

func gateVMDecides(in GateInput) SignalGate {
	g := SignalGate{ID: "G3", Name: "VM karar", Status: GateNA}
	if !in.VMDecides {
		return g
	}
	switch {
	case in.Source == StdSourceVM && in.VMHold:
		g.Status, g.Note = GateFail, "VictoriaMetrics bu turda karar vermedi"
		g.NoteMsg = stdM("gate.vmHold")
	case in.Source == StdSourceVM:
		g.Status = GatePass
	default:
		g.Status, g.Note = GateUnknown, "ölçü span'den (VM kullanılmadı)"
		g.NoteMsg = stdM("gate.vmSpan")
	}
	return g
}

func gateTopology(in GateInput) SignalGate {
	g := SignalGate{ID: "G4", Name: "topoloji", Status: GateNA}
	if !in.RelationClaimed {
		return g
	}
	switch {
	case in.RelationVerified == nil:
		g.Status, g.Note = GateUnknown, "ilişki doğrulanamadı"
		g.NoteMsg = stdM("gate.relUnknown")
	case *in.RelationVerified:
		g.Status = GatePass
	default:
		g.Status, g.Note = GateFail, "iddia edilen ilişki topolojide yok"
		g.NoteMsg = stdM("gate.relFail")
	}
	return g
}

func gateDeploy(in GateInput) SignalGate {
	g := SignalGate{ID: "G5", Name: "deploy", Status: GateNA}
	if in.RecentDeploy != "" {
		g.Status, g.Note = GateContext, "yakın deploy: "+in.RecentDeploy
		g.NoteMsg = stdM("gate.deploy", "name", in.RecentDeploy)
	}
	return g
}

func gateCalendar(in GateInput) SignalGate {
	g := SignalGate{ID: "G6", Name: "takvim", Status: GateNA}
	if in.CalendarKind == "" {
		return g
	}
	switch in.Family {
	case StdFamilyTraffic, StdFamilyError, StdFamilyLatency:
		g.Status, g.Note = GateFlag, "özel gün: "+in.CalendarKind
		g.NoteMsg = stdM("gate.calendar", "calK", "cal."+in.CalendarKind)
	default:
		g.Status, g.Note = GateContext, "özel gün: "+in.CalendarKind
		g.NoteMsg = stdM("gate.calendar", "calK", "cal."+in.CalendarKind)
	}
	return g
}

func gateBatch(in GateInput) SignalGate {
	g := SignalGate{ID: "G7", Name: "batch", Status: GateNA}
	if in.Batch {
		g.Status, g.Note = GateContext, "batch servis"
		g.NoteMsg = stdM("gate.batch")
	}
	return g
}

// ConfidenceInput — kapı dışı güven girdileri (kaynak, büyüklük).
type ConfidenceInput struct {
	Sampled          bool
	Ratio            float64 // ihlal büyüklüğü (değer / normal); +Inf tam kayıp
	BigBreach        float64 // 0 = CurrentProblemPriority().BigBreachRatio
	ImpactUnverified bool    // runtime: kullanıcı etkisi doğrulanmadı → en çok medium
}

// ConfidenceFromGates — SAF: seviye (dosya başı kuralı).
func ConfidenceFromGates(gates []SignalGate, in ConfidenceInput) string {
	flag := false
	for _, g := range gates {
		switch g.ID {
		case "G1", "G2", "G3", "G4":
			if g.Status == GateFail {
				return ConfidenceLow
			}
		case "G6":
			if g.Status == GateFlag {
				flag = true
			}
		}
	}
	bb := in.BigBreach
	if bb <= 0 {
		bb = CurrentProblemPriority().BigBreachRatio
	}
	big := math.IsInf(in.Ratio, 1) || (in.Ratio > 0 && in.Ratio >= bb)
	if !in.Sampled && big && !flag && !in.ImpactUnverified {
		return ConfidenceHigh
	}
	return ConfidenceMedium
}

// ConfidenceRank — SAF: low 0 < medium 1 < high 2 (bilinmeyen = 1).
func ConfidenceRank(level string) int {
	switch level {
	case ConfidenceLow:
		return 0
	case ConfidenceHigh:
		return 2
	}
	return 1
}
