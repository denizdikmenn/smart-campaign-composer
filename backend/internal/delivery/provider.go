package delivery

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Request is one message handed to a provider. Key is stable for the
// recipient, campaign and channel, so a provider that honors it accepts the
// message at most once however often it is retried.
type Request struct {
	Key     string
	Channel string
	To      string // phone (E.164) or email address
	Subject string
	Body    string
	// ManualRetry marks a send the marketer retried by hand. The mock
	// simulates no failures for it (the demo's retried messages go out).
	ManualRetry bool
}

// Response is the provider's HTTP-style answer. 2xx means accepted (not
// delivered to a phone or inbox: providers report only acceptance here).
// Code is the provider's machine-readable reason, e.g. SMS_INVALID_NUMBER.
type Response struct {
	Status     int
	Code       string
	RetryAfter time.Duration // with 429
	MessageID  string
	Error      string
}

// Provider sends one message. An error means no response was received.
type Provider interface {
	Send(ctx context.Context, req Request) (Response, error)
}

// ErrNoResponse is the mock's lost response: the message was accepted but the
// answer never arrived, so the caller cannot know whether it went out.
var ErrNoResponse = errors.New("timeout")

// Fault is a simulated provider behavior for one call.
type Fault int

const (
	FaultNone         Fault = iota
	FaultRateLimit          // 429 with Retry-After
	FaultServer             // 503
	FaultReject             // 400, permanent
	FaultLostResponse       // accepted, then the response is lost
)

// Mock is an in-process SMS or email provider. It sends nothing; it sleeps
// for a simulated latency, applies Recipients rules and Faults, and records
// accepted messages in mock_provider_messages keyed by idempotency key — the
// store a real provider keeps on its side, so deduplication survives worker
// restarts.
type Mock struct {
	Pool       *pgxpool.Pool
	Channel    string
	MinLatency time.Duration
	MaxLatency time.Duration
	RetryAfter time.Duration // sent with 429
	// Recipients, if set, answers for specific recipients before any random
	// fault (see SyntheticRecipients); ok=false means no rule applies.
	Recipients func(req Request, call int) (resp Response, ok bool)
	// Faults picks the behavior of the call-th call (1-based) for a key; nil
	// means always accept. Tests script it; the worker uses RandomFaults.
	Faults func(req Request, call int) Fault
	// OnCall, if set, observes every call as it arrives (tests).
	OnCall func(req Request, at time.Time)

	mu    sync.Mutex
	calls map[string]int
}

func (m *Mock) Send(ctx context.Context, req Request) (Response, error) {
	if m.OnCall != nil {
		m.OnCall(req, time.Now())
	}
	m.mu.Lock()
	if m.calls == nil {
		m.calls = map[string]int{}
	}
	m.calls[req.Key]++
	call := m.calls[req.Key]
	m.mu.Unlock()

	if d := m.latency(req.Key, call); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	}
	if m.Recipients != nil && !req.ManualRetry {
		if resp, ok := m.Recipients(req, call); ok {
			return resp, nil
		}
	}
	fault := FaultNone
	if m.Faults != nil && !req.ManualRetry {
		fault = m.Faults(req, call)
	}
	prefix := strings.ToUpper(m.Channel)
	switch fault {
	case FaultRateLimit:
		return Response{Status: 429, Code: "RATE_LIMITED", RetryAfter: m.RetryAfter, Error: "too many requests"}, nil
	case FaultServer:
		return Response{Status: 503, Code: prefix + "_PROVIDER_UNAVAILABLE", Error: "service unavailable"}, nil
	case FaultReject:
		return Response{Status: 400, Code: prefix + "_REJECTED", Error: "recipient rejected by provider"}, nil
	}
	id, err := m.accept(ctx, req)
	if err != nil {
		return Response{Status: 500, Code: "PROVIDER_STORAGE_ERROR", Error: "provider storage error"}, nil
	}
	if fault == FaultLostResponse {
		return Response{}, ErrNoResponse
	}
	return Response{Status: 202, Code: "ACCEPTED", MessageID: id}, nil
}

// accept stores the message under its key, or returns the message ID of the
// earlier acceptance of the same key (an idempotent replay sends nothing).
func (m *Mock) accept(ctx context.Context, req Request) (string, error) {
	var b [8]byte
	_, _ = rand.Read(b[:])
	var id string
	err := m.Pool.QueryRow(ctx, `WITH ins AS (
			INSERT INTO mock_provider_messages (idempotency_key, channel, message_id) VALUES ($1, $2, $3)
			ON CONFLICT (idempotency_key) DO NOTHING RETURNING message_id)
		SELECT message_id FROM ins UNION ALL
		SELECT message_id FROM mock_provider_messages WHERE idempotency_key = $1 LIMIT 1`,
		req.Key, m.Channel, m.Channel+"_"+hex.EncodeToString(b[:])).Scan(&id)
	return id, err
}

func (m *Mock) latency(key string, call int) time.Duration {
	if m.MaxLatency <= m.MinLatency {
		return m.MinLatency
	}
	return m.MinLatency + time.Duration(unit(latencySeed, key, call)*float64(m.MaxLatency-m.MinLatency))
}

const latencySeed = 0x5eed // independent of the fault draw

// SyntheticRecipients gives a few recipients fixed, reproducible problems,
// like a real provider that knows some numbers and mailboxes. The bucket is a
// hash of the address (0-99), so the same address behaves the same way in
// every campaign, independent of random faults:
//
//	SMS   bucket 0     400 SMS_INVALID_NUMBER         permanent
//	SMS   bucket 1     403 SMS_BLOCKED_BY_CARRIER     permanent
//	SMS   bucket 2     503 SMS_CARRIER_UNAVAILABLE    first 2 calls, then accepted
//	Email bucket 0     400 EMAIL_INVALID_ADDRESS      permanent
//	Email bucket 1     422 EMAIL_SUPPRESSED           permanent (provider suppression list)
//	Email bucket 2     503 EMAIL_MAILBOX_BUSY         first call, then accepted
//	Email bucket 3     503 EMAIL_MAILBOX_BUSY         every call (exhausts retries)
func SyntheticRecipients(req Request, call int) (Response, bool) {
	b := Bucket(req.To)
	if req.Channel == "sms" {
		switch {
		case b == 0:
			return Response{Status: 400, Code: "SMS_INVALID_NUMBER", Error: "number is not a valid mobile number"}, true
		case b == 1:
			return Response{Status: 403, Code: "SMS_BLOCKED_BY_CARRIER", Error: "carrier blocks messages to this number"}, true
		case b == 2 && call <= 2:
			return Response{Status: 503, Code: "SMS_CARRIER_UNAVAILABLE", Error: "carrier temporarily unavailable"}, true
		}
		return Response{}, false
	}
	switch {
	case b == 0:
		return Response{Status: 400, Code: "EMAIL_INVALID_ADDRESS", Error: "mailbox does not exist"}, true
	case b == 1:
		return Response{Status: 422, Code: "EMAIL_SUPPRESSED", Error: "address is on the provider suppression list"}, true
	case (b == 2 && call == 1) || b == 3:
		return Response{Status: 503, Code: "EMAIL_MAILBOX_BUSY", Error: "receiving server temporarily unavailable"}, true
	}
	return Response{}, false
}

// Bucket hashes an address to 0-99 (see SyntheticRecipients).
func Bucket(address string) int {
	h := fnv.New64a()
	h.Write([]byte(strings.ToLower(address)))
	return int(mix(h.Sum64()) % 100)
}

// FaultRates are per-call probabilities of each simulated random fault.
type FaultRates struct {
	RateLimit, Server, Reject, LostResponse float64
}

// DefaultFaultRates make queueing, backoff and failures clearly visible in a
// demo (about 1 in 5 calls has a problem; most recover on retry).
var DefaultFaultRates = FaultRates{RateLimit: 0.06, Server: 0.10, Reject: 0.02, LostResponse: 0.03}

// ParseFaultRates reads "429=0.06,5xx=0.1,4xx=0.02,lost=0.03"; omitted keys
// keep their default.
func ParseFaultRates(s string, def FaultRates) (FaultRates, error) {
	r := def
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		p, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if !ok || err != nil || p < 0 || p > 1 {
			return def, fmt.Errorf("fault rate %q: want key=probability", part)
		}
		switch strings.TrimSpace(k) {
		case "429":
			r.RateLimit = p
		case "5xx":
			r.Server = p
		case "4xx":
			r.Reject = p
		case "lost":
			r.LostResponse = p
		default:
			return def, fmt.Errorf("fault rate %q: unknown key (429, 5xx, 4xx, lost)", part)
		}
	}
	if r.RateLimit+r.Server+r.Reject+r.LostResponse > 1 {
		return def, errors.New("fault rates add up to more than 1")
	}
	return r, nil
}

// RandomFaults draws faults from rates. The draw is a hash of (seed, key,
// call), so a given seed reproduces the same faults regardless of timing.
func RandomFaults(r FaultRates, seed uint64) func(Request, int) Fault {
	return func(req Request, call int) Fault {
		u := unit(seed, req.Key, call)
		for _, f := range []struct {
			p float64
			f Fault
		}{{r.RateLimit, FaultRateLimit}, {r.Server, FaultServer}, {r.Reject, FaultReject}, {r.LostResponse, FaultLostResponse}} {
			if u < f.p {
				return f.f
			}
			u -= f.p
		}
		return FaultNone
	}
}

// unit hashes its inputs to [0, 1).
func unit(seed uint64, key string, call int) float64 {
	h := fnv.New64a()
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], seed)
	binary.BigEndian.PutUint64(b[8:], uint64(call))
	h.Write(b[:])
	h.Write([]byte(key))
	return float64(mix(h.Sum64())>>11) / math.Exp2(53)
}

// mix is the splitmix64 finalizer: FNV alone spreads similar short inputs
// poorly in its high bits, which skews the fault rates.
func mix(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	return x ^ x>>31
}
