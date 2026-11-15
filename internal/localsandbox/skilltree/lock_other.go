//go:build !unix

package skilltree

import (
	"path/filepath"
	"sync"
)

var (
	processSkillLocksMu sync.Mutex
	processSkillLocks   = map[string]*sync.Mutex{}
)

func skillMutex(lockPath string) *sync.Mutex {
	processSkillLocksMu.Lock()
	defer processSkillLocksMu.Unlock()
	m, ok := processSkillLocks[lockPath]
	if !ok {
		m = &sync.Mutex{}
		processSkillLocks[lockPath] = m
	}
	return m
}

// Lock serialises installs of one skill in-process on non-Unix platforms.
func (t *Tree) Lock(name string) (func(), error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	m := skillMutex(filepath.Join(t.root, locksDir, name+".lock"))
	m.Lock()
	var once sync.Once
	return func() { once.Do(m.Unlock) }, nil
}

func (t *Tree) tryLock(name string) (func(), bool, error) {
	if err := validName(name); err != nil {
		return nil, false, err
	}
	m := skillMutex(filepath.Join(t.root, locksDir, name+".lock"))
	if !m.TryLock() {
		return nil, false, nil
	}
	var once sync.Once
	return func() { once.Do(m.Unlock) }, true, nil
}
