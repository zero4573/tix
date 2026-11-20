package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zero4573/tix/internal/notesec"
	"github.com/zero4573/tix/internal/session"
	"github.com/zero4573/tix/internal/vault"
	"github.com/zero4573/tix/internal/workspace"
)

func wsEnvCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "env [<ID>]",
		Short: "Print the workspace's tool environment as shell exports",
		Long: `Prints export lines for the environment a ticket workspace's sessions run
their tools with: GOPATH, GOMODCACHE and GOCACHE under <workspace>/.cache/go
(the workspace named by <ID>, else the one the current folder is in). A
session's settings set them already, but a subagent's or a second agent
CLI's shell doesn't always inherit them, and go then fails on a read-only
default module cache:

  eval "$(tix ws env)"`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWorkspaceID,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := vault.Require()
			if err != nil {
				return err
			}
			id := ""
			if len(args) == 1 {
				id = args[0]
			} else {
				cwd, _ := os.Getwd()
				id = notesec.WorkspaceID(cwd)
			}
			if err := requireKey("env", id); err != nil {
				return fmt.Errorf("env: not in a ticket workspace (name its ID)")
			}
			dir := filepath.Join(ctx.WorkRoot, id)
			if _, err := os.Stat(workspace.File(dir)); err != nil {
				return fmt.Errorf("env: %s has no workspace (%s)", id, dir)
			}
			env := session.GoEnv(dir)
			keys := make([]string, 0, len(env))
			for k := range env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(cmd.OutOrStdout(), "export %s='%s'\n", k, strings.ReplaceAll(env[k], "'", `'\''`))
			}
			return nil
		},
	}
}
