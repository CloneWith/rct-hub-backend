// Package irc contains the Bancho side-effect boundary. IRC observations are
// evidence only; they never mutate a MatchEngine aggregate directly.
package irc

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

var ErrChannelBusy = errors.New("IRC channel has an outstanding delivery")

type Observation struct {
	ID              string
	Channel         string
	Sender          string
	Command         string
	Arguments       []string
	Raw             string
	Observed        time.Time
	SuggestedResult *ResultSuggestion
}

type ResultSuggestion struct {
	WinningTeam  string
	BoardPieceID string
}

type DeliveryReceipt struct {
	JobID        string
	LeaseToken   string
	Channel      string
	Attempts     int
	Acknowledged bool
	Message      string
	ReceivedAt   time.Time
}

type DeliveryGate interface {
	Acquire(context.Context, Job) error
	Release(Job)
}

type pendingDelivery struct {
	jobID    string
	token    string
	kind     string
	channel  string
	target   string
	attempts int
}

type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type Client struct {
	dialer       Dialer
	address      string
	user         string
	pass         string
	channel      string
	mu           sync.Mutex
	conn         net.Conn
	lastErr      string
	joined       map[string]bool
	pending      []pendingDelivery
	receipt      func(DeliveryReceipt)
	gate         DeliveryGate
	connectMu    sync.Mutex
	writeMu      sync.Mutex
	writeTimeout time.Duration
}

const defaultWriteTimeout = 5 * time.Second

type ConnectionStatus struct {
	Configured bool
	Connected  bool
	LastError  string
}

func (c *Client) Status() ConnectionStatus {
	if c == nil {
		return ConnectionStatus{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return ConnectionStatus{Configured: c.address != "" && c.user != "", Connected: c.conn != nil, LastError: c.lastErr}
}

func (c *Client) Send(ctx context.Context, job Job) (Delivery, error) {
	if len(job.Payload) == 0 {
		return Delivery{}, fmt.Errorf("IRC job %q has empty payload", job.ID)
	}
	channel := job.Channel
	if channel == "" {
		channel = c.channel
	}
	if !MatchChannel(channel) {
		return Delivery{}, fmt.Errorf("IRC job %q has invalid match channel", job.ID)
	}
	job.Channel = channel
	target := job.AckTarget
	if target == "" {
		var err error
		target, err = commandTarget(job.Kind, job.Payload)
		if err != nil {
			return Delivery{}, err
		}
	}
	if err := c.join(ctx, channel); err != nil {
		return Delivery{}, err
	}
	c.mu.Lock()
	gate := c.gate
	c.mu.Unlock()
	if gate != nil {
		if err := gate.Acquire(ctx, job); err != nil {
			return Delivery{}, err
		}
	}
	c.mu.Lock()
	for _, pending := range c.pending {
		if pending.channel == channel && pending.jobID != job.ID {
			c.mu.Unlock()
			if gate != nil {
				gate.Release(job)
			}
			return Delivery{}, ErrChannelBusy
		}
	}
	for index := len(c.pending) - 1; index >= 0; index-- {
		if c.pending[index].jobID == job.ID {
			c.pending = append(c.pending[:index], c.pending[index+1:]...)
		}
	}
	c.pending = append(c.pending, pendingDelivery{jobID: job.ID, token: job.LeaseToken, kind: job.Kind, channel: channel, target: target, attempts: job.Attempts})
	c.mu.Unlock()
	if err := c.writeLine(ctx, string(job.Payload)); err != nil {
		c.removePending(job.ID)
		if gate != nil {
			gate.Release(job)
		}
		return Delivery{}, err
	}
	// Bancho commands are not acknowledged by the IRC transport itself. The
	// matching BanchoBot response is correlated by Read and reported later.
	return Delivery{Status: JobSent}, nil
}

func NewClient(dialer Dialer, address, user, pass, channel string) *Client {
	return &Client{dialer: dialer, address: address, user: user, pass: pass, channel: channel, joined: map[string]bool{}, writeTimeout: defaultWriteTimeout}
}

// WithWriteTimeout bounds each IRC socket write, including PONG and JOIN.
// The worker normally uses a long-lived background context, so relying on a
// caller deadline would allow a half-open Bancho connection to stall forever.
func (c *Client) WithWriteTimeout(timeout time.Duration) *Client {
	if timeout > 0 {
		c.writeTimeout = timeout
	}
	return c
}

func (c *Client) SetReceiptHandler(handler func(DeliveryReceipt)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.receipt = handler
}

func (c *Client) WithDeliveryGate(gate DeliveryGate) *Client {
	c.mu.Lock()
	c.gate = gate
	c.mu.Unlock()
	return c
}

func (c *Client) Connect(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("IRC client is not configured")
	}
	c.connectMu.Lock()
	defer c.connectMu.Unlock()
	if c.dialer == nil || c.address == "" || c.user == "" {
		return fmt.Errorf("IRC client is not configured")
	}
	if containsIRCLineBreak(c.user) || containsIRCLineBreak(c.pass) {
		return fmt.Errorf("IRC credentials contain an invalid line break")
	}
	c.mu.Lock()
	alreadyConnected := c.conn != nil
	c.mu.Unlock()
	if alreadyConnected {
		return nil
	}
	conn, err := c.dialer.DialContext(ctx, "tcp", c.address)
	if err != nil {
		return fmt.Errorf("dial Bancho IRC: %w", err)
	}
	c.mu.Lock()
	writeTimeout := c.writeTimeout
	c.mu.Unlock()
	if c.pass != "" {
		if err := writeHandshakeLine(ctx, conn, "PASS "+c.pass, writeTimeout); err != nil {
			_ = conn.Close()
			return fmt.Errorf("initialize Bancho IRC session: %w", err)
		}
	}
	if err := writeHandshakeLine(ctx, conn, "NICK "+c.user, writeTimeout); err != nil {
		_ = conn.Close()
		return fmt.Errorf("initialize Bancho IRC session: %w", err)
	}
	if err := writeHandshakeLine(ctx, conn, "USER "+c.user+" 0 * :"+c.user, writeTimeout); err != nil {
		_ = conn.Close()
		return fmt.Errorf("initialize Bancho IRC session: %w", err)
	}
	if c.channel != "" {
		if err := writeHandshakeLine(ctx, conn, "JOIN "+c.channel, writeTimeout); err != nil {
			_ = conn.Close()
			return fmt.Errorf("initialize Bancho IRC session: %w", err)
		}
	}
	c.mu.Lock()
	c.conn = conn
	c.lastErr = ""
	c.joined = map[string]bool{}
	if c.channel != "" {
		c.joined[c.channel] = true
	}
	c.pending = nil
	c.mu.Unlock()
	return nil
}

func containsIRCLineBreak(value string) bool {
	return strings.ContainsAny(value, "\r\n")
}

func writeHandshakeLine(ctx context.Context, conn net.Conn, line string, timeout time.Duration) error {
	writeCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		writeCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	if deadline, ok := writeCtx.Deadline(); ok {
		if err := conn.SetWriteDeadline(deadline); err != nil {
			return err
		}
		defer conn.SetWriteDeadline(time.Time{})
	}
	_, err := fmt.Fprintf(conn, "%s\r\n", line)
	return err
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.conn == nil {
		c.mu.Unlock()
		return nil
	}
	conn := c.conn
	c.conn = nil
	c.joined = map[string]bool{}
	pending, gate := c.pending, c.gate
	c.pending = nil
	c.mu.Unlock()
	c.releasePending(gate, pending)
	return conn.Close()
}

func (c *Client) Read(ctx context.Context, emit func(Observation)) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("IRC client is not connected")
	}
	defer func() {
		_ = conn.Close()
		c.mu.Lock()
		var pending []pendingDelivery
		var gate DeliveryGate
		if c.conn == conn {
			c.conn = nil
			c.joined = map[string]bool{}
			pending, gate = c.pending, c.gate
			c.pending = nil
		}
		c.mu.Unlock()
		c.releasePending(gate, pending)
	}()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		if res, ok := strings.CutPrefix(line, "PING "); ok {
			_ = c.writeLine(ctx, "PONG "+res)
			continue
		}
		observation, ok := parseObservation(line)
		if ok {
			c.handleReceipt(observation)
			if emit != nil {
				emit(observation)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	return scanner.Err()
}

func (c *Client) releasePending(gate DeliveryGate, pending []pendingDelivery) {
	if gate == nil {
		return
	}
	for _, delivery := range pending {
		gate.Release(Job{ID: delivery.jobID, Channel: delivery.channel, LeaseToken: delivery.token})
	}
}

// Run reconnects with bounded backoff. A disconnect is observable to the
// caller through the next retry delay, while duplicate messages remain safe
// because Observation.ID is derived from the raw line.
func (c *Client) Run(ctx context.Context, emit func(Observation)) error {
	delay := time.Second
	for {
		if err := c.Connect(ctx); err == nil {
			delay = time.Second
			readDone := make(chan struct{})
			go func() {
				select {
				case <-ctx.Done():
					_ = c.Close()
				case <-readDone:
				}
			}()
			readErr := c.Read(ctx, emit)
			close(readDone)
			if readErr == nil && ctx.Err() != nil {
				return ctx.Err()
			} else if readErr != nil && ctx.Err() == nil {
				c.mu.Lock()
				c.lastErr = readErr.Error()
				c.mu.Unlock()
			}
		} else {
			c.mu.Lock()
			c.lastErr = err.Error()
			c.mu.Unlock()
		}
		_ = c.Close()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if delay < time.Minute {
			delay *= 2
		}
	}
}

func parseObservation(line string) (Observation, bool) {
	parts := strings.Fields(line)
	if len(parts) < 4 || parts[1] != "PRIVMSG" {
		return Observation{}, false
	}
	sender := strings.TrimPrefix(parts[0], ":")
	channel := parts[2]
	message := strings.TrimSpace(strings.Join(parts[3:], " "))
	if message == "" {
		return Observation{}, false
	}
	hash := sha256.Sum256([]byte(channel + "\x00" + line))
	return Observation{
		ID: hex.EncodeToString(hash[:]), Channel: channel, Sender: sender,
		Command: message, Arguments: strings.Fields(strings.TrimPrefix(message, ":")),
		Raw: line, Observed: time.Now().UTC(), SuggestedResult: ParseResultSuggestion(message),
	}, true
}

func ParseResultSuggestion(command string) *ResultSuggestion {
	parts := strings.Fields(strings.TrimPrefix(strings.TrimSpace(command), ":"))
	if len(parts) != 3 || !strings.EqualFold(parts[0], "!result") {
		return nil
	}
	team := strings.ToUpper(parts[1])
	if team != "RED" && team != "BLUE" || strings.TrimSpace(parts[2]) == "" {
		return nil
	}
	return &ResultSuggestion{WinningTeam: team, BoardPieceID: parts[2]}
}

func (c *Client) writeLine(ctx context.Context, line string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	conn := c.conn
	timeout := c.writeTimeout
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("IRC client is not connected")
	}
	writeCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		writeCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	if deadline, ok := writeCtx.Deadline(); ok {
		_ = conn.SetWriteDeadline(deadline)
		defer conn.SetWriteDeadline(time.Time{})
	}
	_, err := fmt.Fprintf(conn, "%s\r\n", line)
	return err
}

func (c *Client) join(ctx context.Context, channel string) error {
	c.mu.Lock()
	joined := c.joined[channel]
	c.mu.Unlock()
	if joined {
		return nil
	}
	if err := c.writeLine(ctx, "JOIN "+channel); err != nil {
		return err
	}
	c.mu.Lock()
	c.joined[channel] = true
	c.mu.Unlock()
	return nil
}

func (c *Client) removePending(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for index := range c.pending {
		if c.pending[index].jobID == id {
			c.pending = append(c.pending[:index], c.pending[index+1:]...)
			return
		}
	}
}

// ForgetDelivery removes in-memory correlation after a durable timeout or
// manual retry. It does not alter the persisted job state.
func (c *Client) ForgetDelivery(id string) {
	c.mu.Lock()
	var forgotten *pendingDelivery
	for index := range c.pending {
		if c.pending[index].jobID == id {
			value := c.pending[index]
			forgotten = &value
			c.pending = append(c.pending[:index], c.pending[index+1:]...)
			break
		}
	}
	gate := c.gate
	c.mu.Unlock()
	if gate != nil && forgotten != nil {
		gate.Release(Job{ID: forgotten.jobID, Channel: forgotten.channel, LeaseToken: forgotten.token})
	}
}

func (c *Client) CancelDelivery(job Job) {
	c.mu.Lock()
	channel := job.Channel
	for index := range c.pending {
		if c.pending[index].jobID == job.ID {
			channel = c.pending[index].channel
			c.pending = append(c.pending[:index], c.pending[index+1:]...)
			break
		}
	}
	gate := c.gate
	c.mu.Unlock()
	if gate != nil {
		gate.Release(Job{ID: job.ID, Channel: channel, LeaseToken: job.LeaseToken})
	}
}

func (c *Client) ReleaseDelivery(receipt DeliveryReceipt) {
	c.mu.Lock()
	gate := c.gate
	c.mu.Unlock()
	if gate != nil {
		gate.Release(Job{ID: receipt.JobID, Channel: receipt.Channel, LeaseToken: receipt.LeaseToken})
	}
}

func (c *Client) handleReceipt(observation Observation) {
	nick := strings.SplitN(observation.Sender, "!", 2)[0]
	if !strings.EqualFold(nick, "BanchoBot") {
		return
	}
	c.mu.Lock()
	index := -1
	acknowledged := false
	for i, pending := range c.pending {
		if pending.channel != observation.Channel {
			continue
		}
		matched, success := classifyBanchoReply(pending, observation.Command)
		if matched {
			index, acknowledged = i, success
		}
	}
	if index < 0 {
		c.mu.Unlock()
		return
	}
	pending := c.pending[index]
	c.pending = append(c.pending[:index], c.pending[index+1:]...)
	handler := c.receipt
	c.mu.Unlock()
	if handler != nil {
		handler(DeliveryReceipt{JobID: pending.jobID, LeaseToken: pending.token, Channel: pending.channel, Attempts: pending.attempts, Acknowledged: acknowledged, Message: strings.TrimPrefix(observation.Command, ":"), ReceivedAt: observation.Observed})
	}
}

func classifyBanchoReply(pending pendingDelivery, command string) (matched, success bool) {
	message := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(command), ":"))
	// A generic BanchoBot error has no request identity. Treating it as the
	// current job's rejection would let a delayed response for an older command
	// fail a newer command. Only accept a rejection when the target is echoed.
	if pending.target != "" && replyContainsTarget(message, pending) {
		for _, fragment := range []string{"error", "failed", "not found", "not allowed", "permission denied", "invalid", "denied"} {
			if strings.Contains(message, fragment) {
				return true, false
			}
		}
	}
	switch pending.kind {
	case "INVITE":
		invited, ok := invitedUsername(message)
		success := ok && normalizedBanchoUsername(invited) == normalizedBanchoUsername(pending.target)
		return success, success
	case "MAP", "TB_MAP":
		success := (strings.Contains(message, "beatmap changed") || strings.Contains(message, "changed beatmap")) && beatmapTarget(message) == pending.target
		return success, success
	default:
		return false, false
	}
}

func replyContainsTarget(message string, pending pendingDelivery) bool {
	if pending.kind == "INVITE" {
		return strings.Contains(normalizedBanchoUsername(message), normalizedBanchoUsername(pending.target))
	}
	return strings.Contains(message, strings.ToLower(pending.target))
}

func invitedUsername(message string) (string, bool) {
	const prefix = "invited "
	_, after, ok := strings.Cut(message, prefix)
	if !ok {
		return "", false
	}
	value := strings.TrimSpace(after)
	for _, suffix := range []string{" to the room", " into the room", " to this match", " into this match"} {
		if end := strings.Index(value, suffix); end >= 0 {
			value = strings.TrimSpace(value[:end])
			break
		}
	}
	value = strings.Trim(value, " .")
	return value, value != ""
}

func normalizedBanchoUsername(value string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.ToLower(value), "_", " ")), " ")
}

func commandTarget(kind string, payload []byte) (string, error) {
	parts := strings.Fields(string(payload))
	if len(parts) < 4 {
		return "", fmt.Errorf("IRC %s command has no target", kind)
	}
	for index, part := range parts {
		if strings.EqualFold(part, "map") && index+1 < len(parts) {
			return strings.TrimSpace(parts[index+1]), nil
		}
		if strings.EqualFold(part, "invite") && index+1 < len(parts) {
			return strings.TrimSpace(parts[index+1]), nil
		}
	}
	return "", fmt.Errorf("IRC %s command has no target", kind)
}

func beatmapTarget(message string) string {
	for part := range strings.FieldsSeq(message) {
		part = strings.Trim(part, "()[]<>,.")
		if index := strings.LastIndex(strings.ToLower(part), "/b/"); index >= 0 {
			value := part[index+3:]
			if value != "" {
				return value
			}
		}
	}
	return ""
}
