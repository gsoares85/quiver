package model

// Workspace is the top-level container, mapped one-to-one to a directory on disk.
type Workspace struct {
	SchemaVersion int           `json:"schemaVersion"`
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Collections   []Collection  `json:"collections,omitempty"`
	Environments  []Environment `json:"environments,omitempty"`
	Variables     []Variable    `json:"variables,omitempty"`
	Settings      Settings      `json:"settings"`
}

// Settings holds workspace-wide defaults.
type Settings struct {
	FollowRedirects bool `json:"followRedirects"`
	TimeoutMs       int  `json:"timeoutMs"`
}

// Collection is an ordered tree of folders and requests.
type Collection struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Auth       *Auth      `json:"auth,omitempty"`
	Variables  []Variable `json:"variables,omitempty"`
	PreRequest string     `json:"preRequest,omitempty"`
	Test       string     `json:"test,omitempty"`
	Items      []Item     `json:"items"`
}

// Item is either a Folder or a Request; exactly one field is non-nil.
type Item struct {
	Folder  *Folder  `json:"folder,omitempty"`
	Request *Request `json:"request,omitempty"`
}

// Folder groups items and can carry cascading auth, variables, and scripts.
type Folder struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Auth       *Auth      `json:"auth,omitempty"`
	Variables  []Variable `json:"variables,omitempty"`
	PreRequest string     `json:"preRequest,omitempty"`
	Test       string     `json:"test,omitempty"`
	Items      []Item     `json:"items"`
}

// Environment is a named set of variables selectable at run time.
type Environment struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Variables []Variable `json:"variables"`
}

// Variable is a resolvable key/value. When Secret is true the plain-text Value is
// empty and the real value is resolved from the OS keychain or env at run time.
type Variable struct {
	Key     string `json:"key"`
	Value   string `json:"value,omitempty"`
	Secret  bool   `json:"secret,omitempty"`
	Enabled bool   `json:"enabled"`
}
