package im

import (
	"encoding/json"
	"strings"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/utils"
)

// ParseCredentials parses the JSONB credentials field into a map and
// transparently decrypts any AES-GCM encrypted string entries while remaining
// compatible with historical plaintext values.
func ParseCredentials(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var creds map[string]any
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	for k, v := range creds {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		plain, err := utils.DecryptStoredSecret(s)
		if err != nil {
			return nil, err
		}
		creds[k] = plain
	}
	return creds, nil
}

// EncryptCredentialsJSON encrypts all non-empty string values in a JSONB
// credentials object using SYSTEM_AES_KEY when configured. Non-string values
// and already-encrypted values (enc:v1:) are preserved as-is.
func EncryptCredentialsJSON(raw types.JSON) types.JSON {
	key := utils.GetAESKey()
	if key == nil || len(raw) == 0 {
		return raw
	}
	var creds map[string]any
	if err := json.Unmarshal(raw, &creds); err != nil || len(creds) == 0 {
		return raw
	}
	changed := false
	out := make(map[string]any, len(creds))
	for k, v := range creds {
		if s, ok := v.(string); ok && s != "" {
			if enc, err := utils.EncryptAESGCM(s, key); err == nil {
				if enc != s {
					changed = true
				}
				out[k] = enc
				continue
			}
		}
		out[k] = v
	}
	if !changed {
		return raw
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return raw
	}
	return types.JSON(encoded)
}

// DecryptCredentialsJSON decrypts any enc:v1:-prefixed string entries in a
// JSONB credentials object while leaving legacy plaintext entries untouched.
func DecryptCredentialsJSON(raw types.JSON) types.JSON {
	if len(raw) == 0 {
		return raw
	}
	var creds map[string]any
	if err := json.Unmarshal(raw, &creds); err != nil || len(creds) == 0 {
		return raw
	}
	changed := false
	out := make(map[string]any, len(creds))
	for k, v := range creds {
		if s, ok := v.(string); ok && strings.HasPrefix(s, utils.EncPrefix) {
			changed = true
			if plain, ok := utils.DecryptStoredSecretLenient(s); ok {
				out[k] = plain
			} else {
				out[k] = ""
			}
			continue
		}
		out[k] = v
	}
	if !changed {
		return raw
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return raw
	}
	return types.JSON(encoded)
}

// GetString safely extracts a string value from a credentials map.
func GetString(creds map[string]any, key string) string {
	if v, ok := creds[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// GetBool reads a boolean from JSON credentials (bool, string "true"/"1", or non-zero number).
func GetBool(creds map[string]any, key string) bool {
	v, ok := creds[key]
	if !ok {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		s := strings.TrimSpace(strings.ToLower(x))
		return s == "true" || s == "1" || s == "yes"
	case float64:
		return x != 0
	case int:
		return x != 0
	default:
		return false
	}
}
