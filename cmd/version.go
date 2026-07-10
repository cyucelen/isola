package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Set via ldflags at build time (a release build); left at these defaults for a
// plain `go install`.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// versionInfo returns the version/commit/date, falling back to the module build
// info embedded by `go install` when ldflags weren't applied (so a `go install`
// binary reports its pseudo-version and VCS revision instead of "dev").
func versionInfo() (v, c, d string) {
	v, c, d = version, commit, date
	if v != "dev" {
		return v, c, d
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return v, c, d
	}
	if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		v = bi.Main.Version
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if c == "none" {
				c = s.Value
			}
		case "vcs.time":
			if d == "unknown" {
				d = s.Value
			}
		}
	}
	return v, c, d
}

var versionCmd = &cobra.Command{
	Use:         "version",
	Short:       "Print the version of isola",
	Annotations: map[string]string{"skipRepoDetection": "true"},
	RunE: func(cmd *cobra.Command, args []string) error {
		v, c, d := versionInfo()
		jsonFlag, _ := cmd.Flags().GetBool("json")
		if jsonFlag {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{
				"version": v,
				"commit":  c,
				"date":    d,
			})
		}
		fmt.Printf("isola %s (commit: %s, built: %s)\n", v, c, d)
		return nil
	},
}

func init() {
	versionCmd.Flags().Bool("json", false, "Output in JSON format")
	rootCmd.AddCommand(versionCmd)
}
