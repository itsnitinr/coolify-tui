// Command coolify-tui is a terminal dashboard for Coolify: monitor
// applications across servers, trigger deployments and watch them run.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/itsnitinr/coolify-tui/internal/config"
	"github.com/itsnitinr/coolify-tui/internal/coolify"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

const usage = `coolify-tui — a terminal dashboard for Coolify

Usage:
  coolify-tui [flags]            Launch the dashboard
  coolify-tui doctor             Check connectivity for every configured instance
  coolify-tui instances          List configured instances

Flags:
  -instance NAME   Instance to open (default: active_instance from config)
  -config          Print the config file path and exit
  -version         Print the version and exit
  -help            Show this help

Configuration lives at:
  ${XDG_CONFIG_HOME:-~/.config}/coolify-tui/config.yaml   (mode 0600)

Tokens are read from that file, or from the environment when an instance sets
token_env. Create a token in Coolify under Security -> API Tokens with the
read, read:sensitive, write and deploy permissions.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("coolify-tui", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }

	var (
		instanceName = fs.String("instance", "", "instance to open")
		showConfig   = fs.Bool("config", false, "print the config file path")
		showVersion  = fs.Bool("version", false, "print the version")
	)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	switch {
	case *showVersion:
		fmt.Println("coolify-tui " + version)
		return nil
	case *showConfig:
		path, err := config.Path()
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd := fs.Arg(0); cmd {
	case "doctor":
		return doctor(ctx)
	case "instances":
		return listInstances()
	case "":
		return launch(ctx, *instanceName)
	default:
		return fmt.Errorf("unknown command %q (try -help)", cmd)
	}
}

// launch starts the dashboard. Wired up in phase 3.
func launch(ctx context.Context, instanceName string) error {
	cfg, err := config.Load()
	if errors.Is(err, config.ErrNoConfig) {
		return errors.New("no configuration yet — onboarding lands in phase 2; " +
			"until then write ~/.config/coolify-tui/config.yaml by hand (see README)")
	}
	if err != nil {
		return err
	}
	inst, err := selectInstance(cfg, instanceName)
	if err != nil {
		return err
	}
	client, err := newClient(inst)
	if err != nil {
		return err
	}
	inv, err := client.FetchInventory(ctx)
	if err != nil {
		return err
	}
	running, degraded, stopped := inv.Counts()
	fmt.Printf("%s: %d servers, %d applications (%d running, %d degraded, %d stopped)\n",
		inst.Name, len(inv.Servers), len(inv.Apps), running, degraded, stopped)
	fmt.Println("The dashboard UI lands in phase 3; run `coolify-tui doctor` meanwhile.")
	return nil
}

// listInstances prints the configured instances without revealing tokens.
func listInstances() error {
	cfg, err := config.Load()
	if errors.Is(err, config.ErrNoConfig) {
		fmt.Println("No instances configured yet.")
		return nil
	}
	if err != nil {
		return err
	}
	if warn := cfg.PermissionWarning(); warn != "" {
		fmt.Fprintln(os.Stderr, "warning: "+warn)
	}
	active, _ := cfg.Active()
	for _, inst := range cfg.Instances {
		marker := " "
		if inst.Name == active.Name {
			marker = "*"
		}
		source := "config file"
		if inst.TokenEnv != "" {
			source = "$" + inst.TokenEnv
		}
		fmt.Printf("%s %-16s %-40s token from %s\n", marker, inst.Name, inst.URL, source)
	}
	return nil
}

// doctor checks that each configured instance is reachable and that its token
// carries the permissions the dashboard needs.
func doctor(ctx context.Context) error {
	cfg, err := config.Load()
	if errors.Is(err, config.ErrNoConfig) {
		path, _ := config.Path()
		return fmt.Errorf("no configuration found at %s", path)
	}
	if err != nil {
		return err
	}
	if warn := cfg.PermissionWarning(); warn != "" {
		fmt.Println("⚠ " + warn)
	}
	if len(cfg.Instances) == 0 {
		return errors.New("no instances configured")
	}

	var failed bool
	for _, inst := range cfg.Instances {
		fmt.Printf("\n%s (%s)\n", inst.Name, inst.URL)

		client, err := newClient(inst)
		if err != nil {
			fmt.Println("  ✗ " + err.Error())
			failed = true
			continue
		}
		fmt.Println("  base URL: " + client.BaseURL())

		reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		ver, err := client.Version(reqCtx)
		cancel()
		if err != nil {
			fmt.Println("  ✗ version: " + err.Error())
			failed = true
			continue
		}
		fmt.Println("  ✓ reachable, Coolify " + ver)

		for _, check := range []struct {
			label string
			run   func(context.Context) (string, error)
		}{
			{"read (servers)", func(c context.Context) (string, error) {
				servers, err := client.Servers(c)
				if err != nil {
					return "", err
				}
				var unhealthy []string
				for _, s := range servers {
					if s.Health() != coolify.ServerHealthy {
						unhealthy = append(unhealthy, fmt.Sprintf("%s=%s", s.Name, s.Health()))
					}
				}
				msg := fmt.Sprintf("%d servers", len(servers))
				if len(unhealthy) > 0 {
					msg += " (" + strings.Join(unhealthy, ", ") + ")"
				}
				return msg, nil
			}},
			{"read (applications)", func(c context.Context) (string, error) {
				apps, err := client.Applications(c)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("%d applications", len(apps)), nil
			}},
			{"read (deployments)", func(c context.Context) (string, error) {
				deployments, err := client.Deployments(c)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("%d queued/running", len(deployments)), nil
			}},
		} {
			reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			detail, err := check.run(reqCtx)
			cancel()
			if err != nil {
				fmt.Printf("  ✗ %s: %v\n", check.label, err)
				failed = true
				continue
			}
			fmt.Printf("  ✓ %s: %s\n", check.label, detail)
		}
	}

	fmt.Println()
	if failed {
		return errors.New("one or more checks failed")
	}
	fmt.Println("All checks passed.")
	return nil
}

// selectInstance resolves the instance to use, preferring an explicit name.
func selectInstance(cfg *config.Config, name string) (config.Instance, error) {
	if name != "" {
		inst, ok := cfg.Instance(name)
		if !ok {
			return config.Instance{}, fmt.Errorf("no instance named %q (configured: %s)",
				name, strings.Join(cfg.Names(), ", "))
		}
		return inst, nil
	}
	inst, ok := cfg.Active()
	if !ok {
		return config.Instance{}, errors.New("no instances configured")
	}
	return inst, nil
}

// newClient builds an API client for a configured instance.
func newClient(inst config.Instance) (*coolify.Client, error) {
	token, err := inst.ResolveToken()
	if err != nil {
		return nil, err
	}
	return coolify.New(inst.URL, token,
		coolify.WithInsecureSkipVerify(inst.InsecureSkipVerify),
		coolify.WithUserAgent("coolify-tui/"+version),
	)
}
