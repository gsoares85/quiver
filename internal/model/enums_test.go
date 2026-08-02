package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthTypeValid(t *testing.T) {
	for _, a := range []AuthType{AuthNone, AuthInherit, AuthBasic, AuthBearer, AuthAPIKey, AuthOAuth2} {
		require.Truef(t, a.Valid(), "%q should be valid", a)
	}
	for _, a := range []AuthType{"", "digest", "None", "BASIC"} {
		require.Falsef(t, a.Valid(), "%q should be invalid", a)
	}
}

func TestBodyTypeValid(t *testing.T) {
	for _, b := range []BodyType{BodyNone, BodyJSON, BodyText, BodyXML, BodyForm, BodyMultipart, BodyGraphQL, BodyBinary} {
		require.Truef(t, b.Valid(), "%q should be valid", b)
	}
	for _, b := range []BodyType{"", "yaml", "JSON"} {
		require.Falsef(t, b.Valid(), "%q should be invalid", b)
	}
}

func TestValidMethod(t *testing.T) {
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE", "CONNECT", "WS", "GRPC", "GRAPHQL"} {
		require.Truef(t, ValidMethod(m), "%q should be valid", m)
	}
	for _, m := range []string{"", "get", "FETCH", "Post"} {
		require.Falsef(t, ValidMethod(m), "%q should be invalid", m)
	}
}
