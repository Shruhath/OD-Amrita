package main

import (
	"context"
	"errors"
	"os"
	"regexp"
	"testing"

	svcevent "github.com/Shruhath/OD-Amrita/api/internal/service/event"
	svcuserrole "github.com/Shruhath/OD-Amrita/api/internal/service/userrole"
)

type countingClear struct{ n int }

func (c *countingClear) Clear() { c.n++ }

type stubDemotion struct {
	result svcuserrole.Result
	err    error
}

func (s stubDemotion) Demote(context.Context, svcuserrole.Actor, string) (svcuserrole.Result, error) {
	return s.result, s.err
}

// A demotion drops the role/scope caches only when it changed something, and
// never when it failed.
func TestDemotionDroppingCaches_ClearsOnlyOnARealChange(t *testing.T) {
	for _, tc := range []struct {
		name      string
		inner     stubDemotion
		wantClear int
	}{
		{"demoted", stubDemotion{result: svcuserrole.Result{Changed: true}}, 1},
		{"already a student", stubDemotion{result: svcuserrole.Result{Changed: false}}, 0},
		{"refused", stubDemotion{err: errors.New("refused")}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caches := &countingClear{}
			_, _ = demotionDroppingCaches{inner: tc.inner, caches: caches}.Demote(context.Background(), svcuserrole.Actor{}, "id")
			if caches.n != tc.wantClear {
				t.Errorf("cleared %d times, want %d", caches.n, tc.wantClear)
			}
		})
	}
}

// Removing a coordinator changes what they may mark, so it goes through the
// scope-dropping wrapper like assignment does; a failed removal drops nothing.
func TestEventEditsDroppingScopeCache_RemovalDropsTheScopeOnSuccessOnly(t *testing.T) {
	scope := &countingClear{}
	wrapper := eventEditsDroppingScopeCache{Service: svcevent.NewService(nil, nil, nil), scope: scope}
	// A non-admin is refused before any collaborator is touched.
	err := wrapper.RemoveCoordinatorAssignment(context.Background(), svcevent.Actor{Role: "coordinator"},
		svcevent.RemoveCoordinatorAssignmentInput{EventID: "11111111-1111-4111-8111-111111111111", UserID: "22222222-2222-4222-8222-222222222222"})
	if err == nil {
		t.Fatal("want the refusal surfaced")
	}
	if scope.n != 0 {
		t.Errorf("a failed removal cleared the scope cache %d times", scope.n)
	}
}

// The demotion's cache drop must reach the bus with all three scopes: a role
// cached on a peer would otherwise keep acting under the old role.
func TestRun_DemotionDropsPrincipalScopeAndRevocationCachesThroughTheBus(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	pattern := regexp.MustCompile(`(?s)demotionDroppingCaches\{.*?caches:\s*stream\.Cache\.Clearing\([^)]*cachebus\.ScopePrincipal[^)]*cachebus\.ScopeMarkingScope[^)]*cachebus\.ScopeRevocation`)
	if !pattern.Match(source) {
		t.Error("[P0] the demote route must drop the principal, marking-scope and revocation caches through stream.Cache.Clearing")
	}
}
