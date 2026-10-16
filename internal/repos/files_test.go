package repos

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func write(t *testing.T, root, p, c string) {
	t.Helper()
	full := filepath.Join(root, p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadSumSim(t *testing.T) {
	root := t.TempDir()
	write(t, root, "etc/apk/repositories", "https://x/alpine/v3.23/main\n")
	write(t, root, "etc/apk/repositories.d/extra.list", "https://y/apks\n")
	write(t, root, "lib/apk/repositories.d/base.list", "https://x/alpine/v3.23/community\n")
	fs, err := Read(root)
	if err != nil || len(fs.Extra) != 2 {
		t.Fatalf("Read = %+v, %v", fs, err)
	}
	sum := fs.Sum()
	if len(sum) != 64 {
		t.Fatalf("sum %q", sum)
	}
	write(t, root, "etc/apk/repositories.d/extra.list", "https://z/apks\n")
	fs2, _ := Read(root)
	if fs2.Sum() == sum {
		t.Fatal("the sum must cover the .d files")
	}
	if got := fs2.ExtraMappable(); len(got) != 1 || !strings.HasSuffix(got[0], "base.list") {
		t.Fatalf("ExtraMappable = %q", got)
	}
	sim := fs2.Sim("https://x/alpine/v3.24/main\n")
	for _, want := range []string{"https://x/alpine/v3.24/main", "https://z/apks", "https://x/alpine/v3.23/community"} {
		if !strings.Contains(sim, want) {
			t.Fatalf("sim lacks %q:\n%s", want, sim)
		}
	}
}

// WriteAtomic chmods the temp file, so the mode is exactly perm whatever the umask is, and no temp file stays behind.
func TestWriteAtomicExactPerm(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := WriteAtomic(p, []byte("x"), 0o664); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o664 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("left over: %v", ents)
	}
	if got, want := BackupPath("/r", "j1"), "/r/etc/apk/repositories.edelweiss-j1"; got != want {
		t.Fatalf("BackupPath = %q, want %q", got, want)
	}
}

// A job id is one path element: Backup and Restore refuse everything else, so that BackupPath cannot leave
// /etc/apk/repositories.edelweiss-* (Restore would rename the result over /etc/apk/repositories).
func TestBackupRestoreRefuseBadIDs(t *testing.T) {
	root := t.TempDir()
	write(t, root, "etc/apk/repositories", "keep\n")
	write(t, root, "etc/apk/x", "not a backup\n")
	for _, id := range []string{"", ".", "..", "a/b", "/../x", "../x", `a\b`, `..\x`} {
		if err := Backup(root, id); err == nil {
			t.Errorf("Backup(%q) accepted", id)
		}
		if err := Restore(root, id); err == nil {
			t.Errorf("Restore(%q) accepted", id)
		}
	}
	for p, want := range map[string]string{"etc/apk/repositories": "keep\n", "etc/apk/x": "not a backup\n"} {
		if b, err := os.ReadFile(filepath.Join(root, p)); err != nil || string(b) != want {
			t.Errorf("%s = %q, %v", p, b, err)
		}
	}
	if ents, _ := os.ReadDir(filepath.Join(root, "etc/apk")); len(ents) != 2 {
		t.Errorf("etc/apk has %d entries, want 2: %v", len(ents), ents)
	}
	if err := Backup(root, "job-1.a_b"); err != nil {
		t.Errorf("an ordinary id is refused: %v", err)
	}
}

func TestBackupWriteRestore(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "etc/apk/repositories")
	write(t, root, "etc/apk/repositories", "old\n")
	if err := Backup(root, "j1"); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(main, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(main); string(b) != "new\n" {
		t.Fatalf("after write: %q", b)
	}
	if fi, _ := os.Stat(main); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if err := Restore(root, "j1"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(main); string(b) != "old\n" {
		t.Fatalf("after restore: %q", b)
	}
	if _, err := os.Stat(BackupPath(root, "j1")); !os.IsNotExist(err) {
		t.Fatal("the restore consumes the backup")
	}
}
