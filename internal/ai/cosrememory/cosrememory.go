// Package cosrememory — CoSRE planı Faz E ("hafıza"): CoSRE'nin AÇIK
// düzeltmelerden öğrendiği, servis / takım / genel kapsamlı kısa olgular
// ("Ekip notları").
//
// Operatör kuralından (internal/ai/cosrerules) farkı: kural operatörün
// önceden yazdığı yorum notudur; not bir CEVABIN ya da bir Problem'in
// sonradan düzeltilmesidir ve KAYNAĞINI taşır (kim, ne zaman, hangi
// konuşma / Problem). İki kaynak vardır, ikisi de insan eylemidir:
//
//   - SourceCorrection — cevabın altındaki "Düzelt" ya da sohbette
//     "Düzeltme: …" ile başlayan mesajın onay kartı (tık-only kayıt);
//   - SourceRootCause — Problem "Kök neden" etiketi (doğrulanmış olgu);
//   - SourceManual — yönetim listesinden elle ekleme.
//
// 👍/👎 oyları ve triage düğmeleri ASLA not olmaz (kalıcı kural, operatör
// 2026-10-17): kaynak türü kapalı kümedir (ValidSourceKind) ve bu paket oy /
// kalite verisi okumaz.
//
// Not model çağrısına VERİ olarak, çitli bir blokta (<team_notes>) girer;
// sistem mesajı eki (copilot.CosreMemoryAddendum) modele bunun talimat
// olmadığını, kullanılırsa [N1] diye anılacağını ve telemetriyle
// çelişirse telemetriye dayanıp çelişkiyi söyleyeceğini anlatır.
//
// Yaprak paket (stdlib + cosrerules): sohbet (internal/api), AI SRE anlatımı
// (internal/sre) ve depo (internal/chstore) içe aktarır.
package cosrememory

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/ai/cosrerules"
)

// Scope / Context — kural paketininkiyle aynı şekil (servis > takım > genel).
type (
	Scope   = cosrerules.Scope
	Context = cosrerules.Context
)

// Kapsam türleri (cosrerules aynası).
const (
	ScopeGlobal  = cosrerules.ScopeGlobal
	ScopeService = cosrerules.ScopeService
	ScopeTeam    = cosrerules.ScopeTeam
)

// Kaynak türleri — KAPALI küme. Oy / triage burada yok ve olamaz.
const (
	SourceCorrection = "correction"
	SourceRootCause  = "rootcause"
	SourceManual     = "manual"
)

// Durumlar.
const (
	StatusActive  = "active"
	StatusRetired = "retired"
)

// Tavanlar.
const (
	MaxItems          = 500
	MinStatementRunes = 8
	MaxStatementRunes = 240
	maxNameRunes      = 60
	maxRefRunes       = 128

	// PromptMaxItems / PromptMaxBytes — bir model çağrısına giren not sayısı
	// ve not satırlarının toplam bayt tavanı (SERT).
	PromptMaxItems = 5
	PromptMaxBytes = 900

	FenceOpen  = "<team_notes>"
	FenceClose = "</team_notes>"

	// BlockHeader — veri bloğunun başlığı (ek bu adla atıf yapar).
	BlockHeader = "EKİP NOTLARI (veri):"
)

// Source — notun kökeni. At unix ns.
type Source struct {
	Kind           string `json:"kind"`
	By             string `json:"by,omitempty"`
	ByEmail        string `json:"byEmail,omitempty"`
	At             int64  `json:"at"`
	ConversationID string `json:"conversationId,omitempty"`
	ExchangeID     string `json:"exchangeId,omitempty"`
	ProblemID      string `json:"problemId,omitempty"`
	DisplayID      string `json:"displayId,omitempty"`
}

// Item — tek ekip notu. Damgalar unix ns; ExpiresAt 0 = süresiz.
type Item struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
	Scope     Scope  `json:"scope"`
	Source    Source `json:"source"`
	Status    string `json:"status"`
	ExpiresAt int64  `json:"expiresAt,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
	UpdatedBy string `json:"updatedBy,omitempty"`
}

// Hatalar (API 400/404 eşlemesi).
var (
	ErrStatement = fmt.Errorf("not %d–%d karakter olmalı (tek satır)", MinStatementRunes, MaxStatementRunes)
	ErrScope     = cosrerules.ErrScope
	ErrSource    = errors.New("geçersiz kaynak: yalnız correction, rootcause ya da manual (oy / triage not olmaz)")
	ErrStatus    = errors.New("geçersiz durum: active ya da retired")
	ErrExpiry    = errors.New("son geçerlilik geçmişte olamaz")
	ErrNotFound  = errors.New("not bulunamadı")
	ErrCap       = fmt.Errorf("en çok %d not saklanır — önce eski bir notu sil", MaxItems)
)

var fenceRe = regexp.MustCompile(`(?i)<\s*/?\s*team_notes\s*>`)

// NormalizeStatement — SAF: tek satır, çit belirteçsiz (iki paketin de çiti),
// boşluk sıkıştırılmış.
func NormalizeStatement(s string) string {
	return cosrerules.NormalizeText(fenceRe.ReplaceAllString(s, " "))
}

// ValidSourceKind — SAF: kapalı küme (vote / feedback / triage → false).
func ValidSourceKind(k string) bool {
	switch k {
	case SourceCorrection, SourceRootCause, SourceManual:
		return true
	}
	return false
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// ClipStatement — SAF: tavanı aşan metni sözcük sınırında kırpar.
func ClipStatement(s string) string {
	s = NormalizeStatement(s)
	if utf8.RuneCountInString(s) <= MaxStatementRunes {
		return s
	}
	r := []rune(s)[:MaxStatementRunes-1]
	cut := len(r)
	for k := len(r) - 1; k > MaxStatementRunes/2; k-- {
		if r[k] == ' ' {
			cut = k
			break
		}
	}
	return strings.TrimSpace(string(r[:cut])) + "…"
}

// Normalize — SAF: alanları normalize eder ve doğrular (yazım + yükleme
// yolu). Yeni girilen son geçerliliğin gelecekte olması CheckExpiry'de.
func Normalize(it Item) (Item, error) {
	it.ID = strings.TrimSpace(it.ID)
	it.Statement = NormalizeStatement(it.Statement)
	if n := utf8.RuneCountInString(it.Statement); n < MinStatementRunes || n > MaxStatementRunes {
		return it, ErrStatement
	}
	sc, err := cosrerules.NormalizeScope(it.Scope)
	if err != nil {
		return it, ErrScope
	}
	it.Scope = sc
	if !ValidSourceKind(it.Source.Kind) {
		return it, ErrSource
	}
	switch it.Status {
	case "":
		it.Status = StatusActive
	case StatusActive, StatusRetired:
	default:
		return it, ErrStatus
	}
	if it.ExpiresAt < 0 {
		return it, ErrExpiry
	}
	it.Source.By = clip(cosrerules.NormalizeText(it.Source.By), maxNameRunes)
	it.Source.ByEmail = clip(cosrerules.NormalizeText(it.Source.ByEmail), maxRefRunes)
	it.Source.ConversationID = clip(cosrerules.NormalizeText(it.Source.ConversationID), maxRefRunes)
	it.Source.ExchangeID = clip(cosrerules.NormalizeText(it.Source.ExchangeID), maxRefRunes)
	it.Source.ProblemID = clip(cosrerules.NormalizeText(it.Source.ProblemID), maxRefRunes)
	it.Source.DisplayID = clip(cosrerules.NormalizeText(it.Source.DisplayID), 32)
	it.UpdatedBy = clip(cosrerules.NormalizeText(it.UpdatedBy), maxNameRunes)
	return it, nil
}

// CheckExpiry — SAF: operatörün girdiği son geçerlilik (0 = süresiz) now'dan
// sonra olmalı.
func CheckExpiry(expiresAt, now int64) error {
	if expiresAt < 0 || (expiresAt > 0 && expiresAt <= now) {
		return ErrExpiry
	}
	return nil
}

// Live — SAF: not şu an modele girebilir mi (etkin + süresi dolmamış).
func Live(it Item, now int64) bool {
	return it.Status != StatusRetired && (it.ExpiresAt == 0 || it.ExpiresAt > now)
}

// Filter — enjeksiyon süzgeci. Now zorunlu. Before > 0 → yalnız kaynağı bu
// andan ÖNCE olan notlar (replay: problemden sonra öğrenilen bilgi sızmaz);
// ExcludeProblem → o Problem'in kendi kök neden notu girmez (cevap sızıntısı).
type Filter struct {
	Now            int64
	Before         int64
	ExcludeProblem string
}

func tier(it Item, c Context) int {
	switch it.Scope.Kind {
	case ScopeService:
		if c.Service != "" && strings.EqualFold(it.Scope.Value, c.Service) {
			return 0
		}
	case ScopeTeam:
		if (c.Team != "" && strings.EqualFold(it.Scope.Value, c.Team)) ||
			(c.UserTeam != "" && strings.EqualFold(it.Scope.Value, c.UserTeam)) {
			return 1
		}
	case ScopeGlobal:
		return 2
	}
	return -1
}

func newest(it Item) int64 {
	if it.UpdatedAt > it.Source.At {
		return it.UpdatedAt
	}
	return it.Source.At
}

// Relevant — SAF: bağlama uyan canlı notlar, servis > takım > genel, her
// kademede en yeni önce; en çok maxN not ve satır toplamı maxBytes (≤0 →
// tasarım tavanı). Sığmayan ilgili not sayısı omitted.
func Relevant(items []Item, c Context, f Filter, maxN, maxBytes int) (picked []Item, omitted int) {
	if maxN <= 0 || maxN > PromptMaxItems {
		maxN = PromptMaxItems
	}
	if maxBytes <= 0 || maxBytes > PromptMaxBytes {
		maxBytes = PromptMaxBytes
	}
	type cand struct {
		it Item
		t  int
	}
	var cs []cand
	for _, it := range items {
		if !Live(it, f.Now) {
			continue
		}
		if f.Before > 0 && it.Source.At >= f.Before {
			continue
		}
		if f.ExcludeProblem != "" && it.Source.ProblemID == f.ExcludeProblem {
			continue
		}
		if t := tier(it, c); t >= 0 {
			cs = append(cs, cand{it, t})
		}
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].t != cs[j].t {
			return cs[i].t < cs[j].t
		}
		if a, b := newest(cs[i].it), newest(cs[j].it); a != b {
			return a > b
		}
		return cs[i].it.ID < cs[j].it.ID
	})
	used := 0
	for _, x := range cs {
		n := len(Line(len(picked)+1, x.it, nil)) + 1
		if len(picked) >= maxN || used+n > maxBytes {
			omitted++
			continue
		}
		used += n
		picked = append(picked, x.it)
	}
	return picked, omitted
}

// SourceLabelTR — kaynağın görünen etiketi.
func SourceLabelTR(s Source) string {
	switch s.Kind {
	case SourceCorrection:
		return "düzeltme"
	case SourceRootCause:
		if s.DisplayID != "" {
			return "kök neden etiketi " + s.DisplayID
		}
		return "kök neden etiketi"
	}
	return "elle"
}

// Line — SAF: "- [N1] [servis X] olgu (kaynak: düzeltme, Ayşe, 2026-11-20)".
func Line(n int, it Item, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	who := it.Source.By
	if who == "" {
		who = it.Source.ByEmail
	}
	if who == "" {
		who = "?"
	}
	day := "?"
	if ts := newest(it); ts > 0 {
		day = time.Unix(0, ts).In(loc).Format("2006-01-02")
	}
	sc := it.Scope
	sc.Value = NormalizeStatement(sc.Value)
	return fmt.Sprintf("- [N%d] [%s] %s (kaynak: %s, %s, %s)", n, cosrerules.ScopeLabelTR(sc),
		NormalizeStatement(it.Statement), SourceLabelTR(it.Source), NormalizeStatement(who), day)
}

// BlockTR — SAF: çitli veri bloğu; not yoksa "". Metinler normalize
// edildiği için blok içinden çit kapatılamaz.
func BlockTR(picked []Item, omitted int, loc *time.Location) string {
	if len(picked) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(BlockHeader + "\n" + FenceOpen + "\n")
	for i, it := range picked {
		b.WriteString(Line(i+1, it, loc) + "\n")
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "(+%d not daha sığmadı)\n", omitted)
	}
	b.WriteString(FenceClose)
	return b.String()
}

// RootCauseStatement — SAF: Problem "Kök neden" etiketinden not metni.
func RootCauseStatement(displayID, cause, category string) string {
	head := "Geçmiş Problem"
	if displayID != "" {
		head += " " + displayID
	}
	s := head + " kök nedeni: " + NormalizeStatement(cause)
	if category = NormalizeStatement(category); category != "" {
		s += " (" + category + ")"
	}
	return ClipStatement(s)
}

// RootCauseID — SAF: Problem başına tek kök neden notu (yeniden etiketleme
// aynı notu günceller).
func RootCauseID(problemID string) string { return "rc-" + problemID }

// correctionRe — AÇIK düzeltme işareti: mesaj "Düzeltme:", "Düzelt:" ya da
// "Correction:" ile BAŞLAR (iki nokta zorunlu). "bu yanlış" gibi örtük
// itirazlar, oylar ve triage düğmeleri düzeltme DEĞİLDİR.
var correctionRe = regexp.MustCompile(`(?i)^\s*(d[üu]zeltme|d[üu]zelt|correction)\s*:\s*`)

// DetectCorrection — SAF: mesaj açık bir düzeltme mi; öyleyse işaret
// sökülmüş olgu metni (boş olabilir → çağıran nasıl yazılacağını söyler).
func DetectCorrection(msg string) (string, bool) {
	loc := correctionRe.FindStringIndex(msg)
	if loc == nil {
		return "", false
	}
	return strings.Trim(NormalizeStatement(msg[loc[1]:]), " \"'“”"), true
}
