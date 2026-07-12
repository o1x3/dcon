package compose

import (
	"fmt"
	"os"
	"sync"
)

var (
	warnMu sync.Mutex
	warned = map[string]bool{}
)

// warnOnce emits a `dcon: warning:` line on stderr at most once per key per
// process, matching the repo convention that backend/spec gaps warn rather
// than error.
func warnOnce(key, format string, a ...any) {
	warnMu.Lock()
	defer warnMu.Unlock()
	if warned[key] {
		return
	}
	warned[key] = true
	fmt.Fprintf(os.Stderr, "dcon: warning: "+format+"\n", a...)
}

// resetWarnings clears the once-state; tests only.
func resetWarnings() {
	warnMu.Lock()
	warned = map[string]bool{}
	warnMu.Unlock()
}
