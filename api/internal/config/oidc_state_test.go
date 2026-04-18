package config

import (
	"reflect"
	"sort"
	"testing"
)

// Tests are table-driven. Each case declares the raw inputs
// (providers, DB overrides/opt-out, env flag) and the expected verdict
// plus enabled set. The set is sorted before comparison so test
// ordering isn't sensitive to map iteration order.
func TestDetermineBootState(t *testing.T) {
	cases := []struct {
		name           string
		providers      []ProviderInitView
		db             DBConfigView
		envFlag        bool
		wantState      BootState
		wantEnabledSet []string
	}{
		{
			name:           "no env, no flag -> NoEnvNoFlag",
			providers:      nil,
			db:             DBConfigView{},
			envFlag:        false,
			wantState:      BootStateNoEnvNoFlag,
			wantEnabledSet: []string{},
		},
		{
			name:           "no env, env flag set -> ConfirmedOptOut",
			providers:      nil,
			db:             DBConfigView{},
			envFlag:        true,
			wantState:      BootStateConfirmedOptOut,
			wantEnabledSet: []string{},
		},
		{
			name:           "no env, DB opt_out -> ConfirmedOptOut",
			providers:      nil,
			db:             DBConfigView{OptOut: true},
			envFlag:        false,
			wantState:      BootStateConfirmedOptOut,
			wantEnabledSet: []string{},
		},
		{
			name: "env provider inits OK -> ConfirmedOK",
			providers: []ProviderInitView{
				{ID: "google", Configured: true, Enabled: true, InitOK: true},
			},
			wantState:      BootStateConfirmedOK,
			wantEnabledSet: []string{"google"},
		},
		{
			// Phase 9.10a: strict confirmation — any enabled provider
			// that fails init forces InitFailed regardless of other
			// successes. Operators must explicitly disable a broken
			// provider via /confirm before the app serves traffic.
			name: "two providers, one fails -> InitFailed (strict)",
			providers: []ProviderInitView{
				{ID: "google", Configured: true, Enabled: true, InitOK: true},
				{ID: "microsoft", Configured: true, Enabled: true, InitOK: false},
			},
			wantState:      BootStateInitFailed,
			wantEnabledSet: []string{"google", "microsoft"},
		},
		{
			name: "two providers both init OK -> ConfirmedOK (strict)",
			providers: []ProviderInitView{
				{ID: "google", Configured: true, Enabled: true, InitOK: true},
				{ID: "microsoft", Configured: true, Enabled: true, InitOK: true},
			},
			wantState:      BootStateConfirmedOK,
			wantEnabledSet: []string{"google", "microsoft"},
		},
		{
			name: "all providers fail init -> InitFailed",
			providers: []ProviderInitView{
				{ID: "microsoft", Configured: true, Enabled: true, InitOK: false},
			},
			wantState:      BootStateInitFailed,
			wantEnabledSet: []string{"microsoft"},
		},
		{
			// 9.16: "Enabled" now comes from the merged view (caller
			// populates ProviderInitView.Enabled), not from a separate
			// DB override map. Pre-9.16 this case ran the overrides
			// through DetermineBootState; post-9.16 the caller has
			// already resolved them into each view's Enabled flag.
			name: "google disabled + broken microsoft enabled -> InitFailed",
			providers: []ProviderInitView{
				{ID: "google", Configured: true, Enabled: false, InitOK: true},
				{ID: "microsoft", Configured: true, Enabled: true, InitOK: false},
			},
			wantState:      BootStateInitFailed,
			wantEnabledSet: []string{"microsoft"},
		},
		{
			name: "everything disabled + opt_out true -> ConfirmedOptOut",
			providers: []ProviderInitView{
				{ID: "google", Configured: true, Enabled: false, InitOK: true},
			},
			db:             DBConfigView{OptOut: true},
			wantState:      BootStateConfirmedOptOut,
			wantEnabledSet: []string{},
		},
		{
			name: "two enabled + ok providers -> ConfirmedOK",
			providers: []ProviderInitView{
				{ID: "google", Configured: true, Enabled: true, InitOK: true},
				{ID: "authelia", Configured: true, Enabled: true, InitOK: true},
			},
			wantState:      BootStateConfirmedOK,
			wantEnabledSet: []string{"authelia", "google"},
		},
		{
			name: "everything disabled + opt_out false -> InitFailed (limbo)",
			providers: []ProviderInitView{
				{ID: "google", Configured: true, Enabled: false, InitOK: true},
			},
			// No candidates survive the enabled filter, but opt_out isn't
			// set — caller must explicitly opt out before the app serves
			// traffic. Per algorithm, InitFailed.
			wantState:      BootStateInitFailed,
			wantEnabledSet: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetermineBootState(tc.providers, tc.db, tc.envFlag)
			if got.State != tc.wantState {
				t.Errorf("state: got %q, want %q", got.State, tc.wantState)
			}
			sort.Strings(got.Enabled)
			sort.Strings(tc.wantEnabledSet)
			if !reflect.DeepEqual(got.Enabled, tc.wantEnabledSet) {
				t.Errorf("enabled: got %v, want %v", got.Enabled, tc.wantEnabledSet)
			}
		})
	}
}

// Confirmed() is tiny but public; a quick table test pins the contract
// so future additions to BootState can't silently flip it.
func TestBootState_Confirmed(t *testing.T) {
	cases := []struct {
		state BootState
		want  bool
	}{
		{BootStateConfirmedOK, true},
		{BootStateConfirmedOptOut, true},
		{BootStateInitFailed, false},
		{BootStateNoEnvNoFlag, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			if got := tc.state.Confirmed(); got != tc.want {
				t.Errorf("Confirmed: got %v, want %v", got, tc.want)
			}
		})
	}
}
