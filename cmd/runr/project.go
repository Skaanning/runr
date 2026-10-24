package main

import "github.com/spf13/cobra"

// projectFlags holds the flags shared by add and edit.
type projectFlags struct {
	dir         string
	env         []string
	envFile     string
	dependsOn   []string
	color       string
	displayName string
	readyLog    string
}

func (f *projectFlags) register(cmd *cobra.Command) {
	fs := cmd.Flags()
	fs.StringVar(&f.dir, "dir", "", "working directory (add defaults to the current directory)")
	fs.StringArrayVar(&f.env, "env", nil, "environment variable as KEY=VALUE (repeatable)")
	fs.StringVar(&f.envFile, "env-file", "", "path to a .env file, relative to the working directory")
	fs.StringArrayVar(&f.dependsOn, "depends-on", nil, "project that must be ready first (repeatable)")
	fs.StringVar(&f.color, "color", "", "color used for the project in the TUI")
	fs.StringVar(&f.displayName, "display-name", "", "label shown in the TUI")
	fs.StringVar(&f.readyLog, "ready-log", "", "regex; project is ready once an output line matches")
}

func newAddCmd() *cobra.Command {
	var flags projectFlags
	cmd := &cobra.Command{
		Use:   "add <name> [flags] -- <cmd>",
		Short: "Register a project",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("add")
		},
	}
	flags.register(cmd)
	return cmd
}

func newEditCmd() *cobra.Command {
	var flags projectFlags
	cmd := &cobra.Command{
		Use:   "edit <name> [flags] [-- <cmd>]",
		Short: "Modify a registered project",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("edit")
		},
	}
	flags.register(cmd)
	return cmd
}

func newRmCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a registered project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("rm")
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove even if other projects depend on it")
	return cmd
}

func newLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List projects and groups",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("ls")
		},
	}
}
