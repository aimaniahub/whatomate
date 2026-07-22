package config

import (
	"sync"

	"github.com/google/uuid"
)

// Scope identifies the tenant context for flag resolution.
// Empty OrganizationID / WhatsAppAccount means global-only lookup.
type Scope struct {
	OrganizationID  uuid.UUID
	WhatsAppAccount string
}

// Resolver evaluates feature flags with hierarchical overrides.
// Resolution order: account override → org override → global default.
type Resolver interface {
	// Enabled returns whether the named flag is on for the given scope.
	Enabled(flag string, scope Scope) bool
	// Global returns a copy of the global default flags.
	Global() Flags
}

// StaticResolver is an in-memory resolver suitable for config-file / env defaults.
// Org and account overrides can be set at runtime (e.g. admin API in later phases).
type StaticResolver struct {
	mu       sync.RWMutex
	global   Flags
	byOrg    map[uuid.UUID]Flags
	byAcct   map[string]Flags // key: orgID.String() + "\x00" + account
}

// NewStaticResolver creates a resolver with the given global defaults.
func NewStaticResolver(global Flags) *StaticResolver {
	return &StaticResolver{
		global: global,
		byOrg:  make(map[uuid.UUID]Flags),
		byAcct: make(map[string]Flags),
	}
}

// Global returns a snapshot of global flags.
func (r *StaticResolver) Global() Flags {
	if r == nil {
		return Flags{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.global
}

// SetGlobal replaces global defaults (thread-safe).
func (r *StaticResolver) SetGlobal(f Flags) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.global = f
	r.mu.Unlock()
}

// SetOrgOverride sets or clears org-level overrides (zero Flags clears).
func (r *StaticResolver) SetOrgOverride(orgID uuid.UUID, f Flags) {
	if r == nil || orgID == uuid.Nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !f.AnyEnabled() && f == (Flags{}) {
		delete(r.byOrg, orgID)
		return
	}
	r.byOrg[orgID] = f
}

// SetAccountOverride sets account-level overrides within an org.
func (r *StaticResolver) SetAccountOverride(orgID uuid.UUID, account string, f Flags) {
	if r == nil || orgID == uuid.Nil || account == "" {
		return
	}
	key := accountKey(orgID, account)
	r.mu.Lock()
	defer r.mu.Unlock()
	if !f.AnyEnabled() && f == (Flags{}) {
		delete(r.byAcct, key)
		return
	}
	r.byAcct[key] = f
}

// Enabled implements Resolver.
// Account override wins when the named flag is set on the override layer;
// otherwise org override; otherwise global.
//
// Partial overrides: a layer only wins for flags that are true on that layer
// OR for explicit false we use a mask approach — for Phase 1, overrides are
// "or-merge" upward: any true from more specific scope enables the flag;
// disabling a globally-on flag via override is supported by storing full Flags
// snapshots (specific layer replaces global for Get when layer has been set).
//
// Phase 1 policy: when an org/account override entry exists, that full Flags
// struct is used as the base instead of global (replace semantics). When no
// override exists, global is used. Account overrides replace org when present.
func (r *StaticResolver) Enabled(flag string, scope Scope) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	f := r.global
	if scope.OrganizationID != uuid.Nil {
		if orgF, ok := r.byOrg[scope.OrganizationID]; ok {
			f = orgF
		}
		if scope.WhatsAppAccount != "" {
			if acctF, ok := r.byAcct[accountKey(scope.OrganizationID, scope.WhatsAppAccount)]; ok {
				f = acctF
			}
		}
	}
	return f.Get(flag)
}

func accountKey(orgID uuid.UUID, account string) string {
	return orgID.String() + "\x00" + account
}

// Ensure StaticResolver implements Resolver.
var _ Resolver = (*StaticResolver)(nil)
