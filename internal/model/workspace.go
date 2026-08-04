package model

// Workspace is the top-level container, mapped one-to-one to a directory on disk.
type Workspace struct {
	SchemaVersion int           `json:"schemaVersion" yaml:"schemaVersion"`
	ID            string        `json:"id" yaml:"id"`
	Name          string        `json:"name" yaml:"name"`
	Collections   []Collection  `json:"collections,omitempty" yaml:"collections,omitempty"`
	Environments  []Environment `json:"environments,omitempty" yaml:"environments,omitempty"`
	Variables     []Variable    `json:"variables,omitempty" yaml:"variables,omitempty"`
	Settings      Settings      `json:"settings" yaml:"settings"`
}

// Settings holds workspace-wide defaults.
type Settings struct {
	FollowRedirects bool `json:"followRedirects" yaml:"followRedirects"`
	TimeoutMs       int  `json:"timeoutMs" yaml:"timeoutMs"`
}

// Collection is an ordered tree of folders and requests.
type Collection struct {
	ID         string     `json:"id" yaml:"id"`
	Name       string     `json:"name" yaml:"name"`
	Auth       *Auth      `json:"auth,omitempty" yaml:"auth,omitempty"`
	Variables  []Variable `json:"variables,omitempty" yaml:"variables,omitempty"`
	PreRequest string     `json:"preRequest,omitempty" yaml:"preRequest,omitempty"`
	Test       string     `json:"test,omitempty" yaml:"test,omitempty"`
	Items      []Item     `json:"items" yaml:"items"`
}

// Item is either a Folder or a Request; exactly one field is non-nil.
type Item struct {
	Folder  *Folder  `json:"folder,omitempty" yaml:"folder,omitempty"`
	Request *Request `json:"request,omitempty" yaml:"request,omitempty"`
}

// Folder groups items and can carry cascading auth, variables, and scripts.
type Folder struct {
	ID         string     `json:"id" yaml:"id"`
	Name       string     `json:"name" yaml:"name"`
	Auth       *Auth      `json:"auth,omitempty" yaml:"auth,omitempty"`
	Variables  []Variable `json:"variables,omitempty" yaml:"variables,omitempty"`
	PreRequest string     `json:"preRequest,omitempty" yaml:"preRequest,omitempty"`
	Test       string     `json:"test,omitempty" yaml:"test,omitempty"`
	Items      []Item     `json:"items" yaml:"items"`
}

// Environment is a named set of variables selectable at run time.
type Environment struct {
	ID        string     `json:"id" yaml:"id"`
	Name      string     `json:"name" yaml:"name"`
	Variables []Variable `json:"variables" yaml:"variables"`
}

// Variable is a resolvable key/value. When Secret is true the plain-text Value is
// empty and the real value is resolved from the OS keychain or env at run time.
type Variable struct {
	Key     string `json:"key" yaml:"key"`
	Value   string `json:"value,omitempty" yaml:"value,omitempty"`
	Secret  bool   `json:"secret,omitempty" yaml:"secret,omitempty"`
	Enabled bool   `json:"enabled" yaml:"enabled"`
}
