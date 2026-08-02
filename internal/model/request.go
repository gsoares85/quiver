package model

// Request is the central entity: what to send and how to validate the response.
type Request struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Method     string          `json:"method"`
	URL        string          `json:"url"`
	Headers    []Header        `json:"headers,omitempty"`
	Query      []Param         `json:"query,omitempty"`
	Body       *Body           `json:"body,omitempty"`
	Auth       *Auth           `json:"auth,omitempty"`
	PreRequest string          `json:"preRequest,omitempty"`
	Test       string          `json:"test,omitempty"`
	Examples   []Example       `json:"examples,omitempty"`
	Settings   RequestSettings `json:"settings"`
}

// RequestSettings holds per-request execution policy.
type RequestSettings struct {
	FollowRedirects bool `json:"followRedirects"`
	MaxRedirects    int  `json:"maxRedirects"`
	TimeoutMs       int  `json:"timeoutMs,omitempty"`
}

// Header is a key/value pair with an enabled toggle so users can keep entries without
// deleting them.
type Header struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Enabled bool   `json:"enabled"`
	Desc    string `json:"desc,omitempty"`
}

// Param is a query parameter; it shares Header's shape.
type Param = Header

// Body holds exactly one representation, selected by Type.
type Body struct {
	Type    BodyType  `json:"type"`
	Text    string    `json:"text,omitempty"`    // json / text / xml / graphql query
	Form    []Param   `json:"form,omitempty"`    // urlencoded or multipart fields
	Files   []FileRef `json:"files,omitempty"`   // multipart / binary file references
	GraphQL *GraphQL  `json:"graphql,omitempty"` // variables + operationName
}

// GraphQL carries the auxiliary parts of a GraphQL request; the query text itself
// lives in Body.Text.
type GraphQL struct {
	Variables     string `json:"variables,omitempty"` // raw JSON object
	OperationName string `json:"operationName,omitempty"`
}

// FileRef points at a file on disk used by a multipart or binary body.
type FileRef struct {
	Field string `json:"field,omitempty"` // multipart form field name
	Path  string `json:"path"`
}

// Example is a saved response attached to a request, used for docs and mocking.
type Example struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Request  Request  `json:"request"`
	Response Response `json:"response"`
}
