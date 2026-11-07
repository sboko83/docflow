package gitx

import (
	"strings"
	"time"
)

// IndexLockWaits is the pause before each repeat of a Git command refused
// because another Git process holds index.lock: an editor polling
// `git status` takes the lock for a moment, and a commit or add that meets
// it fails at once. About three seconds in all; a lock left by a crashed
// process still fails, with Git's own message.
var IndexLockWaits = []time.Duration{
	50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond,
	400 * time.Millisecond, 800 * time.Millisecond, 1600 * time.Millisecond,
}

// IndexLocked reports whether Git stderr says the index lock was taken. The
// file name is matched, not the message, which Git translates.
func IndexLocked(stderr []byte) bool {
	return strings.Contains(string(stderr), "index.lock")
}

// RetryIndexLock runs a Git command and repeats it while it fails on a held
// index.lock. A command refused on the lock has changed nothing, so the
// repeat is safe. run reports its stderr and whether it failed.
func RetryIndexLock(run func() (stderr []byte, failed bool)) {
	for _, wait := range IndexLockWaits {
		stderr, failed := run()
		if !failed || !IndexLocked(stderr) {
			return
		}
		time.Sleep(wait)
	}
	run()
}
