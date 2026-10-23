package testx

import (
	"os"
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
	pwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Log(pwd)
	ChdirToProjectRoot(pwd)
	pwd2, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Log(pwd2)
}
