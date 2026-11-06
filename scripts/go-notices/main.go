// Command go-notices collects the original notices of the packages actually
// linked into specified Go build targets. It uses only the Go standard library.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type targets []string

func (t *targets) String() string     { return strings.Join(*t, "; ") }
func (t *targets) Set(s string) error { *t = append(*t, s); return nil }

type build struct {
	Package string `json:"package"`
	Tags    string `json:"tags"`
}
type module struct {
	Path, Version, Dir, Sum string
	Main                    bool
	Replace                 *module
}
type goPackage struct {
	ImportPath       string
	Dir              string
	Standard, Goroot bool
	Module           *module
	Error            *struct{ Err string }
}
type licenseFile struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	BundlePath string `json:"bundlePath"`
	Source     string `json:"source,omitempty"`
	data       []byte
}
type moduleNotice struct {
	Path              string        `json:"path"`
	Version           string        `json:"version"`
	SourcePath        string        `json:"sourcePath"`
	SourceVersion     string        `json:"sourceVersion,omitempty"`
	Sum               string        `json:"sum,omitempty"`
	LocalSourceSHA256 string        `json:"localSourceSHA256,omitempty"`
	Packages          []string      `json:"packages"`
	Licenses          []licenseFile `json:"licenses"`
}
type runtimeNotice struct {
	Version  string        `json:"version"`
	Licenses []licenseFile `json:"licenses"`
}
type manifest struct {
	SchemaVersion int            `json:"schemaVersion"`
	Scope         string         `json:"scope"`
	GOOS          string         `json:"goos"`
	GOARCH        string         `json:"goarch"`
	CGOEnabled    string         `json:"cgoEnabled"`
	Builds        []build        `json:"builds"`
	Modules       []moduleNotice `json:"modules"`
	Runtime       runtimeNotice  `json:"runtime"`
	NoticesSHA256 string         `json:"noticesSha256"`
}

func digest(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func validModuleSum(sum string) bool {
	if !strings.HasPrefix(sum, "h1:") {
		return false
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sum, "h1:"))
	return err == nil && len(data) == sha256.Size
}

func goOutput(root string, env []string, args ...string) ([]byte, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir, cmd.Env = root, env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return data, nil
}

func withEnv(values map[string]string) []string {
	var env []string
	for _, item := range os.Environ() {
		if _, replaced := values[strings.SplitN(item, "=", 2)[0]]; !replaced {
			env = append(env, item)
		}
	}
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

func parseBuild(spec string) (build, error) {
	parts := strings.SplitN(spec, "=", 2)
	b := build{Package: parts[0]}
	if b.Package == "" || strings.HasPrefix(b.Package, "-") || strings.ContainsAny(b.Package, "\r\n\x00") {
		return b, fmt.Errorf("invalid build target %q", spec)
	}
	if len(parts) == 2 {
		tags := strings.FieldsFunc(parts[1], func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
		sort.Strings(tags)
		b.Tags = strings.Join(tags, ",")
	}
	return b, nil
}

func collectModules(root string, env []string, builds []build) (map[string]*module, map[string]map[string]bool, map[string]bool, error) {
	modules := map[string]*module{}
	packages := map[string]map[string]bool{}
	standardDirs := map[string]bool{}
	for _, target := range builds {
		args := []string{"list", "-mod=readonly", "-deps", "-json"}
		if target.Tags != "" {
			args = append(args, "-tags", target.Tags)
		}
		data, err := goOutput(root, env, append(args, target.Package)...)
		if err != nil {
			return nil, nil, nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		for {
			var pkg goPackage
			if err := decoder.Decode(&pkg); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return nil, nil, nil, err
			}
			if pkg.Error != nil {
				return nil, nil, nil, fmt.Errorf("%s: %s", pkg.ImportPath, pkg.Error.Err)
			}
			if pkg.Standard || pkg.Goroot {
				if pkg.Dir != "" {
					standardDirs[pkg.Dir] = true
				}
				continue
			}
			if pkg.Module == nil {
				return nil, nil, nil, fmt.Errorf("no module metadata for linked package %s", pkg.ImportPath)
			}
			if pkg.Module.Main {
				continue
			}
			key := pkg.Module.Path + "@" + pkg.Module.Version
			modules[key] = pkg.Module
			if packages[key] == nil {
				packages[key] = map[string]bool{}
			}
			packages[key][pkg.ImportPath] = true
		}
	}
	if len(modules) == 0 {
		return nil, nil, nil, errors.New("no third-party modules in build graph; refusing an empty notice bundle")
	}
	return modules, packages, standardDirs, nil
}

func licensePath(path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	name := strings.ToUpper(parts[len(parts)-1])
	for _, suffix := range []string{".GO", ".C", ".H", ".CC", ".CPP", ".JS", ".TS", ".PY", ".SH", ".CRT", ".DER", ".PEM", ".KEY", ".P12", ".PFX", ".PNG", ".JPG", ".PDF", ".A", ".SO", ".DLL", ".DYLIB"} {
		if strings.HasSuffix(name, suffix) {
			return false
		}
	}
	for _, part := range parts[:len(parts)-1] {
		if strings.EqualFold(part, "licenses") || strings.EqualFold(part, "licences") {
			return true
		}
	}
	for _, prefix := range []string{"LICENSE", "LICENCE", "COPYING", "NOTICE", "COPYRIGHT", "PATENTS"} {
		if strings.Contains(name, prefix) {
			return true
		}
	}
	return false
}

func runtimeLicenses(root, version string, standardDirs map[string]bool) ([]licenseFile, error) {
	files, err := collectLicensesFiltered(root, "runtime/"+version, func(path string) bool {
		return path == "src/cmd" || path == "test" || filepath.Base(path) == "testdata"
	})
	if err != nil {
		return nil, err
	}
	var selected []licenseFile
	coreLicense := false
	for _, file := range files {
		if file.Path == "LICENSE" {
			coreLicense = true
		}
		if strings.HasPrefix(file.Path, "src/cmd/") || strings.Contains(file.Path, "/testdata/") || strings.HasPrefix(file.Path, "test/") {
			continue
		}
		include := file.Path == "LICENSE" || file.Path == "PATENTS" || strings.HasPrefix(file.Path, "src/vendor/")
		parent := filepath.Dir(filepath.Join(root, filepath.FromSlash(file.Path)))
		for directory := range standardDirs {
			if directory == parent || strings.HasPrefix(directory, parent+string(filepath.Separator)) || strings.HasPrefix(parent, directory+string(filepath.Separator)) {
				include = true
			}
		}
		if include {
			selected = append(selected, file)
		}
	}
	if !coreLicense {
		return nil, errors.New("Go runtime LICENSE is missing")
	}
	return selected, nil
}

func safePath(root, path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute output path: %s", path)
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe output path: %s", path)
	}
	return filepath.Join(root, clean), nil
}

func collectLicenses(directory, prefix string) ([]licenseFile, error) {
	return collectLicensesFiltered(directory, prefix, nil)
}

func collectLicensesFiltered(directory, prefix string, skipDirectory func(string) bool) ([]licenseFile, error) {
	var files []licenseFile
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			rel, err := filepath.Rel(directory, path)
			if err != nil {
				return err
			}
			if skipDirectory != nil && skipDirectory(filepath.ToSlash(rel)) {
				return filepath.SkipDir
			}
			if path != directory && (entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "target") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(directory, path)
		if err != nil || !licensePath(rel) {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("license is a symlink, review its source explicitly: %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(data)) == 0 || !utf8.Valid(data) {
			return fmt.Errorf("empty or non-UTF8 license file: %s", path)
		}
		files = append(files, licenseFile{Path: filepath.ToSlash(rel), SHA256: digest(data), BundlePath: prefix + "/" + filepath.ToSlash(rel), data: data})
		return nil
	})
	return files, err
}

func localSourceDigest(directory string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "target" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("local module source contains a symlink: %s", path)
		}
		for _, suffix := range []string{".a", ".o", ".so", ".dylib", ".dll", ".exe"} {
			if strings.HasSuffix(path, suffix) {
				return nil
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%s\n", filepath.ToSlash(rel), digest(data))
		return nil
	})
	return hex.EncodeToString(h.Sum(nil)), err
}

func supplementalLicense(root string, source *module, prefix string) ([]licenseFile, error) {
	// This archive publishes only a reference to the upstream project's MIT
	// license. Pin all three identities; a changed module needs a new review.
	if source.Path != "github.com/asg017/sqlite-vec-go-bindings" || source.Version != "v0.1.6" || source.Sum != "h1:Nx0jAzyS38XpkKznJ9xQjFXz2X9tI7KqjwVxV8RNoww=" {
		return nil, fmt.Errorf("no original license text for %s@%s", source.Path, source.Version)
	}
	data, err := os.ReadFile(filepath.Join(root, "licenses/go-supplemental/sqlite-vec-v0.1.6-LICENSE-MIT.txt"))
	if err != nil {
		return nil, err
	}
	const sha = "6ce72bbe12d975bd5286e5ab0a064c069693300c47bccbc57bec18485f1621ea"
	if digest(data) != sha {
		return nil, errors.New("sqlite-vec supplemental license checksum mismatch")
	}
	return []licenseFile{{Path: "LICENSE-MIT", SHA256: sha, BundlePath: prefix + "/LICENSE-MIT", Source: "https://raw.githubusercontent.com/asg017/sqlite-vec/v0.1.6/LICENSE-MIT", data: data}}, nil
}

func duckDBNativeLicenses(root string, source *module, prefix string) ([]licenseFile, error) {
	if source.Path != "github.com/duckdb/duckdb-go-bindings" || source.Version != "v0.10502.0" || source.Sum != "h1:Uhg/dfvPLQv4cH35lMD48hqUcdOh2Z7bcuykjr4qnOA=" {
		return nil, errors.New("DuckDB bindings changed; review and update native dependency notices")
	}
	directory := filepath.Join(root, "licenses/go-supplemental/duckdb-v1.5.2")
	metadata, err := os.ReadFile(filepath.Join(directory, "source-manifest.json"))
	if err != nil {
		return nil, err
	}
	const manifestSHA = "f538ed9466e3a83e12a6ec175455192e2e44e98317e803869a5d9f84d2a31651"
	if digest(metadata) != manifestSHA {
		return nil, errors.New("DuckDB native source manifest checksum mismatch")
	}
	var index struct {
		Files []struct{ Path, SHA256, Source string }
	}
	if err := json.Unmarshal(metadata, &index); err != nil {
		return nil, err
	}
	prefix += "/native/duckdb-v1.5.2"
	files := []licenseFile{{Path: "native/duckdb-v1.5.2/source-manifest.json", BundlePath: prefix + "/source-manifest.json", SHA256: manifestSHA, data: metadata}}
	for _, file := range index.Files {
		path, err := safePath(directory, file.Path)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if digest(data) != file.SHA256 {
			return nil, fmt.Errorf("DuckDB native license checksum mismatch: %s", file.Path)
		}
		files = append(files, licenseFile{Path: "native/duckdb-v1.5.2/" + file.Path, BundlePath: prefix + "/" + file.Path, SHA256: file.SHA256, Source: file.Source, data: data})
	}
	return files, nil
}

func verifyRustNative(directory, goos, goarch string) error {
	root := filepath.Join(directory, "licenses/rust")
	data, err := os.ReadFile(filepath.Join(root, "rust-dependencies.json"))
	if err != nil {
		return fmt.Errorf("anydoc native Rust notices missing; run scripts/build-anydoc-lib.sh: %w", err)
	}
	type entry struct {
		Name, Version string
		Licenses      []licenseFile
	}
	var metadata struct {
		SchemaVersion      int
		Target, LockSHA256 string
		Packages           []entry
		Runtime            entry
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return fmt.Errorf("invalid Rust dependency manifest: %w", err)
	}
	if metadata.SchemaVersion != 1 || len(metadata.Packages) == 0 || len(metadata.LockSHA256) != 64 || len(metadata.Runtime.Licenses) == 0 {
		return errors.New("incomplete Rust dependency manifest")
	}
	lock, err := os.ReadFile(filepath.Join(directory, "Cargo.lock"))
	if err != nil {
		return fmt.Errorf("anydoc Cargo.lock unavailable: %w", err)
	}
	if digest(lock) != metadata.LockSHA256 {
		return errors.New("Rust notice bundle is stale: Cargo.lock checksum changed")
	}
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[goarch]
	osTarget := map[string]string{"darwin": "apple-darwin", "linux": "unknown-linux-", "windows": "pc-windows-"}[goos]
	if arch == "" || osTarget == "" || !strings.HasPrefix(metadata.Target, arch+"-"+osTarget) {
		return fmt.Errorf("Rust notice target %q does not match Go target %s/%s", metadata.Target, goos, goarch)
	}
	for _, pkg := range append(metadata.Packages, metadata.Runtime) {
		if pkg.Name == "" || pkg.Version == "" || len(pkg.Licenses) == 0 {
			return errors.New("Rust component lacks identity or license originals")
		}
		for _, file := range pkg.Licenses {
			if !strings.HasPrefix(file.BundlePath, "originals/") {
				return fmt.Errorf("Rust original outside originals/: %s", file.BundlePath)
			}
			path, err := safePath(root, file.BundlePath)
			if err != nil {
				return err
			}
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("Rust original is not a regular file: %s", file.BundlePath)
			}
			original, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if digest(original) != file.SHA256 {
				return fmt.Errorf("Rust original checksum mismatch: %s", file.BundlePath)
			}
		}
	}
	return nil
}

func moduleNotices(root string, modules map[string]*module, packages map[string]map[string]bool, requireRust bool, goos, goarch string) ([]moduleNotice, error) {
	keys := make([]string, 0, len(modules))
	for key := range modules {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var notices []moduleNotice
	for _, key := range keys {
		original := modules[key]
		if original.Path == "" || original.Version == "" {
			return nil, fmt.Errorf("missing module identity: %s", key)
		}
		source := original
		if source.Replace != nil {
			source = source.Replace
		}
		if source.Dir == "" {
			return nil, fmt.Errorf("module source is not installed: %s", key)
		}
		if original.Path == "github.com/firecrawl/anydoc/go" && source.Version == "" && requireRust {
			if err := verifyRustNative(source.Dir, goos, goarch); err != nil {
				return nil, err
			}
		}
		if strings.HasPrefix(original.Path, "github.com/duckdb/duckdb-go-bindings/lib/") && original.Version != "v0.10502.0" {
			return nil, errors.New("DuckDB native module version changed; review its upstream license snapshot")
		}
		notice := moduleNotice{Path: original.Path, Version: original.Version, SourcePath: source.Path, SourceVersion: source.Version, Sum: source.Sum}
		if source.Version == "" {
			rel, err := filepath.Rel(root, source.Dir)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return nil, fmt.Errorf("local replacement is outside the source repository: %s", key)
			}
			notice.SourcePath = filepath.ToSlash(rel)
			notice.LocalSourceSHA256, err = localSourceDigest(source.Dir)
			if err != nil {
				return nil, err
			}
		} else if !validModuleSum(source.Sum) {
			return nil, fmt.Errorf("module has no verified h1 source checksum: %s", key)
		}
		prefix := "modules/" + key
		var err error
		notice.Licenses, err = collectLicenses(source.Dir, prefix)
		if err != nil {
			return nil, err
		}
		if len(notice.Licenses) == 0 {
			notice.Licenses, err = supplementalLicense(root, source, prefix)
			if err != nil {
				return nil, err
			}
		}
		if original.Path == "github.com/duckdb/duckdb-go-bindings" {
			native, err := duckDBNativeLicenses(root, source, prefix)
			if err != nil {
				return nil, err
			}
			notice.Licenses = append(notice.Licenses, native...)
		}
		for name := range packages[key] {
			notice.Packages = append(notice.Packages, name)
		}
		sort.Strings(notice.Packages)
		notices = append(notices, notice)
	}
	return notices, nil
}

func writeBundle(output string, result manifest) error {
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		return fmt.Errorf("output must be a new directory: %s", output)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(output), ".go-notices-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	var text bytes.Buffer
	fmt.Fprintln(&text, "GO DEPENDENCY LICENSES AND NOTICES\nOriginal license and copyright files for the specified build graphs and Go toolchain.\nSee go-dependencies.json for module versions, verified source checksums and original file paths.")
	writeFiles := func(label string, files []licenseFile) error {
		fmt.Fprintf(&text, "\n%s\n%s\n", strings.Repeat("=", 80), label)
		for _, file := range files {
			dest, err := safePath(stage, file.BundlePath)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(dest, file.data, 0644); err != nil {
				return err
			}
			fmt.Fprintf(&text, "\n--- %s ---\n", file.BundlePath)
			text.Write(file.data)
			text.WriteByte('\n')
		}
		return nil
	}
	for _, item := range result.Modules {
		if err := writeFiles(item.Path+"@"+item.Version, item.Licenses); err != nil {
			return err
		}
	}
	if err := writeFiles("Go runtime/toolchain "+result.Runtime.Version, result.Runtime.Licenses); err != nil {
		return err
	}
	result.NoticesSHA256 = digest(text.Bytes())
	metadata, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "go-dependencies.json"), append(metadata, '\n'), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "go-third-party-notices.txt"), text.Bytes(), 0644); err != nil {
		return err
	}
	return os.Rename(stage, output)
}

func run() error {
	root := flag.String("root", ".", "source repository")
	output := flag.String("output", "", "new output directory")
	goos := flag.String("goos", "", "target GOOS")
	goarch := flag.String("goarch", "", "target GOARCH")
	cgo := flag.String("cgo", "", "target CGO_ENABLED")
	var specs targets
	flag.Var(&specs, "target", "build target PACKAGE=TAGS (repeat for binaries with different tags)")
	flag.Parse()
	if *output == "" || *goos == "" || *goarch == "" || (*cgo != "0" && *cgo != "1") || flag.NArg() > 0 {
		return errors.New("--output, --goos, --goarch and --cgo (0 or 1) are required")
	}
	var err error
	*root, err = filepath.Abs(*root)
	if err != nil {
		return err
	}
	if len(specs) == 0 {
		specs = targets{"./cmd/server="}
	}
	result := manifest{SchemaVersion: 1, Scope: "Go build package graphs, runtime, and module-bundled native notices; not a complete operating-system or container SBOM", GOOS: *goos, GOARCH: *goarch, CGOEnabled: *cgo}
	requireRust := false
	for _, spec := range specs {
		b, err := parseBuild(spec)
		if err != nil {
			return err
		}
		result.Builds = append(result.Builds, b)
		for _, tag := range strings.Split(b.Tags, ",") {
			if tag == "anydoc" {
				requireRust = true
			}
		}
	}
	sort.Slice(result.Builds, func(i, j int) bool {
		a, b := result.Builds[i], result.Builds[j]
		return a.Package+"="+a.Tags < b.Package+"="+b.Tags
	})
	env := withEnv(map[string]string{"GOOS": *goos, "GOARCH": *goarch, "CGO_ENABLED": *cgo})
	modules, packages, standardDirs, err := collectModules(*root, env, result.Builds)
	if err != nil {
		return err
	}
	result.Modules, err = moduleNotices(*root, modules, packages, requireRust, *goos, *goarch)
	if err != nil {
		return err
	}
	data, err := goOutput(*root, env, "env", "-json", "GOROOT", "GOVERSION")
	if err != nil {
		return err
	}
	var toolchain struct{ GOROOT, GOVERSION string }
	if err := json.Unmarshal(data, &toolchain); err != nil {
		return err
	}
	if toolchain.GOROOT == "" || toolchain.GOVERSION == "" {
		return errors.New("Go toolchain source/version unavailable")
	}
	result.Runtime.Version = toolchain.GOVERSION
	result.Runtime.Licenses, err = runtimeLicenses(toolchain.GOROOT, toolchain.GOVERSION, standardDirs)
	if err != nil {
		return err
	}
	if len(result.Runtime.Licenses) == 0 {
		return errors.New("Go runtime license missing")
	}
	if err := writeBundle(*output, result); err != nil {
		return err
	}
	fmt.Printf("Go notices: %d linked modules, %d runtime/vendor notice files -> %s\n", len(result.Modules), len(result.Runtime.Licenses), *output)
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Go notice generation:", err)
		os.Exit(1)
	}
}
