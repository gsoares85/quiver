package model

// AuthType selects which authentication variant an Auth carries.
type AuthType string

const (
	AuthNone    AuthType = "none"
	AuthInherit AuthType = "inherit"
	AuthBasic   AuthType = "basic"
	AuthBearer  AuthType = "bearer"
	AuthAPIKey  AuthType = "apikey"
	AuthOAuth2  AuthType = "oauth2"
)

// Valid reports whether t is a known authentication type.
func (t AuthType) Valid() bool {
	switch t {
	case AuthNone, AuthInherit, AuthBasic, AuthBearer, AuthAPIKey, AuthOAuth2:
		return true
	default:
		return false
	}
}

// BodyType selects which representation a Body carries.
type BodyType string

const (
	BodyNone      BodyType = "none"
	BodyJSON      BodyType = "json"
	BodyText      BodyType = "text"
	BodyXML       BodyType = "xml"
	BodyForm      BodyType = "form"      // application/x-www-form-urlencoded
	BodyMultipart BodyType = "multipart" // multipart/form-data
	BodyGraphQL   BodyType = "graphql"
	BodyBinary    BodyType = "binary"
)

// Valid reports whether t is a known body type.
func (t BodyType) Valid() bool {
	switch t {
	case BodyNone, BodyJSON, BodyText, BodyXML, BodyForm, BodyMultipart, BodyGraphQL, BodyBinary:
		return true
	default:
		return false
	}
}

// validMethods is the set of accepted request methods: the standard HTTP verbs plus
// the pseudo-methods that select the non-HTTP protocols handled by the request engine.
var validMethods = map[string]struct{}{
	"GET":     {},
	"POST":    {},
	"PUT":     {},
	"PATCH":   {},
	"DELETE":  {},
	"HEAD":    {},
	"OPTIONS": {},
	"TRACE":   {},
	"CONNECT": {},
	"WS":      {},
	"GRPC":    {},
	"GRAPHQL": {},
}

// ValidMethod reports whether m is an accepted request method. Methods are stored
// uppercase by convention, so the comparison is case-sensitive.
func ValidMethod(m string) bool {
	_, ok := validMethods[m]
	return ok
}
