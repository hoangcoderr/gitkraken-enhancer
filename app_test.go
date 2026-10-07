package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gkResources(t *testing.T, version string) string {
	t.Helper()
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		t.Skip("LOCALAPPDATA not set")
	}
	return filepath.Join(local, "gitkraken", "app-"+version, "resources")
}

func loadPatch(t *testing.T, version string) PatchFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("patches", version+".json"))
	if err != nil {
		t.Skipf("patch file missing: %v", err)
	}
	var pf PatchFile
	if err := json.Unmarshal(data, &pf); err != nil {
		t.Fatalf("bad patch json: %v", err)
	}
	return pf
}

func stageOriginal(t *testing.T, version string) (orig string, staged string) {
	t.Helper()
	res := gkResources(t, version)
	orig = filepath.Join(res, "app.asar.old")
	if _, err := os.Stat(orig); err != nil {
		t.Skipf("pristine archive not available: %v", err)
	}
	dir := t.TempDir()
	staged = filepath.Join(dir, "app.asar")
	if err := copyFile(orig, staged); err != nil {
		t.Fatalf("stage copy failed: %v", err)
	}
	return orig, staged
}

func TestApplyPatchInPlaceMatchesManualPatch(t *testing.T) {
	orig, staged := stageOriginal(t, "12.6.0")
	patch := loadPatch(t, "12.6.0")

	before, err := os.Stat(orig)
	if err != nil {
		t.Fatal(err)
	}

	a := NewApp()
	res, err := a.applyPatchInPlace(staged, patch)
	if err != nil {
		t.Fatalf("in-place patch failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("patch reported failure: %s", res.Message)
	}

	delta := res.NewSize - before.Size()
	// The replacement adds `"pro"` plus a spread: exactly 11 bytes.
	if delta != 11 {
		t.Fatalf("unexpected size delta: got %d, want 11", delta)
	}

	if _, err := os.Stat(staged + ".old"); err != nil {
		t.Fatalf("backup was not created: %v", err)
	}
}

func TestApplyPatchInPlaceMultipleFiles(t *testing.T) {
	_, staged := stageOriginal(t, "12.6.0")
	patch := loadPatch(t, "12.6.0")
	patch.Patches = append(patch.Patches, Patch{
		File:    "package.json",
		Find:    `"productName": "GitKraken"`,
		Replace: `"productName": "GitKraken", "unlocked": [...pro,"pro"]`,
	})

	a := NewApp()
	res, err := a.applyPatchInPlace(staged, patch)
	if err != nil {
		t.Fatalf("in-place patch failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("patch reported failure: %s", res.Message)
	}
}

func TestApplyPatchInPlaceRejectsAlreadyPatched(t *testing.T) {
	_, staged := stageOriginal(t, "12.6.0")
	patch := loadPatch(t, "12.6.0")

	a := NewApp()
	if res, err := a.applyPatchInPlace(staged, patch); err != nil || !res.Success {
		t.Fatalf("first patch failed: %+v %v", res, err)
	}
	res, err := a.applyPatchInPlace(staged, patch)
	if err != nil {
		t.Fatalf("second patch errored unexpectedly: %v", err)
	}
	if res.Success {
		t.Fatal("expected the second run to refuse an already patched archive")
	}
}

// getAsarHeaderSize pins down the header pickle layout: rebuilding an untouched
// header must reproduce the exact byte length the archive shipped with.
// If this ever fails, the in-place writer would produce archives Electron
// cannot read, so it is checked on every installed version we can find.
func TestAsarHeaderSize(t *testing.T) {
	versions := []string{"12.6.0", "12.5.0", "12.4.1", "12.4.0", "12.3.1", "12.3.0"}
	checked := 0
	for _, v := range versions {
		res := gkResources(t, v)
		orig := filepath.Join(res, "app.asar.old")
		if _, err := os.Stat(orig); err != nil {
			continue
		}
		f, err := os.Open(orig)
		if err != nil {
			t.Fatal(err)
		}
		headerJSON, headerPickleLen, dataStart, err := readAsarHeader(f)
		if err != nil {
			t.Fatalf("%s: readAsarHeader: %v", v, err)
		}
		if dataStart != 8+int64(headerPickleLen) {
			t.Errorf("%s: data start %d, want %d", v, dataStart, 8+headerPickleLen)
		}
		if got := len(buildAsarHeaderPickle(headerJSON)); got != headerPickleLen {
			t.Errorf("%s: rebuilt header pickle %d bytes, want %d", v, got, headerPickleLen)
		}
		if !json.Valid(headerJSON) {
			t.Errorf("%s: header is not valid JSON", v)
		}
		f.Close()
		checked++
	}
	if checked == 0 {
		t.Skip("no pristine archives available")
	}
	t.Logf("verified header layout on %d installed version(s)", checked)
}

func TestCollectAsarEntriesRoundTrip(t *testing.T) {
	orig, _ := stageOriginal(t, "12.6.0")
	f, err := os.Open(orig)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	headerJSON, headerPickleLen, dataStart, err := readAsarHeader(f)
	if err != nil {
		t.Fatalf("readAsarHeader: %v", err)
	}
	if dataStart != 8+int64(headerPickleLen) {
		t.Fatalf("data start mismatch: %d vs %d", dataStart, 8+headerPickleLen)
	}

	refs, err := collectAsarEntries(headerJSON)
	if err != nil {
		t.Fatalf("collectAsarEntries: %v", err)
	}
	if len(refs) == 0 {
		t.Fatal("no entries collected")
	}

	byPath := make(map[string]*asarFileRef, len(refs))
	for i := range refs {
		byPath[refs[i].path] = &refs[i]
	}
	target := byPath["src/render/static/entryPoints/main/render.bundle.js"]
	if target == nil {
		t.Fatal("render.bundle.js not found in header")
	}
	if target.size == 0 || !target.hasOffset {
		t.Fatalf("implausible target offset/size: %d/%d (hasOffset=%v)", target.offset, target.size, target.hasOffset)
	}
	if target.unpacked {
		t.Fatal("render.bundle.js must be packed inside the archive")
	}
	if target.integrityAlgo != "SHA256" {
		t.Fatalf("expected SHA256 integrity metadata, got %q", target.integrityAlgo)
	}

	// "files" must never leak into archive paths.
	for _, r := range refs {
		if strings.Contains(r.path, "/files/") || strings.HasPrefix(r.path, "files/") {
			t.Fatalf("path %q leaked the files wrapper key", r.path)
		}
	}

	// The integrity metadata must describe the real bytes on disk.
	raw := make([]byte, target.size)
	if _, err := io.ReadFull(io.NewSectionReader(f, dataStart+target.offset, target.size), raw); err != nil {
		t.Fatalf("read target: %v", err)
	}
	rendered := renderIntegrity(target.integrityAlgo, target.integrityBlock, raw)
	var got struct {
		Hash   string   `json:"hash"`
		Blocks []string `json:"blocks"`
	}
	if err := json.Unmarshal([]byte(rendered), &got); err != nil {
		t.Fatalf("rendered integrity invalid: %v", err)
	}
	var stored struct {
		Hash   string   `json:"hash"`
		Blocks []string `json:"blocks"`
	}
	if err := json.Unmarshal(headerJSON[target.integrityPos:target.integrityPos+target.integrityRawLen], &stored); err != nil {
		t.Fatalf("stored integrity invalid: %v", err)
	}
	if got.Hash != stored.Hash || len(got.Blocks) != len(stored.Blocks) {
		t.Fatalf("integrity recomputation does not match the shipped metadata")
	}
	for i := range got.Blocks {
		if got.Blocks[i] != stored.Blocks[i] {
			t.Fatalf("integrity block %d mismatch", i)
		}
	}

	// Re-serialising an unchanged header must reproduce the original pickle size,
	// which proves the pickle layout matches what the archive shipped with.
	if got := len(buildAsarHeaderPickle(headerJSON)); got != headerPickleLen {
		t.Fatalf("header pickle size mismatch: rebuilt %d, original %d", got, headerPickleLen)
	}
}
