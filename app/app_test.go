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

func TestFileSecretStore_EncryptedPersistence(t *testing.T) {
	dir := t.TempDir()
	vault, err := app.OpenFileSecretStore(dir)
	if err != nil {
		t.Fatalf("OpenFileSecretStore: %v", err)
	}

	store, err := app.OpenStore(dir, vault)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	saved, err := store.SaveProfile(app.SaveProfileInput{
		Profile: core.Profile{
			Name:          "Encrypted Vault VPN",
			Protocol:      core.ProtocolEasyConnect,
			ServerAddress: "vpn.example.edu",
			ServerPort:    443,
			Username:      "bob",
			Password:      "ultra-secret-pass-123",
			TOTPSecret:    "MFRGGZDFMZTWQ2LK",
		},
	})
	if err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}

	// Verify neither profiles.json nor secrets.vault stores plaintext secrets.
	for _, fname := range []string{"profiles.json", "secrets.vault"} {
		raw, err := os.ReadFile(filepath.Join(dir, fname))
		if err != nil {
			t.Fatalf("read %s: %v", fname, err)
		}
		if strings.Contains(string(raw), "ultra-secret-pass-123") || strings.Contains(string(raw), "MFRGGZDFMZTWQ2LK") {
			t.Fatalf("plaintext secret leaked to %s", fname)
		}
	}

	// Verify 0600 file modes for vault and key.
	for _, fname := range []string{"secrets.vault", ".vault.key"} {
		info, err := os.Stat(filepath.Join(dir, fname))
		if err != nil {
			t.Fatalf("stat %s: %v", fname, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("%s permissions = %o, want 0600", fname, perm)
		}
	}

	// Re-open store from disk (simulating app restart) and verify secrets hydrate cleanly.
	vault2, err := app.OpenFileSecretStore(dir)
	if err != nil {
		t.Fatalf("reopen FileSecretStore: %v", err)
	}
	store2, err := app.OpenStore(dir, vault2)
	if err != nil {
		t.Fatalf("reopen Store: %v", err)
	}

	hydrated, err := store2.HydrateProfile(saved.ID)
	if err != nil {
		t.Fatalf("HydrateProfile after reopen: %v", err)
	}
	if hydrated.Password != "ultra-secret-pass-123" || hydrated.TOTPSecret != "MFRGGZDFMZTWQ2LK" {
		t.Fatalf("hydrated secrets mismatch after reopen: %+v", hydrated)
	}

	// SensitiveLiterals should include local vault secrets for log redaction.
	literals := store2.SensitiveLiterals()
	joined := strings.Join(literals, "\n")
	if !strings.Contains(joined, "ultra-secret-pass-123") || !strings.Contains(joined, "MFRGGZDFMZTWQ2LK") {
		t.Fatalf("expected SensitiveLiterals to include local vault secrets, got %v", literals)
	}

	// Delete must wipe secrets from vault too.
	if err := store2.DeleteProfile(saved.ID); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	vault3, err := app.OpenFileSecretStore(dir)
	if err != nil {
		t.Fatalf("reopen FileSecretStore after delete: %v", err)
	}
	if len(vault3.LocalValues()) != 0 {
		t.Fatalf("expected empty vault after delete, got %v", vault3.LocalValues())
	}
}

type countingSecretStore struct {
	m        map[string][]byte
	setCalls int
	getCalls int
	delCalls int
	failGet  bool
}

func (c *countingSecretStore) Set(key string, val []byte) error {
	c.setCalls++
	c.m[key] = append([]byte(nil), val...)
	return nil
}

func (c *countingSecretStore) Get(key string) ([]byte, error) {
	c.getCalls++
	if c.failGet {
		return nil, os.ErrPermission
	}
	v, ok := c.m[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), v...), nil
}

func (c *countingSecretStore) Delete(key string) error {
	c.delCalls++
	delete(c.m, key)
	return nil
}

func TestMigratingSecretStore_NeverPromptsOnWriteAndMigratesOnce(t *testing.T) {
	dir := t.TempDir()
	vault, err := app.OpenFileSecretStore(dir)
	if err != nil {
		t.Fatalf("OpenFileSecretStore: %v", err)
	}
	legacy := &countingSecretStore{
		m: map[string][]byte{
			"profile:old:password": []byte("legacy-pass"),
		},
	}

	mig := app.NewMigratingSecretStore(vault, legacy)

	// Adding a new connection secret writes ONLY to vault, never to legacy keychain.
	if err := mig.Set("profile:new:password", []byte("new-pass")); err != nil {
		t.Fatalf("Set new: %v", err)
	}
	if legacy.setCalls != 0 {
		t.Fatalf("expected 0 legacy Set calls, got %d", legacy.setCalls)
	}

	// Reading the new secret reads ONLY from vault, never touching legacy keychain.
	got, err := mig.Get("profile:new:password")
	if err != nil || string(got) != "new-pass" {
		t.Fatalf("Get new = %q, %v", string(got), err)
	}
	if legacy.getCalls != 0 {
		t.Fatalf("expected 0 legacy Get calls for new secret, got %d", legacy.getCalls)
	}

	// Deleting a secret never touches legacy keychain.
	if err := mig.Delete("profile:new:password"); err != nil {
		t.Fatalf("Delete new: %v", err)
	}
	if legacy.delCalls != 0 {
		t.Fatalf("expected 0 legacy Delete calls, got %d", legacy.delCalls)
	}

	// Reading a pre-existing legacy secret migrates it once into vault.
	gotOld, err := mig.Get("profile:old:password")
	if err != nil || string(gotOld) != "legacy-pass" {
		t.Fatalf("Get old = %q, %v", string(gotOld), err)
	}
	if legacy.getCalls != 1 {
		t.Fatalf("expected 1 legacy Get call for migration, got %d", legacy.getCalls)
	}

	// Subsequent reads hit vault directly (no second legacy call).
	gotOld2, err := mig.Get("profile:old:password")
	if err != nil || string(gotOld2) != "legacy-pass" {
		t.Fatalf("second Get old = %q, %v", string(gotOld2), err)
	}
	if legacy.getCalls != 1 {
		t.Fatalf("expected legacy Get count to remain 1 after migration, got %d", legacy.getCalls)
	}

	// A failed/denied legacy read is not retried repeatedly in the same session.
	legacy.failGet = true
	_, _ = mig.Get("profile:missing:password")
	_, _ = mig.Get("profile:missing:password")
	if legacy.getCalls != 2 {
		t.Fatalf("expected denied legacy key to be queried at most once, got %d total calls", legacy.getCalls)
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
