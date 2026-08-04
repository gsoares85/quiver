package model

// Request is the central entity: what to send and how to validate the response.
type Request struct {
	ID         string          `json:"id" yaml:"id"`
	Name       string          `json:"name" yaml:"name"`
	Method     string          `json:"method" yaml:"method"`
	URL        string          `json:"url" yaml:"url"`
	Headers    []Header        `json:"headers,omitempty" yaml:"headers,omitempty"`
	Query      []Param         `json:"query,omitempty" yaml:"query,omitempty"`
	Body       *Body           `json:"body,omitempty" yaml:"body,omitempty"`
	Auth       *Auth           `json:"auth,omitempty" yaml:"auth,omitempty"`
	PreRequest string          `json:"preRequest,omitempty" yaml:"preRequest,omitempty"`
	Test       string          `json:"test,omitempty" yaml:"test,omitempty"`
	Examples   []Example       `json:"examples,omitempty" yaml:"examples,omitempty"`
	Settings   RequestSettings `json:"settings" yaml:"settings"`
}

// RequestSettings holds per-request execution policy.
type RequestSettings struct {
	FollowRedirects bool `json:"followRedirects" yaml:"followRedirects"`
	MaxRedirects    int  `json:"maxRedirects" yaml:"maxRedirects"`
	TimeoutMs       int  `json:"timeoutMs,omitempty" yaml:"timeoutMs,omitempty"`
}

// Header is a key/value pair with an enabled toggle so users can keep entries without
// deleting them.
type Header struct {
	Key     string `json:"key" yaml:"key"`
	Value   string `json:"value" yaml:"value"`
	Enabled bool   `json:"enabled" yaml:"enabled"`
	Desc    string `json:"desc,omitempty" yaml:"desc,omitempty"`
}

// Param is a query parameter; it shares Header's shape.
type Param = Header

// Body holds exactly one representation, selected by Type.
type Body struct {
	Type    BodyType  `json:"type" yaml:"type"`
	Text    string    `json:"text,omitempty" yaml:"text,omitempty"`       // json / text / xml / graphql query
	Form    []Param   `json:"form,omitempty" yaml:"form,omitempty"`       // urlencoded or multipart fields
	Files   []FileRef `json:"files,omitempty" yaml:"files,omitempty"`     // multipart / binary file references
	GraphQL *GraphQL  `json:"graphql,omitempty" yaml:"graphql,omitempty"` // variables + operationName
}

// GraphQL carries the auxiliary parts of a GraphQL request; the query text itself
// lives in Body.Text.
type GraphQL struct {
	Variables     string `json:"variables,omitempty" yaml:"variables,omitempty"` // raw JSON object
	OperationName string `json:"operationName,omitempty" yaml:"operationName,omitempty"`
}

// FileRef points at a file on disk used by a multipart or binary body.
type FileRef struct {
	Field string `json:"field,omitempty" yaml:"field,omitempty"` // multipart form field name
	Path  string `json:"path" yaml:"path"`
}

// Example is a saved response attached to a request, used for docs and mocking.
type Example struct {
	ID       string   `json:"id" yaml:"id"`
	Name     string   `json:"name" yaml:"name"`
	Request  Request  `json:"request" yaml:"request"`
	Response Response `json:"response" yaml:"response"`
}
