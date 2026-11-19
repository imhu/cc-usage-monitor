package platform

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var ErrLocked = errors.New("directory lock held by another process")

// DirectoryLock is compatible with Claude Code's proper-lockfile directory locking protocol.
type DirectoryLock struct {
	Path           string
	Stale          time.Duration
	UpdateInterval time.Duration

	mu          sync.Mutex
	ownMtime    time.Time
	compromised bool
	stopCh      chan struct{}
}

func NewDirectoryLock(path string, stale, update time.Duration) *DirectoryLock {
	if update <= 0 {
		update = stale / 2
	}
	return &DirectoryLock{
		Path:           path,
		Stale:          stale,
		UpdateInterval: update,
	}
}

func (l *DirectoryLock) Acquire() error {
	err := os.Mkdir(l.Path, 0o777)
	if err == nil {
		l.didAcquire()
		return nil
	}
	if !os.IsExist(err) {
		return err
	}
	info, statErr := os.Stat(l.Path)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			if err := os.Mkdir(l.Path, 0o777); err == nil {
				l.didAcquire()
				return nil
			}
			return ErrLocked
		}
		return statErr
	}
	if time.Since(info.ModTime()) < l.Stale {
		return ErrLocked
	}
	// Stale lock: remove and retry once
	if rmErr := os.Remove(l.Path); rmErr != nil && !os.IsNotExist(rmErr) {
		return rmErr
	}
	if err := os.Mkdir(l.Path, 0o777); err != nil {
		if os.IsExist(err) {
			return ErrLocked
		}
		return err
	}
	l.didAcquire()
	return nil
}

func (l *DirectoryLock) IsCompromised() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.compromised
}

func (l *DirectoryLock) Release() {
	l.mu.Lock()
	if l.stopCh != nil {
		close(l.stopCh)
		l.stopCh = nil
	}
	shouldRemove := !l.compromised && l.isStillOursLocked()
	l.ownMtime = time.Time{}
	l.mu.Unlock()
	if shouldRemove {
		_ = os.Remove(l.Path)
	}
}

func (l *DirectoryLock) didAcquire() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.compromised = false
	l.ownMtime = l.touchLocked()
	stop := make(chan struct{})
	l.stopCh = stop
	go func() {
		ticker := time.NewTicker(l.UpdateInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				l.mu.Lock()
				if l.stopCh != stop || l.compromised {
					l.mu.Unlock()
					return
				}
				if !l.isStillOursLocked() {
					l.compromised = true
					l.mu.Unlock()
					return
				}
				if mt := l.touchLocked(); !mt.IsZero() {
					l.ownMtime = mt
				}
				l.mu.Unlock()
			}
		}
	}()
}

func (l *DirectoryLock) touchLocked() time.Time {
	now := time.Now()
	if err := os.Chtimes(l.Path, now, now); err != nil {
		return time.Time{}
	}
	info, err := os.Stat(l.Path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func (l *DirectoryLock) isStillOursLocked() bool {
	if l.ownMtime.IsZero() {
		return false
	}
	info, err := os.Stat(l.Path)
	if err != nil {
		return false
	}
	return info.ModTime().Equal(l.ownMtime)
}

func AcquireClaudeRefreshLocks(configDir string) ([]*DirectoryLock, error) {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, err
	}
	primary := NewDirectoryLock(filepath.Join(configDir, ".oauth_refresh.lock"), 60*time.Second, 5*time.Second)
	if err := primary.Acquire(); err != nil {
		return nil, err
	}
	legacy := NewDirectoryLock(filepath.Clean(configDir)+".lock", 60*time.Second, 5*time.Second)
	if err := legacy.Acquire(); err != nil {
		primary.Release()
		return nil, err
	}
	return []*DirectoryLock{primary, legacy}, nil
}

func AcquireClaudeWriteLock(configDir string) (*DirectoryLock, error) {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, err
	}
	lock := NewDirectoryLock(filepath.Join(configDir, ".storage-write.lock"), 15*time.Second, 5*time.Second)
	if err := lock.Acquire(); err != nil {
		return nil, err
	}
	return lock, nil
}

func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".ccmonitor-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	_ = f.Sync()
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Chmod(tmp, perm)
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// WriteAndVerifyFile atomically writes data and reads it back to confirm persistence.
func WriteAndVerifyFile(path string, data []byte, perm os.FileMode) bool {
	if err := AtomicWriteFile(path, data, perm); err != nil {
		return false
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return bytes.Equal(got, data)
}
