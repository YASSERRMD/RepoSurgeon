package auth

import "encoding/base64"

func decodeBase64URL(token string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(token)
}
