package sysproxy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSplitAddr(t *testing.T) {
	tests := []struct {
		in       string
		wantHost string
		wantPort int
		wantErr  bool
	}{
		{"", "", 0, false},
		{"127.0.0.1:1080", "127.0.0.1", 1080, false},
		{"0.0.0.0:1081", "127.0.0.1", 1081, false},
		{":1082", "127.0.0.1", 1082, false},
		{"[::]:1083", "127.0.0.1", 1083, false},
		{"127.0.0.1:0", "", 0, true},
		{"127.0.0.1:70000", "", 0, true},
		{"not-an-addr", "", 0, true},
	}

	for _, tc := range tests {
		h, p, err := splitAddr(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("splitAddr(%q) err=%v wantErr=%v", tc.in, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && (h != tc.wantHost || p != tc.wantPort) {
			t.Errorf("splitAddr(%q) = (%q, %d), want (%q, %d)", tc.in, h, p, tc.wantHost, tc.wantPort)
		}
	}
}

func TestManagerRecoverIfDirtyCleanAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	// Clean state: no journal file.
	recovered, err := mgr.RecoverIfDirty()
	if err != nil || recovered {
		t.Fatalf("clean RecoverIfDirty() = (%v, %v), want (false, nil)", recovered, err)
	}

	// Disable when not enabled should be a clean no-op.
	if err := mgr.Disable(); err != nil {
		t.Fatalf("Disable on clean manager failed: %v", err)
	}

	// Corrupt journal should be cleaned up automatically.
	journal := filepath.Join(dir, "sysproxy_active.json")
	if err := os.WriteFile(journal, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err = mgr.RecoverIfDirty()
	if err == nil || recovered {
		t.Fatalf("corrupt RecoverIfDirty() = (%v, %v), want (false, error)", recovered, err)
	}
	if _, statErr := os.Stat(journal); !os.IsNotExist(statErr) {
		t.Fatalf("expected corrupt journal to be deleted, statErr=%v", statErr)
	}
}

func TestManagerEnableValidation(t *testing.T) {
	mgr := NewManager(t.TempDir())
	if err := mgr.Enable("", ""); err == nil {
		t.Fatal("expected error when both HTTP and SOCKS addresses are empty")
	}
	if err := mgr.Enable("bad-addr", ""); err == nil {
		t.Fatal("expected error for invalid HTTP proxy address")
	}
	if err := mgr.Enable("", "bad-addr"); err == nil {
		t.Fatal("expected error for invalid SOCKS proxy address")
	}
}
