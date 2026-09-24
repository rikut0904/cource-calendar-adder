package calendar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"class-calendar-adder/internal/domain"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const calendarScope = "https://www.googleapis.com/auth/calendar.events"

type Google struct {
	calendarID string
	timeZone   string
	config     *oauth2.Config
	tokenPath  string
	httpClient *http.Client
}

type event struct {
	Summary     string        `json:"summary"`
	Description string        `json:"description,omitempty"`
	Location    string        `json:"location,omitempty"`
	Start       eventDateTime `json:"start"`
	End         eventDateTime `json:"end"`
	Recurrence  []string      `json:"recurrence,omitempty"`
}

type eventDateTime struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

type createdEvent struct {
	ID   string `json:"id"`
	HTML string `json:"htmlLink"`
}

func NewGoogle(ctx context.Context, credentialsPath, calendarID, timeZone, tokenPath string) (*Google, error) {
	credentials, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("credentials file: %w", err)
	}
	config, err := google.ConfigFromJSON(credentials, calendarScope)
	if err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}
	if calendarID == "" {
		calendarID = "primary"
	}
	if timeZone == "" {
		timeZone = "Asia/Tokyo"
	}
	if tokenPath == "" {
		tokenPath = filepath.Join(os.TempDir(), "class-calendar-adder-token.json")
	}
	client, err := authenticatedClient(ctx, config, tokenPath)
	if err != nil {
		return nil, err
	}
	return &Google{calendarID: calendarID, timeZone: timeZone, config: config, tokenPath: tokenPath, httpClient: client}, nil
}

func authenticatedClient(ctx context.Context, config *oauth2.Config, tokenPath string) (*http.Client, error) {
	if token, err := readToken(tokenPath); err == nil {
		return config.Client(ctx, token), nil
	}

	listener, err := netListen()
	if err != nil {
		return nil, fmt.Errorf("start OAuth callback: %w", err)
	}
	defer listener.Close()
	config.RedirectURL = "http://" + listener.Addr().String() + "/oauth2callback"
	codeCh := make(chan string, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth2callback" {
			http.NotFound(w, r)
			return
		}
		if oauthError := r.URL.Query().Get("error"); oauthError != "" {
			codeCh <- ""
			_, _ = io.WriteString(w, "Authorization was denied. You can close this window.")
			return
		}
		codeCh <- r.URL.Query().Get("code")
		_, _ = io.WriteString(w, "Authorization complete. You can close this window.")
	})}
	go func() { _ = server.Serve(listener) }()

	fmt.Printf("\nブラウザで次のURLを開いてGoogle Calendarへのアクセスを許可してください:\n%s\n認証後、この画面に戻ってEnterを押してください。\n", config.AuthCodeURL("state", oauth2.AccessTypeOffline, oauth2.ApprovalForce))
	if _, err := fmt.Scanln(); err != nil && err.Error() != "unexpected newline" {
		return nil, fmt.Errorf("wait for OAuth callback: %w", err)
	}
	select {
	case code := <-codeCh:
		if code == "" {
			return nil, fmt.Errorf("OAuth authorization was denied")
		}
		token, err := config.Exchange(ctx, code)
		if err != nil {
			return nil, fmt.Errorf("exchange OAuth code: %w", err)
		}
		if err := writeToken(tokenPath, token); err != nil {
			return nil, err
		}
		return config.Client(ctx, token), nil
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("timed out waiting for OAuth callback")
	}
}

// netListen is a variable to keep OAuth setup easy to replace in tests.
var netListen = func() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }

func (g *Google) CreateLesson(lesson domain.Lesson) (string, error) {
	description := lesson.Description
	if lesson.Teacher != "" {
		description = "教師: " + lesson.Teacher + "\n" + description
	}
	payload := event{
		Summary: lesson.Title, Description: description, Location: lesson.Location,
		Start:      eventDateTime{DateTime: lesson.Start.Format(time.RFC3339), TimeZone: g.timeZone},
		End:        eventDateTime{DateTime: lesson.End.Format(time.RFC3339), TimeZone: g.timeZone},
		Recurrence: lesson.Recurrence,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode calendar event: %w", err)
	}
	endpoint := "https://www.googleapis.com/calendar/v3/calendars/" + url.PathEscape(g.calendarID) + "/events"
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("create calendar request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := g.httpClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("calendar request: %w", err)
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		resp.Body.Close()
		if readErr != nil {
			return "", fmt.Errorf("read calendar response: %w", readErr)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			var created createdEvent
			if err := json.Unmarshal(responseBody, &created); err != nil {
				return "", fmt.Errorf("decode calendar response: %w", err)
			}
			return created.HTML, nil
		}
		message := strings.TrimSpace(string(responseBody))
		if attempt < 3 && retryableCalendarResponse(resp.StatusCode, message) {
			time.Sleep(time.Duration(1<<attempt) * time.Second)
			continue
		}
		return "", fmt.Errorf("calendar API returned %s: %s", resp.Status, message)
	}
	return "", fmt.Errorf("calendar request retry limit exceeded")
}

func retryableCalendarResponse(status int, message string) bool {
	return status == http.StatusTooManyRequests || status >= 500 || (status == http.StatusForbidden && (strings.Contains(message, "rateLimitExceeded") || strings.Contains(message, "userRateLimitExceeded")))
}

func readToken(path string) (*oauth2.Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, err
	}
	return &token, nil
}

func writeToken(path string, token *oauth2.Token) error {
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("encode OAuth token: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("save OAuth token: %w", err)
	}
	return nil
}
