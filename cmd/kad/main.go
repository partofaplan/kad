// Command kad builds, inspects and removes local Kubernetes development
// environments from a single declaration.
package main

import (
	"os"

	"github.com/partofaplan/kad/internal/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:])) }
