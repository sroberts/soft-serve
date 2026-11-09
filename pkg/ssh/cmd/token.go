package cmd

import (
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2/table"
	"github.com/caarlos0/duration"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
)

// TokenCommand returns a command that manages user access tokens.
func TokenCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "token",
		Aliases: []string{"access-token"},
		Short:   "Manage access tokens",
	}

	var createExpiresIn string
	createCmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a new access token",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			opts := ops.CreateToken{Name: strings.Join(args, " ")}

			var expiresAt time.Time
			var expiresIn time.Duration
			if createExpiresIn != "" {
				d, err := duration.Parse(createExpiresIn)
				if err != nil {
					return err
				}

				expiresIn = d
				expiresAt = time.Now().Add(d)
				opts.ExpiresAt = &expiresAt
			}

			token, err := service(ctx).CreateAccessToken(ctx, opts)
			if err != nil {
				return err
			}

			notice := "Access token created"
			if expiresIn != 0 {
				notice += " (expires in " + humanize.Time(expiresAt) + ")"
			}

			cmd.PrintErrln(notice)
			cmd.Println(token.Token)

			return nil
		},
	}

	createCmd.Flags().StringVar(&createExpiresIn, "expires-in", "", "Token expiration time (e.g. 1y, 3mo, 2w, 5d4h, 1h30m)")

	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List access tokens",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			tokens, err := service(ctx).AccessTokens(ctx)
			if err != nil {
				return err
			}

			if len(tokens) == 0 {
				cmd.Println("No tokens found")
				return nil
			}

			now := time.Now()
			table := table.New().Headers("ID", "Name", "Created At", "Expires In")
			for _, token := range tokens {
				expiresAt := "-"
				if token.ExpiresAt != nil {
					if token.Expired(now) {
						expiresAt = "expired"
					} else {
						expiresAt = humanize.Time(*token.ExpiresAt)
					}
				}

				table = table.Row(strconv.FormatInt(token.ID, 10),
					token.Name,
					humanize.Time(token.CreatedAt),
					expiresAt,
				)
			}
			cmd.Println(table)
			return nil
		},
	}

	deleteCmd := &cobra.Command{
		Use:     "delete ID",
		Aliases: []string{"rm", "remove"},
		Short:   "Delete an access token",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return ops.Invalid("token ID must be a number: %q", args[0])
			}

			if err := service(ctx).DeleteAccessToken(ctx, id); err != nil {
				return err
			}

			cmd.PrintErrln("Access token deleted")
			return nil
		},
	}

	cmd.AddCommand(
		createCmd,
		listCmd,
		deleteCmd,
	)

	return cmd
}
