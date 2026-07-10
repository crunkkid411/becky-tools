// notify.go — the pure, testable core of becky-notify: resolving the Telegram
// bot token and chat_id, and talking to the Telegram Bot API. main.go is just
// flag parsing + wiring; everything with a decision in it lives here so it can
// be unit-tested without a real network call or a real manifest file.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// extractJSONFlag pulls a --json/-json flag out of args wherever it appears
// (positional args come before OR after it - Go's stdlib flag package stops
// parsing at the first non-flag arg, which would wrongly reject
// `becky-notify "text" --json`) and returns whether it was present plus the
// remaining positional args in order.
func extractJSONFlag(args []string) (bool, []string) {
	found := false
	rest := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--json" || a == "-json" {
			found = true
			continue
		}
		rest = append(rest, a)
	}
	return found, rest
}

// Result is becky-notify's stdout JSON envelope, always one of:
//
//	{"ok":true,"message_id":N}
//	{"ok":false,"error":"..."}
type Result struct {
	OK        bool   `json:"ok"`
	MessageID int    `json:"message_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

// defaultManifestPointer is the gitignored file (in the sibling hj-mission-control
// repo) that names the real, messy API-keys manifest on this machine
// (AUTOPILOT.md Law 18d). BECKY_API_MANIFEST_PATH overrides it directly with the
// manifest file itself, skipping the pointer indirection.
const defaultManifestPointer = `X:\AI-2\hj-mission-control\data\.api-manifest-path`

// telegramTokenRe matches Telegram's distinctive bot-token shape: a numeric bot
// id, a colon, then a 30+ char secret. Nothing else in Jordan's manifest (JWTs,
// sk-... keys, plain hex tokens) matches this exact digits-colon-secret shape,
// so one regex scoped to the Telegram section is enough - no fragile
// line-position parsing of a manifest that gets hand-edited.
var telegramTokenRe = regexp.MustCompile(`\b\d{6,}:[A-Za-z0-9_-]{30,}\b`)

// resolveToken finds the Telegram bot token. Law 18d: read at call time, never
// write it anywhere, keep it in process memory only. BECKY_TELEGRAM_BOT_TOKEN
// wins outright (explicit override / testing); otherwise it is extracted from
// the manifest located via BECKY_API_MANIFEST_PATH or the pointer chain.
func resolveToken() (string, error) {
	if t := strings.TrimSpace(os.Getenv("BECKY_TELEGRAM_BOT_TOKEN")); t != "" {
		return t, nil
	}
	manifestPath, err := resolveManifestPath()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", fmt.Errorf("read API manifest: %w", err)
	}
	return extractTelegramToken(string(raw))
}

// resolveManifestPath finds the real manifest file: BECKY_API_MANIFEST_PATH if
// set (as the manifest itself), else the gitignored pointer file's contents.
func resolveManifestPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv("BECKY_API_MANIFEST_PATH")); p != "" {
		return p, nil
	}
	raw, err := os.ReadFile(defaultManifestPointer)
	if err != nil {
		return "", fmt.Errorf("no Telegram token configured: set BECKY_TELEGRAM_BOT_TOKEN, or BECKY_API_MANIFEST_PATH, or create %s (%w)", defaultManifestPointer, err)
	}
	path := strings.TrimSpace(string(raw))
	if path == "" {
		return "", fmt.Errorf("%s is empty", defaultManifestPointer)
	}
	return path, nil
}

// extractTelegramToken finds the bot token near a "Telegram" section header in
// the raw manifest text.
func extractTelegramToken(manifest string) (string, error) {
	lines := strings.Split(manifest, "\n")
	start := -1
	for i, ln := range lines {
		if strings.Contains(strings.ToLower(ln), "telegram") {
			start = i
			break
		}
	}
	if start == -1 {
		return "", fmt.Errorf("no Telegram section found in the API manifest")
	}
	end := start + 12
	if end > len(lines) {
		end = len(lines)
	}
	section := strings.Join(lines[start:end], "\n")
	m := telegramTokenRe.FindString(section)
	if m == "" {
		return "", fmt.Errorf("no bot token found near the Telegram section in the API manifest")
	}
	return m, nil
}

// maskToken shows just enough of a token to confirm it's the right one without
// ever revealing the secret (Law 18d - masked in every log/output/error).
func maskToken(token string) string {
	if len(token) <= 10 {
		return "***"
	}
	return token[:6] + "..." + token[len(token)-4:]
}

// redactToken replaces every occurrence of the raw token in s with its masked
// form. Go's http.Client errors embed the full request URL (which contains
// the token) in their Error() text, so every error string built from a failed
// request MUST pass through this before it can reach stderr, JSON output, or
// a WORKLOG - the one Law 18d cannot afford to miss.
func redactToken(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, maskToken(token))
}

// chatStateFile caches a discovered chat_id so repeat runs don't need to call
// getUpdates again (Telegram only returns each update once - a second
// getUpdates call after the first won't see the same "say hi" message).
func chatStateFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".becky", "telegram-chat-id.txt")
}

// httpGetter is the one method notify.go needs from *http.Client, so tests can
// fake the network without a real server.
type httpGetter interface {
	Get(url string) (*http.Response, error)
	PostForm(url string, data url.Values) (*http.Response, error)
}

// resolveChatID finds the chat_id to send to: an explicit override, a cached
// prior discovery, or a fresh discovery via getUpdates. If none is
// discoverable it returns a plain-language instruction rather than guessing.
func resolveChatID(token string, client httpGetter) (string, error) {
	if id := strings.TrimSpace(os.Getenv("BECKY_TELEGRAM_CHAT_ID")); id != "" {
		return id, nil
	}
	if raw, err := os.ReadFile(chatStateFile()); err == nil {
		if id := strings.TrimSpace(string(raw)); id != "" {
			return id, nil
		}
	}
	id, err := discoverChatID(token, client)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("no chat_id on file and no messages found - open the bot in Telegram and send it any message once, then run becky-notify again")
	}
	_ = os.MkdirAll(filepath.Dir(chatStateFile()), 0o700)
	_ = os.WriteFile(chatStateFile(), []byte(id), 0o600)
	return id, nil
}

// tgUpdatesResponse is the shape of Telegram's getUpdates response, trimmed to
// the one field we need: the chat_id of each message received.
type tgUpdatesResponse struct {
	OK     bool `json:"ok"`
	Result []struct {
		Message *struct {
			Chat struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"message"`
	} `json:"result"`
}

// discoverChatID calls getUpdates once and returns the chat_id of the most
// recent message the bot has received, or "" if there are none yet.
func discoverChatID(token string, client httpGetter) (string, error) {
	resp, err := client.Get(fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates", token))
	if err != nil {
		return "", fmt.Errorf("network error reaching Telegram (getUpdates): %s", redactToken(err.Error(), token))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read Telegram getUpdates response: %w", err)
	}
	return parseUpdatesChatID(body)
}

// parseUpdatesChatID is the pure parse step of discoverChatID, testable
// without a network call.
func parseUpdatesChatID(body []byte) (string, error) {
	var r tgUpdatesResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("unexpected Telegram getUpdates response: %w", err)
	}
	if !r.OK {
		return "", fmt.Errorf("telegram getUpdates API error")
	}
	for i := len(r.Result) - 1; i >= 0; i-- {
		if m := r.Result[i].Message; m != nil {
			return strconv.FormatInt(m.Chat.ID, 10), nil
		}
	}
	return "", nil
}

// tgSendResponse is the shape of Telegram's sendMessage response.
type tgSendResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Result      *struct {
		MessageID int `json:"message_id"`
	} `json:"result"`
}

// sendMessage posts text to chatID via the Telegram Bot API and returns the
// new message's id.
func sendMessage(client httpGetter, token, chatID, text string) (int, error) {
	form := url.Values{}
	form.Set("chat_id", chatID)
	form.Set("text", text)
	resp, err := client.PostForm(fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token), form)
	if err != nil {
		return 0, fmt.Errorf("network error reaching Telegram (sendMessage): %s", redactToken(err.Error(), token))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read Telegram sendMessage response: %w", err)
	}
	return parseSendResponse(body)
}

// parseSendResponse is the pure parse step of sendMessage, testable without a
// network call.
func parseSendResponse(body []byte) (int, error) {
	var r tgSendResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return 0, fmt.Errorf("unexpected Telegram sendMessage response: %w", err)
	}
	if !r.OK {
		desc := r.Description
		if desc == "" {
			desc = "unknown Telegram API error"
		}
		return 0, fmt.Errorf("telegram API error: %s", desc)
	}
	if r.Result == nil {
		return 0, fmt.Errorf("telegram API returned ok=true with no message_id")
	}
	return r.Result.MessageID, nil
}
