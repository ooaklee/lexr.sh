package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	userspacesource "github.com/ooaklee/lexr.sh/internal/userspace/source"
)

// userspaceSourcePreparer is source acquisition, never an installation hook.
type userspaceSourcePreparer interface {
	// Prepare acquires or previews one explicitly pinned component checkout.
	Prepare(context.Context, userspacesource.Request) (userspacesource.Receipt, error)
}

// newUserspaceSourceCommand separates opt-in network checkout from the existing
// authenticated, non-mutating build dry-run and component install contracts.
func (a *application) newUserspaceSourceCommand() *cobra.Command {
	group := &cobra.Command{Use: "source", Short: "Prepare explicitly pinned experimental component source", Args: cobra.NoArgs}
	var request userspacesource.Request
	var asJSON bool
	prepare := &cobra.Command{
		Use:   "prepare <camera|iptsd>",
		Short: "Fetch and validate a new support checkout at an expected exact commit",
		Long:  "Prepare a new component-scoped checkout from a public canonical HTTPS Git URL. Requires explicit experimental opt-in, a fully qualified branch/tag ref, and its expected full peeled commit SHA. Redirects, interactive credentials, hooks, filters and recursive submodules are disabled. A dry run performs no network or filesystem changes and cannot verify the remote ref or component inputs. Failed staging is retained, not resumable. Source preparation grants no installation or hardware authority.",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			request.Component = args[0]
			if a.userspaceSources == nil {
				return fmt.Errorf("userspace source preparer is unavailable")
			}
			receipt, err := a.userspaceSources.Prepare(command.Context(), request)
			if asJSON {
				if writeErr := a.writeJSON(receipt); writeErr != nil {
					return writeErr
				}
				return err
			}
			if err != nil {
				return err
			}
			if receipt.DryRun {
				_, err = fmt.Fprintf(a.out, "userspace source acquisition plan only\ncomponent: %s\nrepository: %s\nref: %s\nexpected commit (not verified): %s\noutput: %s\nno network access or filesystem changes; source inputs are not yet validated\n", receipt.Component, receipt.Repository, receipt.Ref, receipt.ExpectedCommit, receipt.Directory)
				return err
			}
			_, err = fmt.Fprintf(a.out, "prepared %s support source\ncommit: %s\ntree: %s\nrepository root: %s\nsource receipt: %s/%s\nThis is not a native build, install authority or hardware qualification.\n", receipt.Component, receipt.Commit, receipt.Tree, receipt.RepositoryRoot, receipt.Directory, userspacesource.ReceiptName)
			return err
		},
	}
	prepare.Flags().BoolVar(&request.Experimental, "experimental", false, "explicitly opt in to caller-selected experimental support source")
	prepare.Flags().StringVar(&request.Repository, "repository", "", "public canonical HTTPS Git repository URL (no redirects or credentials)")
	prepare.Flags().StringVar(&request.Ref, "ref", "", "exact refs/heads/<branch> or refs/tags/<tag> to fetch")
	prepare.Flags().StringVar(&request.Commit, "commit", "", "required full 40-character lowercase peeled commit SHA")
	prepare.Flags().StringVar(&request.OutputDirectory, "output-dir", "", "new source directory; its parent must already exist")
	prepare.Flags().BoolVar(&request.DryRun, "dry-run", false, "show unverified acquisition plan without network or filesystem changes")
	prepare.Flags().BoolVar(&asJSON, "json", false, "write the source plan or preparation receipt")
	group.AddCommand(prepare)
	return group
}
