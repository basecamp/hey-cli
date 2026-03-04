package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

type entryCommand struct {
	cmd *cobra.Command
}

func newEntryCommand() *entryCommand {
	entryCommand := &entryCommand{}
	entryCommand.cmd = &cobra.Command{
		Use:   "entry <id>",
		Short: "Read a single email entry",
		Example: `  hey entry 67890
  hey entry 67890 --json`,
		RunE: entryCommand.run,
		Args:  cobra.ExactArgs(1),
	}

	return entryCommand
}

func (c *entryCommand) run(cmd *cobra.Command, args []string) error {
	if err := requireAuth(); err != nil {
		return err
	}

	e, err := apiClient.GetEntry(args[0])
	if err != nil {
		return err
	}

	if jsonOutput {
		return printJSON(e)
	}

	from := e.Creator.Name
	if from == "" {
		from = e.Creator.EmailAddress
	}
	if e.AlternativeSenderName != "" {
		from = e.AlternativeSenderName
	}
	date := ""
	if len(e.CreatedAt) >= 16 {
		date = e.CreatedAt[:16]
	}

	fmt.Printf("Entry #%d\n", e.ID)
	fmt.Printf("From:    %s\n", from)
	fmt.Printf("Date:    %s\n", date)
	fmt.Printf("Kind:    %s\n", e.Kind)
	if e.Summary != "" {
		fmt.Printf("Summary: %s\n", e.Summary)
	}
	if e.Body != "" {
		fmt.Println()
		fmt.Println(e.Body)
	}

	return nil
}
