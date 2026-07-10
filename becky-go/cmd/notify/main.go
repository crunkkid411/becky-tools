// becky-notify — Telegram Bot API text message. The Manus-gap "world-action
// channel": pure API, zero browser, zero OAuth consent flow. One dumb call:
// give it text, it reaches Jordan.
//
//	becky-notify "message text" [--json]
//
// Token: BECKY_TELEGRAM_BOT_TOKEN env var, or resolved at call time from the
// gitignored API-keys manifest (BECKY_API_MANIFEST_PATH, or the pointer chain
// at X:\AI-2\hj-mission-control\data\.api-manifest-path). Read at call time,
// kept in process memory only, never written or logged (AUTOPILOT.md Law 18d).
//
// chat_id: BECKY_TELEGRAM_CHAT_ID env var, a cached prior discovery
// (~/.becky/telegram-chat-id.txt), or one getUpdates call to find the chat_id
// of the most recent message the bot has received. If none is discoverable,
// becky-notify prints a plain instruction and exits non-zero - it never
// guesses a chat_id.
//
// Exit codes: 0 = sent; 1 = anything went wrong (missing token, no chat_id,
// network failure, Telegram API error) - always {"ok":false,"error":"..."}
// on stdout either way; 2 = usage error.
package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"becky-go/internal/beckyio"
)

func main() {
	asJSON, rest := extractJSONFlag(os.Args[1:])
	if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
		fmt.Fprintln(os.Stderr, `usage: becky-notify "message text" [--json]`)
		os.Exit(2)
	}
	text := rest[0]

	client := &http.Client{Timeout: 15 * time.Second}

	token, err := resolveToken()
	if err != nil {
		fail(asJSON, err)
	}
	chatID, err := resolveChatID(token, client)
	if err != nil {
		fail(asJSON, err)
	}
	msgID, err := sendMessage(client, token, chatID, text)
	if err != nil {
		fail(asJSON, err)
	}

	if !asJSON {
		fmt.Fprintf(os.Stderr, "Sent to Telegram (message_id %d, token %s).\n", msgID, maskToken(token))
	}
	beckyio.PrintJSON(Result{OK: true, MessageID: msgID})
}

// fail prints the plain-language line (unless --json) and the JSON envelope,
// then exits 1. Every error string that reaches here has already had any
// secret masked by the caller that formed it (Law 18d).
func fail(asJSON bool, err error) {
	if !asJSON {
		fmt.Fprintln(os.Stderr, "becky-notify:", err)
	}
	beckyio.PrintJSON(Result{OK: false, Error: err.Error()})
	os.Exit(1)
}
