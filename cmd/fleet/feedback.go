package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func cmdFeedback(args []string) int {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		failCode(80, "feedback_message_required", "feedback requires a non-empty message", "fleet feedback \"what happened\"")
	}
	message := args[0]
	kind, contextText := "note", ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--kind", "-kind":
			if i+1 >= len(args) {
				failCode(80, "invalid_arguments", "feedback --kind requires bug, idea, praise, or note", "fleet help-json")
			}
			kind = args[i+1]
			i++
		case "--context", "-context":
			if i+1 >= len(args) {
				failCode(80, "invalid_arguments", "feedback --context requires text", "fleet help-json")
			}
			contextText = args[i+1]
			i++
		default:
			failCode(80, "invalid_arguments", fmt.Sprintf("unknown feedback argument %q", args[i]), "fleet help-json")
		}
	}
	if kind != "bug" && kind != "idea" && kind != "praise" && kind != "note" {
		failCode(85, "invalid_feedback_kind", fmt.Sprintf("feedback kind %q is invalid", kind), "use --kind bug|idea|praise|note")
	}

	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		failCode(110, "internal_error", "could not generate feedback id")
	}
	id := hex.EncodeToString(idBytes)
	reporter := os.Getenv("USER")
	if reporter == "" {
		reporter = "agent"
	}
	payload, _ := json.Marshal(map[string]string{
		"id": id, "app": "fleet-cli", "version": fleetVersion,
		"kind": kind, "message": message, "context": contextText, "reporter": reporter,
	})

	relay := os.Getenv("FEEDBACK_RELAY")
	if relay == "" {
		relay = "https://feedback.intrane.fr"
	}
	relayed := false
	if relay == "off" {
		fmt.Fprintln(os.Stderr, "[feedback] relay delivery disabled via FEEDBACK_RELAY=off")
	} else {
		fmt.Fprintln(os.Stderr, "[feedback] sending one best-effort report to the configured relay")
		relayed = postFeedback(relay+"/v1/feedback", payload)
		if !relayed {
			fmt.Fprintln(os.Stderr, "[feedback] relay unavailable; the caller still succeeded")
		}
	}
	stored := 0 // fleet-cli is a relay-only client; it has no local feedback endpoint.
	relayedInt := 0
	if relayed {
		relayedInt = 1
	}
	outputJSON(map[string]interface{}{
		"ok":   true,
		"data": map[string]interface{}{"id": id, "stored": stored, "relayed": relayedInt},
	})
	return 0
}

func postFeedback(url string, payload []byte) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
