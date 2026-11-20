package cli

import "github.com/spf13/cobra"

func usageArgs(fn cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := fn(cmd, args); err != nil {
			return exitWith(2, err)
		}
		return nil
	}
}

func usageExit(cmd *cobra.Command) *cobra.Command {
	if cmd.Args != nil {
		cmd.Args = usageArgs(cmd.Args)
	}
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return exitWith(2, err) })
	return cmd
}
