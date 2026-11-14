package git

import (
	"fmt"
	"os"
	"strconv"
)

// Git quotes paths with bytes outside ASCII by default ("\346\226\207"),
// and the git module doesn't undo it: such files were listed under the
// quoted name and couldn't be opened. Turning core.quotePath off for every
// git this process runs keeps names as they are; git then quotes only names
// with control characters, quotes or backslashes in them.
//
// It's set through the environment, which git commands inherit, so it
// covers the git module and the commands run directly alike. Configuration
// already given that way is kept.
func init() {
	n := 0
	if v := os.Getenv("GIT_CONFIG_COUNT"); v != "" {
		c, err := strconv.Atoi(v)
		if err != nil || c < 0 {
			return
		}
		n = c
	}
	os.Setenv(fmt.Sprintf("GIT_CONFIG_KEY_%d", n), "core.quotePath") //nolint:errcheck
	os.Setenv(fmt.Sprintf("GIT_CONFIG_VALUE_%d", n), "false")        //nolint:errcheck
	os.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(n+1))                 //nolint:errcheck
}
