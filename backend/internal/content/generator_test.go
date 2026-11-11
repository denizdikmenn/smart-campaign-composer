package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/smart-campaign-composer/backend/internal/llm"
)

const goodReply = `{"settings":{"tone":"friendly","occasion":"cart_recovery","length":"short","cta":"complete_purchase"},` +
	`"sms_body":"Hi {{first_name}}, your cart is waiting.","email_subject":"Your cart is waiting","email_body":"Hi {{first_name}},\n\nCome back.\n\nThe team"}`

func TestParseReply(t *testing.T) {
	r, err := parseReply(goodReply)
	if err != nil || r.Settings.CTA != "complete_purchase" || !strings.Contains(r.EmailBody, "\n\n") {
		t.Fatalf("got %+v, %v", r, err)
	}
	bad := map[string]string{
		"unknown field": strings.Replace(goodReply, `"email_body"`, `"html":"x","email_body"`, 1),
		"bad enum":      strings.Replace(goodReply, `"friendly"`, `"sarcastic"`, 1),
		"empty field":   strings.Replace(goodReply, `"Your cart is waiting"`, `"  "`, 1),
		"not json":      "Sure! Here is your campaign:",
	}
	for name, raw := range bad {
		if _, err := parseReply(raw); !errors.Is(err, llm.ErrBadReply) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

// sse writes Anthropic-style streaming events. stop "" ends the stream
// without message_delta/message_stop (an interrupted connection).
func fakeAnthropic(t *testing.T, status int, chunks []string, stop string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			fmt.Fprint(w, `{"type":"error","error":{"type":"error","message":"nope"}}`)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] != true {
			t.Errorf("request is not streaming: %v", body["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(event string, data string) {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
			w.(http.Flusher).Flush()
		}
		send("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":321,"output_tokens":1}}}`)
		send("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		for _, c := range chunks {
			text, _ := json.Marshal(c)
			send("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`+string(text)+`}}`)
		}
		if stop == "" {
			return // connection closes mid-message
		}
		send("content_block_stop", `{"type":"content_block_stop","index":0}`)
		send("message_delta", `{"type":"message_delta","delta":{"stop_reason":"`+stop+`","stop_sequence":null},"usage":{"output_tokens":77}}`)
		send("message_stop", `{"type":"message_stop"}`)
	}))
}

func chunk(s string, n int) []string {
	var out []string
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	return append(out, s)
}

func TestClaudeStreaming(t *testing.T) {
	gen := func(srv *httptest.Server) *Claude {
		return NewClaude("test-key", "test-model", option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	}
	req := Request{Brief: "Remind cart abandoners in Istanbul.", Audience: Audience{Name: "Cart", MaxFirstNameLen: 9}}

	t.Run("deltas then parsed reply and usage", func(t *testing.T) {
		srv := fakeAnthropic(t, 200, chunk(goodReply, 17), "end_turn")
		defer srv.Close()
		var got strings.Builder
		n := 0
		res, err := gen(srv).Generate(context.Background(), req, func(s string) { got.WriteString(s); n++ })
		if err != nil {
			t.Fatal(err)
		}
		if got.String() != goodReply || n < 5 {
			t.Fatalf("deltas: %d chunks, %q", n, got.String())
		}
		if res.Reply.SMSBody != "Hi {{first_name}}, your cart is waiting." || res.InputTokens != 321 || res.OutputTokens != 77 {
			t.Fatalf("result %+v", res)
		}
	})

	t.Run("interrupted stream", func(t *testing.T) {
		srv := fakeAnthropic(t, 200, chunk(goodReply[:60], 10), "")
		defer srv.Close()
		res, err := gen(srv).Generate(context.Background(), req, func(string) {})
		if !errors.Is(err, ErrInterrupted) || !errors.Is(err, llm.ErrRetry) || res.Raw != goodReply[:60] {
			t.Fatalf("got %v, raw %q", err, res.Raw)
		}
	})

	t.Run("stop reasons", func(t *testing.T) {
		for stop, want := range map[string]error{"max_tokens": llm.ErrBadReply, "refusal": llm.ErrRefusal} {
			srv := fakeAnthropic(t, 200, []string{`{"settings":`}, stop)
			_, err := gen(srv).Generate(context.Background(), req, func(string) {})
			srv.Close()
			if !errors.Is(err, want) {
				t.Errorf("%s: got %v", stop, err)
			}
		}
	})

	t.Run("API errors", func(t *testing.T) {
		for status, want := range map[int]error{401: llm.ErrAuth, 529: llm.ErrRetry, 429: llm.ErrRetry} {
			srv := fakeAnthropic(t, status, nil, "")
			_, err := gen(srv).Generate(context.Background(), req, func(string) {})
			srv.Close()
			if !errors.Is(err, want) {
				t.Errorf("%d: got %v", status, err)
			}
		}
	})
}

func TestMockStreamsParsableReply(t *testing.T) {
	var buf strings.Builder
	res, err := Mock{}.Generate(context.Background(), Request{Brief: "Send a 30% discount reminder to cart abandoners. Keep it short and friendly."},
		func(s string) { buf.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseReply(buf.String()); err != nil {
		t.Fatal(err)
	}
	if res.Reply.Settings.Occasion != "cart_recovery" || res.Reply.Settings.Length != "short" || !strings.Contains(res.Reply.SMSBody, "30% off") {
		t.Fatalf("got %+v", res.Reply)
	}
	if issues := Validate(Content{SMSEnabled: true, EmailEnabled: true, Settings: res.Reply.Settings, SMSBody: res.Reply.SMSBody,
		EmailSubject: res.Reply.EmailSubject, EmailBody: res.Reply.EmailBody}, "30% discount", 12); len(issues) != 0 {
		t.Fatalf("mock output should validate: %+v", issues)
	}
}
