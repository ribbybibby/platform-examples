package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/chainguard-dev/customer-success/scripts/image-mapper/internal/helm"
	"github.com/chainguard-dev/customer-success/scripts/image-mapper/internal/mapper"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(HelmValuesCmd())
}

func HelmValuesCmd() *cobra.Command {
	opts := struct {
		Repo string
	}{}
	cmd := &cobra.Command{
		Use:   "helm-values",
		Short: "Map upstream image references in Helm values to Chainguard images.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			m, err := mapper.NewMapper(
				ctx,
				mapper.WithRepository(opts.Repo),
				mapper.WithIgnoreFns(
					mapper.IgnoreIamguarded(),
					mapper.IgnoreTiers([]string{"FIPS"}),
				),
			)
			if err != nil {
				return fmt.Errorf("constructing mapper: %w", err)
			}

			var input []byte
			switch args[0] {
			case "-":
				input, err = io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("reading stdin: %w", err)
				}
			default:
				input, err = os.ReadFile(args[0])
				if err != nil {
					return fmt.Errorf("reading file: %s: %w", args[0], err)
				}
			}

			output, err := helm.MapValues(m, input)
			if err != nil {
				return fmt.Errorf("mapping values: %w", err)
			}

			if _, err := os.Stdout.Write(output); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&opts.Repo, "repository", "cgr.dev/chainguard", "Modifies the repository URI in the mappings. For instance, registry.internal.dev/chainguard would result in registry.internal.dev/chainguard/<image> in the output.")

	return cmd
}
