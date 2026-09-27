package chstore

import (
	"context"
	"fmt"
	"strings"

	"github.com/cilcenk/coremetry/internal/argocd"
)

// argocd_status_store.go — v0.10.983 — ROLLOUTS v2 P3.1 CH kapısı:
// argocd_app_status (docs/rollouts/v2-audit.md §10.3.3, §10.6; şema
// rollout_v2_schema.go ≡ migrations/0015). Tek yazıcı: argocd-metrics lideri.
//
// Okuma (§10.3.3 "failover'da önceki durum BURADAN kurulur"; §10.6 "latest
// status (LIMIT 1 BY)"): instance başına uygulama başına SON satır — FINAL +
// WHERE instance_id = ? (ORDER BY öneki; nokta öneki okuma) + keyset
// (app_namespace, app_name) > (?, ?) + ORDER BY …, changed_at DESC +
// LIMIT 1 BY app_namespace, app_name + sayfa LIMIT + max_execution_time.
// LIMIT 1 BY sayfa LIMIT'inden ÖNCE uygulanır: her grup tek satır olduğundan
// sayfa sınırı grup sınırına denk gelir, keyset son grubun anahtarından
// devam eder. Tavan aşımı HATA — kesik durumdan diff alınmaz (eksik bellek
// ~binlerce sahte 'appeared' basardı; rollout_v2_store emsali). Okuma in-order
// ANA bağlantıda (s.conn; stateTables'ta "FROM argocd_app_status" pinli).
//
// Yazım: tam satır (RMT(version), invariant #4 — her kolon taşınır), AÇIK
// istemci version'ı (işçi tekdüze üretir), asyncInsertCtx (async_insert
// korunur, wait_for_async_insert=1 → işçi yazımın bittiğini bilir). Her satır
// batch'e girmeden argocd.ValidateStatusRow'dan geçer; TEK geçersiz satır
// bütün batch'i reddeder (yarım yazım yok; işçi belleği düşürüp CH'den kurar).

// v0.10.983 inceleme: son satırı 'deleted' olan uygulama yeniden kuruluma
// ALINMAZ (argocdRebuildKeep) ve tavana sayılmaz — ApplicationSet PR/önizleme
// üreteci günde binlerce benzersiz adı yaratıp silen instance'ta silinmiş
// uygulamalar 180 günlük TTL boyunca birikir, 200k tavanını aşıp her edinimi
// kalıcı "tavan aşıldı"ya düşürürdü. İşçi için fark yok: bilinmeyen uygulama
// taban envanterinde 'baseline', sonrasında 'appeared' — silinmiş-ve-dönen
// ile aynı. Taranan satır ayrıca argocdStatusScanCap ile sınırlı (sonsuz
// sayfa yok).
const (
	argocdStatusPage    = 20_000
	argocdStatusHardCap = 200_000   // bellek kurulan (silinmemiş) uygulama
	argocdStatusScanCap = 2_000_000 // taranan son satır (silinmişler dahil)
)

// argocdRebuildKeep — SAF: son satır işçi belleğine alınır mı (silinmemiş).
func argocdRebuildKeep(r argocd.StatusRow) bool { return r.ChangeKind != argocd.ChangeDeleted }

// argocdStatusCols — StatusRow `ch` etiketleri, sırasıyla (tek kaynak).
var argocdStatusCols = strings.Join(argocd.StatusColumns(), ", ")

// argocdLatestStatusPageSQL — bağlar: instance_id, (app_namespace, app_name) kursörü.
func argocdLatestStatusPageSQL() string {
	return fmt.Sprintf(`SELECT %s
		FROM argocd_app_status FINAL
		WHERE instance_id = ?
		  AND (app_namespace, app_name) > (?, ?)
		ORDER BY app_namespace, app_name, changed_at DESC
		LIMIT 1 BY app_namespace, app_name
		LIMIT %d
		SETTINGS max_execution_time = 15`, argocdStatusCols, argocdStatusPage)
}

// ArgoCDLatestStatuses — v0.10.983 — instance'ın uygulama başına son satırı
// (işçinin lider ediniminde yeniden kurulumu); son satırı 'deleted' olanlar
// hariç (argocdRebuildKeep). Boş dilim = instance'ın canlı satırı yok (ilk
// koşu → taban).
func (s *Store) ArgoCDLatestStatuses(ctx context.Context, instanceID string) ([]argocd.StatusRow, error) {
	out := []argocd.StatusRow{}
	var curNS, curName string
	scanned := 0
	for {
		rows, err := s.conn.Query(ctx, argocdLatestStatusPageSQL(), instanceID, curNS, curName)
		if err != nil {
			return nil, fmt.Errorf("argocd_app_status: %w", err)
		}
		n := 0
		for rows.Next() {
			var r argocd.StatusRow
			if err := rows.Scan(&r.InstanceID, &r.AppNamespace, &r.AppName, &r.ChangedAt, &r.ChangeKind, &r.SyncStatus,
				&r.HealthStatus, &r.Operation, &r.SyncPhase, &r.AutoSync, &r.Project, &r.Repo, &r.DestServer,
				&r.DestNamespace, &r.ClusterID, &r.Version); err != nil {
				rows.Close()
				return nil, fmt.Errorf("argocd_app_status scan: %w", err)
			}
			if argocdRebuildKeep(r) {
				out = append(out, r)
			}
			curNS, curName = r.AppNamespace, r.AppName
			n++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		scanned += n
		if len(out) > argocdStatusHardCap {
			return nil, fmt.Errorf("argocd_app_status: instance %s için %d satır tavanı aşıldı — kesik durumdan diff alınmaz", instanceID, argocdStatusHardCap)
		}
		if scanned > argocdStatusScanCap {
			return nil, fmt.Errorf("argocd_app_status: instance %s için %d taranan satır tavanı aşıldı — kesik durumdan diff alınmaz", instanceID, argocdStatusScanCap)
		}
		if n < argocdStatusPage {
			return out, nil
		}
	}
}

// argocdStatusArgs — SAF: tam satır bağları (kolon sırası argocdStatusCols).
func argocdStatusArgs(r argocd.StatusRow) []any {
	return []any{r.InstanceID, r.AppNamespace, r.AppName, r.ChangedAt.UTC(), r.ChangeKind, r.SyncStatus, r.HealthStatus,
		r.Operation, r.SyncPhase, r.AutoSync, r.Project, r.Repo, r.DestServer, r.DestNamespace, r.ClusterID, r.Version}
}

// validateArgoCDStatuses — SAF: batch'in HEPSİ yazıcı sözleşmesinden geçmeli.
func validateArgoCDStatuses(rows []argocd.StatusRow) error {
	for _, r := range rows {
		if err := argocd.ValidateStatusRow(r); err != nil {
			return fmt.Errorf("argocd_app_status %s/%s/%s: %s", r.InstanceID, r.AppNamespace, r.AppName,
				strings.ReplaceAll(err.Error(), "\n", "; "))
		}
	}
	return nil
}

// ArgoCDWriteStatuses — v0.10.983 — argocd_app_status tam satır batch'i.
func (s *Store) ArgoCDWriteStatuses(ctx context.Context, rows []argocd.StatusRow) error {
	if len(rows) == 0 {
		return nil
	}
	if err := validateArgoCDStatuses(rows); err != nil {
		return err
	}
	b, err := s.conn.PrepareBatch(asyncInsertCtx(ctx), `INSERT INTO argocd_app_status (`+argocdStatusCols+`)`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := b.Append(argocdStatusArgs(r)...); err != nil {
			return err
		}
	}
	return b.Send()
}
