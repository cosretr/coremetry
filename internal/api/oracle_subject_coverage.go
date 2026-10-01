package api

// oracle_subject_coverage.go — v0.10.999 — Oracle kaynağının ÖZNE KAPSAMI
// (Oracle odak "2": trace'i Coremetry'de olmayan satırlar servissiz kalıyordu).
//
//	GET /api/settings/oracle/{id}/subject-coverage?hours=24   admin
//
// "Hata satırlarının ne kadarı gerçek bir servise bağlanıyor, bağlanmayan
// neden bağlanmıyor?" Salt okuma, üç parça: oracle_error_log'dan operasyon
// başına satır dökümü (chstore.OracleOpCoverage — canlı Oracle'a gidilmez),
// öğrenilmiş op→servis haritası (system_settings blobu) ve son 24 saatte canlı
// servis adları. Sınıflama saf: oracle.BuildCoverage.
//
// v0.10.1000 — fonksiyon kodu basamağı da ölçülür: (operasyon, kod) çiftleri
// (chstore.OracleOpCodes) + o kodları taşıyan span'lerin servis dağılımı
// (chstore.FunctionCodeServices — geniş rollup → terfi kolonu). Kaynakta
// eşleme kapalıyken de koşar: rapor "açılırsa şu kadar satır bağlanır" der.
// Bu iki okuma düşerse rapor yine döner (fnError dolu, fnChecked=false).
//
// Pencere 1 | 6 | 24 saat (varsayılan 24); serveCached 60 sn, anahtar kaynak
// kimliği + pencere. Canlı servis listesi okunamazsa rapor yine döner
// (aliveKnown=false; pod basamağı doğrulanamaz).

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/oracle"
)

func init() {
	registerRoutesExtra("oracle-subject-coverage", (*Server).registerOracleSubjectCoverageRoutes)
}

func (s *Server) registerOracleSubjectCoverageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/oracle/{id}/subject-coverage", auth.RequireRole(auth.RoleAdmin, s.getOracleSubjectCoverage))
}

const (
	oracleCoverageOps        = 200 // sınıflanan en çok operasyon (en çok satırlı önce)
	oracleCoverageUnresolved = 20  // cevapta listelenen çözülmeyen operasyon
	oracleCoverageAliveSince = 24 * time.Hour
)

// oracleCoverageHours — SAF: ?hours → 1 | 6 | 24 (bilinmeyen / boş → 24).
func oracleCoverageHours(raw string) int {
	switch n, _ := strconv.Atoi(strings.TrimSpace(raw)); n {
	case 1, 6:
		return n
	}
	return 24
}

type oracleSubjectCoverage struct {
	SourceID   string `json:"sourceId"`
	SourceName string `json:"sourceName"`
	Hours      int    `json:"hours"`
	oracle.CoverageReport
	// FnError — fonksiyon kodu ölçümü okunamadı (rapor geri kalanıyla döner).
	FnError        string `json:"fnError,omitempty"`
	LearnedEntries int    `json:"learnedEntries"`
	GeneratedAt    int64  `json:"generatedAt"` // ms
}

func (s *Server) getOracleSubjectCoverage(w http.ResponseWriter, r *http.Request) {
	if s.oracle == nil || s.store == nil {
		http.Error(w, "oracle not available", http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	name, fnOn := "", false
	for _, c := range s.oracle.CurrentSettings().Sources {
		if c.ID == id {
			name, fnOn = c.Name, c.FunctionCodeMatch
			break
		}
	}
	if id == "" || name == "" {
		writeJSONError(w, http.StatusNotFound, "oracle kaynağı bulunamadı (önce kaydedin): "+id)
		return
	}
	hours := oracleCoverageHours(r.URL.Query().Get("hours"))
	key := fmt.Sprintf("oracle-subject-coverage:id=%s:h=%d:fn=%t", id, hours, fnOn)
	s.serveCached(w, r, key, 60*time.Second, func(ctx context.Context) (any, error) {
		now := time.Now()
		from := now.Add(-time.Duration(hours) * time.Hour)
		obs, totals, err := s.store.OracleOpCoverage(ctx, id, from, now, oracleCoverageOps)
		if err != nil {
			return nil, err
		}
		fn, fnErr := s.oracleCoverageFn(ctx, id, fnOn, from, now)
		if fnErr != "" {
			log.Printf("[oracle] özne kapsamı %s: fonksiyon kodu ölçümü: %s", name, fnErr)
		}
		learned := oracle.LearnedMap{V: 1, Entries: map[string]*oracle.LearnedEntry{}}
		if raw, err := s.store.GetSetting(ctx, "oracle_opsvc:"+id); err != nil {
			return nil, err
		} else if len(raw) > 0 {
			_ = json.Unmarshal(raw, &learned)
			if learned.Entries == nil {
				learned.Entries = map[string]*oracle.LearnedEntry{}
			}
		}
		var alive map[string]bool
		if names, err := s.store.ListActiveServiceNames(ctx, oracleCoverageAliveSince); err != nil {
			log.Printf("[oracle] özne kapsamı %s: canlı servis listesi okunamadı: %v", name, err)
		} else {
			alive = make(map[string]bool, len(names))
			for _, n := range names {
				alive[n] = true
			}
		}
		return oracleSubjectCoverage{
			SourceID: id, SourceName: name, Hours: hours,
			CoverageReport: oracle.BuildCoverage(obs, totals, learned, alive, now, oracleCoverageUnresolved, fn),
			FnError:        fnErr,
			LearnedEntries: len(learned.Entries), GeneratedAt: now.UnixMilli(),
		}, nil
	})
}

// oracleCoverageFn — fonksiyon kodu basamağının girdisi: pencerenin (operasyon,
// kod) çiftleri + kodların span tarafındaki servis dağılımı. Kodlar satır
// sayısına göre sıralı gider (okuyucu ilk 200'ü alır). Hata raporu düşürmez:
// nil + mesaj döner.
func (s *Server) oracleCoverageFn(ctx context.Context, sourceID string, enabled bool, from, to time.Time) (*oracle.CoverageFn, string) {
	pairs, err := s.store.OracleOpCodes(ctx, sourceID, from, to)
	if err != nil {
		return nil, err.Error()
	}
	codes := make([]string, 0, len(pairs))
	seen := map[string]bool{}
	for _, p := range pairs {
		if !seen[p.Code] {
			seen[p.Code] = true
			codes = append(codes, p.Code)
		}
	}
	facts, err := s.store.FunctionCodeServices(ctx, codes, from, to)
	if err != nil {
		return nil, err.Error()
	}
	return &oracle.CoverageFn{Enabled: enabled, Source: facts.Source, Pairs: pairs, ByCode: facts.ByCode}, ""
}
