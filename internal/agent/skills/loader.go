package skills

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Usir233/GENYEE/internal/sandbox"
)

// Loader handles skill discovery and loading from the filesystem
// It implements the Progressive Disclosure pattern by separating
// metadata discovery (Level 1) from instructions loading (Level 2/3)
type Loader struct {
	// skillDirs are the directories to search for skills
	skillDirs []string
	// discoveredSkills caches discovered skill metadata
	mu               sync.RWMutex
	discoveredSkills map[string]*Skill
}

// NewLoader creates a new skill loader with the specified search directories
func NewLoader(skillDirs []string) *Loader {
	return &Loader{
		skillDirs:        skillDirs,
		discoveredSkills: make(map[string]*Skill),
	}
}

func (l *Loader) getCachedSkill(name string) (*Skill, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	skill, ok := l.discoveredSkills[name]
	return skill, ok
}

func (l *Loader) putCachedSkill(name string, skill *Skill) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.discoveredSkills == nil {
		l.discoveredSkills = make(map[string]*Skill)
	}
	l.discoveredSkills[name] = skill
}

func readContainedSkillFile(subRoot *os.Root, basePath string) (*Skill, error) {
	f, err := subRoot.Open(SkillFileName)
	if err != nil {
		return nil, fmt.Errorf("failed to open skill file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat skill file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("skill file is not a regular file")
	}
	if info.Size() > maxBundleEntryBytes {
		return nil, fmt.Errorf("skill file exceeds maximum size")
	}
	content, err := io.ReadAll(io.LimitReader(f, maxBundleEntryBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read skill file: %w", err)
	}
	if len(content) > maxBundleEntryBytes {
		return nil, fmt.Errorf("skill file exceeds maximum size")
	}

	skill, err := ParseSkillFile(string(content))
	if err != nil {
		return nil, err
	}
	skill.BasePath = basePath
	skill.FilePath = filepath.Join(basePath, SkillFileName)
	return skill, nil
}

// DiscoverSkills scans all configured directories for SKILL.md files
// and extracts their metadata (Level 1). This is a lightweight operation
// that only reads the frontmatter of each skill file.
func (l *Loader) DiscoverSkills() ([]*SkillMetadata, error) {
	var allMetadata []*SkillMetadata

	for _, dir := range l.skillDirs {
		metadata, err := l.discoverInDirectory(dir)
		if err != nil {
			// Log warning but continue with other directories
			continue
		}
		allMetadata = append(allMetadata, metadata...)
	}

	return allMetadata, nil
}

// discoverInDirectory scans a single directory for skill subdirectories
func (l *Loader) discoverInDirectory(dir string) ([]*SkillMetadata, error) {
	var metadata []*SkillMetadata

	// Check if directory exists
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // Directory doesn't exist, skip silently
		}
		return nil, fmt.Errorf("failed to access skill directory %s: %w", dir, err)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}

	dirRoot, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to open skill directory %s: %w", dir, err)
	}
	defer dirRoot.Close()

	// Read directory entries
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read skill directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() || !sandbox.IsValidSkillName(entry.Name()) {
			continue
		}

		subRoot, err := dirRoot.OpenRoot(entry.Name())
		if err != nil {
			continue
		}
		skillPath := filepath.Join(dir, entry.Name())
		skill, err := readContainedSkillFile(subRoot, skillPath)
		_ = subRoot.Close()
		if err != nil {
			continue
		}

		// Cache the skill
		l.putCachedSkill(skill.Name, skill)

		metadata = append(metadata, skill.ToMetadata())
	}

	return metadata, nil
}

// LoadSkillInstructions loads the full instructions of a skill (Level 2)
// Returns the cached skill if already loaded
func (l *Loader) LoadSkillInstructions(skillName string) (*Skill, error) {
	if !sandbox.IsValidSkillName(skillName) {
		return nil, fmt.Errorf("invalid skill name: %s", skillName)
	}

	// Check cache first
	if skill, ok := l.getCachedSkill(skillName); ok {
		if skill.Loaded {
			return skill, nil
		}
	}

	// Search for the skill in all directories
	for _, dir := range l.skillDirs {
		skill, err := l.loadSkillFromDirectory(dir, skillName)
		if err == nil {
			l.putCachedSkill(skillName, skill)
			return skill, nil
		}
	}

	return nil, fmt.Errorf("skill not found: %s", skillName)
}

// loadSkillFromDirectory attempts to load a skill from a specific directory
func (l *Loader) loadSkillFromDirectory(dir, skillName string) (*Skill, error) {
	if !sandbox.IsValidSkillName(skillName) {
		return nil, fmt.Errorf("invalid skill name: %s", skillName)
	}

	dirRoot, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer dirRoot.Close()

	// First, check if we can find by directory name matching skill name
	if subRoot, err := dirRoot.OpenRoot(skillName); err == nil {
		skill, loadErr := readContainedSkillFile(subRoot, filepath.Join(dir, skillName))
		_ = subRoot.Close()
		if loadErr == nil && skill.Name == skillName {
			return skill, nil
		}
	}

	// Otherwise, scan all subdirectories to find the skill by name
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() || !sandbox.IsValidSkillName(entry.Name()) || entry.Name() == skillName {
			continue
		}

		subRoot, err := dirRoot.OpenRoot(entry.Name())
		if err != nil {
			continue
		}
		skill, loadErr := readContainedSkillFile(subRoot, filepath.Join(dir, entry.Name()))
		_ = subRoot.Close()
		if loadErr != nil {
			continue
		}

		if skill.Name == skillName {
			return skill, nil
		}
	}

	return nil, fmt.Errorf("skill not found in %s: %s", dir, skillName)
}

// LoadSkillFile loads an additional file from a skill directory (Level 3)
// The filePath should be relative to the skill's base directory
func (l *Loader) LoadSkillFile(skillName, relativePath string) (*SkillFile, error) {
	// Get the skill first
	skill, ok := l.getCachedSkill(skillName)
	if !ok {
		// Try to load the skill
		var err error
		skill, err = l.LoadSkillInstructions(skillName)
		if err != nil {
			return nil, fmt.Errorf("skill not found: %s", skillName)
		}
	}

	if strings.TrimSpace(relativePath) == "" || strings.ContainsRune(relativePath, '\\') || filepath.IsAbs(relativePath) {
		return nil, fmt.Errorf("invalid file path: %s", relativePath)
	}

	// Validate and resolve the file path
	cleanPath := filepath.Clean(relativePath)

	// Security: prevent path traversal
	if cleanPath == "." || strings.HasPrefix(cleanPath, "..") || filepath.IsAbs(cleanPath) {
		return nil, fmt.Errorf("invalid file path: %s", relativePath)
	}

	fullPath := filepath.Join(skill.BasePath, cleanPath)

	// Verify the file is within the skill directory
	absSkillPath, err := filepath.Abs(skill.BasePath)
	if err != nil {
		return nil, err
	}
	absFilePath, err := filepath.Abs(fullPath)
	if err != nil {
		return nil, err
	}
	relPath, err := filepath.Rel(absSkillPath, absFilePath)
	if err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) || filepath.IsAbs(relPath) {
		return nil, fmt.Errorf("file path outside skill directory: %s", relativePath)
	}

	// Read the file
	// The resource reader must not follow a bundle symlink into host files.
	// os.Root enforces containment during the open, including symlink races.
	root, err := os.OpenRoot(skill.BasePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open skill directory: %w", err)
	}
	defer root.Close()

	f, err := root.Open(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", relativePath)
	}
	if info.Size() > maxBundleEntryBytes {
		return nil, fmt.Errorf("file exceeds maximum size: %s", relativePath)
	}
	content, err := io.ReadAll(io.LimitReader(f, maxBundleEntryBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	if len(content) > maxBundleEntryBytes {
		return nil, fmt.Errorf("file exceeds maximum size: %s", relativePath)
	}

	return &SkillFile{
		Name:     relativePath,
		Path:     absFilePath, // Use absolute path for sandbox execution
		Content:  string(content),
		IsScript: IsScript(relativePath),
	}, nil
}

// ListSkillFiles lists all files in a skill directory
func (l *Loader) ListSkillFiles(skillName string) ([]string, error) {
	skill, ok := l.getCachedSkill(skillName)
	if !ok {
		var err error
		skill, err = l.LoadSkillInstructions(skillName)
		if err != nil {
			return nil, fmt.Errorf("skill not found: %s", skillName)
		}
	}

	root, err := os.OpenRoot(skill.BasePath)
	if err != nil {
		return nil, fmt.Errorf("failed to list skill files: %w", err)
	}
	defer root.Close()

	var files []string
	err = fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if len(files) >= maxBundleEntries {
			return fmt.Errorf("skill holds more than %d files", maxBundleEntries)
		}
		files = append(files, filepath.FromSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list skill files: %w", err)
	}

	return files, nil
}

// GetSkillByName returns a cached skill by name
func (l *Loader) GetSkillByName(name string) (*Skill, bool) {
	return l.getCachedSkill(name)
}

// GetSkillBasePath returns the base path for a skill (always absolute)
func (l *Loader) GetSkillBasePath(skillName string) (string, error) {
	skill, ok := l.getCachedSkill(skillName)
	if !ok {
		var err error
		skill, err = l.LoadSkillInstructions(skillName)
		if err != nil {
			return "", fmt.Errorf("skill not found: %s", skillName)
		}
	}
	// Return absolute path for consistent sandbox execution
	return filepath.Abs(skill.BasePath)
}

// Reload clears the cache and rediscovers all skills
func (l *Loader) Reload() ([]*SkillMetadata, error) {
	l.mu.Lock()
	l.discoveredSkills = make(map[string]*Skill)
	l.mu.Unlock()
	return l.DiscoverSkills()
}
