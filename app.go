package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

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
	Type    string `json:"type,omitempty"` // "exact" (default) or "regex"
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

// ---------------------------------------------------------------------------
// Fast in-place asar patcher
//
// An asar archive is: [4B payload size][4B headerPickleLen][headerPickle][file data]
// and headerPickle is: [4B payload size][4B jsonLen][header JSON][padding].
// Every packed file records its byte offset inside the data section, so growing
// or shrinking one file only requires updating the header JSON and splicing the
// new bytes into the data section. That avoids extracting and repacking tens of
// thousands of files, which takes minutes on real GitKraken installs.
// ---------------------------------------------------------------------------

type asarFileRef struct {
	path            string
	offset          int64
	size            int64
	hasOffset       bool
	unpacked        bool
	offPos          int
	offQuoted       bool
	offRawLen       int
	sizePos         int
	sizeQuoted      bool
	sizeRawLen      int
	integrityPos    int
	integrityRawLen int
	integrityAlgo   string
	integrityBlock  int64
}

// asarSplice replaces text[pos:pos+oldLen] of the header JSON with text.
type asarSplice struct {
	pos    int
	oldLen int
	text   string
}

// asarSegment replaces the original bytes [start,end) of the data section.
type asarSegment struct {
	path  string
	start int64 // relative to the data section
	end   int64
	data  []byte
}

var errAsarNeedsFallback = errors.New("archive layout not supported by the in-place patcher")

func readAsarHeader(f *os.File) (headerJSON []byte, headerPickleLen int, dataStart int64, err error) {
	var sizeField [8]byte
	if _, err = io.ReadFull(io.NewSectionReader(f, 0, 8), sizeField[:]); err != nil {
		return nil, 0, 0, err
	}
	headerPickleLen = int(binary.LittleEndian.Uint32(sizeField[4:8]))
	if headerPickleLen <= 8 || headerPickleLen > 512<<20 {
		return nil, 0, 0, fmt.Errorf("implausible asar header size: %d", headerPickleLen)
	}
	headerBuf := make([]byte, headerPickleLen)
	if _, err = io.ReadFull(io.NewSectionReader(f, 8, int64(headerPickleLen)), headerBuf); err != nil {
		return nil, 0, 0, err
	}
	jsonLen := int(binary.LittleEndian.Uint32(headerBuf[4:8]))
	if jsonLen < 0 || 8+jsonLen > headerPickleLen {
		return nil, 0, 0, fmt.Errorf("implausible asar header json length: %d", jsonLen)
	}
	return headerBuf[8 : 8+jsonLen], headerPickleLen, 8 + int64(headerPickleLen), nil
}

// buildAsarHeaderPickle reproduces the exact pickle layout used by the archives
// GitKraken ships: [uint32 payloadLen][uint32 jsonLen][json][padding].
// getAsarHeaderSize() locks this down with a round-trip assertion.
func buildAsarHeaderPickle(j []byte) []byte {
	pad := (4 - len(j)%4) % 4
	out := make([]byte, 0, 8+len(j)+pad)
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(4+len(j)+pad))
	out = append(out, b[:]...)
	binary.LittleEndian.PutUint32(b[:], uint32(len(j)))
	out = append(out, b[:]...)
	out = append(out, j...)
	out = append(out, make([]byte, pad)...)
	return out
}

func buildAsarSizeField(headerPickleLen int) []byte {
	out := make([]byte, 8)
	binary.LittleEndian.PutUint32(out[0:4], 4)
	binary.LittleEndian.PutUint32(out[4:8], uint32(headerPickleLen))
	return out
}

// collectAsarEntries walks the header JSON keeping the original key order and
// recording the byte position of every "offset"/"size" value so they can be
// rewritten in place without re-serialising the whole document.
func collectAsarEntries(headerJSON []byte) ([]asarFileRef, error) {
	dec := json.NewDecoder(bytes.NewReader(headerJSON))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("asar header is not a JSON object")
	}
	var refs []asarFileRef
	if err := walkAsarObject(dec, "", headerJSON, &refs); err != nil {
		return nil, err
	}
	return refs, nil
}

func walkAsarObject(dec *json.Decoder, prefix string, headerJSON []byte, refs *[]asarFileRef) error {
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("unexpected object key %v", keyTok)
		}
		childPath := key
		switch {
		case key == "files":
			// Every asar directory nests its children under a literal "files"
			// object, so that key is never part of the archive path.
			childPath = prefix
		case prefix != "":
			childPath = prefix + "/" + key
		}

		before := dec.InputOffset()
		valTok, err := dec.Token()
		if err != nil {
			return err
		}
		after := dec.InputOffset()

		if d, ok := valTok.(json.Delim); ok {
			switch d {
			case '{':
				objStart := skipToValue(headerJSON, int(before))
				if err := walkAsarObject(dec, childPath, headerJSON, refs); err != nil {
					return err
				}
				if key == "integrity" {
					objEnd := int(dec.InputOffset())
					ref := ensureRef(refs, prefix)
					if ref != nil && objEnd > objStart {
						ref.integrityPos = objStart
						ref.integrityRawLen = objEnd - objStart
						parseIntegrityMeta(headerJSON[objStart:objEnd], ref)
					}
				}
			case '[':
				if err := skipJSONValue(dec); err != nil {
					return err
				}
			}
			continue
		}

		switch key {
		case "unpacked":
			if b, isBool := valTok.(bool); isBool && b {
				if ref := ensureRef(refs, prefix); ref != nil {
					ref.unpacked = true
				}
			}
			continue
		case "offset", "size":
		default:
			continue
		}

		pos := skipToValue(headerJSON, int(before))
		end := trimEndPos(headerJSON, int(after))
		if pos >= end {
			continue
		}
		raw := headerJSON[pos:end]
		if key == "offset" {
			if n, ok := parseJSONInt(raw); ok {
				if ref := ensureRef(refs, prefix); ref != nil {
					ref.offset = n
					ref.hasOffset = true
					ref.offPos = pos
					ref.offRawLen = end - pos
					ref.offQuoted = raw[0] == '"'
				}
			}
			continue
		}
		if n, ok := parseJSONInt(raw); ok {
			ref := ensureRef(refs, prefix)
			if ref != nil {
				ref.size = n
				ref.sizePos = pos
				ref.sizeRawLen = end - pos
				ref.sizeQuoted = raw[0] == '"'
			}
		}
	}
	_, err := dec.Token() // closing '}'
	return err
}

func trimEndPos(b []byte, pos int) int {
	for pos > 0 && (b[pos-1] == ' ' || b[pos-1] == '\n' || b[pos-1] == '\r' || b[pos-1] == '\t') {
		pos--
	}
	return pos
}

func findRef(refs *[]asarFileRef, path string) *asarFileRef {
	for i := range *refs {
		if (*refs)[i].path == path {
			return &(*refs)[i]
		}
	}
	return nil
}

func ensureRef(refs *[]asarFileRef, path string) *asarFileRef {
	if ref := findRef(refs, path); ref != nil {
		return ref
	}
	*refs = append(*refs, asarFileRef{
		path:         path,
		offPos:       -1,
		sizePos:      -1,
		integrityPos: -1,
	})
	return &(*refs)[len(*refs)-1]
}

// skipToValue advances past the key separator and any whitespace so the offset
// lands exactly on the first character of the value token.
func skipToValue(b []byte, pos int) int {
	for pos < len(b) {
		switch b[pos] {
		case ' ', '\t', '\r', '\n', ':':
			pos++
		default:
			return pos
		}
	}
	return pos
}

func parseIntegrityMeta(raw []byte, ref *asarFileRef) {
	var meta struct {
		Algorithm string `json:"algorithm"`
		BlockSize int64  `json:"blockSize"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return
	}
	if meta.Algorithm == "" {
		return
	}
	ref.integrityAlgo = meta.Algorithm
	ref.integrityBlock = meta.BlockSize
	if ref.integrityBlock <= 0 {
		ref.integrityBlock = 4 << 20
	}
}

// renderIntegrity rebuilds the integrity object for rewritten content. GitKraken
// ships per-file SHA256 integrity metadata, so it has to be recomputed or
// Electron refuses to load the patched file.
func renderIntegrity(algo string, blockSize int64, data []byte) string {
	sum := sha256.Sum256(data)
	var sb strings.Builder
	sb.WriteString(`{"algorithm":`)
	sb.WriteString(strconv.Quote(algo))
	sb.WriteString(`,"hash":"`)
	sb.WriteString(hex.EncodeToString(sum[:]))
	sb.WriteString(`","blockSize":`)
	sb.WriteString(strconv.FormatInt(blockSize, 10))
	sb.WriteString(`,"blocks":[`)
	for off := int64(0); off < int64(len(data)); off += blockSize {
		end := off + blockSize
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		if off > 0 {
			sb.WriteString(",")
		}
		blockSum := sha256.Sum256(data[off:end])
		sb.WriteString(`"`)
		sb.WriteString(hex.EncodeToString(blockSum[:]))
		sb.WriteString(`"`)
	}
	sb.WriteString(`]}`)
	return sb.String()
}

func skipJSONValue(dec *json.Decoder) error {
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '[', '{':
				depth++
			case ']', '}':
				depth--
				if depth <= 0 {
					return nil
				}
			}
		}
	}
}

func parseJSONInt(raw []byte) (int64, bool) {
	s := string(raw)
	if strings.HasPrefix(s, "\"") {
		s = strings.Trim(s, "\"")
	}
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func quoteInt(n int64, quoted bool) string {
	if quoted {
		return strconv.Quote(strconv.FormatInt(n, 10))
	}
	return strconv.FormatInt(n, 10)
}

func applyAsarSplices(headerJSON []byte, splices []asarSplice) []byte {
	sort.Slice(splices, func(i, j int) bool { return splices[i].pos < splices[j].pos })
	out := make([]byte, 0, len(headerJSON)+64)
	prev := 0
	for _, sp := range splices {
		if sp.pos < prev {
			continue
		}
		out = append(out, headerJSON[prev:sp.pos]...)
		out = append(out, sp.text...)
		prev = sp.pos + sp.oldLen
	}
	out = append(out, headerJSON[prev:]...)
	return out
}

func copyRange(dst io.Writer, src *os.File, start, end int64) error {
	if end <= start {
		return nil
	}
	if _, err := io.Copy(dst, io.NewSectionReader(src, start, end-start)); err != nil {
		return err
	}
	return nil
}

// applyPatchInPlace rewrites only the patched files inside the archive.
func (a *App) applyPatchInPlace(asarPath string, patch PatchFile) (PatchResult, error) {
	fail := func(msg string) (PatchResult, error) {
		a.emitProgress("error", msg, 0)
		return PatchResult{Success: false, Message: msg, Path: asarPath, Version: patch.Version}, nil
	}

	f, err := os.Open(asarPath)
	if err != nil {
		return fail("Cannot open app.asar: " + err.Error())
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fail("Cannot read app.asar: " + err.Error())
	}

	headerJSON, _, dataStart, err := readAsarHeader(f)
	if err != nil {
		return PatchResult{}, errAsarNeedsFallback
	}
	refs, err := collectAsarEntries(headerJSON)
	if err != nil {
		return PatchResult{}, errAsarNeedsFallback
	}
	byPath := make(map[string]*asarFileRef, len(refs))
	for i := range refs {
		byPath[refs[i].path] = &refs[i]
	}

	a.emitProgress("scan", "Reading archive index…", 20)

	// Group patches per file and rewrite each target once.
	type fileWork struct {
		ref      *asarFileRef
		patches  []Patch
		newBytes []byte
	}
	order := []string{}
	work := map[string]*fileWork{}
	for _, p := range patch.Patches {
		key := strings.TrimPrefix(strings.ReplaceAll(p.File, "\\", "/"), "./")
		w := work[key]
		if w == nil {
			ref := byPath[key]
			if ref == nil {
				return fail("File not found in archive: " + p.File)
			}
			w = &fileWork{ref: ref}
			work[key] = w
			order = append(order, key)
		}
		w.patches = append(w.patches, p)
	}

	segments := make([]asarSegment, 0, len(work))
	for _, key := range order {
		w := work[key]
		if w.ref.unpacked {
			return PatchResult{}, errAsarNeedsFallback
		}
		raw := make([]byte, w.ref.size)
		if _, err := io.ReadFull(io.NewSectionReader(f, dataStart+w.ref.offset, w.ref.size), raw); err != nil {
			return fail(fmt.Sprintf("Cannot read %s from archive: %s", key, err.Error()))
		}
		content := string(raw)
		if isAlreadyPatched(content) {
			return fail("Already patched! This asar has been modified before.")
		}
		for _, p := range w.patches {
			next, err := applyPatchToContent(content, p)
			if err != nil {
				return fail(fmt.Sprintf("%s: %s", p.File, err.Error()))
			}
			content = next
		}
		if !hasProReplace(content) {
			return fail("Patch verification failed — replacement not found")
		}
		w.newBytes = []byte(content)
		segments = append(segments, asarSegment{
			path:  key,
			start: w.ref.offset,
			end:   w.ref.offset + w.ref.size,
			data:  w.newBytes,
		})
	}

	sort.Slice(segments, func(i, j int) bool { return segments[i].start < segments[j].start })

	// Shift every entry that lives after a rewritten segment.
	splices := make([]asarSplice, 0, len(refs))
	for i := range refs {
		ref := &refs[i]
		if !ref.hasOffset || ref.unpacked {
			continue
		}
		delta := int64(0)
		for _, seg := range segments {
			if seg.end <= ref.offset {
				delta += int64(len(seg.data)) - (seg.end - seg.start)
			}
		}
		newOff := ref.offset + delta
		newSize := ref.size
		if ref.offset >= segments[0].start {
			for _, seg := range segments {
				if ref.offset == seg.start {
					newSize = int64(len(seg.data))
				}
			}
		}
		if newOff != ref.offset {
			splices = append(splices, asarSplice{
				pos:    ref.offPos,
				oldLen: ref.offRawLen,
				text:   quoteInt(newOff, ref.offQuoted),
			})
		}
		if newSize != ref.size && ref.sizePos >= 0 {
			splices = append(splices, asarSplice{
				pos:    ref.sizePos,
				oldLen: ref.sizeRawLen,
				text:   quoteInt(newSize, ref.sizeQuoted),
			})
		}
	}

	// Recompute the SHA256 integrity metadata of every rewritten file.
	for _, seg := range segments {
		ref := byPath[seg.path]
		if ref == nil || ref.integrityPos < 0 || ref.integrityAlgo == "" {
			continue
		}
		splices = append(splices, asarSplice{
			pos:    ref.integrityPos,
			oldLen: ref.integrityRawLen,
			text:   renderIntegrity(ref.integrityAlgo, ref.integrityBlock, seg.data),
		})
	}

	newHeaderJSON := applyAsarSplices(headerJSON, splices)
	if !json.Valid(newHeaderJSON) {
		return fail("Internal error: rebuilt asar header is not valid JSON")
	}
	newHeaderBuf := buildAsarHeaderPickle(newHeaderJSON)

	tmpPath := asarPath + ".tmp"
	a.emitProgress("write", "Writing patched archive…", 60)
	out, err := os.Create(tmpPath)
	if err != nil {
		return fail("Cannot create temp archive: " + err.Error())
	}
	if _, err = out.Write(buildAsarSizeField(len(newHeaderBuf))); err == nil {
		_, err = out.Write(newHeaderBuf)
	}
	cursor := dataStart
	for _, seg := range segments {
		if err == nil {
			err = copyRange(out, f, cursor, dataStart+seg.start)
		}
		if err == nil {
			_, err = out.Write(seg.data)
		}
		cursor = dataStart + seg.end
	}
	if err == nil {
		err = copyRange(out, f, cursor, info.Size())
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	// The original must be closed before the swap: Windows refuses to rename
	// over a file that is still open.
	if cerr := f.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmpPath)
		return fail("Write failed: " + err.Error())
	}

	a.emitProgress("verify", "Verifying patched archive…", 85)
	if err := verifyPatchedAsar(tmpPath, segments); err != nil {
		os.Remove(tmpPath)
		return fail("Verification failed: " + err.Error())
	}

	newStat, err := os.Stat(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return fail("Cannot stat patched archive: " + err.Error())
	}
	if newStat.Size() < 1000000 {
		os.Remove(tmpPath)
		return fail(fmt.Sprintf("Patched archive too small: %d bytes", newStat.Size()))
	}

	// The original bytes are still on disk untouched, so back up before swapping.
	if _, err := os.Stat(asarPath + ".old"); err != nil {
		a.emitProgress("backup", "Creating backup (app.asar.old)…", 92)
		if err := copyFile(asarPath, asarPath+".old"); err != nil {
			os.Remove(tmpPath)
			return fail("Backup failed: " + err.Error())
		}
	}

	a.emitProgress("install", "Installing patched archive…", 95)
	if err := replaceFile(tmpPath, asarPath); err != nil {
		os.Remove(tmpPath)
		if errors.Is(err, os.ErrPermission) {
			return fail("Cannot replace app.asar: the file is locked. Close GitKraken completely (check the tray icon and Task Manager) and try again.")
		}
		return fail("Install failed: " + err.Error())
	}

	msg := fmt.Sprintf("Applied %d patch(es)! New size: %.1f MB. Restart GitKraken.",
		len(patch.Patches), float64(newStat.Size())/1024/1024)
	a.emitProgress("done", msg, 100)
	return PatchResult{
		Success: true,
		Message: msg,
		NewSize: newStat.Size(),
		Path:    asarPath,
		Version: patch.Version,
	}, nil
}

func applyPatchToContent(content string, p Patch) (string, error) {
	if p.Type == "regex" {
		re, err := regexp.Compile(p.Find)
		if err != nil {
			return "", fmt.Errorf("invalid regex: %s", err.Error())
		}
		loc := re.FindStringIndex(content)
		if loc == nil {
			return "", fmt.Errorf("regex pattern not found in %s", p.File)
		}
		return content[:loc[0]] + p.Replace + content[loc[1]:], nil
	}
	idx := strings.Index(content, p.Find)
	if idx < 0 {
		return "", fmt.Errorf("pattern not found in %s", p.File)
	}
	return content[:idx] + p.Replace + content[idx+len(p.Find):], nil
}

// verifyPatchedAsar re-opens the produced archive and confirms every rewritten
// file is readable at its new offset and carries the replacement.
func verifyPatchedAsar(path string, segments []asarSegment) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	headerJSON, _, dataStart, err := readAsarHeader(f)
	if err != nil {
		return err
	}
	refs, err := collectAsarEntries(headerJSON)
	if err != nil {
		return err
	}
	byPath := make(map[string]*asarFileRef, len(refs))
	for i := range refs {
		byPath[refs[i].path] = &refs[i]
	}

	proRe := regexp.MustCompile(`\[\.\.\.[A-Za-z]+,"pro"\]`)
	for _, seg := range segments {
		ref := byPath[seg.path]
		if ref == nil {
			return fmt.Errorf("%s missing from the new header", seg.path)
		}
		if ref.size != int64(len(seg.data)) {
			return fmt.Errorf("size mismatch for %s: header %d, data %d", seg.path, ref.size, len(seg.data))
		}
		buf := make([]byte, ref.size)
		if _, err := io.ReadFull(io.NewSectionReader(f, dataStart+ref.offset, ref.size), buf); err != nil {
			return fmt.Errorf("cannot re-read %s: %s", seg.path, err.Error())
		}
		if !proRe.Match(buf) {
			return fmt.Errorf("%s does not contain the replacement", seg.path)
		}
	}
	return nil
}

// replaceFile swaps dst with src, falling back to a copy across volumes.
func replaceFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !isCrossDevice(err) {
		return err
	}
	if err := copyFile(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

func isCrossDevice(err error) bool {
	return errors.Is(err, syscall.EXDEV)
}

func (a *App) ApplyPatch(asarPath string, patch PatchFile) PatchResult {
	if _, err := os.Stat(asarPath); err != nil {
		a.emitProgress("error", "app.asar not found: "+err.Error(), 0)
		return PatchResult{Success: false, Message: "app.asar not found: " + err.Error(), Path: asarPath, Version: patch.Version}
	}

	a.emitProgress("start", "Preparing patch environment…", 5)

	res, err := a.applyPatchInPlace(asarPath, patch)
	if err == nil {
		return res
	}
	if !errors.Is(err, errAsarNeedsFallback) {
		return res
	}

	return a.applyPatchExtractRepack(asarPath, patch)
}

// applyPatchExtractRepack is the original, slow implementation. It is only used
// for archives whose layout the in-place patcher cannot handle.
func (a *App) applyPatchExtractRepack(asarPath string, patch PatchFile) PatchResult {
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
		switch p.Type {
		case "regex":
			re, err := regexp.Compile(p.Find)
			if err != nil {
				return fail(fmt.Sprintf("Invalid regex in patch for %s: %s", p.File, err.Error()))
			}
			loc := re.FindStringIndex(content)
			if loc == nil {
				return fail(fmt.Sprintf("Regex pattern not found in %s", p.File))
			}
			content = content[:loc[0]] + p.Replace + content[loc[1]:]
		default:
			if !strings.Contains(content, p.Find) {
				return fail(fmt.Sprintf("Pattern not found in %s", p.File))
			}
			idx := strings.Index(content, p.Find)
			content = content[:idx] + p.Replace + content[idx+len(p.Find):]
		}
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
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}
