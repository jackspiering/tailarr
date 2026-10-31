package deploy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"unicode"

	"github.com/jackspiering/tailarr/internal/scaletail"
	"github.com/jackspiering/tailarr/internal/security/paths"
	"github.com/jackspiering/tailarr/internal/security/redact"
)

// Limits for the Apply preview. The preview is advisory, so it prints a
// capped summary and never allocates in proportion to hostile input.
const (
	// previewReadLimit bounds each file the preview reads.
	previewReadLimit = 1 << 20
	// previewDiffMaxLines caps the printed compose diff.
	previewDiffMaxLines = 120
	// previewUpgradeMaxLines caps the printed Upgrading section.
	previewUpgradeMaxLines = 40
	// previewListMaxLines caps each printed list: files, warnings, and keys.
	previewListMaxLines = 40
	// previewDiffMaxInput is the most lines on one side that the diff accepts.
	previewDiffMaxInput = 5000
	// previewDiffMaxCells bounds the LCS table, after the common prefix and
	// suffix are cut off.
	previewDiffMaxCells = 1 << 20
	// previewContext is the number of unchanged lines around each hunk.
	previewContext = 3
)

// previewFn is the preview entry point. Tests replace it to see whether Apply
// calls it.
var previewFn = (*Manager).previewApply

// previewANSIRE matches CSI and OSC escape sequences. It repeats the pattern
// of ansiRE in internal/ui, because ui imports deploy and deploy cannot import
// ui. Keep the two in step.
var previewANSIRE = regexp.MustCompile(`\x1b(\[[0-9;?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\))`)

// previewSecretLineRE finds a "KEY = value" or "KEY: value" prefix on a line.
// It also takes a leading list dash and quotes.
var previewSecretLineRE = regexp.MustCompile(`^(\s*(?:-\s+)?"?)([A-Za-z0-9_.]+)("?\s*[=:]\s*)\S`)

// previewFileChange is one template file that Apply would create or change.
type previewFileChange struct {
	rel  string
	kind string
	note string
}

// previewApply prints a read-only summary of what Apply would change in the
// deployment at service. It holds no lock and never fails: it reads files
// that containers can write, so a problem becomes a warning line and Apply
// goes on to ask.
func (m *Manager) previewApply(service, dest string) {
	lines := m.previewLines(service, dest)
	for _, l := range lines {
		m.UI.Printf("%s\n", previewClean(l))
	}
}

// previewLines builds the preview text for service.
func (m *Manager) previewLines(service, dest string) []string {
	templateDir, err := m.catalogTemplate(service)
	if err != nil {
		return []string{"Warning: Apply preview is not available: " + err.Error()}
	}

	var out []string
	files, warns, composeDiff, composeNote := previewTemplateFiles(templateDir, dest)
	keys, envWarn := previewNewEnvKeys(templateDir, dest)
	if envWarn != "" {
		warns = append(warns, envWarn)
	}

	out = append(out, fmt.Sprintf("Apply preview for %s (read only, nothing has changed yet):", service))
	switch {
	case len(files) == 0 && len(keys) == 0 && len(warns) == 0:
		out = append(out, "No template changes. Apply still pulls images and recreates containers.")
	case len(files) == 0:
		out = append(out, "No template file changes.")
	default:
		out = append(out, "Template files Apply would write (.env is never replaced):")
		out = append(out, capLines(previewFileLines(files), previewListMaxLines, "files")...)
	}
	if composeNote != "" {
		out = append(out, composeNote)
	}
	out = append(out, composeDiff...)
	if len(keys) > 0 {
		out = append(out, "New .env variables that Apply adds (names only, values are never shown):")
		list := make([]string, len(keys))
		for i, k := range keys {
			list[i] = "  " + k
		}
		out = append(out, capLines(list, previewListMaxLines, "variables")...)
	}
	if len(files) > 0 {
		out = append(out, previewUpgrading(templateDir)...)
	}
	out = append(out, capLines(warns, previewListMaxLines, "warnings")...)
	return out
}

// previewFileLines formats the changed-file list.
func previewFileLines(files []previewFileChange) []string {
	out := make([]string, len(files))
	for i, f := range files {
		l := fmt.Sprintf("  %-8s %s", f.kind, f.rel)
		if f.note != "" {
			l += " (" + f.note + ")"
		}
		out[i] = l
	}
	return out
}

// previewTemplateFiles walks the template the way syncTemplateFiles does and
// compares each file with the deployed one. It returns the files Apply would
// create or change, warnings for paths Apply would refuse or the preview
// could not read, and the compose diff lines and note.
func previewTemplateFiles(templateDir, dest string) (files []previewFileChange, warns, composeDiff []string, composeNote string) {
	tplCompose := composeBaseName(templateDir)
	active := previewActiveCompose(dest)
	renamed := active != tplCompose
	if renamed {
		composeNote = fmt.Sprintf("Compose file changes from %s to %s. The two files are not compared.", active, tplCompose)
	}
	walkErr := filepath.Walk(templateDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(templateDir, path)
		if err != nil {
			return err
		}
		if rel == "." || rel == ".env" {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			warns = append(warns, fmt.Sprintf("Warning: template path is a symlink, Apply will refuse it: %s", rel))
			return nil
		}
		target := filepath.Join(dest, rel)
		if err := paths.RefuseSymlinkAncestry(filepath.Dir(target)); err != nil {
			warns = append(warns, fmt.Sprintf("Warning: %s: %v", rel, err))
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		destInfo, err := os.Lstat(target)
		if os.IsNotExist(err) {
			if !info.IsDir() {
				files = append(files, previewFileChange{rel: rel, kind: "new"})
			}
			return nil
		}
		if err != nil {
			warns = append(warns, fmt.Sprintf("Warning: cannot inspect %s: %v", rel, err))
			return nil
		}
		switch {
		case destInfo.Mode()&os.ModeSymlink != 0:
			warns = append(warns, fmt.Sprintf("Warning: deployed %s is a symlink, Apply will refuse it", rel))
			return skipDirIf(info)
		case info.IsDir() && !destInfo.IsDir():
			warns = append(warns, fmt.Sprintf("Warning: template %s is a directory but the deployed path is a file, Apply will fail", rel))
			return filepath.SkipDir
		case info.IsDir():
			return nil
		case destInfo.IsDir():
			warns = append(warns, fmt.Sprintf("Warning: template %s is a file but the deployed path is a directory, Apply will fail", rel))
			return nil
		case !destInfo.Mode().IsRegular():
			warns = append(warns, fmt.Sprintf("Warning: deployed %s is not a regular file, Apply will refuse it", rel))
			return nil
		}

		want, wantTrunc, err := readPreviewFile(path)
		if err != nil {
			warns = append(warns, fmt.Sprintf("Warning: cannot read template %s: %v", rel, err))
			return nil
		}
		have, haveTrunc, err := readPreviewFile(target)
		if err != nil {
			warns = append(warns, fmt.Sprintf("Warning: cannot read deployed %s: %v", rel, err))
			return nil
		}
		contentDiffers := wantTrunc || haveTrunc || !bytes.Equal(want, have)
		modeDiffers := info.Mode().Perm() != destInfo.Mode().Perm()
		if !contentDiffers && !modeDiffers {
			return nil
		}
		change := previewFileChange{rel: rel, kind: "changed"}
		if !contentDiffers {
			change.kind = "mode"
			change.note = fmt.Sprintf("%04o to %04o", destInfo.Mode().Perm(), info.Mode().Perm())
		}
		files = append(files, change)
		if contentDiffers && rel == tplCompose && !renamed {
			composeDiff = previewComposeDiff(rel, have, want, haveTrunc || wantTrunc)
		}
		return nil
	})
	if walkErr != nil {
		warns = append(warns, fmt.Sprintf("Warning: cannot read the template: %v", walkErr))
	}
	return files, warns, composeDiff, composeNote
}

// skipDirIf skips the directory that info describes and ignores a file.
func skipDirIf(info os.FileInfo) error {
	if info.IsDir() {
		return filepath.SkipDir
	}
	return nil
}

// previewActiveCompose returns the compose file the deployment in dest runs.
// It follows activeComposeFile but reads the managed override without
// following a symlink or blocking on a FIFO, because containers can write to
// the deployment.
func previewActiveCompose(dest string) string {
	data, _, err := readPreviewFile(filepath.Join(dest, overrideFilename))
	if err == nil {
		if sub := composeFileMarkerRE.FindSubmatch(data); sub != nil {
			name := string(sub[1])
			if slices.Contains(scaletail.ComposeCandidates, name) {
				if info, err := os.Lstat(filepath.Join(dest, name)); err == nil && info.Mode().IsRegular() {
					return name
				}
			}
		}
	}
	return composeBaseName(dest)
}

// previewNewEnvKeys returns the names of variables that the template .env has
// and the deployed .env lacks, in template order. It never returns a value.
func previewNewEnvKeys(templateDir, dest string) (keys []string, warn string) {
	tplData, _, err := readPreviewFile(filepath.Join(templateDir, ".env"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ""
		}
		return nil, fmt.Sprintf("Warning: cannot read the template .env: %v", err)
	}
	_, tplKeys, err := scaletail.ParseEnv(bytes.NewReader(tplData))
	if err != nil {
		return nil, fmt.Sprintf("Warning: cannot parse the template .env: %v", err)
	}
	have := map[string]bool{}
	haveData, _, err := readPreviewFile(filepath.Join(dest, ".env"))
	switch {
	case err == nil:
		haveMap, _, perr := scaletail.ParseEnv(bytes.NewReader(haveData))
		if perr != nil {
			return nil, fmt.Sprintf("Warning: cannot parse the deployed .env: %v", perr)
		}
		for k := range haveMap {
			have[k] = true
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, fmt.Sprintf("Warning: cannot read the deployed .env: %v", err)
	}
	for _, k := range tplKeys {
		if !have[k] {
			keys = append(keys, k)
		}
	}
	return keys, ""
}

// readPreviewFile reads up to previewReadLimit bytes of the regular file at
// path. It opens the file without following a final symlink and without
// blocking, so a symlink or FIFO planted in the deployment fails instead of
// redirecting the read or hanging Apply. truncated reports a larger file.
func readPreviewFile(path string) (data []byte, truncated bool, err error) {
	f, err := paths.OpenFileNoFollow(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false, symlinkHint(err, path)
	}
	defer func() { _ = f.Close() }()
	if err := requireRegular(f, path); err != nil {
		return nil, false, err
	}
	data, err = io.ReadAll(io.LimitReader(f, previewReadLimit+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > previewReadLimit {
		return data[:previewReadLimit], true, nil
	}
	return data, false, nil
}

// previewComposeDiff returns the printed diff of the compose file rel, from
// the deployed text have to the catalog text want.
func previewComposeDiff(rel string, have, want []byte, truncated bool) []string {
	head := []string{
		fmt.Sprintf("Compose file %s, deployed (-) compared with catalog (+):", rel),
		"Apply overwrites the deployed file. Lines marked - are removed and lines marked + are added.",
	}
	if truncated {
		return append(head, "  The file is larger than 1 MiB, so it is too large to diff.")
	}
	body, tooLarge := lineDiff(splitLines(have), splitLines(want))
	switch {
	case tooLarge:
		body = []string{"  The change is too large to diff."}
	case len(body) == 0:
		body = []string{"  Only line endings differ."}
	}
	return append(head, capLines(body, previewDiffMaxLines, "lines")...)
}

// splitLines splits data into lines and drops a trailing carriage return.
func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// diffOp is one line of an edit script: kind is ' ', '-' or '+'.
type diffOp struct {
	kind byte
	text string
	a, b int
}

// lineDiff returns a unified-style diff from a to b with hunk headers. It
// returns tooLarge when the input is over the size bounds, and no lines when
// the two are equal.
func lineDiff(a, b []string) (out []string, tooLarge bool) {
	if len(a) > previewDiffMaxInput || len(b) > previewDiffMaxInput {
		return nil, true
	}
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	if len(ma) == 0 && len(mb) == 0 {
		return nil, false
	}
	if (len(ma)+1)*(len(mb)+1) > previewDiffMaxCells {
		return nil, true
	}

	ops := make([]diffOp, 0, len(a)+len(b))
	for i := 0; i < pre; i++ {
		ops = append(ops, diffOp{' ', a[i], i + 1, i + 1})
	}
	for _, op := range lcsOps(ma, mb) {
		op.a += pre
		op.b += pre
		ops = append(ops, op)
	}
	for i := 0; i < suf; i++ {
		ia, ib := len(a)-suf+i, len(b)-suf+i
		ops = append(ops, diffOp{' ', a[ia], ia + 1, ib + 1})
	}
	return formatHunks(ops), false
}

// lcsOps returns the edit script from a to b using a longest common
// subsequence table. Line numbers in the ops are 1-based within a and b.
func lcsOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	w := m + 1
	t := make([]int32, (n+1)*w)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				t[i*w+j] = t[(i+1)*w+j+1] + 1
			} else {
				t[i*w+j] = max(t[(i+1)*w+j], t[i*w+j+1])
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i], i + 1, j + 1})
			i++
			j++
		case t[(i+1)*w+j] >= t[i*w+j+1]:
			ops = append(ops, diffOp{'-', a[i], i + 1, j + 1})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j], i + 1, j + 1})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i], i + 1, j + 1})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j], i + 1, j + 1})
	}
	return ops
}

// formatHunks groups the changed ops with previewContext lines around them
// and formats each group with an "@@ -a,n +b,n @@" header.
func formatHunks(ops []diffOp) []string {
	var out []string
	i := 0
	for i < len(ops) {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := max(i-previewContext, 0)
		// Extend the hunk while the next change is within 2*context lines.
		last := i
		for k := i + 1; k < len(ops); k++ {
			if ops[k].kind != ' ' {
				last = k
			} else if k-last > 2*previewContext {
				break
			}
		}
		end := min(last+previewContext, len(ops)-1)
		var na, nb int
		firstA, firstB := 0, 0
		for k := start; k <= end; k++ {
			op := ops[k]
			if k == start {
				firstA, firstB = op.a, op.b
			}
			if op.kind != '+' {
				na++
			}
			if op.kind != '-' {
				nb++
			}
		}
		out = append(out, fmt.Sprintf("@@ -%d,%d +%d,%d @@", firstA, na, firstB, nb))
		for k := start; k <= end; k++ {
			op := ops[k]
			out = append(out, string(op.kind)+" "+previewMaskSecret(op.text))
		}
		i = end + 1
	}
	return out
}

// previewMaskSecret hides the value of a "KEY = value" or "KEY: value" line
// whose key looks secret. redact.Text already covers KEY=value and KEY: value.
// It does not cover spaces around the equals sign, so the preview does.
func previewMaskSecret(line string) string {
	line = previewStrip(line)
	if m := previewSecretLineRE.FindStringSubmatchIndex(line); m != nil {
		key := line[m[4]:m[5]]
		if redact.LooksSecret(key) {
			return line[:m[7]] + redact.Redacted
		}
	}
	return line
}

// previewUpgrading returns the "## Upgrading" section of the template
// README.md, capped, or nothing when the README has none.
func previewUpgrading(templateDir string) []string {
	data, truncated, err := readPreviewFile(filepath.Join(templateDir, "README.md"))
	if err != nil || truncated {
		return nil
	}
	sec := upgradingSection(string(data))
	if len(sec) == 0 {
		return nil
	}
	out := []string{"Upgrading notes from the catalog README.md:"}
	body := make([]string, len(sec))
	// The bar keeps a note such as "Volume data moved" from reading as a
	// compose progress line, which the output panel would replace in place.
	for i, l := range sec {
		body[i] = "  | " + l
	}
	return append(out, capLines(body, previewUpgradeMaxLines, "lines")...)
}

// upgradingSection returns the lines of the "## Upgrading" section of a
// Markdown text, from the heading to the next "## " heading or the end. A
// "## " line inside a fenced code block is not a heading. Leading and
// trailing blank lines are dropped. It returns nil when there is no section.
func upgradingSection(text string) []string {
	var sec []string
	in := false
	fence := ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		if mark, ok := fenceMarker(line); ok {
			switch {
			case fence == "":
				fence = mark
			case strings.HasPrefix(mark, fence[:1]) && len(mark) >= len(fence) && strings.TrimSpace(line) == mark:
				fence = ""
			}
		} else if fence == "" && strings.HasPrefix(line, "## ") {
			if in {
				break
			}
			if strings.EqualFold(strings.TrimSpace(line[3:]), "Upgrading") {
				in = true
			}
		}
		if in {
			sec = append(sec, line)
		}
	}
	for len(sec) > 0 && strings.TrimSpace(sec[len(sec)-1]) == "" {
		sec = sec[:len(sec)-1]
	}
	if len(sec) <= 1 {
		return sec
	}
	// Drop the blank lines after the heading, but keep the heading itself.
	rest := sec[1:]
	for len(rest) > 0 && strings.TrimSpace(rest[0]) == "" {
		rest = rest[1:]
	}
	return append([]string{sec[0]}, rest...)
}

// fenceMarker returns the run of backticks or tildes that opens or closes a
// fenced code block on line, and reports whether line is a fence line.
func fenceMarker(line string) (string, bool) {
	t := strings.TrimLeft(line, " ")
	if len(line)-len(t) > 3 || len(t) < 3 {
		return "", false
	}
	c := t[0]
	if c != '`' && c != '~' {
		return "", false
	}
	n := 0
	for n < len(t) && t[n] == c {
		n++
	}
	if n < 3 {
		return "", false
	}
	return t[:n], true
}

// capLines returns lines, or the first max lines and a "... N more" note.
func capLines(lines []string, limit int, unit string) []string {
	if len(lines) <= limit {
		return lines
	}
	out := slices.Clone(lines[:limit])
	return append(out, fmt.Sprintf("... %d more %s", len(lines)-limit, unit))
}

// previewStrip removes escape sequences, control characters, and invisible
// format characters from s, so text from a deployment or the catalog cannot
// move the cursor, restyle the TUI, or reorder what the operator reads.
func previewStrip(s string) string {
	s = previewANSIRE.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == unicode.ReplacementChar:
			return -1
		}
		return r
	}, s)
}

// previewClean makes one output line safe to print: it strips escape
// sequences and control characters, then redacts secrets.
func previewClean(s string) string {
	return redact.Text(previewStrip(s))
}
