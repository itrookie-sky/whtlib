package filepathx

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWithin(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(root, "base")
	for _, tc := range []struct {
		name string
		base string
		path string
		want string
		err  error
	}{
		{name: "child", base: base, path: "a/b", want: filepath.Join(base, "a", "b")},
		{name: "self", base: base, path: ".", want: base},
		{name: "clean", base: "base", path: "a/../b", want: filepath.Join(base, "b")},
		{name: "parent", base: base, path: "..", err: ErrOutsideBase},
		{name: "escape", base: base, path: "a/../../b", err: ErrOutsideBase},
		{name: "absolute", base: base, path: filepath.Join(base, "a"), err: ErrOutsideBase},
		{name: "empty child", base: base, want: base},
		{name: "empty base", path: "a", err: ErrInvalidPath},
		{name: "nul child", base: base, path: "a\x00b", err: ErrInvalidPath},
		{name: "nul base", base: "a\x00b", path: "a", err: ErrInvalidPath},
	} {
		t.Run("join "+tc.name, func(t *testing.T) {
			got, err := JoinWithin(tc.base, tc.path)
			if got != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("JoinWithin = %q, %v; want %q, %v", got, err, tc.want, tc.err)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		base   string
		target string
		want   string
		err    error
	}{
		{name: "child", base: base, target: filepath.Join(base, "a", "b"), want: filepath.Join("a", "b")},
		{name: "self", base: base, target: base, want: "."},
		{name: "relative to cwd", base: "base", target: "base/a", want: "a"},
		{name: "parent", base: base, target: root, err: ErrOutsideBase},
		{name: "sibling prefix", base: base, target: base + "-other", err: ErrOutsideBase},
		{name: "relative outside", base: "base", target: "a", err: ErrOutsideBase},
		{name: "empty base", target: base, err: ErrInvalidPath},
		{name: "empty target", base: base, err: ErrInvalidPath},
		{name: "nul target", base: base, target: "a\x00b", err: ErrInvalidPath},
	} {
		t.Run("rel "+tc.name, func(t *testing.T) {
			got, err := RelWithin(tc.base, tc.target)
			if got != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("RelWithin = %q, %v; want %q, %v", got, err, tc.want, tc.err)
			}
		})
	}
}

func TestJoinWithinMultipleElements(t *testing.T) {
	base := t.TempDir()
	for _, tc := range []struct {
		name  string
		elems []string
		want  string
		err   error
	}{
		{name: "no elements", want: base},
		{name: "empty elements", elems: []string{"", ""}, want: base},
		{name: "multiple", elems: []string{"config", "prod", "app.json"}, want: filepath.Join(base, "config", "prod", "app.json")},
		{name: "ignore empty", elems: []string{"", "config", "", "app.json", ""}, want: filepath.Join(base, "config", "app.json")},
		{name: "parent within base", elems: []string{"config", "..", "app.json"}, want: filepath.Join(base, "app.json")},
		{name: "back to base", elems: []string{"config", ".."}, want: base},
		{name: "escape", elems: []string{"config", "..", "..", "app.json"}, err: ErrOutsideBase},
		{name: "absolute later", elems: []string{"config", base}, err: ErrOutsideBase},
		{name: "root later", elems: []string{"config", "/"}, err: ErrOutsideBase},
		{name: "nul later", elems: []string{"config", "a\x00b", ".."}, err: ErrInvalidPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := JoinWithin(base, tc.elems...)
			if got != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("JoinWithin = %q, %v; want %q, %v", got, err, tc.want, tc.err)
			}
		})
	}
	_, err := JoinWithin("")
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("empty base = %v", err)
	}
}

func TestWithinPlatformPaths(t *testing.T) {
	base := t.TempDir()
	if runtime.GOOS != "windows" {
		// Unix 中反斜杠不是路径分隔符，沿用标准库语义。
		got, err := JoinWithin(base, `..\child`)
		if err != nil || got != filepath.Join(base, `..\child`) {
			t.Fatalf("JoinWithin = %q, %v", got, err)
		}
		return
	}
	for _, name := range []string{`..\child`, `C:\child`, `C:child`, `\child`, `\\server\share`, "CON"} {
		_, err := JoinWithin(base, name)
		if !errors.Is(err, ErrOutsideBase) {
			t.Fatalf("JoinWithin(%q) = %v", name, err)
		}
	}
	for _, name := range []string{`C:\child`, `C:child`, `\child`, `\\server\share`, "CON"} {
		_, err := JoinWithin(base, "parent", name)
		if !errors.Is(err, ErrOutsideBase) {
			t.Fatalf("later Windows element %q = %v", name, err)
		}
	}
	_, err := RelWithin(`C:\base`, `D:\target`)
	if !errors.Is(err, ErrOutsideBase) {
		t.Fatalf("different volumes = %v", err)
	}
}

func TestFindUp(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	err := os.MkdirAll(nested, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	marker := ".filepathx-marker"
	for _, dir := range []string{root, filepath.Dir(nested)} {
		err = os.WriteFile(filepath.Join(dir, marker), nil, 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := FindUp(filepath.Join("a", "b"), marker)
	if err != nil || got != filepath.Join(before, "a") {
		t.Fatalf("nearest marker = %q, %v", got, err)
	}
	got, err = FindUp(root, marker)
	if err != nil || got != root {
		t.Fatalf("start marker = %q, %v", got, err)
	}
	err = os.Mkdir(filepath.Join(root, ".marker-directory"), 0o700)
	if err != nil {
		t.Fatal(err)
	}
	got, err = FindUp(nested, ".marker-directory")
	if err != nil || got != root {
		t.Fatalf("directory marker = %q, %v", got, err)
	}
	after, err := os.Getwd()
	if err != nil || after != before {
		t.Fatalf("cwd changed = %q, %v", after, err)
	}
	_, err = FindUp(nested, ".filepathx-missing-"+filepath.Base(root))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing marker = %v", err)
	}
	missing := filepath.Join(root, "missing")
	_, err = FindUp(missing, marker)
	pathErr, ok := errors.AsType[*fs.PathError](err)
	if !errors.Is(err, fs.ErrNotExist) || !ok || pathErr.Path != missing {
		t.Fatalf("missing start error chain = %v", err)
	}
	for _, invalid := range []string{"", ".", "..", "a/b", `a\b`, "a\x00b"} {
		_, err = FindUp(root, invalid)
		if !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("invalid marker %q = %v", invalid, err)
		}
	}
	for _, invalid := range []string{"", "a\x00b", filepath.Join(root, marker)} {
		_, err = FindUp(invalid, marker)
		if !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("invalid start %q = %v", invalid, err)
		}
	}
}

func TestFindUpPreservesStatError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("符号链接创建依赖 Windows 权限")
	}
	root := t.TempDir()
	// 自引用符号链接产生 ELOOP，确认不会被当成未找到而继续查找。
	err := os.Symlink("loop", filepath.Join(root, "loop"))
	if err != nil {
		t.Fatal(err)
	}
	_, statErr := os.Stat(filepath.Join(root, "loop"))
	_, err = FindUp(root, "loop")
	pathErr, ok := errors.AsType[*fs.PathError](statErr)
	if !ok || !errors.Is(err, pathErr.Err) || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat error lost: %v, original %v", err, statErr)
	}
}

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	key := "HOME"
	if runtime.GOOS == "windows" {
		key = "USERPROFILE"
	}
	t.Setenv(key, home)
	for _, tc := range []struct {
		name string
		want string
		err  error
	}{
		{name: "~", want: home},
		{name: "~/", want: home},
		{name: "~/a/../b", want: filepath.Join(home, "b")},
		{name: "~/../sibling", want: filepath.Join(home, "..", "sibling")},
		{name: "", want: ""},
		{name: "$HOME/a", want: "$HOME/a"},
		{name: "a/../b", want: "a/../b"},
		{name: "~user/a", err: ErrInvalidPath},
		{name: "a\x00b", err: ErrInvalidPath},
	} {
		got, err := ExpandHome(tc.name)
		if got != tc.want || !errors.Is(err, tc.err) {
			t.Errorf("ExpandHome(%q) = %q, %v; want %q, %v", tc.name, got, err, tc.want, tc.err)
		}
	}
	got, err := ExpandHome(`~\a`)
	if runtime.GOOS == "windows" {
		if err != nil || got != filepath.Join(home, "a") {
			t.Fatalf("Windows home = %q, %v", got, err)
		}
	} else if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("unsupported tilde form = %v", err)
	}
	t.Setenv(key, "")
	_, err = ExpandHome("~")
	if err == nil {
		t.Fatal("missing home must fail")
	}
}

func TestStemAndReplaceExt(t *testing.T) {
	for _, tc := range []struct {
		name string
		stem string
	}{
		{name: "archive.tar.gz", stem: "archive.tar"},
		{name: ".env", stem: ".env"},
		{name: ".env.local", stem: ".env"},
		{name: "file.", stem: "file"},
		{name: "plain", stem: "plain"},
		{name: "目录/文件.json", stem: "文件"},
		{name: "dir.with.dot/file", stem: "file"},
		{name: "dir/"},
		{name: "/"},
		{name: "."},
		{name: ".."},
		{name: ""},
	} {
		got := Stem(tc.name)
		if got != tc.stem {
			t.Errorf("Stem(%q) = %q; want %q", tc.name, got, tc.stem)
		}
	}
	for _, tc := range []struct {
		name string
		ext  string
		want string
		err  error
	}{
		{name: "archive.tar.gz", ext: "zip", want: "archive.tar.zip"},
		{name: "a/../b//file.txt", ext: ".json", want: "a/../b//file.json"},
		{name: ".env", ext: "local", want: ".env.local"},
		{name: ".env", want: ".env"},
		{name: ".env.local", want: ".env"},
		{name: "plain", ext: "tar.gz", want: "plain.tar.gz"},
		{name: "file.", want: "file"},
		{name: "", err: ErrInvalidPath},
		{name: "dir/", err: ErrInvalidPath},
		{name: ".", err: ErrInvalidPath},
		{name: "..", err: ErrInvalidPath},
		{name: "a\x00b", err: ErrInvalidPath},
		{name: "file", ext: "a/b", err: ErrInvalidExtension},
		{name: "file", ext: `a\b`, err: ErrInvalidExtension},
		{name: "file", ext: "a\x00b", err: ErrInvalidExtension},
		{name: "file", ext: ".", err: ErrInvalidExtension},
		{name: "file", ext: "..", err: ErrInvalidExtension},
	} {
		got, err := ReplaceExt(tc.name, tc.ext)
		if got != tc.want || !errors.Is(err, tc.err) {
			t.Errorf("ReplaceExt(%q, %q) = %q, %v; want %q, %v", tc.name, tc.ext, got, err, tc.want, tc.err)
		}
	}
}
