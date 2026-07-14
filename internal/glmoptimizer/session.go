package glmoptimizer

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const CachePolicyVersion = "glm52-cache-v1"

func DeriveSessionID(secret []byte, tenantScope, conversationID string) string {
	digest := keyedDigest(secret, "session:v1", tenantScope, conversationID)
	return "atm_" + base64.RawURLEncoding.EncodeToString(digest[:24])
}

// InjectStableSessionID replaces a supplied session identifier with an HMAC
// pseudonym. When no reliable identifier is supplied, it leaves session_id
// absent rather than inventing unstable identity.
func InjectStableSessionID(body, secret []byte, tenantScope, conversationID string) ([]byte, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("decode session request: %w", err)
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		if raw, found := request["session_id"]; found {
			if err := json.Unmarshal(raw, &conversationID); err != nil {
				return nil, fmt.Errorf("session_id must be a string: %w", err)
			}
			conversationID = strings.TrimSpace(conversationID)
		}
	}
	if conversationID == "" {
		delete(request, "session_id")
	} else {
		encoded, _ := json.Marshal(DeriveSessionID(secret, tenantScope, conversationID))
		request["session_id"] = encoded
	}
	result, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode session request: %w", err)
	}
	return result, nil
}

func BuildCacheKey(secret []byte, tenantScope, policyVersion string, canonicalBody []byte) string {
	digest := keyedDigest(secret, "cache:v1", tenantScope, policyVersion, string(canonicalBody))
	return hex.EncodeToString(digest)
}

func keyedDigest(secret []byte, fields ...string) []byte {
	mac := hmac.New(sha256.New, secret)
	for _, field := range fields {
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte(field))
	}
	return mac.Sum(nil)
}
