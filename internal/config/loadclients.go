package config

import (
	"encoding/json"
	"strings"

	"github.com/spf13/viper"
)

// This file reads the OAuth client registry off the configuration layers.
//
// The registry is the one setting that is a list of records rather than a scalar,
// so it has neither a flag nor a scalar environment form. Two spellings are
// accepted, and both end in the same wire type:
//
//   - a configuration-file list, which is how an operator normally writes it;
//   - a JSON array in GARMIN_MCP_OAUTH_CLIENTS, which is how a container
//     deployment supplies one without mounting a file.
//
// Nothing here reads a digest file. A "-file" setting yields a path, and opening
// it belongs to the composition root, where ownership and mode are enforced at the
// same time.

// clientWire is the serialized shape of one registration. The field names are the
// configuration-file keys, so a YAML list, a JSON document, and the sub-keys in
// settings.go all read the same.
type clientWire struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	RedirectURIs   []string `json:"redirect-uris"`
	Scopes         []string `json:"scopes"`
	Resources      []string `json:"resources"`
	Public         bool     `json:"public"`
	SecretHash     string   `json:"secret-hash"`
	SecretHashFile string   `json:"secret-hash-file"`
}

// clientList reads the registry from store.
//
// A value that cannot be understood yields an error rather than a partial
// registry: a half-read entry is a client silently missing its redirect URIs,
// which is worse than a refusal to start.
func clientList(store *viper.Viper) ([]OAuthClient, error) {
	raw := store.Get(keyOAuthClients)
	if raw == nil {
		return nil, nil
	}

	document, err := clientDocument(raw)
	if err != nil || len(document) == 0 {
		return nil, err
	}

	var wire []clientWire
	if err := json.Unmarshal(document, &wire); err != nil {
		return nil, newFieldError(keyOAuthClients,
			"must be a list of client registrations", ErrInvalidConfig)
	}
	return clientsOf(wire), nil
}

// clientDocument renders the resolved value as JSON, whichever layer produced it.
// A string is taken as a JSON document; anything else is re-encoded, which is what
// turns a configuration file's list of maps into the wire shape.
func clientDocument(raw any) ([]byte, error) {
	if text, ok := raw.(string); ok {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return nil, nil
		}
		return []byte(trimmed), nil
	}

	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, newFieldError(keyOAuthClients,
			"must be a list of client registrations", ErrInvalidConfig)
	}
	return encoded, nil
}

// clientsOf converts the wire shape into the configuration type, trimming every
// scalar the way every other setting is trimmed.
func clientsOf(wire []clientWire) []OAuthClient {
	out := make([]OAuthClient, 0, len(wire))
	for _, entry := range wire {
		out = append(out, OAuthClient{
			ID:             strings.TrimSpace(entry.ID),
			Name:           strings.TrimSpace(entry.Name),
			RedirectURIs:   normalizeList(entry.RedirectURIs),
			Scopes:         normalizeList(entry.Scopes),
			Resources:      normalizeList(entry.Resources),
			Public:         entry.Public,
			SecretHashPath: strings.TrimSpace(entry.SecretHashFile),
			SecretHash:     NewSecret(entry.SecretHash),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
