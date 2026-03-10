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

	"github.com/basecamp/hey-cli/internal/auth"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/version"
	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

var sdk *hey.Client

// cliAuthStrategy bridges the CLI's auth.Manager to the SDK's AuthStrategy interface.
// This preserves session cookie support (which the SDK's BearerAuth doesn't have).
type cliAuthStrategy struct {
	mgr *auth.Manager
}

func (a *cliAuthStrategy) Authenticate(ctx context.Context, req *http.Request) error {
	return a.mgr.AuthenticateRequest(ctx, req)
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
	opts = append(opts, hey.WithUserAgent(version.UserAgent()))

	if verboseFlag > 0 {
		opts = append(opts, hey.WithLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	}

	sdkStats = &statsHooks{}
	opts = append(opts, hey.WithHooks(sdkStats))

	sdk = hey.NewClient(sdkCfg, nil, opts...)
}

// convertSDKError maps hey.Error to apierr.Error for CLI error display.
func convertSDKError(err error) error {
	if err == nil {
		return nil
	}
	sdkErr := hey.AsError(err)
	switch sdkErr.Code {
	case hey.CodeAuth:
		return output.ErrAuth(sdkErr.Message)
	case hey.CodeNotFound:
		return &output.Error{Code: "not_found", Message: sdkErr.Message, HTTPStatus: 404}
	case hey.CodeForbidden:
		return output.ErrForbidden(sdkErr.Message)
	case hey.CodeRateLimit:
		var retryAfter int
		_, _ = fmt.Sscanf(sdkErr.Hint, "%d", &retryAfter)
		return output.ErrRateLimit(retryAfter)
	case hey.CodeNetwork:
		return output.ErrNetwork(err)
	default:
		return output.ErrAPI(sdkErr.HTTPStatus, sdkErr.Message)
	}
}

// --- Timestamp formatting helpers ---

// formatTimestamp formats a time.Time to "YYYY-MM-DDTHH:MM" display format.
func formatTimestamp(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format("2006-01-02T15:04")
}

// formatDate formats a time.Time to "YYYY-MM-DD" display format.
func formatDate(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format("2006-01-02")
}

// --- Posting topic ID helper ---

// resolvePostingTopicID extracts the topic ID from an SDK Posting.
// The SDK Posting doesn't have a TopicID field, so we parse from AppUrl.
func resolvePostingTopicID(p generated.Posting) int64 {
	if i := strings.LastIndex(p.AppUrl, "/topics/"); i >= 0 {
		segment := p.AppUrl[i+len("/topics/"):]
		// Strip trailing path components or query strings
		if j := strings.IndexAny(segment, "/?#"); j >= 0 {
			segment = segment[:j]
		}
		if id, err := strconv.ParseInt(segment, 10, 64); err == nil {
			return id
		}
	}
	return p.Id
}

// --- Calendar helpers ---

// unwrapCalendars extracts []generated.Calendar from a CalendarListPayload.
func unwrapCalendars(payload *generated.CalendarListPayload) []generated.Calendar {
	if payload == nil {
		return nil
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

// listPersonalRecordings fetches all recordings from the user's personal calendar
// with a lookback/lookahead window matching the old CLI behavior.
func listPersonalRecordings(ctx context.Context) (*generated.CalendarRecordingsResponse, error) {
	payload, err := sdk.Calendars().List(ctx)
	if err != nil {
		return nil, convertSDKError(err)
	}

	calendars := unwrapCalendars(payload)
	calID, err := findPersonalCalendarID(calendars)
	if err != nil {
		return nil, output.ErrNotFound("calendar", "personal")
	}

	now := time.Now()
	startsOn := now.AddDate(-personalRecordingsLookbackYears, 0, 0).Format("2006-01-02")
	endsOn := now.AddDate(personalRecordingsLookaheadYears, 0, 0).Format("2006-01-02")

	resp, err := sdk.Calendars().GetRecordings(ctx, calID, &generated.GetCalendarRecordingsParams{
		StartsOn: startsOn,
		EndsOn:   endsOn,
	})
	if err != nil {
		return nil, convertSDKError(err)
	}
	return resp, nil
}

// filterRecordingsByType returns recordings matching the given type string.
func filterRecordingsByType(resp *generated.CalendarRecordingsResponse, recType string) []generated.Recording {
	if resp == nil {
		return nil
	}
	recordings, ok := (*resp)[recType]
	if !ok {
		return nil
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

// normalizeAny converts a typed SDK response to an any suitable for writeOK
// by JSON round-tripping through json.Number-preserving decoder.
func normalizeAny(v any) (any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return output.NormalizeJSONNumbers(data)
}
