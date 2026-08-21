package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/config"
	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/version"
)

var (
	rootSDK *hey.Client
	sdk     *hey.Client
)

// cliAuthStrategy bridges the CLI's auth.Manager to the SDK's AuthStrategy interface.
// This preserves session cookie support (which the SDK's BearerAuth doesn't have).
type cliAuthStrategy struct {
	mgr *auth.Manager
}

func (a *cliAuthStrategy) Authenticate(ctx context.Context, req *http.Request) error {
	return a.mgr.AuthenticateRequest(ctx, req)
}

// Refresh satisfies the SDK's TokenRefresher, which is how a 401 gets one retry with
// renewed credentials rather than being surfaced. Without this the SDK has no way to
// know these credentials can be renewed at all, since it did not issue them.
func (a *cliAuthStrategy) Refresh(ctx context.Context) error {
	return a.mgr.Refresh(ctx)
}

// statsHooks implements hey.Hooks for --stats tracking.
type statsHooks struct {
	requestCount atomic.Int64
	totalLatency atomic.Int64 // nanoseconds
}

func (h *statsHooks) OnOperationStart(ctx context.Context, _ hey.OperationInfo) context.Context {
	return ctx
}
func (h *statsHooks) OnOperationEnd(context.Context, hey.OperationInfo, error, time.Duration) {}
func (h *statsHooks) OnRequestStart(ctx context.Context, _ hey.RequestInfo) context.Context {
	return ctx
}
func (h *statsHooks) OnRequestEnd(_ context.Context, _ hey.RequestInfo, result hey.RequestResult) {
	h.requestCount.Add(1)
	h.totalLatency.Add(int64(result.Duration))
}
func (h *statsHooks) OnRetry(context.Context, hey.RequestInfo, int, error) {}

func (h *statsHooks) RequestCount() int           { return int(h.requestCount.Load()) }
func (h *statsHooks) TotalLatency() time.Duration { return time.Duration(h.totalLatency.Load()) }

var sdkStats *statsHooks

// initSDK creates the SDK client, bridging the CLI's auth and config.
func initSDK(authMgr *auth.Manager, baseURL string) {
	sdkCfg := &hey.Config{
		BaseURL:      baseURL,
		CacheEnabled: false,
	}

	var opts []hey.ClientOption
	opts = append(opts, hey.WithAuthStrategy(&cliAuthStrategy{mgr: authMgr}))
	opts = append(opts, hey.WithUserAgent(version.UserAgent()+" "+hey.DefaultUserAgent))
	opts = append(opts, hey.WithTransport(newCappedTransport(maxTextResponseBytes)))

	if verboseFlag > 0 {
		opts = append(opts, hey.WithLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	}

	sdkStats = &statsHooks{}
	opts = append(opts, hey.WithHooks(sdkStats))

	rootSDK = hey.NewClient(sdkCfg, nil, opts...)
	sdk = rootSDK
}

func selectConfiguredAccount(ctx context.Context) error {
	client, err := clientForAccountSelection(ctx, rootSDK, cfg.AccountID)
	if err != nil {
		return err
	}
	sdk = client
	return nil
}

func clientForAccountSelection(ctx context.Context, client *hey.Client, accountID string) (*hey.Client, error) {
	if accountID == "" || accountID == config.AllAccounts {
		return client, nil
	}

	id, err := strconv.ParseInt(accountID, 10, 64)
	if err != nil || id <= 0 {
		return nil, apierr.ErrUsage(fmt.Sprintf("invalid account selection: %s", accountID))
	}
	scoped, err := client.ForAccount(ctx, id)
	if err != nil {
		return nil, apierr.FromSDK(err)
	}
	return scoped, nil
}

func clientForResourceAccount(ctx context.Context, accountID int64) (*hey.Client, error) {
	if accountID <= 0 {
		return nil, apierr.ErrAPI(0, "thread did not identify its mail account")
	}
	scoped, err := rootSDK.ForAccount(ctx, accountID)
	if err != nil {
		return nil, apierr.FromSDK(err)
	}
	return scoped, nil
}

// --- Timestamp formatting helpers ---

// formatTimestamp formats a time.Time to "YYYY-MM-DDTHH:MM" display format, in the
// zone the recording carries. Converting to UTC first moved every wall-clock time it
// printed: a 09:00 Berlin time track read as 07:00.
func formatTimestamp(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.Format("2006-01-02T15:04")
}

// formatDate formats a time.Time to "YYYY-MM-DD" display format, in the zone the
// recording carries. In UTC a day starting at midnight east of Greenwich printed as
// the day before, which `hey journal read` then read as an empty day.
func formatDate(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.Format(dateLayout)
}

// --- Posting topic ID helper ---

// resolvePostingTopicID extracts the topic ID from an SDK Posting. The SDK Posting has
// no TopicID field, so the thread is read out of app_url — the one parse of it lives in
// internal/mail, which is where a posting is described.
func resolvePostingTopicID(p generated.Posting) int64 {
	return mail.TopicIDIn(p.AppUrl)
}

// --- Calendar helpers ---

// unwrapCalendars extracts []generated.Calendar from a CalendarListPayload.
func unwrapCalendars(payload *generated.CalendarListPayload) []generated.Calendar {
	if payload == nil {
		return []generated.Calendar{}
	}
	calendars := make([]generated.Calendar, 0, len(payload.Calendars))
	for _, cw := range payload.Calendars {
		calendars = append(calendars, cw.Calendar)
	}
	return calendars
}

// findPersonalCalendarID finds the personal calendar from a list of calendars.
func findPersonalCalendarID(calendars []generated.Calendar) (int64, error) {
	for _, cal := range calendars {
		if cal.Personal {
			return cal.Id, nil
		}
	}
	for _, cal := range calendars {
		if strings.EqualFold(cal.Name, "Personal") {
			return cal.Id, nil
		}
	}
	return 0, fmt.Errorf("personal calendar not found")
}

const (
	personalRecordingsLookbackYears  = 4
	personalRecordingsLookaheadYears = 1
)

// listPersonalRecordings fetches recordings from the user's personal calendar
// with a lookback/lookahead window matching the old CLI behavior.
func listPersonalRecordings(ctx context.Context) (*generated.CalendarRecordingsResponse, error) {
	payload, err := sdk.Calendars().List(ctx)
	if err != nil {
		return nil, apierr.FromSDK(err)
	}

	calendars := unwrapCalendars(payload)
	calID, err := findPersonalCalendarID(calendars)
	if err != nil {
		return nil, apierr.ErrNotFound("calendar", "personal")
	}

	now := time.Now()
	startsOn := now.AddDate(-personalRecordingsLookbackYears, 0, 0).Format("2006-01-02")
	endsOn := now.AddDate(personalRecordingsLookaheadYears, 0, 0).Format("2006-01-02")

	resp, err := sdk.Calendars().GetRecordings(ctx, calID, &generated.GetCalendarRecordingsParams{
		StartsOn: &startsOn,
		EndsOn:   &endsOn,
	})
	if err != nil {
		return nil, apierr.FromSDK(err)
	}
	return resp, nil
}

// filterRecordingsByType returns recordings matching the given type string. The empty
// result is an empty slice rather than nil so that `--json` gives `"data": []` and a
// `.data[]` filter has something to iterate.
func filterRecordingsByType(resp *generated.CalendarRecordingsResponse, recType string) []generated.Recording {
	if resp == nil {
		return []generated.Recording{}
	}
	recordings, ok := (*resp)[recType]
	if !ok {
		return []generated.Recording{}
	}
	return recordings
}

// --- Mutation info extraction ---

// extractMutationInfoFromResult extracts mutation info from a typed SDK response
// by JSON round-tripping to map[string]any, then using the existing extractMutationInfo.
func extractMutationInfoFromResult(v any) string {
	if v == nil {
		return ""
	}
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return extractMutationInfo(data)
}
