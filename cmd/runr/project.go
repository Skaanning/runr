package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Skaanning/runr/internal/config"
)

// projectFlags holds the flags shared by add and edit.
type projectFlags struct {
	dir         string
	env         []string
	envFile     string
	dependsOn   []string
	color       string
	displayName string
	ready       string
	readyLog    string
	readyPort   string // a string so that --ready-port= can clear it
	readyURL    string
	pty         bool
	stopCmd     string
}

func (f *projectFlags) register(cmd *cobra.Command) {
	fs := cmd.Flags()
	fs.StringVar(&f.dir, "dir", "", "working directory (add defaults to the current directory)")
	fs.StringArrayVar(&f.env, "env", nil, "environment variable as KEY=VALUE (repeatable)")
	fs.StringVar(&f.envFile, "env-file", "", "path to a .env file, relative to the working directory")
	fs.StringSliceVar(&f.dependsOn, "depends-on", nil, "project that must be ready first (repeatable or comma-separated)")
	fs.StringVar(&f.color, "color", "", "color used for the project in the TUI")
	fs.StringVar(&f.displayName, "display-name", "", "label shown in the TUI")
	fs.StringVar(&f.ready, "ready", "", `"exit": a one-shot task, ready once it exits with code 0`)
	fs.StringVar(&f.readyLog, "ready-log", "", "regex; project is ready once an output line matches")
	fs.StringVar(&f.readyPort, "ready-port", "", "project is ready once this TCP port on localhost accepts connections")
	fs.StringVar(&f.readyURL, "ready-url", "", "project is ready once a GET of this URL answers 2xx or 3xx")
	fs.StringVar(&f.stopCmd, "stop-cmd", "", `command run after runr had to kill the project, e.g. "docker compose down"`)
	fs.BoolVar(&f.pty, "pty", false, "run on a pseudo-terminal, so it behaves as in a terminal (colors, line buffering); --pty=false turns it off")

	_ = cmd.RegisterFlagCompletionFunc("depends-on", completeProjects)
	_ = cmd.RegisterFlagCompletionFunc("ready", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{config.ReadyExit}, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("color", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return config.ColorNames, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.MarkFlagDirname("dir")
}

// apply copies every flag the user set onto p. An empty value clears the field.
func (f *projectFlags) apply(cmd *cobra.Command, p *config.Project) error {
	changed := cmd.Flags().Changed
	if changed("dir") {
		dir, err := config.NormalizeDir(f.dir)
		if err != nil {
			return err
		}
		p.Dir = dir
	}
	if changed("env") {
		if p.Env == nil {
			p.Env = map[string]string{}
		}
		for _, kv := range f.env {
			key, value, ok := strings.Cut(kv, "=")
			if !ok {
				return fmt.Errorf("--env %q: expected KEY=VALUE", kv)
			}
			p.Env[key] = value
		}
	}
	if changed("env-file") {
		p.EnvFile = f.envFile
	}
	if changed("depends-on") {
		p.DependsOn = nil
		for _, dep := range f.dependsOn {
			if dep = strings.TrimSpace(dep); dep != "" {
				p.DependsOn = append(p.DependsOn, dep)
			}
		}
	}
	if changed("color") {
		p.Color = f.color
	}
	if changed("display-name") {
		p.DisplayName = f.displayName
	}
	if changed("ready") {
		p.Ready = f.ready
	}
	if changed("ready-log") {
		p.ReadyLog = f.readyLog
	}
	if changed("ready-port") {
		p.ReadyPort = 0
		if f.readyPort != "" {
			port, err := strconv.Atoi(f.readyPort)
			if err != nil {
				return fmt.Errorf("--ready-port %q: not a number", f.readyPort)
			}
			p.ReadyPort = port
		}
	}
	if changed("ready-url") {
		p.ReadyURL = f.readyURL
	}
	if changed("pty") {
		p.PTY = f.pty
	}
	if changed("stop-cmd") {
		p.StopCmd = strings.TrimSpace(f.stopCmd)
	}
	return nil
}

// splitNameAndCommand parses "<name> -- <cmd...>" or "<name> [<cmd>]".
func splitNameAndCommand(cmd *cobra.Command, args []string) (name, command string, err error) {
	dash := cmd.ArgsLenAtDash()
	if dash == -1 {
		switch len(args) {
		case 1:
			return args[0], "", nil
		case 2:
			return args[0], args[1], nil
		default:
			return "", "", fmt.Errorf("too many arguments; put the command after --, e.g. runr %s %s -- npm run dev", cmd.Name(), args[0])
		}
	}
	if dash != 1 {
		return "", "", errors.New("expected exactly one project name before --")
	}
	rest := args[1:]
	switch len(rest) {
	case 0:
		return "", "", errors.New("missing command after --")
	case 1:
		return args[0], rest[0], nil
	default:
		return args[0], joinCommand(rest), nil
	}
}

func newAddCmd() *cobra.Command {
	var flags projectFlags
	cmd := &cobra.Command{
		Use:   "add <name> [flags] -- <cmd>",
		Short: "Register a project",
		Long: "Register a project. The working directory defaults to the current directory.\n\n" +
			"Everything after -- is the command, e.g.\n" +
			"  runr add api --env PORT=8080 -- go run ./cmd/server",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, command, err := splitNameAndCommand(cmd, args)
			if err != nil {
				return err
			}
			if command == "" {
				return fmt.Errorf("missing command, e.g. runr add %s -- npm run dev", name)
			}

			s, err := loadStore()
			if err != nil {
				return err
			}
			if s.cfg.Projects[name] != nil {
				return fmt.Errorf("project %q already exists (use runr edit to change it)", name)
			}

			p := &config.Project{Cmd: command}
			if err := flags.apply(cmd, p); err != nil {
				return err
			}
			if p.Dir == "" {
				if p.Dir, err = os.Getwd(); err != nil {
					return err
				}
				if p.Dir, err = config.NormalizeDir(p.Dir); err != nil {
					return err
				}
			}
			s.cfg.Projects[name] = p

			warnings, err := s.save()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added project %q\n  dir: %s\n  cmd: %s\n", name, config.DisplayPath(p.Dir), p.Cmd)
			printWarnings(cmd.ErrOrStderr(), warnings, name)
			return nil
		},
	}
	flags.register(cmd)
	return cmd
}

func newEditCmd() *cobra.Command {
	var flags projectFlags
	var unsetEnv []string
	cmd := &cobra.Command{
		Use:   "edit <name> [flags] [-- <cmd>]",
		Short: "Modify a registered project",
		Long: "Modify a registered project. Only the given flags are changed.\n\n" +
			"  --env sets or overrides single variables; --unset-env removes them.\n" +
			"  --depends-on replaces the whole list; --depends-on= clears it.\n" +
			"  Other flags set with an empty value (e.g. --color=) clear the field.",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeFirstArg(completeProjects),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, command, err := splitNameAndCommand(cmd, args)
			if err != nil {
				return err
			}

			s, err := loadStore()
			if err != nil {
				return err
			}
			p := s.cfg.Projects[name]
			if p == nil {
				return fmt.Errorf("unknown project %q", name)
			}
			if command == "" && cmd.Flags().NFlag() == 0 {
				return errors.New("nothing to change; pass flags or a new command after --")
			}

			if command != "" {
				p.Cmd = command
			}
			if err := flags.apply(cmd, p); err != nil {
				return err
			}
			for _, key := range unsetEnv {
				delete(p.Env, key)
			}
			if len(p.Env) == 0 {
				p.Env = nil
			}

			warnings, err := s.save()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated project %q\n", name)
			printWarnings(cmd.ErrOrStderr(), warnings, name)
			return nil
		},
	}
	flags.register(cmd)
	cmd.Flags().StringArrayVar(&unsetEnv, "unset-env", nil, "environment variable to remove (repeatable)")
	return cmd
}

func newRmCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:               "rm <name>",
		Short:             "Remove a registered project",
		Long:              "Remove a registered project. It is also removed from any groups.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeFirstArg(completeProjects),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			s, err := loadStore()
			if err != nil {
				return err
			}
			if s.cfg.Projects[name] == nil {
				return fmt.Errorf("unknown project %q", name)
			}

			dependents := s.cfg.Dependents(name)
			if len(dependents) > 0 && !force {
				return fmt.Errorf("cannot remove %q: required by %s\n(use --force to remove it and drop it from their depends_on)",
					name, strings.Join(dependents, ", "))
			}

			groups, _ := s.cfg.RemoveProject(name)

			if _, err := s.save(); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Removed project %q\n", name)
			if len(groups) > 0 {
				fmt.Fprintf(out, "  removed from groups: %s\n", strings.Join(groups, ", "))
			}
			if len(dependents) > 0 {
				fmt.Fprintf(out, "  removed from depends_on of: %s\n", strings.Join(dependents, ", "))
			}
			return nil
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
			s, err := loadStore()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(s.cfg.Projects) == 0 {
				fmt.Fprintln(out, "No projects registered yet. Add one with:\n  runr add <name> -- <command>")
				return nil
			}

			// The PTY and STOP CMD columns only when a project uses them.
			pty := slices.ContainsFunc(slices.Collect(maps.Values(s.cfg.Projects)), func(p *config.Project) bool { return p.PTY })
			stopCmd := slices.ContainsFunc(slices.Collect(maps.Values(s.cfg.Projects)), func(p *config.Project) bool { return p.StopCmd != "" })
			var table strings.Builder
			tw := tabwriter.NewWriter(&table, 0, 0, 2, ' ', 0)
			header := "PROJECT\tDIR\tCMD\tDEPENDS ON\tREADY"
			if pty {
				header += "\tPTY"
			}
			if stopCmd {
				header += "\tSTOP CMD"
			}
			fmt.Fprintln(tw, header)
			for _, name := range sortedNames(s.cfg.Projects) {
				p := s.cfg.Projects[name]
				row := fmt.Sprintf("%s\t%s\t%s\t%s\t%s", name, config.DisplayPath(p.Dir), p.Cmd, strings.Join(p.DependsOn, ", "), readyText(p))
				if pty && p.PTY {
					row += "\tyes"
				} else if pty {
					row += "\t"
				}
				if stopCmd {
					row += "\t" + p.StopCmd
				}
				fmt.Fprintln(tw, row)
			}
			tw.Flush()
			for line := range strings.Lines(table.String()) {
				fmt.Fprintln(out, strings.TrimRight(line, " \n"))
			}

			if len(s.cfg.Groups) > 0 {
				fmt.Fprintln(out)
				printGroups(out, s.cfg)
			}

			// Listing still works with an invalid config, but the problems are reported.
			warnings, err := s.cfg.Validate()
			printWarnings(cmd.ErrOrStderr(), warnings, "")
			return err
		},
	}
}

// readyText describes when a project counts as ready, for runr ls; "" when
// it is ready as soon as it is started.
func readyText(p *config.Project) string {
	switch {
	case p.OneShot():
		return "on exit (one-shot)"
	case p.ReadyLog != "":
		return "log: " + p.ReadyLog
	case p.ReadyPort != 0:
		return "port " + strconv.Itoa(p.ReadyPort)
	case p.ReadyURL != "":
		return "url " + p.ReadyURL
	}
	return ""
}

func without(list []string, item string) []string {
	var out []string
	for _, s := range list {
		if s != item {
			out = append(out, s)
		}
	}
	return out
}

func sortedNames[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// completeProjects offers registered project names for shell completion.
func completeProjects(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	s, err := loadStore()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return sortedNames(s.cfg.Projects), cobra.ShellCompDirectiveNoFileComp
}

// completeFirstArg applies a completion function to the first positional
// argument only.
func completeFirstArg(fn cobra.CompletionFunc) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return fn(cmd, args, toComplete)
	}
}
