package cmd

import "github.com/spf13/cobra"

var curatedHelpTopics = []string{"output", "exit-codes", "environment", "linked-accounts"}

func newHelpTopicCommands() []*cobra.Command {
	return []*cobra.Command{
		{
			Use:   "output",
			Short: "Output formats and filtering",
			Long: `Choose how hey writes results for people and programs.

DEFAULT OUTPUT
  At a terminal, hey presents human-readable styled output.
  When stdout is piped or redirected, hey writes a JSON response envelope.

FORMATS
  --styled    Force human-readable terminal output.
  --json      Write the JSON response envelope.
  --quiet     Write result data without the response envelope.
  --markdown  Write Markdown suitable for another document.
  --html      Write original HTML for commands that support it.

FILTERING
  --jq EXPR   Filter the JSON response with a built-in jq expression.
  --ids-only  Write result IDs, one per line.
  --count     Write only the result count.
  --stats     Include request statistics in JSON metadata.

Formats and selectors apply when a command returns the corresponding data shape. Unsupported combinations return a usage error.`,
		},
		{
			Use:   "exit-codes",
			Short: "Exit status reference",
			Long: `Use hey's exit status to handle results in scripts and agents.

EXIT CODES
  0  The command completed successfully.
  1  Usage, validation, conflict, or other command error.
  2  The requested resource was not found.
  3  Authentication is required or failed.
  4  The signed-in identity cannot perform the operation.
  5  HEY rate-limited the request.
  6  A network connection failed.
  7  An API, server, or local operational failure occurred.
  8  The request matched more than one resource.

JSON failures also carry a machine-readable error code and an actionable hint when one is available.`,
		},
		{
			Use:   "environment",
			Short: "Environment variable reference",
			Long: `Configure hey for the current process with environment variables.

CONNECTION & AUTHENTICATION
  HEY_TOKEN          Use a bearer token instead of stored credentials.
  HEY_BASE_URL       Override the HEY server URL.
  HEY_ACCOUNT_ID     Select a linked mail account ID or all.
  HEY_NO_KEYRING     Store credentials in the config directory instead of a keyring.

INTERACTION & DIAGNOSTICS
  HEY_NONINTERACTIVE Disable prompts when set to 1 or true.
  HEY_DEBUG          Show request details, equivalent to -v.

TUI & SETUP
  HEY_THEME          Load a TUI theme overlay from a TOML file.
  HEY_CABLE_URL      Override the Action Cable websocket URL.
  HEY_SETUP_AGENT    Select claude, codex, all, or none during agent setup.

Command-line flags take precedence over environment values.`,
		},
		{
			Use:   "linked-accounts",
			Short: "Linked account selection",
			Long: `Choose a linked mail account for mail commands within one HEY identity.

DISCOVERY & DEFAULTS
  hey account list      List All Accounts and every linked account.
  hey account use ID    Save a linked account as the default mail filter.
  hey account use all   Return to All Accounts.

ONE INVOCATION
  hey --account ID box list
  HEY_ACCOUNT_ID=ID hey search "quarterly planning"

SELECTION ORDER
  1. --account
  2. HEY_ACCOUNT_ID
  3. A trusted repository .hey/config.json
  4. The global default for the active server
  5. All Accounts

Compose and contact creation use an individually selected account. Replies and forwards use the thread's account. Calendar, task, time tracking, and journal commands remain identity-wide.`,
		},
	}
}
