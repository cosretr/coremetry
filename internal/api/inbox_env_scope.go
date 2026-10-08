package api

import (
	"context"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// inbox_env_scope.go — v0.10.1131 (operatör-bildirimli): /inbox ortam
// kapsamının TEK çözümü. Liste satırları (Go, EnvScopeKeepsRow), şerit /
// tür çiplerinin COUNT'u (SQL, envScopeConjunct — eşitliği
// chstore.TestEnvScopeSQLAndGoAgree kanıtlıyor) ve kenar çubuğu rozeti
// (computeInboxCountFor) env üyelerini BURADAN alır.
//
// Hata: çip sayımı env'i hiç almıyordu; env seçiliyken "Dış kaynak (N)"
// listenin gizlediği `ext:` öznelerini sayıyordu (rozet ≠ liste).

// resolveInboxEnvMembers — nil = env kısıtı YOK (env boş ya da harita
// hatası: soft-fail → kısıtsız, geçici bir CH tökezlemesi ateş eden P1'i
// gizlemesin — v0.8.387 duruşu). Non-nil (boş olabilir) = kısıt.
func (s *Server) resolveInboxEnvMembers(ctx context.Context, env string) []string {
	if env == "" {
		return nil
	}
	members, err := s.store.EnvMemberServices(ctx, env)
	if err != nil {
		return nil
	}
	if members == nil {
		return []string{} // çözüldü ama boş — nil "kısıt yok" olurdu
	}
	return members
}

// inboxEnvScopeItems — liste tarafı: env üyeleri non-nil ise satırları
// chstore.EnvScopeKeepsRow ile daraltır; nil ise dokunmaz.
func inboxEnvScopeItems(items []InboxItem, members []string) []InboxItem {
	if members == nil {
		return items
	}
	set := make(map[string]bool, len(members))
	for _, m := range members {
		set[m] = true
	}
	return envFilterInboxItems(items, set)
}

// inboxProblemCountScope — sayım tarafı: listeyle AYNI statü dışlaması,
// takım kümesi ve env üyeleri. Ayrı bir env kuralı yazılmaz.
func inboxProblemCountScope(statusFilter string, teamServices, envMembers []string) chstore.ProblemCountScope {
	return chstore.ProblemCountScope{
		Exclude: pickExcludedStatuses(statusFilter),
		Team:    teamServices,
		Env:     envMembers,
	}
}
