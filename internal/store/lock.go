package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	// lockWait is how long a database writer waits for another process.
	lockWait = 3 * time.Second
	// lockStale is the age after which a database lock left behind by a
	// crashed process is broken. Writes take milliseconds.
	lockStale = 10 * time.Second
)

// ErrLocked is returned by AcquireLock when the lock is held by someone else
// for longer than the caller was willing to wait.
var ErrLocked = fmt.Errorf("locked by another jmp process")

// AcquireLock takes an advisory cross-process lock represented by the file at
// path (created exclusively, removed on release). It retries for up to wait
// (0 means a single attempt) and breaks locks older than stale. The exclusive
// create works on every platform jmp supports, unlike flock(2).
func AcquireLock(path string, wait, stale time.Duration) (release func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString(strconv.Itoa(os.Getpid()))
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if info, serr := os.Stat(path); serr == nil && time.Since(info.ModTime()) > stale {
			os.Remove(path)
			continue
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("%s: %w", path, ErrLocked)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
