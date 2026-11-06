// Collect the original notices for a locked Cargo graph, including build tools.
// No Rust compilation is required. Cargo verifies registry downloads; the
// manifest preserves their package checksums and the selected target triple.
package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
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
	"strconv"
	"strings"
	"unicode/utf8"
)

type cargoPackage struct {
	ID, Name, Version, Source, License string
	ManifestPath                       string `json:"manifest_path"`
	LicenseFile                        string `json:"license_file"`
}
type fileNotice struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	BundlePath string `json:"bundlePath"`
	Source     string `json:"source,omitempty"`
	data       []byte
}

var missingLicense = errors.New("no original license text")

type packageNotice struct {
	Name     string       `json:"name"`
	Version  string       `json:"version"`
	Source   string       `json:"source"`
	License  string       `json:"license"`
	Checksum string       `json:"checksum,omitempty"`
	Licenses []fileNotice `json:"licenses"`
}
type manifest struct {
	SchemaVersion int             `json:"schemaVersion"`
	Target        string          `json:"target"`
	Scope         string          `json:"scope"`
	LockSHA256    string          `json:"lockSha256"`
	Packages      []packageNotice `json:"packages"`
	Runtime       packageNotice   `json:"runtime"`
}

func digest(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func output(command string, args ...string) ([]byte, error) {
	cmd := exec.Command(command, args...)
	cmd.Stderr = os.Stderr
	return cmd.Output()
}

// Cargo's generated v4 lockfile stores these package identity fields as
// single-line quoted strings. Reject malformed identities instead of guessing.
func lockChecksums(data []byte) (map[string]string, error) {
	checksums := map[string]string{}
	fields := map[string]string{}
	flush := func() error {
		if fields["checksum"] == "" {
			return nil
		}
		key := fields["name"] + "@" + fields["version"] + "@" + fields["source"]
		decoded, err := hex.DecodeString(fields["checksum"])
		if err != nil || len(decoded) != sha256.Size || fields["name"] == "" || fields["version"] == "" || fields["source"] == "" {
			return fmt.Errorf("invalid Cargo.lock package checksum")
		}
		if _, ok := checksums[key]; ok {
			return fmt.Errorf("duplicate Cargo.lock identity: %s", key)
		}
		checksums[key] = fields["checksum"]
		return nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	inPackage := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "[[package]]" {
			if err := flush(); err != nil {
				return nil, err
			}
			fields = map[string]string{}
			inPackage = true
			continue
		}
		if !inPackage {
			continue
		}
		key, value, ok := strings.Cut(line, " = ")
		if !ok || (key != "name" && key != "version" && key != "source" && key != "checksum") {
			continue
		}
		v, err := strconv.Unquote(value)
		if err != nil {
			return nil, err
		}
		fields[key] = v
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return checksums, nil
}

func isNotice(path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, part := range parts[:len(parts)-1] {
		if strings.EqualFold(part, "licenses") || strings.EqualFold(part, "licences") {
			return true
		}
	}
	name := strings.ToUpper(parts[len(parts)-1])
	for _, suffix := range []string{".RS", ".GO", ".C", ".H", ".PY", ".SH"} {
		if strings.HasSuffix(name, suffix) {
			return false
		}
	}
	for _, prefix := range []string{"LICENSE", "LICENCE", "COPYING", "NOTICE", "COPYRIGHT", "AUTHORS"} {
		if strings.Contains(name, prefix) {
			return true
		}
	}
	return false
}

func collect(root, explicit, prefix string, allFiles bool) ([]fileNotice, error) {
	var files []fileNotice
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// Generated notices must never be included in their own next run.
			if rel != "." && (entry.Name() == "target" || entry.Name() == ".git" || filepath.ToSlash(rel) == "licenses/rust" || entry.Name() == "patched-anydoc") {
				return filepath.SkipDir
			}
			return nil
		}
		if !allFiles && !isNotice(rel) && path != explicit {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("notice is not a regular file: %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if len(data) == 0 || !utf8.Valid(data) {
			return fmt.Errorf("empty or non-UTF8 notice: %s", path)
		}
		files = append(files, fileNotice{Path: filepath.ToSlash(rel), SHA256: digest(data), BundlePath: prefix + "/" + filepath.ToSlash(rel), data: data})
		return nil
	})
	if err == nil && len(files) == 0 {
		err = fmt.Errorf("%w in %s", missingLicense, root)
	}
	if explicit != "" {
		found := false
		for _, f := range files {
			if filepath.Join(root, filepath.FromSlash(f.Path)) == explicit {
				found = true
			}
		}
		if !found && err == nil {
			err = fmt.Errorf("declared license_file is missing: %s", explicit)
		}
	}
	return files, err
}

func supplemental(root string, pkg packageNotice) ([]fileNotice, error) {
	directory := filepath.Join(root, "licenses/rust-supplemental")
	data, err := readRegular(root, filepath.Join(directory, "sources.json"))
	if err != nil {
		return nil, err
	}
	var policy map[string]struct {
		Checksum string                                        `json:"checksum"`
		Licenses []struct{ File, Path, Source, SHA256 string } `json:"licenses"`
	}
	if err = json.Unmarshal(data, &policy); err != nil {
		return nil, err
	}
	entry, ok := policy[pkg.Name+"@"+pkg.Version]
	if !ok || entry.Checksum != pkg.Checksum || len(entry.Licenses) == 0 {
		return nil, fmt.Errorf("no reviewed supplemental license for %s@%s", pkg.Name, pkg.Version)
	}
	var files []fileNotice
	for _, item := range entry.Licenses {
		if !filepath.IsLocal(item.File) || !filepath.IsLocal(item.Path) {
			return nil, fmt.Errorf("invalid supplemental license path")
		}
		data, err := readRegular(root, filepath.Join(directory, item.File))
		if err != nil {
			return nil, err
		}
		if len(data) == 0 || !utf8.Valid(data) || digest(data) != item.SHA256 {
			return nil, fmt.Errorf("supplemental license checksum mismatch: %s", item.File)
		}
		files = append(files, fileNotice{Path: item.Path, Source: item.Source, SHA256: item.SHA256, BundlePath: "originals/" + pkg.Name + "-" + pkg.Version + "/" + item.Path, data: data})
	}
	return files, nil
}

func readRegular(root, path string) ([]byte, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("notice escapes source directory")
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("notice path contains a symlink: %s", current)
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	return os.ReadFile(path)
}

func verifyRegistryOriginals(data []byte, pkg packageNotice) error {
	expected := map[string]string{}
	for _, file := range pkg.Licenses {
		if file.Source == "" {
			expected[pkg.Name+"-"+pkg.Version+"/"+file.Path] = file.SHA256
		}
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gz.Close()
	archive := tar.NewReader(gz)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		sha, ok := expected[header.Name]
		if !ok {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return fmt.Errorf("registry original is not a regular file: %s", header.Name)
		}
		h := sha256.New()
		if _, err = io.Copy(h, archive); err != nil {
			return err
		}
		if hex.EncodeToString(h.Sum(nil)) != sha {
			return fmt.Errorf("unpacked license differs from locked registry original: %s", header.Name)
		}
		delete(expected, header.Name)
	}
	if len(expected) != 0 {
		return fmt.Errorf("unpacked license absent from locked registry archive for %s@%s", pkg.Name, pkg.Version)
	}
	return nil
}

func validateDestination(crate, destination string) error {
	if destination != filepath.Join(crate, "licenses/rust") {
		return fmt.Errorf("output must be the crate's generated licenses/rust directory")
	}
	for _, path := range []string{filepath.Join(crate, "licenses"), destination} {
		info, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return fmt.Errorf("output path must be a real directory: %s", path)
		}
	}
	entries, err := os.ReadDir(destination)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(entries) > 0 {
		data, err := readRegular(crate, filepath.Join(destination, "rust-dependencies.json"))
		if err != nil {
			return fmt.Errorf("refusing to replace a non-generated directory: %w", err)
		}
		var prior manifest
		if json.Unmarshal(data, &prior) != nil || prior.SchemaVersion != 1 || prior.Target == "" || len(prior.Packages) == 0 {
			return fmt.Errorf("refusing to replace an unrecognized notice directory")
		}
	}
	return nil
}

func run() error {
	manifestPath := flag.String("manifest", "", "Cargo.toml path")
	target := flag.String("target", "", "Rust target triple")
	destination := flag.String("output", "", "Generated notice directory")
	printTargetDirectory := flag.Bool("print-target-dir", false, "Print only Cargo's resolved target directory to stdout")
	flag.Parse()
	if *manifestPath == "" || *target == "" || *destination == "" || flag.NArg() != 0 {
		return fmt.Errorf("--manifest, --target and --output are required")
	}
	absManifest, err := filepath.Abs(*manifestPath)
	if err != nil {
		return err
	}
	*destination, err = filepath.Abs(*destination)
	if err != nil {
		return err
	}
	if err = validateDestination(filepath.Dir(absManifest), *destination); err != nil {
		return err
	}
	lock, err := os.ReadFile(filepath.Join(filepath.Dir(absManifest), "Cargo.lock"))
	if err != nil {
		return err
	}
	checksums, err := lockChecksums(lock)
	if err != nil {
		return err
	}
	raw, err := output("cargo", "metadata", "--locked", "--format-version", "1", "--filter-platform", *target, "--manifest-path", absManifest)
	if err != nil {
		return err
	}
	var metadata struct {
		TargetDirectory string `json:"target_directory"`
		Packages        []cargoPackage
		Resolve         struct {
			Root  string
			Nodes []struct {
				ID           string
				Dependencies []string
			}
		}
	}
	if err = json.Unmarshal(raw, &metadata); err != nil {
		return err
	}
	if *printTargetDirectory && (!filepath.IsAbs(metadata.TargetDirectory) || strings.ContainsAny(metadata.TargetDirectory, "\r\n")) {
		return fmt.Errorf("Cargo metadata returned an invalid absolute target directory")
	}
	if metadata.Resolve.Root == "" {
		return fmt.Errorf("Cargo graph has no root")
	}
	nodes := map[string][]string{}
	for _, node := range metadata.Resolve.Nodes {
		nodes[node.ID] = node.Dependencies
	}
	reachable := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		if reachable[id] {
			return
		}
		reachable[id] = true
		for _, child := range nodes[id] {
			visit(child)
		}
	}
	visit(metadata.Resolve.Root)
	result := manifest{SchemaVersion: 1, Target: *target, Scope: "Cargo locked target graph, including build dependencies; original license files only", LockSHA256: digest(lock)}
	for _, pkg := range metadata.Packages {
		if !reachable[pkg.ID] {
			continue
		}
		root := filepath.Dir(pkg.ManifestPath)
		notice := packageNotice{Name: pkg.Name, Version: pkg.Version, Source: pkg.Source, License: pkg.License}
		var registryArchive []byte
		if pkg.Source == "" {
			notice.Source = "local patched source distributed in third_party/anydoc-go"
		} else {
			if !strings.HasPrefix(pkg.Source, "registry+") {
				return fmt.Errorf("unreviewed non-registry source for %s", pkg.ID)
			}
			notice.Checksum = checksums[pkg.Name+"@"+pkg.Version+"@"+pkg.Source]
			if notice.Checksum == "" {
				return fmt.Errorf("no locked registry checksum for %s", pkg.ID)
			}
			// Registry archives are already downloaded by cargo metadata. Hash
			// them again to retain independently checked release provenance.
			registry := filepath.Dir(filepath.Dir(filepath.Dir(root)))
			archive := filepath.Join(registry, "cache", filepath.Base(filepath.Dir(root)), pkg.Name+"-"+pkg.Version+".crate")
			data, readErr := os.ReadFile(archive)
			if readErr != nil {
				return readErr
			}
			if digest(data) != notice.Checksum {
				return fmt.Errorf("registry archive checksum mismatch for %s", pkg.ID)
			}
			registryArchive = data
		}
		explicit := ""
		if pkg.LicenseFile != "" {
			explicit = pkg.LicenseFile
			if !filepath.IsAbs(explicit) {
				explicit = filepath.Join(root, explicit)
			}
		}
		notice.Licenses, err = collect(root, explicit, "originals/"+pkg.Name+"-"+pkg.Version, false)
		if errors.Is(err, missingLicense) && explicit == "" {
			notice.Licenses, err = supplemental(filepath.Dir(filepath.Dir(filepath.Dir(absManifest))), notice)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", pkg.ID, err)
		}
		if registryArchive != nil {
			if err = verifyRegistryOriginals(registryArchive, notice); err != nil {
				return err
			}
		}
		result.Packages = append(result.Packages, notice)
	}
	if len(result.Packages) < 2 {
		return fmt.Errorf("refusing empty Cargo dependency graph")
	}
	sort.Slice(result.Packages, func(i, j int) bool {
		a, b := result.Packages[i], result.Packages[j]
		return a.Name+"@"+a.Version < b.Name+"@"+b.Version
	})
	sysroot, err := output("rustc", "--print", "sysroot")
	if err != nil {
		return err
	}
	version, err := output("rustc", "--version")
	if err != nil {
		return err
	}
	// rustc's minimal distribution includes these files. The library copyright
	// document attributes bundled standard-library third parties; LICENSE alone
	// would not cover those contributors.
	doc := filepath.Join(strings.TrimSpace(string(sysroot)), "share/doc/rust")
	result.Runtime = packageNotice{Name: "rust-standard-library", Version: strings.TrimSpace(string(version)), Source: "installed Rust toolchain"}
	for _, name := range []string{"COPYRIGHT-library.html"} {
		data, readErr := os.ReadFile(filepath.Join(doc, name))
		if readErr != nil {
			return readErr
		}
		if len(data) == 0 || !utf8.Valid(data) {
			return fmt.Errorf("invalid Rust copyright file")
		}
		result.Runtime.Licenses = append(result.Runtime.Licenses, fileNotice{Path: name, SHA256: digest(data), BundlePath: "originals/rust-runtime/" + name, data: data})
	}
	runtimeFiles, err := collect(filepath.Join(doc, "licenses"), "", "originals/rust-runtime/licenses", true)
	if err != nil {
		return err
	}
	result.Runtime.Licenses = append(result.Runtime.Licenses, runtimeFiles...)
	// Build a complete new tree before publishing it, so errors cannot leave a
	// half-populated output that later Go packaging would mistake for success.
	if err = os.MkdirAll(filepath.Dir(*destination), 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(*destination), ".rust-notices-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	all := append(append([]packageNotice{}, result.Packages...), result.Runtime)
	for _, pkg := range all {
		for _, file := range pkg.Licenses {
			path := filepath.Join(stage, filepath.FromSlash(file.BundlePath))
			if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return err
			}
			if err = os.WriteFile(path, file.data, 0644); err != nil {
				return err
			}
		}
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(stage, "rust-dependencies.json"), append(data, '\n'), 0644); err != nil {
		return err
	}
	if err = os.RemoveAll(*destination); err != nil {
		return err
	}
	if err = os.Rename(stage, *destination); err != nil {
		return err
	}
	if *printTargetDirectory {
		fmt.Fprintf(os.Stderr, "Rust notices: %d packages for %s\n", len(result.Packages), result.Target)
		fmt.Println(metadata.TargetDirectory)
	} else {
		fmt.Printf("Rust notices: %d packages for %s\n", len(result.Packages), result.Target)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
