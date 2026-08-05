package httpengine

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gsoares85/quiver/internal/model"
)

// opApplyAuth labels failures raised while attaching credentials to a request.
const opApplyAuth = "apply auth"

// Where an API key can be placed, as named by model.APIKeyAuth.In.
const (
	apiKeyInHeader = "header"
	apiKeyInQuery  = "query"
)

// applyAuth attaches the request's credentials to the outgoing wire request.
//
// Secret values are resolved by the caller before this point: the engine never reads the
// OS keychain, an environment variable, or any other credential source, and it never
// puts a credential value into an error message.
//
// Configured auth deliberately wins over an Authorization header typed by hand. The auth
// configuration is the more specific statement of intent, and silently sending a stale
// pasted header instead would be the more surprising outcome.
func applyAuth(hr *http.Request, auth *model.Auth) error {
	if auth == nil {
		return nil // the caller flattened inheritance; there is nothing to attach
	}
	switch auth.Type {
	case model.AuthNone:
		return nil
	case model.AuthBasic:
		return applyBasic(hr, auth.Basic)
	case model.AuthBearer:
		if auth.Bearer == nil {
			return authError(hr, errors.New("bearer auth has no credentials"))
		}
		return setBearer(hr, "bearer", auth.Bearer.Token)
	case model.AuthAPIKey:
		return applyAPIKey(hr, auth.APIKey)
	case model.AuthOAuth2:
		return applyOAuth2(hr, auth.OAuth2)
	case model.AuthInherit:
		return authError(hr, errors.New("inherited auth must be flattened by the caller before execution"))
	default:
		return authError(hr, fmt.Errorf("unknown auth type %q", auth.Type))
	}
}

// authError wraps a credential problem with the request's URL for context. Auth failures
// are KindRequest: nothing has been sent, and the fix is in the request, not the network.
func authError(hr *http.Request, err error) error {
	return newError(KindRequest, opApplyAuth, hr.URL.String(), err)
}

// applyBasic sets HTTP Basic credentials. Neither field is trimmed: the pair is
// base64-encoded, so it survives any bytes intact and trimming could corrupt a password
// that legitimately ends in whitespace. An empty password is allowed — sending a token as
// the username is a common API convention.
func applyBasic(hr *http.Request, cred *model.BasicAuth) error {
	if cred == nil {
		return authError(hr, errors.New("basic auth has no credentials"))
	}
	hr.SetBasicAuth(cred.Username, cred.Password)
	return nil
}

// applyAPIKey places the key in a header or a query parameter, per its configuration.
func applyAPIKey(hr *http.Request, cred *model.APIKeyAuth) error {
	if cred == nil {
		return authError(hr, errors.New("api key auth has no credentials"))
	}
	if cred.Key == "" {
		return authError(hr, errors.New("api key auth has no key name"))
	}

	value := strings.TrimSpace(cred.Value)
	switch strings.ToLower(strings.TrimSpace(cred.In)) {
	case "", apiKeyInHeader: // header is the default placement
		if err := checkHeaderValue(value); err != nil {
			return authError(hr, fmt.Errorf("api key %s", err))
		}
		hr.Header.Set(cred.Key, value)
	case apiKeyInQuery:
		appendQuery(hr.URL, []model.Param{{Key: cred.Key, Value: value}})
	default:
		return authError(hr, fmt.Errorf("unknown api key location %q, want %q or %q",
			cred.In, apiKeyInHeader, apiKeyInQuery))
	}
	return nil
}

// applyOAuth2 sends the access token the caller already acquired. Obtaining and
// refreshing tokens is a separate concern: the engine only ever sets the header.
func applyOAuth2(hr *http.Request, cred *model.OAuth2Auth) error {
	if cred == nil {
		return authError(hr, errors.New("oauth2 auth has no credentials"))
	}
	return setBearer(hr, "oauth2 access", cred.AccessToken)
}

// setBearer attaches a bearer token, trimming the stray whitespace that pasted
// credentials so often carry. Unlike basic credentials, a bearer token travels as a raw
// header value, so trimming fixes it rather than corrupting it.
func setBearer(hr *http.Request, label, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return authError(hr, fmt.Errorf("%s token is empty", label))
	}
	if err := checkHeaderValue(token); err != nil {
		return authError(hr, fmt.Errorf("%s %s", label, err))
	}
	hr.Header.Set("Authorization", "Bearer "+token)
	return nil
}

// checkHeaderValue rejects a credential that would break the request: a value carrying a
// line break is a header-injection risk, and the transport would otherwise refuse it with
// a far less obvious message. The value itself never appears in the error — it is a
// secret.
func checkHeaderValue(value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return errors.New("value contains a line break")
	}
	return nil
}
