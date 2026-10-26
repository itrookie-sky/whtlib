package testx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPwd(t *testing.T) {
	pwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Log(pwd)
}

func TestChdirToProjectRoot(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	err := os.MkdirAll(nested, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	// t.Chdir 确保测试结束后恢复工作目录。
	t.Chdir(nested)
	ChdirToProjectRoot(".")
	got, err := os.Stat(".")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(got, want) {
		t.Fatal("工作目录未切换到包含 go.mod 的根目录")
	}
}
