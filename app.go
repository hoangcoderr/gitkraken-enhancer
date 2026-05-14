package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type AsarInfo struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Size    int64  `json:"size"`
}

type Patch struct {
	File    string `json:"file"`
	Find    string `json:"find"`
	Replace string `json:"replace"`
}

type PatchFile struct {
	Version string  `json:"version"`
	Patches []Patch `json:"patches"`
}

type PatchResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

var baseDir string

func init() {
	exe, _ := os.Executable()
	baseDir = filepath.Dir(exe)
	if _, err := os.Stat(filepath.Join(baseDir, "asar-helper.mjs")); err != nil {
		wd, _ := os.Getwd()
		baseDir = wd
	}
}

type App struct {
	ctx context.Context
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func findNode() string {
	if p := os.Getenv("NODE_PATH"); p != "" {
		if st, err := os.Stat(filepath.Join(p, "node.exe")); err == nil && !st.IsDir() {
			return filepath.Join(p, "node.exe")
		}
	}
	for _, p := range []string{
		"C:\\Program Files\\nodejs\\node.exe",
		"C:\\Program Files (x86)\\nodejs\\node.exe",
	} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	node, _ := exec.LookPath("node")
	return node
}

func runNode(args ...string) (string, error) {
	node := findNode()
	helper := filepath.Join(baseDir, "asar-helper.mjs")
	allArgs := append([]string{helper}, args...)
	cmd := exec.Command(node, allArgs...)
	cmd.Dir = baseDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("%s: %s", err.Error(), strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (a *App) DetectAsar() []AsarInfo {
	var results []AsarInfo
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData != "" {
		gkDir := filepath.Join(localAppData, "gitkraken")
		results = append(results, scanGitKrakenDir(gkDir)...)
	}
	programData := os.Getenv("ProgramData")
	if programData != "" {
		results = append(results, scanGitKrakenDir(filepath.Join(programData, "gitkraken"))...)
		userName := os.Getenv("USERNAME")
		if userName != "" {
			results = append(results, scanGitKrakenDir(filepath.Join(programData, userName, "gitkraken"))...)
		}
	}
	macPath := "/Applications/GitKraken.app/Contents/Resources/app.asar"
	if st, err := os.Stat(macPath); err == nil {
		results = append(results, AsarInfo{Path: macPath, Version: "mac", Size: st.Size()})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Version > results[j].Version
	})
	return results
}

func scanGitKrakenDir(gkDir string) []AsarInfo {
	var results []AsarInfo
	entries, err := os.ReadDir(gkDir)
	if err != nil {
		return results
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "app-") {
			continue
		}
		asarPath := filepath.Join(gkDir, e.Name(), "resources", "app.asar")
		st, err := os.Stat(asarPath)
		if err != nil {
			continue
		}
		re := regexp.MustCompile(`(\d+\.\d+\.\d+)`)
		m := re.FindStringSubmatch(e.Name())
		v := "?"
		if len(m) > 1 {
			v = m[1]
		}
		results = append(results, AsarInfo{
			Path:    asarPath,
			Version: v,
			Size:    st.Size(),
		})
	}
	return results
}

func (a *App) GetPatches() []PatchFile {
	var results []PatchFile
	pDir := filepath.Join(baseDir, "patches")
	entries, err := os.ReadDir(pDir)
	if err != nil {
		return results
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(pDir, e.Name()))
		if err != nil {
			continue
		}
		var pf PatchFile
		if json.Unmarshal(data, &pf) == nil && pf.Version != "" {
			results = append(results, pf)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Version > results[j].Version
	})
	return results
}

func (a *App) SelectAsar(path string) AsarInfo {
	st, err := os.Stat(path)
	if err != nil {
		return AsarInfo{}
	}
	re := regexp.MustCompile(`app[.-](\d+\.\d+\.\d+)`)
	m := re.FindStringSubmatch(path)
	v := ""
	if len(m) > 1 {
		v = m[1]
	}
	return AsarInfo{Path: path, Version: v, Size: st.Size()}
}

func (a *App) ApplyPatch(asarPath string, patch PatchFile) PatchResult {
	extractDir, err := os.MkdirTemp("", "gk-extract-")
	if err != nil {
		return PatchResult{false, "Failed to create temp dir: " + err.Error()}
	}
	defer os.RemoveAll(extractDir)

	if out, err := runNode("extract", asarPath, extractDir); err != nil {
		return PatchResult{false, "Extract failed: " + err.Error() + "\n" + out}
	}

	for _, p := range patch.Patches {
		fp := filepath.Join(extractDir, p.File)
		data, err := os.ReadFile(fp)
		if err != nil {
			return PatchResult{false, fmt.Sprintf("File not found: %s", p.File)}
		}
		content := string(data)
		if strings.Contains(content, "[...Ve,\"pro\"]") || strings.Contains(content, "[...We,\"pro\"]") {
			return PatchResult{false, "Already patched! This asar has been modified before"}
		}
		if !strings.Contains(content, p.Find) {
			return PatchResult{false, fmt.Sprintf("Pattern not found in %s", p.File)}
		}
		idx := strings.Index(content, p.Find)
		content = content[:idx] + p.Replace + content[idx+len(p.Find):]
		if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
			return PatchResult{false, fmt.Sprintf("Write failed: %s", err.Error())}
		}
		// Verify patch was applied
		if !strings.Contains(content, "[...Ve,\"pro\"]") {
			return PatchResult{false, "Patch verification failed - replacement not found"}
		}
	}

	oldPath := asarPath + ".old"
	tmpAsar := asarPath + ".tmp"
	if _, err := os.Stat(oldPath); err != nil {
		if err := copyFile(asarPath, oldPath); err != nil {
			return PatchResult{false, "Backup failed: " + err.Error()}
		}
	}

	if out, err := runNode("pack", extractDir, tmpAsar); err != nil {
		os.Remove(tmpAsar)
		return PatchResult{false, "Repack failed: " + err.Error() + "\n" + out}
	}

	// Verify tmpAsar was created
	st, err := os.Stat(tmpAsar)
	if err != nil {
		return PatchResult{false, "Tmp asar not created"}
	}
	if st.Size() < 1000000 {
		return PatchResult{false, fmt.Sprintf("Tmp asar too small: %d bytes", st.Size())}
	}

	os.Remove(asarPath)
	if err := os.Rename(tmpAsar, asarPath); err != nil {
		return PatchResult{false, "Rename failed: " + err.Error()}
	}

	return PatchResult{true, fmt.Sprintf("Applied %d patch(es)! New size: %.1f MB. Restart GitKraken.", len(patch.Patches), float64(st.Size())/1024/1024)}
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
