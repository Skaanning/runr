package proc

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestBuildEnv(t *testing.T) {
	base := []string{"PATH=/bin", "FORCE_COLOR=0", "HOME=/home/me", "EQ=a=b", "=C:=C:\\dir", "", "JUNK"}
	got := BuildEnv(base,
		DefaultEnv,
		map[string]string{"PORT": "3000", "HOME": "/file"},
		map[string]string{"PORT": "8080", "ZED": "z"},
	)
	want := []string{
		"PATH=/bin",
		"FORCE_COLOR=1",
		"HOME=/file",
		"EQ=a=b",
		"=C:=C:\\dir",
		"CLICOLOR_FORCE=1",
		"PYTHONUNBUFFERED=1",
		"PORT=8080",
		"ZED=z",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildEnv:\n got  %q\n want %q", got, want)
	}
}

func TestBuildEnvCaseSensitivity(t *testing.T) {
	got := BuildEnv([]string{"Path=/a"}, map[string]string{"PATH": "/b"})
	want := []string{"Path=/a", "PATH=/b"}
	if runtime.GOOS == "windows" {
		want = []string{"PATH=/b"}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLoadEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "# comment\nPORT=8080\nexport NAME=\"hello world\"\nSINGLE='a b'\n\nEMPTY=\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"PORT": "8080", "NAME": "hello world", "SINGLE": "a b", "EMPTY": ""}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	if _, err := LoadEnvFile(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("expected error for missing file")
	}
}
