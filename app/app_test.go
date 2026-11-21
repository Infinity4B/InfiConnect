package app_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inficonnect/inficonnect/app"
	"github.com/inficonnect/inficonnect/core"
)

type memSecretStore struct {
	m map[string][]byte
}

func newMemSecretStore() *memSecretStore {
	return &memSecretStore{m: make(map[string][]byte)}
}

func (s *memSecretStore) Set(key string, val []byte) error {
	s.m[key] = append([]byte(nil), val...)
	return nil
}

func (s *memSecretStore) Get(key string) ([]byte, error) {
	v, ok := s.m[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), v...), nil
}

func (s *memSecretStore) Delete(key string) error {
	delete(s.m, key)
	return nil
}

func TestStore_SecretsNeverWrittenToDisk(t *testing.T) {
	dir := t.TempDir()
	secrets := newMemSecretStore()
	store, err := app.OpenStore(dir, secrets)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	saved, err := store.SaveProfile(app.SaveProfileInput{
		Profile: core.Profile{
			Name:          "Campus VPN",
			Protocol:      core.ProtocolATrust,
			ServerAddress: "vpn.example.edu",
			ServerPort:    443,
			Username:      "alice",
			Password:      "top-secret-password",
			TOTPSecret:    "JBSWY3DPEHPK3PXP",
		},
	})
	if err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	if !saved.HasPassword || !saved.HasTOTP {
		t.Fatalf("expected HasPassword and HasTOTP true, got %+v", saved)
	}
	if saved.Password != "" || saved.TOTPSecret != "" {
		t.Fatalf("returned profile must strip plaintext secrets")
	}

	// Verify disk JSON contains NO plaintext secrets.
	diskData, err := os.ReadFile(filepath.Join(dir, "profiles.json"))
	if err != nil {
		t.Fatalf("read profiles.json: %v", err)
	}
	if strings.Contains(string(diskData), "top-secret-password") || strings.Contains(string(diskData), "JBSWY3DPEHPK3PXP") {
		t.Fatalf("plaintext secret leaked to profiles.json: %s", string(diskData))
	}

	// Verify hydration restores secrets in memory.
	hydrated, err := store.HydrateProfile(saved.ID)
	if err != nil {
		t.Fatalf("HydrateProfile: %v", err)
	}
	if hydrated.Password != "top-secret-password" || hydrated.TOTPSecret != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("hydrated secrets mismatch: %+v", hydrated)
	}

	// Delete must wipe secrets too.
	if err := store.DeleteProfile(saved.ID); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	if len(secrets.m) != 0 {
		t.Fatalf("expected keychain secrets wiped on delete, remaining: %v", secrets.m)
	}
}

func TestExternalLoginCallbackMatcher(t *testing.T) {
	if !app.IsExternalLoginCallbackURL(core.ExternalLoginCAS, "https://vpn.zju.edu.cn/passport/v1/auth/cas?sfDomain=cas&ticket=ST-1234") {
		t.Fatal("expected CAS callback URL to match")
	}
	if app.IsExternalLoginCallbackURL(core.ExternalLoginCAS, "https://idp.zju.edu.cn/login") {
		t.Fatal("did not expect IdP login page to match")
	}
	if !app.IsExternalLoginCallbackURL(core.ExternalLoginOAuth2, "https://vpn.example.edu/passport/v1/auth/httpsOauth2?code=abc123&state=xyz") {
		t.Fatal("expected OAuth2 callback URL to match")
	}
}

// TestArchitecture_OnlyCoreImportsZJUConnect enforces Section 2 of the spec:
// "整个项目只有 core 层可以 import zju-connect。"
func TestArchitecture_OnlyCoreImportsZJUConnect(t *testing.T) {
	root := ".."
	fset := token.NewFileSet()

	for _, dir := range []string{"app", "cmd"} {
		full := filepath.Join(root, dir)
		err := filepath.WalkDir(full, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			file, pErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if pErr != nil {
				return pErr
			}
			for _, imp := range file.Imports {
				unq := strings.Trim(imp.Path.Value, `"`)
				if strings.HasPrefix(unq, "github.com/mythologyli/zju-connect") {
					t.Errorf("layer violation: %s imports %s (only core/ may import zju-connect)", path, unq)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}
