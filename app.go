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

	"github.com/wailsapp/wails/v2/pkg/runtime"
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
	NewSize int64  `json:"newSize"`
	Path    string `json:"path"`
	Version string `json:"version"`
}

var baseDir string

func init() {
	exe, _ := os.Executable()
	baseDir = filepath.Dir(exe)
	if _, err := os.Stat(filepath.Join(baseDir, "patches")); err != nil {
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

func (a *App) DetectAsar() []AsarInfo {
	var results []AsarInfo
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData != "" {
		results = append(results, scanGitKrakenDir(filepath.Join(localAppData, "gitkraken"))...)
	}
	programData := os.Getenv("ProgramData")
	if programData != "" {
		results = append(results, scanGitKrakenDir(filepath.Join(programData, "gitkraken"))...)
		if userName := os.Getenv("USERNAME"); userName != "" {
			results = append(results, scanGitKrakenDir(filepath.Join(programData, userName, "gitkraken"))...)
		}
	}
	if st, err := os.Stat("/Applications/GitKraken.app/Contents/Resources/app.asar"); err == nil {
		results = append(results, AsarInfo{Path: "/Applications/GitKraken.app/Contents/Resources/app.asar", Version: "mac", Size: st.Size()})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Version > results[j].Version })
	return results
}

func scanGitKrakenDir(gkDir string) []AsarInfo {
	var results []AsarInfo
	entries, err := os.ReadDir(gkDir)
	if err != nil {
		return results
	}
	re := regexp.MustCompile(`(\d+\.\d+\.\d+)`)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "app-") {
			continue
		}
		asarPath := filepath.Join(gkDir, e.Name(), "resources", "app.asar")
		st, err := os.Stat(asarPath)
		if err != nil {
			continue
		}
		v := "?"
		if m := re.FindStringSubmatch(e.Name()); len(m) > 1 {
			v = m[1]
		}
		results = append(results, AsarInfo{Path: asarPath, Version: v, Size: st.Size()})
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
	sort.Slice(results, func(i, j int) bool { return results[i].Version > results[j].Version })
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

// BrowseAsar opens a native file dialog for selecting an app.asar file.
// (window.runtime.OpenFileDialog is not available from the frontend in Wails v2.)
func (a *App) BrowseAsar() AsarInfo {
	if a.ctx == nil {
		return AsarInfo{}
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select GitKraken app.asar",
		Filters: []runtime.FileFilter{
			{DisplayName: "ASAR Archive (*.asar)", Pattern: "*.asar"},
			{DisplayName: "All Files", Pattern: "*.*"},
		},
	})
	if err != nil || path == "" {
		return AsarInfo{}
	}
	return a.SelectAsar(path)
}

func (a *App) emitProgress(stage, message string, percent int) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, "patch:progress", map[string]interface{}{
		"stage":   stage,
		"message": message,
		"percent": percent,
	})
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

func ensureAsarModule(node string) (string, error) {
	helperEmbed := filepath.Join(baseDir, "asar-helper.mjs")

	helperContent := `const m = await import('@electron/asar');
const [,, action, src, dest] = process.argv;
try {
  if (action === 'extract') await m.extractAll(src, dest);
  else if (action === 'pack') await m.createPackageWithOptions(src, dest, {});
  process.exit(0);
} catch (e) {
  console.error(e.message);
  process.exit(1);
}`

	if _, err := os.Stat(helperEmbed); err != nil {
		os.WriteFile(helperEmbed, []byte(helperContent), 0644)
	}

	bundledModule := filepath.Join(baseDir, "node_modules", "@electron", "asar")
	if st, err := os.Stat(bundledModule); err == nil && st.IsDir() {
		return helperEmbed, nil
	}

	if node == "" {
		return "", fmt.Errorf("Node.js not found. Please install Node.js")
	}

	npmDir := filepath.Join(os.TempDir(), "gk-npm-install")
	if _, err := os.Stat(filepath.Join(npmDir, "node_modules", "@electron", "asar")); err != nil {
		os.MkdirAll(npmDir, 0755)
		helperDest := filepath.Join(npmDir, "asar-helper.mjs")
		os.WriteFile(helperDest, []byte(helperContent), 0644)
		jsonContent := []byte(`{"name":"gk-patch","private":true}`)
		os.WriteFile(filepath.Join(npmDir, "package.json"), jsonContent, 0644)

		npm, _ := exec.LookPath("npm")
		if npm == "" {
			npx, _ := exec.LookPath("npx")
			if npx == "" {
				return "", fmt.Errorf("Cannot find npm or npx. Install Node.js or bundle @electron/asar manually")
			}
			cmd := exec.Command(npx, "--yes", "@electron/asar")
			cmd.Dir = npmDir
			cmd.Run()
		} else {
			cmd := exec.Command(npm, "install", "@electron/asar")
			cmd.Dir = npmDir
			out, err := cmd.CombinedOutput()
			if err != nil {
				os.RemoveAll(npmDir)
				return "", fmt.Errorf("npm install failed: %s: %s", err.Error(), strings.TrimSpace(string(out)))
			}
		}
	}

	helperFinal := filepath.Join(npmDir, "asar-helper.mjs")
	if _, err := os.Stat(helperFinal); err != nil {
		os.WriteFile(helperFinal, []byte(helperContent), 0644)
	}
	return helperFinal, nil
}

var proFeatureRe = regexp.MustCompile(`\[\.\.\.[A-Za-z]+,"pro"\]`)

func isAlreadyPatched(content string) bool {
	return proFeatureRe.MatchString(content)
}

func hasProReplace(content string) bool {
	return proFeatureRe.MatchString(content)
}

func (a *App) ApplyPatch(asarPath string, patch PatchFile) PatchResult {
	fail := func(msg string) PatchResult {
		a.emitProgress("error", msg, 0)
		return PatchResult{Success: false, Message: msg, Path: asarPath, Version: patch.Version}
	}

	a.emitProgress("start", "Preparing patch environment…", 5)

	node := findNode()
	asarHelper, err := ensureAsarModule(node)
	if err != nil {
		return fail(err.Error())
	}

	tmpWork := filepath.Dir(asarHelper)
	dirName := strings.TrimSuffix(filepath.Base(asarPath), ".asar")
	extractDir := filepath.Join(tmpWork, dirName)
	// Clean previous extract so we never patch a stale tree
	os.RemoveAll(extractDir)

	runNode := func(args ...string) (string, error) {
		allArgs := append([]string{asarHelper}, args...)
		cmd := exec.Command(node, allArgs...)
		cmd.Dir = tmpWork
		out, err := cmd.CombinedOutput()
		if err != nil {
			return strings.TrimSpace(string(out)), fmt.Errorf("%s: %s", err.Error(), strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	}

	a.emitProgress("extract", "Extracting app.asar (this may take a while)…", 15)
	if out, err := runNode("extract", asarPath, extractDir); err != nil {
		return fail("Extract failed: " + err.Error() + "\n" + out)
	}

	a.emitProgress("patch", fmt.Sprintf("Applying %d patch(es) for v%s…", len(patch.Patches), patch.Version), 45)
	for i, p := range patch.Patches {
		fp := filepath.Join(extractDir, p.File)
		// Windows asar may use backslashes in the archive; try both
		data, err := os.ReadFile(fp)
		if err != nil {
			alt := filepath.Join(extractDir, filepath.FromSlash(p.File))
			data, err = os.ReadFile(alt)
			if err != nil {
				return fail(fmt.Sprintf("File not found: %s", p.File))
			}
			fp = alt
		}
		content := string(data)
		if isAlreadyPatched(content) {
			return fail("Already patched! This asar has been modified before.")
		}
		if !strings.Contains(content, p.Find) {
			return fail(fmt.Sprintf("Pattern not found in %s", p.File))
		}
		idx := strings.Index(content, p.Find)
		content = content[:idx] + p.Replace + content[idx+len(p.Find):]
		if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
			return fail(fmt.Sprintf("Write failed: %s", err.Error()))
		}
		if !hasProReplace(content) {
			return fail("Patch verification failed — replacement not found")
		}
		n := len(patch.Patches)
		if n < 1 {
			n = 1
		}
		pct := 45 + ((i + 1) * 15 / n)
		a.emitProgress("patch", fmt.Sprintf("Patched %s", p.File), pct)
	}

	a.emitProgress("backup", "Creating backup (app.asar.old)…", 65)
	oldPath := asarPath + ".old"
	if _, err := os.Stat(oldPath); err != nil {
		if err := copyFile(asarPath, oldPath); err != nil {
			return fail("Backup failed: " + err.Error())
		}
	}

	a.emitProgress("pack", "Repacking app.asar…", 75)
	tmpAsar := asarPath + ".tmp"
	if out, err := runNode("pack", extractDir, tmpAsar); err != nil {
		os.Remove(tmpAsar)
		return fail("Repack failed: " + err.Error() + "\n" + out)
	}

	st, err := os.Stat(tmpAsar)
	if err != nil {
		return fail("Tmp asar not created")
	}
	if st.Size() < 1000000 {
		os.Remove(tmpAsar)
		return fail(fmt.Sprintf("Tmp asar too small: %d bytes", st.Size()))
	}

	a.emitProgress("install", "Installing patched asar…", 90)
	os.Remove(asarPath)
	if err := os.Rename(tmpAsar, asarPath); err != nil {
		// Fallback copy if rename fails across volumes
		if err2 := copyFile(tmpAsar, asarPath); err2 != nil {
			return fail("Rename failed: " + err.Error())
		}
		os.Remove(tmpAsar)
	}

	// Best-effort cleanup of extract dir
	go os.RemoveAll(extractDir)

	msg := fmt.Sprintf("Applied %d patch(es)! New size: %.1f MB. Restart GitKraken.", len(patch.Patches), float64(st.Size())/1024/1024)
	a.emitProgress("done", msg, 100)
	return PatchResult{
		Success: true,
		Message: msg,
		NewSize: st.Size(),
		Path:    asarPath,
		Version: patch.Version,
	}
}



func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
