package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/cyucelen/isola/internal/logging"
	"github.com/cyucelen/isola/internal/proxy"
	"github.com/cyucelen/isola/internal/registry"
	"github.com/spf13/cobra"
)

var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Manage the shared reverse proxy",
	Long:  "Start or stop the machine-wide reverse proxy that routes <branch>.<project>.localhost to every registered project's services.",
}

var proxyStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Run the shared reverse proxy daemon (foreground)",
	Long: `Run the machine-wide reverse proxy in the foreground.

It binds the union of every registered project's proxy ports and routes each
request by its <branch-slug>.<project>.localhost host to that project's backend,
serving HTTPS on ports where any project enabled it. It runs until interrupted
with Ctrl+C (SIGINT) or SIGTERM. 'isola up' normally starts it for you in the
background.`,
	// The daemon is machine-wide; it needs no repo or config of its own.
	Annotations: map[string]string{"skipRepoDetection": "true"},
	RunE: func(cmd *cobra.Command, args []string) error {
		reg, err := registry.Open()
		if err != nil {
			return err
		}
		if err := reg.SetDaemon(registry.Daemon{PID: os.Getpid(), Running: true}); err != nil {
			return fmt.Errorf("recording daemon state: %w", err)
		}
		defer func() { _ = reg.SetDaemon(registry.Daemon{}) }()

		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		logging.Info("isola proxy: routing <branch>.<project>.localhost for all registered projects (Ctrl+C to stop)")
		return proxy.NewDaemon(reg).Serve(ctx)
	},
}

var proxyStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the shared reverse proxy daemon",
	Long: `Stop the machine-wide reverse proxy.

This affects every project on this machine, not just the current repo.`,
	Annotations: map[string]string{"skipRepoDetection": "true"},
	RunE: func(cmd *cobra.Command, args []string) error {
		reg, err := registry.Open()
		if err != nil {
			return err
		}
		stopped, err := proxy.StopDaemon(reg)
		if err != nil {
			return fmt.Errorf("stopping proxy: %w", err)
		}
		if stopped {
			fmt.Println("Proxy stopped.")
		} else {
			fmt.Println("Proxy is not running.")
		}
		return nil
	},
}

func init() {
	proxyCmd.AddCommand(proxyStartCmd)
	proxyCmd.AddCommand(proxyStopCmd)
	rootCmd.AddCommand(proxyCmd)
}
