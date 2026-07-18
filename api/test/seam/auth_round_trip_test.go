package seam_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}

func readRepoFile(t *testing.T, root, rel string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(content)
}

func requireContains(t *testing.T, content, rel string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if !strings.Contains(content, needle) {
			t.Errorf("%s must contain %q", rel, needle)
		}
	}
}

func TestAuthenticatedRoundTripSeamsAreWired(t *testing.T) {
	root := repoRoot(t)
	app := readRepoFile(t, root, "mobile/App.tsx")
	oauth := readRepoFile(t, root, "mobile/src/features/auth/oauth.ts")
	client := readRepoFile(t, root, "mobile/src/api/client.ts")
	supabase := readRepoFile(t, root, "mobile/src/lib/supabase/client.ts")
	appConfig := readRepoFile(t, root, "mobile/app.json")

	requireContains(t, app, "mobile/App.tsx", "signInWithMicrosoft", "reauthSignal.subscribe", "/api/v1/auth/me")
	requireContains(t, oauth, "mobile/src/features/auth/oauth.ts", "provider: 'azure'", "skipBrowserRedirect: true", "exchangeCodeForSession")
	requireContains(t, client, "mobile/src/api/client.ts", "apiRequest", "createAuthedRequest")
	requireContains(t, supabase, "mobile/src/lib/supabase/client.ts", "flowType: 'pkce'")
	requireContains(t, appConfig, "mobile/app.json", `"scheme": "odamrita"`)
}
