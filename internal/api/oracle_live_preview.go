package api

// oracle_live_preview.go — v0.10.998 — Oracle kaynağı için "canlıya geçiş
// önizlemesi" (operatör kararı 2026-10-01: kaynak kipi gölge → canlı).
//
//	GET /api/settings/oracle/{id}/live-preview   admin
//
// Kaynak kipi (Settings › Oracle › Problem üretimi) üç değerlidir: off |
// shadow | live. Gölgede Problem açılır ama bildirim gitmez; canlıda aynı
// tarayıcı bildirimli koşar (main.go: extScanner). "Canlıya alırsam kaç
// bildirim gelir, kime gider?" sorusunun cevabı bugüne dek ekranda yoktu —
// operatör Inbox'ı elle saymak zorundaydı. Bu uç o cevabı VERİDEN kurar:
//
//   - Gölgede açılan Problem sayısı (24 sa / 7 g; chstore.
//     ExternalSourceProblemStats). Canlı kip bildirimi YALNIZ açılışta
//     gönderir, yani bu sayı gidecek bildirim sayısının ölçüsüdür.
//   - Bildirimin türü ve alıcıları: dış seri Problem'leri bildirimde
//     "anomali" türüdür (chstore.ProblemNotifyKind). Türü süzen kanal ya da
//     ekip maili bunları ALMAZ — "canlıya aldım, hiçbir şey gelmedi"nin en
//     olası sebebi, o yüzden burada açıkça sayılır.
//
// Salt okuma; hiçbir ayarı değiştirmez. Kipi operatör değiştirir.
// serveCached 60 sn (anahtar: kaynak kimliği + adı + kipi).

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/oracle"
)

func init() { registerRoutesExtra("oracle-live-preview", (*Server).registerOracleLivePreviewRoutes) }

func (s *Server) registerOracleLivePreviewRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/oracle/{id}/live-preview", auth.RequireRole(auth.RoleAdmin, s.getOracleLivePreview))
}

// oracleLivePreviewChannelNames — cevapta adı geçen kanal tavanı.
const oracleLivePreviewChannelNames = 10

// oracleLivePreview — uç cevabı.
type oracleLivePreview struct {
	SourceID   string `json:"sourceId"`
	SourceName string `json:"sourceName"`
	Mode       string `json:"mode"` // off | shadow | live
	chstore.ExternalProblemStats
	// NotifyKind — bu Problem'lerin bildirim türü (kanal / ekip maili süzgeci).
	NotifyKind string `json:"notifyKind"`
	// ChannelsEnabled — etkin kanal sayısı; ChannelsAccepting — bunlardan
	// türü kabul edenlerin sayısı; ChannelNames ilk N'inin adı.
	ChannelsEnabled   int      `json:"channelsEnabled"`
	ChannelsAccepting int      `json:"channelsAccepting"`
	ChannelNames      []string `json:"channelNames"`
	// TeamMail — ekip yönlendirme maili açık VE bu türü kabul ediyor.
	TeamMail bool `json:"teamMail"`
	// OpenCapPerTick — tik başına açılış tavanı (aşan seriler tek özet Problem).
	OpenCapPerTick int   `json:"openCapPerTick"`
	GeneratedAt    int64 `json:"generatedAt"` // ms
}

// buildOracleLivePreview — SAF (tablo testli): okunan parçalardan cevap.
// Tür, kaynağın seri kural önekinden ProblemNotifyKind ile hesaplanır —
// sabit bir "anomaly" dizgesi değil: sınıflama değişirse önizleme de değişir.
func buildOracleLivePreview(src oracle.SourceConfig, stats chstore.ExternalProblemStats, channels []chstore.NotificationChannel,
	tc chstore.TeamContacts, openCap int, now time.Time) oracleLivePreview {
	series, _ := chstore.ExternalRulePrefixes(src.Name)
	kind := chstore.ProblemNotifyKind(chstore.Problem{RuleID: series + "x:ext:error_count"})
	if stats.Top == nil {
		stats.Top = []chstore.ExternalProblemTop{}
	}
	out := oracleLivePreview{
		SourceID: src.ID, SourceName: src.Name, Mode: oracle.ProblemModeOf(src),
		ExternalProblemStats: stats, NotifyKind: kind, ChannelNames: []string{},
		TeamMail:       tc.Enabled && len(tc.Contacts) > 0 && tc.KindAllows(kind),
		OpenCapPerTick: openCap, GeneratedAt: now.UnixMilli(),
	}
	for _, c := range channels {
		if !c.Enabled {
			continue
		}
		out.ChannelsEnabled++
		if !c.MatchRules.AllowsKind(kind) {
			continue
		}
		out.ChannelsAccepting++
		if len(out.ChannelNames) < oracleLivePreviewChannelNames {
			out.ChannelNames = append(out.ChannelNames, c.Name)
		}
	}
	return out
}

func (s *Server) getOracleLivePreview(w http.ResponseWriter, r *http.Request) {
	if s.oracle == nil || s.store == nil {
		http.Error(w, "oracle not available", http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	var src *oracle.SourceConfig
	for _, c := range s.oracle.CurrentSettings().Sources {
		if c.ID == id {
			cc := c
			src = &cc
			break
		}
	}
	if id == "" || src == nil {
		writeJSONError(w, http.StatusNotFound, "oracle kaynağı bulunamadı (önce kaydedin): "+id)
		return
	}
	key := fmt.Sprintf("oracle-live-preview:id=%s:name=%s:mode=%s", src.ID, src.Name, oracle.ProblemModeOf(*src))
	s.serveCached(w, r, key, 60*time.Second, func(ctx context.Context) (any, error) {
		now := time.Now()
		stats, err := s.store.ExternalSourceProblemStats(ctx, src.Name, now)
		if err != nil {
			return nil, err
		}
		channels, err := s.store.ListChannels(ctx)
		if err != nil {
			return nil, err
		}
		tc, err := s.store.GetTeamContacts(ctx)
		if err != nil {
			return nil, err
		}
		return buildOracleLivePreview(*src, stats, channels, tc, s.store.AnomalySensitivity().ExternalOpenCapPerTick, now), nil
	})
}
