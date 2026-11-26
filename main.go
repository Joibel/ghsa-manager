// Command ghsa-manager shows every draft argo-workflows security advisory
// with its fork, fix PRs, mirror CI and reviews, and runs CI on request.
package main

import (
	"os"

	"github.com/Joibel/ghsa-manager/cmd"
)

func main() {
	if err := cmd.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
