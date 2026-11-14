package chstore

// problem_owner.go — v0.10.2155 (Davis tarzı tek Problem D1, spec
// docs/plans/spec-davis-style-problem-merge.md, operatör onayı 2026-11-14):
// "servis başına TEK açık Problem; önce açan sahip, sonra gelen üye" kuralının
// (v0.10.2130, ML ailesi) ML ailesi DIŞINA taşınan SAF çekirdeği. Eski
// dedektörler emekli edilmez; aynı serviste açık bir sahip varken yeni satır
// açmak yerine o Problem'in ÜYESİ olurlar.
//
// AİLE SINIFI kural kimliğinden (önek tip sistemidir, /aiops §2):
//
//	sahip olabilen  svc-slowdown:<svc> · anomaly:<svc>:error_rate ·
//	                anomaly-auto:svc:<svc> · anomaly-auto:group:<olay>
//	yalnız üye      anomaly:<svc>:p99_ms|request_rate · anomaly-auto:<fp>
//	                (eski terfi) · anomaly-auto:subject:…
//	ayrı satır      kalan her şey: alarm kuralı, dış kaynak (anomaly:ext…),
//	                db-*, self-*, monitor:, runtime, anomaly-cluster:,
//	                exception-storm, heap bandı
//
// "Sahip olabilen" = operatörün inbox'ta kaynak önceliğini KORUDUĞU aileler
// (problem_priority.inboxKeepSourcePriority). Bu yüzden iki koşul birlikte:
// statik aile VE etkin istisna listesine uyma (OwnerPolicy.Keep). Liste dışı
// bir aile sahip olsaydı satır inbox'ta P3'e zorlanır ve üyesi olan P1
// (ör. yaygın yavaşlama) görünmez kalırdı. `anomaly-auto:subject:` listede
// OLMADIĞI için yalnız üyedir (spec tablosundaki "sahip/üye" okuması bu
// yüzden alınmadı — DECISIONS 2026-11-14).
//
// ÖNCELİK YALNIZ YÜKSELİR: üye sahibi ancak üyenin TEK BAŞINA önceliği
// sahibinkinden yüksekse değiştirir; o zaman şiddet + ölçü (Metric / Value /
// Threshold / Comparator) BİRLİKTE üyeninki olur (çapraz çarpım yok: sahibin
// şiddeti × üyenin oranı diye bir öncelik uydurulmaz). Kimlik, kural, başlık,
// açıklama, başlangıç ve operatör alanları sahibin kalır. Yalnız-üye aileler
// (operatörün P3'e indirdikleri) sahibi hiç yükseltmez.
//
// "PROBLEM DEĞİL": imzası (p:<kural>|<servis>) "problem değil" olan satır ne
// sahip ne üye — bugünkü gibi kendi satırında kalır (susturma korunur; ML
// Problem'i susturulmuş bir satırın altına gizlenmez).

import (
	"sort"
	"strings"
)

// MergeClass — bir kural ailesinin birleşmedeki rolü.
type MergeClass uint8

const (
	MergeSeparate   MergeClass = iota // ayrı satır: birleşmeye katılmaz
	MergeMemberOnly                   // yalnız üye olabilir
	MergeOwner                        // sahip olabilir (sahip varsa üye olur)
)

// anomalyServiceMetric — SAF: "anomaly:<svc>:<metrik>" kimliğinin metriği;
// dış kaynak serileri ("…:ext:<metrik>", anomaly:ext…) ve biçimsiz kimlik "".
func anomalyServiceMetric(ruleID string) string {
	rest, ok := strings.CutPrefix(ruleID, "anomaly:")
	if !ok {
		return ""
	}
	i := strings.LastIndex(rest, ":")
	if i <= 0 || i == len(rest)-1 {
		return ""
	}
	body := rest[:i]
	if body == "ext" || strings.HasPrefix(body, "ext:") || strings.HasPrefix(body, "ext-") || strings.HasSuffix(body, ":ext") {
		return ""
	}
	return rest[i+1:]
}

// ProblemMergeClass — SAF: kural kimliği → aile sınıfı (dosya başı tablo).
func ProblemMergeClass(ruleID string) MergeClass {
	switch {
	case IsMLLiveProblem(ruleID):
		return MergeOwner
	case strings.HasPrefix(ruleID, RuleSvcSlowdownPrefix):
		if len(ruleID) > len(RuleSvcSlowdownPrefix) {
			return MergeOwner
		}
	case strings.HasPrefix(ruleID, PromotedAnomalyRulePrefix):
		if len(ruleID) > len(PromotedAnomalyRulePrefix) {
			return MergeMemberOnly
		}
	default:
		switch anomalyServiceMetric(ruleID) {
		case "error_rate":
			return MergeOwner
		case "p99_ms", "request_rate":
			return MergeMemberOnly
		}
	}
	return MergeSeparate
}

// OwnerEligibleRule — SAF: aile sahip olabilir mi (statik; etkin istisna
// listesi OwnerPolicy'de).
func OwnerEligibleRule(ruleID string) bool { return ProblemMergeClass(ruleID) == MergeOwner }

// MemberEligibleRule — SAF: aile birleşmeye katılır mı (sahip ya da üye).
func MemberEligibleRule(ruleID string) bool { return ProblemMergeClass(ruleID) != MergeSeparate }

// OwnerPolicy — bir tikin birleşme girdileri (çağıran bir kez kurar).
type OwnerPolicy struct {
	// Keep — etkin inbox istisna listesi (InboxKeepSourcePriorityList). nil =
	// bilinmiyor → yalnız statik aile; boş liste = kimse sahip değil.
	Keep []string
	// Noise — "problem değil" imzaları (NoiseVerdictSignatures).
	Noise map[string]struct{}
	// Priority / NowNs — öncelik kıyası (computePriority ile AYNI girdi).
	Priority ProblemPriorityConfig
	NowNs    int64
}

func (pol OwnerPolicy) muted(ruleID, service string) bool {
	_, ok := pol.Noise[RuleProblemVerdictSignature(ruleID, service)]
	return ok
}

// OwnerRule — SAF: (kural, servis) sahip olabilir / sahibi yükseltebilir mi.
func (pol OwnerPolicy) OwnerRule(ruleID, service string) bool {
	if service == "" || ProblemMergeClass(ruleID) != MergeOwner || pol.muted(ruleID, service) {
		return false
	}
	if pol.Keep == nil {
		return true
	}
	_, ok := MatchInboxKeepSourcePriority(pol.Keep, ruleID)
	return ok
}

// MemberRule — SAF: (kural, servis) açık bir sahibe ÜYE olabilir mi.
func (pol OwnerPolicy) MemberRule(ruleID, service string) bool {
	return service != "" && ProblemMergeClass(ruleID) != MergeSeparate && !pol.muted(ruleID, service)
}

// mergeable — SAF: satır birleşmeye girebilecek açık bir servis satırı mı.
func mergeable(p *Problem) bool {
	return p != nil && p.ID != "" && p.Service != "" && p.Status != "resolved" &&
		ProblemSubjectKind(p.Kind) == ProblemKindService
}

// CanOwn — SAF: açık satır servisinin sahibi olabilir mi.
func (pol OwnerPolicy) CanOwn(p *Problem) bool {
	return mergeable(p) && pol.OwnerRule(p.RuleID, p.Service)
}

// olderOwner — SAF: sahiplik sırası (StartedAt, eşitse ID).
func olderOwner(a, b *Problem) bool {
	if a.StartedAt != b.StartedAt {
		return a.StartedAt < b.StartedAt
	}
	return a.ID < b.ID
}

// ServiceOwnerOf — SAF: servisin sahibi = en eski açık, sahip olabilen satır
// (KOPYA; yoksa nil).
func ServiceOwnerOf(open []*Problem, svc string, pol OwnerPolicy) *Problem {
	if svc == "" {
		return nil
	}
	var best *Problem
	for _, p := range open {
		if p == nil || p.Service != svc || !pol.CanOwn(p) {
			continue
		}
		if best == nil || olderOwner(p, best) {
			best = p
		}
	}
	if best == nil {
		return nil
	}
	q := *best
	return &q
}

// ServiceOwner — anlık görüntüden servisin sahibi (nil alıcı güvenli; KOPYA).
func (o *OpenProblems) ServiceOwner(svc string, pol OwnerPolicy) *Problem {
	if o == nil {
		return nil
	}
	return ServiceOwnerOf(o.all, svc, pol)
}

// JoinRole — yeni satır açacak bir yazıcının kararı.
type JoinRole uint8

const (
	JoinOpen   JoinRole = iota // bugünkü gibi kendi satırını açar (sahip ya da ayrı)
	JoinMember                 // açık sahibe üye olur: yeni satır / bildirim yok
)

// DecideJoin — SAF: (kural, servis) şimdi açılacak olsa ne olur. Sahip yoksa
// ya da aile birleşmiyorsa / susturulmuşsa JoinOpen (yalnız-üye aile de sahip
// yokken bugünkü satırını açar). Kuralın kendi satırı sahipse de JoinOpen
// (o bir tazelemedir).
func DecideJoin(open []*Problem, ruleID, service string, pol OwnerPolicy) (JoinRole, *Problem) {
	if !pol.MemberRule(ruleID, service) {
		return JoinOpen, nil
	}
	owner := ServiceOwnerOf(open, service, pol)
	if owner == nil || owner.RuleID == ruleID {
		return JoinOpen, nil
	}
	return JoinMember, owner
}

var mergePrioRank = map[string]int{"P1": 3, "P2": 2, "P3": 1}

func (pol OwnerPolicy) prio(p Problem) string {
	s, _ := computePriority(p, pol.NowNs, pol.Priority)
	return s
}

// RaiseOwner — SAF: üye sahibi yükseltir mi. Yalnız sahip olabilen (ve
// istisna listesindeki) aile, yalnız TEK BAŞINA önceliği sahibinkinden
// yüksekse: şiddet + ölçü birlikte üyeninki olur. ok=false → satır aynen.
func RaiseOwner(owner, member Problem, pol OwnerPolicy) (Problem, bool) {
	if !pol.OwnerRule(member.RuleID, member.Service) {
		return owner, false
	}
	if mergePrioRank[pol.prio(member)] <= mergePrioRank[pol.prio(owner)] {
		return owner, false
	}
	out := owner
	out.Severity, out.Metric, out.Value, out.Threshold, out.Comparator =
		member.Severity, member.Metric, member.Value, member.Threshold, member.Comparator
	if mergePrioRank[pol.prio(out)] <= mergePrioRank[pol.prio(owner)] {
		return owner, false // sahibin kuralına özgü merdiven yükseltmiyorsa dokunma
	}
	return out, true
}

// EscalatedToP1 — SAF: satır bu yazımla P1'e Mİ çıktı (aynı an, aynı ayar:
// yaş kuralı iki tarafa eşit uygulanır, fark yalnız şiddet / ölçüden).
func EscalatedToP1(before, after Problem, pol OwnerPolicy) bool {
	return pol.prio(before) != "P1" && pol.prio(after) == "P1"
}

// MergedIntoNote — kapanan ikizin açıklama eki (küme katılımıyla aynı desen,
// anomaly/clustering.go mergeIntoCluster).
func MergedIntoNote(ownerID string) string { return " · merged into " + ownerID }

// ServiceMerge — bir servisin birleşme planı.
type ServiceMerge struct {
	Owner Problem // sahibin (gerekirse yükseltilmiş / atananı taşınmış) tam satırı
	// OwnerChanged — sahip satırı değişti (yazılmalı); değilse yazım yok.
	OwnerChanged bool
	// OwnerBefore — sahibin plan öncesi hali (yükseliş bildirimi kıyası).
	OwnerBefore Problem
	Merged      []Problem // kapanmış KOPYALAR ("merged into" ekli), ID sıralı
}

// MergeInto — SAF: genç satırı sahibe katar. Dönen sahip gerekirse
// yükseltilmiştir ve atananı boşsa gencinkini alır; dönen kapalı kopya
// Value'ya dokunmaz (MarkResolved), ek idempotent.
func MergeInto(owner, young Problem, pol OwnerPolicy) (Problem, bool, Problem) {
	out, changed := RaiseOwner(owner, young, pol)
	if out.Assignee == "" && young.Assignee != "" {
		out.Assignee, changed = young.Assignee, true
	}
	closed := young
	if note := MergedIntoNote(owner.ID); !strings.Contains(closed.Description, note) {
		closed.Description = strings.TrimRight(closed.Description, " ") + note
	}
	MarkResolved(&closed, pol.NowNs)
	return out, changed, closed
}

// PlanServiceMerges — SAF: açık anlık görüntüden birleşme planı. Sahibi olan
// her serviste, sahip DIŞINDAKİ her birleşen (sahip olabilen ikiz ya da
// yalnız-üye) açık satır sahibe katılır. Anlık görüntüden türediği için
// idempotent: kapanan satır bir sonraki görüntüde yoktur. Servis sıralı.
func PlanServiceMerges(open []*Problem, pol OwnerPolicy) []ServiceMerge {
	bySvc := map[string][]*Problem{}
	for _, p := range open {
		if mergeable(p) && pol.MemberRule(p.RuleID, p.Service) {
			bySvc[p.Service] = append(bySvc[p.Service], p)
		}
	}
	svcs := make([]string, 0, len(bySvc))
	for s, rows := range bySvc {
		if len(rows) > 1 {
			svcs = append(svcs, s)
		}
	}
	sort.Strings(svcs)
	var out []ServiceMerge
	for _, s := range svcs {
		rows := bySvc[s]
		owner := ServiceOwnerOf(rows, s, pol)
		if owner == nil {
			continue
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
		m := ServiceMerge{Owner: *owner, OwnerBefore: *owner}
		for _, p := range rows {
			if p.ID == owner.ID {
				continue
			}
			next, changed, closed := MergeInto(m.Owner, *p, pol)
			m.Owner = next
			m.OwnerChanged = m.OwnerChanged || changed
			m.Merged = append(m.Merged, closed)
		}
		if len(m.Merged) > 0 {
			out = append(out, m)
		}
	}
	return out
}
