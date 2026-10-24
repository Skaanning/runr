package main

import (
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"runr/internal/config"
	"runr/internal/supervisor"
	"runr/internal/tui"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "runr",
		Short: "Run multiple local projects at once",
		Long: "runr runs multiple local projects (e.g. microservices) at once and shows\n" +
			"their output in a terminal UI. Run without arguments to open the launcher.",
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLauncher(cmd)
		},
	}

	root.AddCommand(
		newUpCmd(),
		newAddCmd(),
		newEditCmd(),
		newRmCmd(),
		newRenameCmd(),
		newLsCmd(),
		newGroupCmd(),
	)
	return root
}

// runLauncher opens the launcher; whatever the user starts from it runs in
// the same program.
func runLauncher(cmd *cobra.Command) error {
	if !isTerminal(cmd.InOrStdin()) || !isTerminal(cmd.OutOrStdout()) {
		return errors.New("the launcher needs a terminal; use runr up <name> instead")
	}
	s, err := loadStore()
	if err != nil {
		return err
	}
	if _, err := s.cfg.Validate(); err != nil {
		return err
	}

	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)

	var sup *supervisor.Supervisor
	start := func(names []string) (tui.Session, error) {
		if _, err := prepareUp(s.cfg, names, io.Discard); err != nil {
			return nil, err
		}
		sup = supervisor.New(s.cfg, supervisor.Options{})
		if err := sup.Start(names...); err != nil {
			sup.Close()
			sup = nil
			return nil, err
		}
		return sup, nil
	}
	// Forms edit a copy; once it validates and is written, the launcher
	// copies it into s.cfg, which start reads.
	save := func(c *config.Config) error {
		_, err := (&store{path: s.path, cfg: c}).save()
		return err
	}
	err = tui.RunLauncher(s.cfg, start, save, gitReader(), sigs)
	if sup != nil {
		finish(cmd.ErrOrStderr(), sup)
	}
	return err
}
