package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// maxBinarySize is how big a release build of tele may be. A build is about
// 28 MB; one that finds a method by reflection anywhere it links - a
// reflect.Type or reflect.Value Method or MethodByName with an index or a name
// the linker cannot see - keeps every exported method of every type it links,
// and the thousands of generated Telegram types double it. That doubled the
// download once and went unnoticed until it was published (#208).
const maxBinarySize = 40 << 20

// The release build stays the size it should be. It builds tele the way a
// release does, so it is skipped under -short.
func TestBinary_StaysSmall(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	if runtime.GOOS == "windows" {
		// go-toast executes a text/template, which keeps every method on
		// Windows already, so the size tells nothing there (#314).
		t.Skip("every method is kept on Windows")
	}
	out := filepath.Join(t.TempDir(), "tele")
	build := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", out, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("building tele: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		out += ".exe"
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}

	if info.Size() > maxBinarySize {
		t.Fatalf("tele is %d MB, more than %d MB: something reachable from main looks a method up by reflection, "+
			"which keeps every exported method of every type the binary links", info.Size()>>20, maxBinarySize>>20)
	}
}
