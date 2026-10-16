package repos

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// File is a repository file of the host.
type File struct {
	Path    string // absolute on the host, e.g. /etc/apk/repositories
	Content string
}

// Files are /etc/apk/repositories and the *.list files of /etc/apk/repositories.d and /lib/apk/repositories.d.
type Files struct {
	Main  File
	Extra []File
}

const mainPath = "etc/apk/repositories"

// Read reads the repository files below root.
func Read(root string) (Files, error) {
	var fs Files
	data, err := os.ReadFile(filepath.Join(root, mainPath))
	if err != nil {
		return fs, err
	}
	fs.Main = File{Path: "/" + mainPath, Content: string(data)}
	for _, dir := range []string{"etc/apk/repositories.d", "lib/apk/repositories.d"} {
		matches, _ := filepath.Glob(filepath.Join(root, dir, "*.list"))
		sort.Strings(matches)
		for _, m := range matches {
			b, err := os.ReadFile(m)
			if err != nil {
				return fs, err
			}
			rel, _ := filepath.Rel(root, m)
			fs.Extra = append(fs.Extra, File{Path: "/" + filepath.ToSlash(rel), Content: string(b)})
		}
	}
	return fs, nil
}

// Sum is the sha256 over the paths and contents of all files.
func (fs Files) Sum() string {
	h := sha256.New()
	for _, f := range append([]File{fs.Main}, fs.Extra...) {
		fmt.Fprintf(h, "%s\x00%s\x00", f.Path, f.Content)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ExtraMappable lists the .d files with a line a release upgrade would have to rewrite: v1 refuses to rewrite them.
func (fs Files) ExtraMappable() []string {
	var out []string
	for _, f := range fs.Extra {
		if len(Branches(f.Content)) > 0 {
			out = append(out, f.Path)
		}
	}
	return out
}

// Sim is the file for apk --repositories-file: main, then every .d file verbatim (apk then reads only that file).
func (fs Files) Sim(main string) string {
	var b strings.Builder
	b.WriteString(main)
	if !strings.HasSuffix(main, "\n") {
		b.WriteString("\n")
	}
	for _, f := range fs.Extra {
		fmt.Fprintf(&b, "# %s\n%s", f.Path, f.Content)
		if !strings.HasSuffix(f.Content, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// WriteAtomic writes data to path through a temp file in the same directory: fsync, rename, fsync of the directory.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".edelweiss-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op after the rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// BackupPath is the backup of /etc/apk/repositories that job id made.
func BackupPath(root, id string) string { return filepath.Join(root, mainPath+".edelweiss-"+id) }

// validID reports whether id is one plain path element, so that BackupPath stays a sibling of the main file.
func validID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\`)
}

var errBadID = errors.New("repos: invalid backup id")

// Backup copies /etc/apk/repositories next to itself, so that Restore is one atomic rename. It refuses an id that is
// empty, "." or "..", or that contains a slash or backslash.
func Backup(root, id string) error {
	if !validID(id) {
		return errBadID
	}
	data, err := os.ReadFile(filepath.Join(root, mainPath))
	if err != nil {
		return err
	}
	return WriteAtomic(BackupPath(root, id), data, 0o644)
}

// Restore puts the backup of job id back in place. It refuses the ids Backup refuses.
func Restore(root, id string) error {
	if !validID(id) {
		return errBadID
	}
	if err := os.Rename(BackupPath(root, id), filepath.Join(root, mainPath)); err != nil {
		return err
	}
	return syncDir(filepath.Join(root, "etc/apk"))
}
