package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/importer"
)

type importFlags struct {
	format  string
	group   string
	noGroup bool
	prefix  string
	dryRun  bool
}

func newImportCmd() *cobra.Command {
	var flags importFlags
	cmd := &cobra.Command{
		Use:   "import [file|dir]",
		Short: "Register projects from a Procfile or compose file",
		Long: "Register one project per line of a Procfile, or per service of a Docker Compose\n" +
			"file, plus a group of them all. Without a file, the current directory (or the given\n" +
			"one) is searched for " + strings.Join(importer.Candidates, ", ") + ".\n\n" +
			"Compose services run as `docker compose up --no-deps --no-log-prefix <service>`;\n" +
			"runr starts their depends_on itself, and a published port becomes ready_port.\n" +
			"This is a one-time copy: later changes to the file are not picked up.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			return runImport(cmd.OutOrStdout(), cmd.ErrOrStderr(), arg, flags)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&flags.format, "format", "", "procfile or compose (default: from the file name)")
	fs.StringVar(&flags.group, "group", "", "name of the group of imported projects (default: the compose name or the directory)")
	fs.BoolVar(&flags.noGroup, "no-group", false, "don't create a group")
	fs.StringVar(&flags.prefix, "prefix", "", "put this in front of every project name, e.g. shop-")
	fs.BoolVar(&flags.dryRun, "dry-run", false, "print the TOML that would be added and save nothing")
	cmd.MarkFlagsMutuallyExclusive("group", "no-group")
	_ = cmd.RegisterFlagCompletionFunc("format", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		var names []string
		for _, f := range importer.Formats {
			names = append(names, string(f))
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

func runImport(out, stderr io.Writer, arg string, flags importFlags) error {
	format := importer.Format(flags.format)
	if format != "" && !slices.Contains(importer.Formats, format) {
		return fmt.Errorf("--format %q: use procfile or compose", flags.format)
	}
	path, err := importPath(arg, format)
	if err != nil {
		return err
	}
	if format == "" {
		var ok bool
		if format, ok = importer.FormatOf(path); !ok {
			return fmt.Errorf("can't tell the format of %s from its name; pass --format procfile or --format compose", path)
		}
	}
	file, err := importer.Load(path, format)
	if err != nil {
		return err
	}

	s, err := loadStore()
	if err != nil {
		return err
	}
	added, order, err := importedProjects(file, flags.prefix)
	if err != nil {
		return err
	}
	group := ""
	if !flags.noGroup {
		if group = flags.group; group == "" {
			group = file.Name
			if group == "" {
				group = filepath.Base(file.Dir)
			}
			if group = cleanName(group); group == "" {
				return fmt.Errorf("can't name a group after %s; pass --group <name> or --no-group", file.Dir)
			}
		}
	}
	example := group
	if example == "" {
		example = cleanName(filepath.Base(file.Dir))
	}
	if err := checkClashes(s.cfg, added, group, example+"-"); err != nil {
		return err
	}

	for name, p := range added {
		s.cfg.Projects[name] = p
	}
	if group != "" {
		s.cfg.Groups[group] = order
	}
	warnings, err := s.cfg.Validate()
	if err != nil {
		return err
	}

	if flags.dryRun {
		addition := &config.Config{Projects: added}
		if group != "" {
			addition.Groups = map[string][]string{group: order}
		}
		data, err := toml.Marshal(addition)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "# runr import --dry-run: would add to %s\n\n%s", config.DisplayPath(s.path), data)
		return nil
	}
	if err := config.Save(s.path, s.cfg); err != nil {
		return err
	}

	what := "projects"
	if len(order) == 1 {
		what = "project"
	}
	fmt.Fprintf(out, "Imported %d %s from %s\n", len(order), what, config.DisplayPath(file.Path))
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  PROJECT\tCMD\tDEPENDS ON\tREADY")
	for _, name := range order {
		p := added[name]
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", name, p.Cmd, strings.Join(p.DependsOn, ", "), readyText(p))
	}
	tw.Flush()
	if group != "" {
		fmt.Fprintf(out, "Created group %q; start it with: runr up %s\n", group, group)
	}
	for _, name := range order {
		printWarnings(stderr, warnings, name)
	}
	return nil
}

// importPath picks the file to import: arg itself, or the one file to
// import found in arg's directory (the current directory if arg is "").
func importPath(arg string, format importer.Format) (string, error) {
	dir := arg
	if arg == "" {
		dir = "."
	} else if fi, err := os.Stat(arg); err != nil {
		return "", err
	} else if !fi.IsDir() {
		return arg, nil
	}

	var found []string
	for _, name := range importer.Find(dir) {
		if f, _ := importer.FormatOf(name); format == "" || f == format {
			found = append(found, name)
		}
	}
	where := "the current directory"
	if arg != "" {
		where = arg
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no Procfile or compose file in %s; name the file to import, e.g. runr import Procfile.dev", where)
	case 1:
		return filepath.Join(dir, found[0]), nil
	}
	return "", fmt.Errorf("found several files to import in %s: %s\nname the one to import, e.g. runr import %s",
		where, strings.Join(found, ", "), filepath.Join(arg, found[0]))
}

// importedProjects turns the file's services into projects named
// prefix+service (cleaned up to a valid name). order lists the names in
// file order.
func importedProjects(file *importer.File, prefix string) (projects map[string]*config.Project, order []string, err error) {
	names := map[string]string{} // service → project
	from := map[string]string{}  // project → service
	for _, svc := range file.Services {
		name := cleanName(prefix + svc.Name)
		if name == "" {
			return nil, nil, fmt.Errorf("%q can't be made into a project name", prefix+svc.Name)
		}
		if other, ok := from[name]; ok {
			return nil, nil, fmt.Errorf("%q and %q would both be named %q", other, svc.Name, name)
		}
		names[svc.Name], from[name] = name, svc.Name
		order = append(order, name)
	}

	dir, err := config.NormalizeDir(file.Dir) // $RUNR_ROOT/… when under the root
	if err != nil {
		return nil, nil, err
	}
	projects = map[string]*config.Project{}
	for _, svc := range file.Services {
		p := &config.Project{Dir: dir, Cmd: svc.Cmd, EnvFile: file.EnvFile, ReadyPort: svc.Port}
		if svc.Args != nil {
			p.Cmd = joinCommand(svc.Args)
		}
		if svc.OneShot {
			p.Ready = config.ReadyExit
		}
		for _, dep := range svc.DependsOn {
			p.DependsOn = append(p.DependsOn, names[dep])
		}
		projects[names[svc.Name]] = p
	}
	return projects, order, nil
}

var invalidNameChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// cleanName makes s a valid project or group name: runs of other
// characters become '-', and leading '.', '_' and '-' are dropped.
func cleanName(s string) string {
	return strings.TrimLeft(invalidNameChars.ReplaceAllString(s, "-"), "._-")
}

// checkClashes refuses names that are already taken by a project or a
// group, naming every clash. prefix is the --prefix to suggest.
func checkClashes(cfg *config.Config, added map[string]*config.Project, group, prefix string) error {
	taken := func(name string) string {
		if cfg.Projects[name] != nil {
			return "a project"
		}
		if _, ok := cfg.Groups[name]; ok {
			return "a group"
		}
		return ""
	}
	var clashes []string
	for _, name := range sortedNames(added) {
		if what := taken(name); what != "" {
			clashes = append(clashes, fmt.Sprintf("%s (%s)", name, what))
		}
	}
	var problems []string
	if len(clashes) > 0 {
		problems = append(problems, "these names are taken: "+strings.Join(clashes, ", ")+
			"\nuse --prefix to import them under other names, e.g. --prefix "+prefix)
	}
	if group != "" {
		what := taken(group)
		if what == "" && added[group] != nil {
			what = "an imported project"
		}
		if what != "" {
			problems = append(problems, fmt.Sprintf("the group name %q is taken by %s\nuse --group <name> to name it differently, or --no-group", group, what))
		}
	}
	if len(problems) > 0 {
		return errors.New("nothing imported: " + strings.Join(problems, "\n"))
	}
	return nil
}
