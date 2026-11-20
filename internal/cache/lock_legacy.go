package cache

import (
	"context"
	"strings"
	"time"
)

// lock_legacy.go — Redis R6 lock rename transition (2026-11-20).
//
// Every lock key now lives under `coremetry:lock:*` (bare names such as
// `topology-aggregator-leader` or `problem-explainer:lock` sat in the same
// flat keyspace as bare cache prefixes; the audit's §1.2 namespace finding).
//
// ROLLING UPGRADE: for a few minutes old pods (bare key) and new pods
// (prefixed key) run side by side. If a new pod only took the new key, an
// old pod holding the bare key and a new pod holding the prefixed key would
// BOTH be leader — duplicate alert evaluation / notifications. So for a
// renamed key the Redis lock takes BOTH names: new first, then the legacy
// one; failing the legacy one releases the new one and reports "not
// acquired". Refresh / Release cover both. Result:
//   - old vs new pod: they contend on the legacy key → mutual exclusion.
//   - new vs new pod: they contend on the new key (and legacy) → same.
//   - after the rollout only new pods exist; the legacy key is an extra
//     SET/PEXPIRE per lock round (≤ one per few seconds per worker).
// Removal: drop legacyLockKeys + this wrapper once every supported upgrade
// path starts from a release that already holds the prefixed key (the
// release after this one is enough — old pods are gone after one rollout).

const lockPrefix = "coremetry:lock:"

// legacyLockKeys — renamed lock key → the bare name older pods still use.
// Constants stay greppable here; TestLegacyLockKeysMatchCallSites pins
// that every entry's new name is prefixed.
var legacyLockKeys = map[string]string{
	lockPrefix + "entity-sync":              "entity-sync",
	lockPrefix + "oracle-poller":            "oracle-poller",
	lockPrefix + "cluster-label-check":      "cluster-label-check",
	lockPrefix + "rollout-reconciler":       "rollout-reconciler",
	lockPrefix + "rollout-detector":         "rollout-detector",
	lockPrefix + "argocd-metrics":           "argocd-metrics",
	lockPrefix + "login-summary":            "login-summary",
	lockPrefix + "exception-explainer":      "exception-explainer:lock",
	lockPrefix + "rootcause-synthesizer":    "rootcause-synthesizer:lock",
	lockPrefix + "problem-explainer":        "problem-explainer:lock",
	lockPrefix + "exception-notifier":       "exception-notifier:lock",
	lockPrefix + "topology-aggregator":      "topology-aggregator-leader",
	lockPrefix + "topology-aggregator-live": "topology-aggregator-live-leader",
	lockPrefix + "elasticml-poller":         "elasticml-poller-leader",
	lockPrefix + "anomaly-subjects-flip":    "anomaly-subjects-flip",
	lockPrefix + "cosre-operation-catalog":  "cosre-operation-catalog",
	lockPrefix + "topology-hidden-seed":     "topology-hidden-seed",
	lockPrefix + "cosre-briefing":           "cosre-briefing",
	lockPrefix + "cosre-evalset-daily":      "cosre-evalset-daily",
	lockPrefix + "cosre-case-builder":       "cosre-case-builder",
	lockPrefix + "cosre-quality-nightly":    "cosre-quality-nightly",
}

// legacyLockPrefixes — per-item claim keys (prefix + id).
var legacyLockPrefixes = [][2]string{
	{lockPrefix + "rb-claim:", "rb:claim:"}, // runbook agent step claim
}

// LegacyLockKey — the pre-rename name for key, or "" when key was never
// renamed (exported for tests in other packages).
func LegacyLockKey(key string) string {
	if l, ok := legacyLockKeys[key]; ok {
		return l
	}
	for _, p := range legacyLockPrefixes {
		if rest, ok := strings.CutPrefix(key, p[0]); ok {
			return p[1] + rest
		}
	}
	return ""
}

// legacyKeyLock — Lock wrapper that also holds the legacy name.
type legacyKeyLock struct{ Lock }

func withLegacyLockKeys(l Lock) Lock { return legacyKeyLock{l} }

func (l legacyKeyLock) TryAcquire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	ok, err := l.Lock.TryAcquire(ctx, key, ttl)
	legacy := LegacyLockKey(key)
	if err != nil || !ok || legacy == "" {
		return ok, err
	}
	ok, err = l.Lock.TryAcquire(ctx, legacy, ttl)
	if err != nil || !ok {
		// An old pod holds the bare name: it is still the leader.
		_ = l.Lock.Release(ctx, key)
		return false, err
	}
	return true, nil
}

func (l legacyKeyLock) Refresh(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	ok, err := l.Lock.Refresh(ctx, key, ttl)
	legacy := LegacyLockKey(key)
	if legacy == "" || err != nil {
		return ok, err
	}
	if !ok {
		// Lost the new key: drop the legacy half too so a peer can take both.
		_ = l.Lock.Release(ctx, legacy)
		return false, nil
	}
	ok, err = l.Lock.Refresh(ctx, legacy, ttl)
	if err == nil && !ok {
		// Legacy half lost (expired / evicted) — release the new half, else
		// our own stale token would block the re-acquire until its TTL.
		_ = l.Lock.Release(ctx, key)
	}
	return ok, err
}

func (l legacyKeyLock) Release(ctx context.Context, key string) error {
	err := l.Lock.Release(ctx, key)
	if legacy := LegacyLockKey(key); legacy != "" {
		if e := l.Lock.Release(ctx, legacy); err == nil {
			err = e
		}
	}
	return err
}
