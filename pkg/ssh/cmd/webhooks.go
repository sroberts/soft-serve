package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/soft-serve/pkg/ops"
	"github.com/charmbracelet/soft-serve/pkg/webhook"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
)

func webhookCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "webhook",
		Aliases: []string{"webhooks"},
		Short:   "Manage repository webhooks",
	}

	cmd.AddCommand(
		webhookListCommand(),
		webhookCreateCommand(),
		webhookDeleteCommand(),
		webhookUpdateCommand(),
		webhookDeliveriesCommand(),
	)

	return cmd
}

var webhookEvents []string

func init() {
	events := webhook.Events()
	webhookEvents = make([]string, len(events))
	for i, e := range events {
		webhookEvents[i] = e.String()
	}
}

func parseWebhookID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid webhook ID: %w", err)
	}
	return id, nil
}

func webhookListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list REPOSITORY",
		Short: "List repository webhooks",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			webhooks, err := service(cmd.Context()).Webhooks(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			table := table.New().Headers("ID", "URL", "Events", "Active", "Created At", "Updated At")
			for _, h := range webhooks {
				table = table.Row(
					strconv.FormatInt(h.ID, 10),
					h.URL,
					strings.Join(h.Events, ","),
					strconv.FormatBool(h.Active),
					humanize.Time(h.CreatedAt),
					humanize.Time(h.UpdatedAt),
				)
			}
			cmd.Println(table)
			return nil
		},
	}
}

func webhookFlags(cmd *cobra.Command, opts *ops.WebhookOptions) {
	cmd.Flags().StringSliceVarP(&opts.Events, "events", "e", nil, fmt.Sprintf("events to trigger the webhook, available events are (%s)", strings.Join(webhookEvents, ", ")))
	cmd.Flags().StringVarP(&opts.Secret, "secret", "s", "", "secret to sign the webhook payload")
}

func webhookCreateCommand() *cobra.Command {
	var opts ops.WebhookOptions
	var active bool

	cmd := &cobra.Command{
		Use:   "create REPOSITORY URL",
		Short: "Create a repository webhook",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.URL = args[1]
			opts.Active = &active
			_, err := service(cmd.Context()).CreateWebhook(cmd.Context(), args[0], opts)
			return err
		},
	}

	webhookFlags(cmd, &opts)
	cmd.Flags().BoolVarP(&active, "active", "a", true, "whether the webhook is active")
	cmd.Flags().StringVarP(&opts.ContentType, "content-type", "c", "json", "content type of the webhook payload, can be either `json` or `form`")

	return cmd
}

func webhookDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "delete REPOSITORY WEBHOOK_ID",
		Short: "Delete a repository webhook",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseWebhookID(args[1])
			if err != nil {
				return err
			}
			return service(cmd.Context()).DeleteWebhook(cmd.Context(), args[0], id)
		},
	}
}

func webhookUpdateCommand() *cobra.Command {
	var opts ops.WebhookOptions
	var active string

	cmd := &cobra.Command{
		Use:   "update REPOSITORY WEBHOOK_ID",
		Short: "Update a repository webhook",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseWebhookID(args[1])
			if err != nil {
				return err
			}
			if active != "" {
				a, err := strconv.ParseBool(active)
				if err != nil {
					return fmt.Errorf("invalid active value: %w", err)
				}
				opts.Active = &a
			}
			return service(cmd.Context()).UpdateWebhook(cmd.Context(), args[0], id, opts)
		},
	}

	webhookFlags(cmd, &opts)
	cmd.Flags().StringVarP(&active, "active", "a", "", "whether the webhook is active")
	cmd.Flags().StringVarP(&opts.ContentType, "content-type", "c", "", "content type of the webhook payload, can be either `json` or `form`")
	cmd.Flags().StringVarP(&opts.URL, "url", "u", "", "webhook URL")

	return cmd
}

func webhookDeliveriesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "deliveries",
		Short:   "Manage webhook deliveries",
		Aliases: []string{"delivery", "deliver"},
	}

	cmd.AddCommand(
		webhookDeliveriesListCommand(),
		webhookDeliveriesRedeliverCommand(),
		webhookDeliveriesGetCommand(),
	)

	return cmd
}

func webhookDeliveriesListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list REPOSITORY WEBHOOK_ID",
		Short: "List webhook deliveries",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseWebhookID(args[1])
			if err != nil {
				return err
			}
			dels, err := service(cmd.Context()).Deliveries(cmd.Context(), args[0], id)
			if err != nil {
				return err
			}

			table := table.New().Headers("Status", "ID", "Event", "Created At")
			for _, d := range dels {
				status := "❌"
				if d.OK() {
					status = "✅"
				}
				table = table.Row(
					status,
					d.ID,
					d.Event,
					humanize.Time(d.CreatedAt),
				)
			}
			cmd.Println(table)
			return nil
		},
	}
}

func webhookDeliveriesRedeliverCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "redeliver REPOSITORY WEBHOOK_ID DELIVERY_ID",
		Short: "Redeliver a webhook delivery",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseWebhookID(args[1])
			if err != nil {
				return err
			}
			return service(cmd.Context()).Redeliver(cmd.Context(), args[0], id, args[2])
		},
	}
}

func webhookDeliveriesGetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "get REPOSITORY WEBHOOK_ID DELIVERY_ID",
		Short: "Get a webhook delivery",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseWebhookID(args[1])
			if err != nil {
				return err
			}
			del, err := service(cmd.Context()).Delivery(cmd.Context(), args[0], id, args[2])
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			indented := func(s string) {
				for _, l := range strings.Split(s, "\n") {
					fmt.Fprintf(out, "  %s\n", l) //nolint:errcheck
				}
			}
			fmt.Fprintf(out, "ID: %s\n", del.ID)                        //nolint:errcheck
			fmt.Fprintf(out, "Event: %s\n", del.Event)                  //nolint:errcheck
			fmt.Fprintf(out, "Request URL: %s\n", del.RequestURL)       //nolint:errcheck
			fmt.Fprintf(out, "Request Method: %s\n", del.RequestMethod) //nolint:errcheck
			fmt.Fprintf(out, "Request Error: %s\n", del.RequestError)   //nolint:errcheck
			fmt.Fprintf(out, "Request Headers:\n")                      //nolint:errcheck
			indented(del.RequestHeaders)
			fmt.Fprintf(out, "Request Body:\n") //nolint:errcheck
			indented(del.RequestBody)
			fmt.Fprintf(out, "Response Status: %d\n", del.ResponseStatus) //nolint:errcheck
			fmt.Fprintf(out, "Response Headers:\n")                       //nolint:errcheck
			indented(del.ResponseHeaders)
			fmt.Fprintf(out, "Response Body:\n") //nolint:errcheck
			indented(del.ResponseBody)
			return nil
		},
	}
}
