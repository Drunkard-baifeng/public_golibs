package filewriter

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func resetDefaultForTest(t *testing.T) {
	t.Helper()
	defaultMu.Lock()
	old := defaultWriter
	defaultWriter = nil
	defaultDir = ""
	defaultMu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

func TestSaveDataFormat(t *testing.T) {
	tmpDir := t.TempDir()
	w, err := New(tmpDir)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer w.Close()

	if err := w.SaveData("a", "b", "c", "d", "format_test"); err != nil {
		t.Fatalf("SaveData 4 columns failed: %v", err)
	}
	if err := w.SaveData("a", "b", "", "", "format_test"); err != nil {
		t.Fatalf("SaveData 2 columns failed: %v", err)
	}
	if err := w.SaveData("only-one", "", "", "", "format_test"); err != nil {
		t.Fatalf("SaveData 1 column failed: %v", err)
	}

	filePath := filepath.Join(tmpDir, "format_test.txt")
	f, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("open file failed: %v", err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan file failed: %v", err)
	}

	want := []string{
		"a----b----c----d",
		"a----b",
		"only-one",
	}
	if len(lines) != len(want) {
		t.Fatalf("unexpected line count, got=%d want=%d", len(lines), len(want))
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("line %d mismatch, got=%q want=%q", i, lines[i], want[i])
		}
	}
}

func TestConcurrentAppendLine(t *testing.T) {
	tmpDir := t.TempDir()
	w, err := New(tmpDir)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer w.Close()

	const goroutines = 100
	const perGoroutine = 200
	const expected = goroutines * perGoroutine

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				line := fmt.Sprintf("g=%d,i=%d", gid, i)
				if err := w.AppendLine("concurrent_write", line); err != nil {
					t.Errorf("AppendLine failed: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	filePath := filepath.Join(tmpDir, "concurrent_write.txt")
	f, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("open file failed: %v", err)
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan file failed: %v", err)
	}

	if count != expected {
		t.Fatalf("line count mismatch, got=%d want=%d", count, expected)
	}
}

func TestSetDefaultDirBeforeInit(t *testing.T) {
	resetDefaultForTest(t)
	defer resetDefaultForTest(t)

	targetDir := filepath.Join(t.TempDir(), "before_init")
	if err := SetDefaultDir(targetDir); err != nil {
		t.Fatalf("SetDefaultDir failed: %v", err)
	}

	w, err := Default()
	if err != nil {
		t.Fatalf("Default failed: %v", err)
	}

	if got, want := w.Dir(), filepath.Clean(targetDir); got != want {
		t.Fatalf("default dir mismatch, got=%q want=%q", got, want)
	}

	if err := SaveLine("hello", "default_before"); err != nil {
		t.Fatalf("SaveLine failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(targetDir, "default_before.txt")); err != nil {
		t.Fatalf("expected file not found in target dir: %v", err)
	}
}

func TestSetDefaultDirAfterInit(t *testing.T) {
	resetDefaultForTest(t)
	defer resetDefaultForTest(t)

	w, err := Default()
	if err != nil {
		t.Fatalf("Default failed: %v", err)
	}
	oldDir := w.Dir()

	newDir := filepath.Join(t.TempDir(), "after_init")
	if err := SetDefaultDir(newDir); err != nil {
		t.Fatalf("SetDefaultDir failed: %v", err)
	}

	w2, err := Default()
	if err != nil {
		t.Fatalf("Default(second) failed: %v", err)
	}
	if w != w2 {
		t.Fatalf("expected same singleton instance")
	}
	if got, want := w2.Dir(), filepath.Clean(newDir); got != want {
		t.Fatalf("singleton dir mismatch, got=%q want=%q", got, want)
	}

	if err := w2.AppendLine("default_after", "switched"); err != nil {
		t.Fatalf("AppendLine failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(newDir, "default_after.txt")); err != nil {
		t.Fatalf("expected file not found in new dir: %v", err)
	}
	if oldDir != newDir {
		if _, err := os.Stat(filepath.Join(oldDir, "default_after.txt")); err == nil {
			t.Fatalf("file unexpectedly written to old dir")
		}
	}
}

func TestSetDirSwitchInstance(t *testing.T) {
	dir1 := filepath.Join(t.TempDir(), "dir1")
	dir2 := filepath.Join(t.TempDir(), "dir2")

	w, err := New(dir1)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer w.Close()

	if err := w.AppendLine("switch_file", "line-in-dir1"); err != nil {
		t.Fatalf("AppendLine(dir1) failed: %v", err)
	}

	if err := w.SetDir(dir2); err != nil {
		t.Fatalf("SetDir failed: %v", err)
	}
	if got, want := w.Dir(), filepath.Clean(dir2); got != want {
		t.Fatalf("dir mismatch after switch, got=%q want=%q", got, want)
	}

	if err := w.AppendLine("switch_file", "line-in-dir2"); err != nil {
		t.Fatalf("AppendLine(dir2) failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir1, "switch_file.txt")); err != nil {
		t.Fatalf("expected file not found in dir1: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir2, "switch_file.txt")); err != nil {
		t.Fatalf("expected file not found in dir2: %v", err)
	}
}

func TestPackageCloseAndReopen(t *testing.T) {
	resetDefaultForTest(t)
	defer resetDefaultForTest(t)

	targetDir := filepath.Join(t.TempDir(), "pkg_close_reopen")
	if err := SetDefaultDir(targetDir); err != nil {
		t.Fatalf("SetDefaultDir failed: %v", err)
	}

	if err := SaveLine("first", "pkg_close_case"); err != nil {
		t.Fatalf("SaveLine(first) failed: %v", err)
	}

	if err := Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if err := SaveLine("second", "pkg_close_case"); err != nil {
		t.Fatalf("SaveLine(second) failed: %v", err)
	}

	filePath := filepath.Join(targetDir, "pkg_close_case.txt")
	f, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("open file failed: %v", err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan file failed: %v", err)
	}

	if len(lines) != 2 || lines[0] != "first" || lines[1] != "second" {
		t.Fatalf("unexpected lines: %+v", lines)
	}
}
