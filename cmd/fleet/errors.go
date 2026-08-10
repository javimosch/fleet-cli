package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type cliError struct {
	Code        int      `json:"code"`
	Type        string   `json:"type"`
	Message     string   `json:"message"`
	Recoverable bool     `json:"recoverable"`
	Suggestions []string `json:"suggestions,omitempty"`
}

func fail(format string, args ...interface{}) {
	failCode(80, "invalid_arguments", fmt.Sprintf(format, args...))
}

func failCode(code int, typ, message string, suggestions ...string) {
	recoverable := code >= 100 && code <= 109
	body, _ := json.Marshal(map[string]interface{}{
		"ok": false,
		"error": cliError{
			Code: code, Type: typ, Message: message,
			Recoverable: recoverable, Suggestions: suggestions,
		},
	})
	fmt.Fprintln(os.Stderr, string(body))
	os.Exit(code)
}

func outputJSON(v interface{}) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintln(os.Stdout, string(b))
}
