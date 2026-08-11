package relais

import (
	"time"

	"github.com/javimosch/fleet-cli/internal/state"
)

// SetToken stores a relais token and inbox id keyed by proposal id in fleet state.
func SetToken(st *state.Store, proposalID, token, inboxID string) error {
	var tokens map[string]interface{}
	if v, ok := st.Get("relais_tokens"); ok {
		if m, ok := v.(map[string]interface{}); ok {
			tokens = m
		}
	}
	if tokens == nil {
		tokens = make(map[string]interface{})
	}
	tokens[proposalID] = map[string]interface{}{
		"token":      token,
		"inbox_id":   inboxID,
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	return st.Set("relais_tokens", tokens)
}

// GetToken returns the relais token for a proposal id.
func GetToken(st *state.Store, id string) (string, bool) {
	v, ok := st.Get("relais_tokens")
	if !ok {
		return "", false
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return "", false
	}
	v2, ok := m[id]
	if !ok {
		return "", false
	}
	rec, ok := v2.(map[string]interface{})
	if !ok {
		return "", false
	}
	tok, ok := rec["token"].(string)
	return tok, ok
}
