package output

type Response struct {
	OK          bool           `json:"ok"`
	Data        any            `json:"data,omitempty"`
	Summary     string         `json:"summary,omitempty"`
	Notice      string         `json:"notice,omitempty"`
	Breadcrumbs []Breadcrumb   `json:"breadcrumbs,omitempty"`
	Meta        map[string]any `json:"meta,omitempty"`
}

type ErrorResponse struct {
	OK    bool           `json:"ok"`
	Error string         `json:"error"`
	Code  string         `json:"code"`
	Hint  string         `json:"hint,omitempty"`
	Meta  map[string]any `json:"meta,omitempty"`
}

type Breadcrumb struct {
	Action      string `json:"action"`
	Command     string `json:"command"`
	Description string `json:"description"`
}

type ResponseOption func(*Response)

func WithSummary(s string) ResponseOption {
	return func(r *Response) { r.Summary = s }
}

func WithNotice(s string) ResponseOption {
	return func(r *Response) { r.Notice = s }
}

func WithBreadcrumbs(b ...Breadcrumb) ResponseOption {
	return func(r *Response) { r.Breadcrumbs = b }
}

func WithMeta(key string, value any) ResponseOption {
	return func(r *Response) {
		if r.Meta == nil {
			r.Meta = map[string]any{}
		}
		r.Meta[key] = value
	}
}
