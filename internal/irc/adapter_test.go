package irc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingDeliveryGate struct{ released []Job }

func (g *recordingDeliveryGate) Acquire(context.Context, Job) error { return nil }
func (g *recordingDeliveryGate) Release(job Job)                    { g.released = append(g.released, job) }

func TestParseObservationKeepsRawEvidenceAndStableID(t *testing.T) {
	line := ":referee!u PRIVMSG #rct :!result RED 123"
	first, ok := parseObservation(line)
	if !ok || first.Channel != "#rct" || first.Sender != "referee!u" || first.Command != ":!result RED 123" {
		t.Fatalf("observation = %+v ok=%v", first, ok)
	}
	second, _ := parseObservation(line)
	if first.ID != second.ID || first.Raw != line {
		t.Fatalf("observation identity is not stable: %+v %+v", first, second)
	}
}

func TestObservationIDIncludesMatchChannel(t *testing.T) {
	first, ok := parseObservation(":ref!u PRIVMSG #mp_42 :!result BLUE piece-2")
	if !ok {
		t.Fatal("first observation was not parsed")
	}
	second, ok := parseObservation(":ref!u PRIVMSG #mp_43 :!result BLUE piece-2")
	if !ok {
		t.Fatal("second observation was not parsed")
	}
	if first.ID == second.ID {
		t.Fatal("identical messages in different match rooms collided")
	}
}

func TestReadClearsConnectedStatusAfterEOF(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewClient(nil, "test", "bot", "", "")
	client.conn = clientConn
	gate := &recordingDeliveryGate{}
	client.gate = gate
	client.pending = []pendingDelivery{{jobID: "job-1", token: "lease-1", channel: "#mp_42"}}
	done := make(chan error, 1)
	go func() { done <- client.Read(context.Background(), nil) }()
	_ = serverConn.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("read error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("read did not stop after EOF")
	}
	if client.Status().Connected {
		t.Fatal("client remained connected after EOF")
	}
	if len(gate.released) != 1 || gate.released[0].ID != "job-1" || gate.released[0].LeaseToken != "lease-1" {
		t.Fatalf("released deliveries=%+v", gate.released)
	}
}

func TestClientWriteTimesOutOnHalfOpenConnection(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	client := NewClient(nil, "test", "bot", "", "").WithWriteTimeout(30 * time.Millisecond)
	client.conn = clientConn
	client.joined["#mp_42"] = true
	started := time.Now()
	_, err := client.Send(context.Background(), Job{
		ID: "blocked-write", Kind: "MAP", Channel: "#mp_42",
		Payload: []byte("PRIVMSG #mp_42 :!mp map 123"),
	})
	if err == nil {
		t.Fatal("blocked IRC write did not time out")
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("blocked IRC write took %s", elapsed)
	}
}

func TestClientConnectHandshakeTimesOutOnHalfOpenConnection(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	client := NewClient(staticDialer{conn: clientConn}, "test", "bot", "secret", "#mp_42").WithWriteTimeout(30 * time.Millisecond)
	start := time.Now()
	err := client.Connect(context.Background())
	if err == nil {
		t.Fatal("blocked IRC handshake did not time out")
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("blocked IRC handshake took %s", elapsed)
	}
}

func TestClientRejectsCredentialLineInjectionBeforeDial(t *testing.T) {
	for _, test := range []struct {
		name string
		user string
		pass string
	}{
		{name: "username", user: "bot\r\nJOIN #other", pass: "secret"},
		{name: "password", user: "bot", pass: "secret\nJOIN #other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dialer := &recordingDialer{}
			client := NewClient(dialer, "irc.example.test:6667", test.user, test.pass, "#mp_42")
			if err := client.Connect(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid line break") {
				t.Fatalf("Connect error = %v, want invalid credential rejection", err)
			}
			if dialer.called {
				t.Fatal("client dialed the network before validating credentials")
			}
		})
	}
}

type recordingDialer struct{ called bool }

func (d *recordingDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	d.called = true
	return nil, errors.New("unexpected dial")
}

type staticDialer struct{ conn net.Conn }

func (d staticDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return d.conn, nil
}

func TestChannelFromMPLinkRequiresOfficialPositiveRoomID(t *testing.T) {
	channel, err := ChannelFromMPLink("https://osu.ppy.sh/community/matches/42")
	if err != nil || channel != "#mp_42" {
		t.Fatalf("channel=%q err=%v", channel, err)
	}
	for _, link := range []string{
		"http://osu.ppy.sh/community/matches/42", "https://example.test/community/matches/42",
		"https://osu.ppy.sh/community/matches/0", "https://osu.ppy.sh/community/matches/not-a-number",
		"https://osu.ppy.sh/community/matches/42?wrong=1", "42",
	} {
		if _, err := ChannelFromMPLink(link); err == nil {
			t.Fatalf("link %q was accepted", link)
		}
	}
}

type fakeJobs struct {
	job           Job
	claimed       bool
	markedSent    bool
	acked, failed bool
	rejected      bool
	failure       string
	markSentErr   error
}

func (f *fakeJobs) Claim(context.Context, time.Time, time.Time) (*Job, error) {
	if f.claimed {
		return nil, nil
	}
	f.claimed = true
	if f.job.Status == "" {
		f.job.Status = JobPending
	}
	if f.job.LeaseToken == "" {
		f.job.LeaseToken = "test-lease"
	}
	return &f.job, nil
}
func (f *fakeJobs) MarkSent(context.Context, string, string, time.Time, time.Time) error {
	f.markedSent = true
	return f.markSentErr
}
func (f *fakeJobs) Ack(context.Context, string, string, time.Time) error { f.acked = true; return nil }
func (f *fakeJobs) Fail(_ context.Context, _, _ string, message string, _ time.Time) error {
	f.failed = true
	f.failure = message
	return nil
}
func (f *fakeJobs) Reject(_ context.Context, _, _ string, message string) error {
	f.rejected = true
	f.failure = message
	return nil
}
func (f *fakeJobs) Cancel(_ context.Context, _, _ string, message string) error {
	f.failed = true
	f.failure = message
	return nil
}

type fakeSender struct{ err error }

func (f fakeSender) Send(context.Context, Job) (Delivery, error) {
	if f.err != nil {
		return Delivery{}, f.err
	}
	return Delivery{Status: JobAcknowledged}, nil
}

type SenderFunc func(context.Context, Job) (Delivery, error)

func (f SenderFunc) Send(ctx context.Context, job Job) (Delivery, error) { return f(ctx, job) }

type recordingSender struct {
	mu    sync.Mutex
	times []time.Time
}

func (s *recordingSender) Send(context.Context, Job) (Delivery, error) {
	s.mu.Lock()
	s.times = append(s.times, time.Now())
	s.mu.Unlock()
	return Delivery{Status: JobAcknowledged}, nil
}

func TestWorkerEnforcesRateLimit(t *testing.T) {
	store := newMemoryJobStore(Job{ID: "1", Status: JobPending}, Job{ID: "2", Status: JobPending})
	sender := &recordingSender{}
	worker := NewWorker(store, sender, 40*time.Millisecond)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.times) != 2 || sender.times[1].Sub(sender.times[0]) < 35*time.Millisecond {
		t.Fatalf("send times=%v, rate limit was not enforced", sender.times)
	}
}

func TestWorkerAcknowledgesAndRetriesFailures(t *testing.T) {
	store := &fakeJobs{job: Job{ID: "1"}}
	if err := NewWorker(store, fakeSender{}, time.Nanosecond).RunOnce(context.Background()); err != nil || !store.acked || !store.markedSent {
		t.Fatalf("ack err=%v acked=%v", err, store.acked)
	}
	store = &fakeJobs{job: Job{ID: "2"}}
	if err := NewWorker(store, fakeSender{err: errors.New("offline")}, time.Nanosecond).RunOnce(context.Background()); err != nil || !store.failed {
		t.Fatalf("fail err=%v failed=%v", err, store.failed)
	}
}

func TestWorkerParksJobAfterAutomaticRetryLimit(t *testing.T) {
	store := &fakeJobs{job: Job{ID: "offline", Attempts: maxAutomaticAttempts - 1}}
	if err := NewWorker(store, fakeSender{err: errors.New("offline")}, time.Nanosecond).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !store.rejected || store.failed {
		t.Fatalf("rejected=%v failed=%v, want a parked job", store.rejected, store.failed)
	}
}

type failingLimiter struct{ err error }

func (l failingLimiter) Wait(context.Context) error { return l.err }

func TestWorkerPersistsRateLimiterFailure(t *testing.T) {
	store := &fakeJobs{job: Job{ID: "rate-limited"}}
	want := errors.New("redis unavailable")
	err := NewWorker(store, fakeSender{}, time.Nanosecond).
		WithRateLimiter(failingLimiter{err: want}).
		RunOnce(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("RunOnce error = %v, want %v", err, want)
	}
	if !store.failed || store.failure != want.Error() {
		t.Fatalf("failed=%v failure=%q", store.failed, store.failure)
	}
}

func TestWorkerPersistsMarkSentFailure(t *testing.T) {
	want := errors.New("mongo unavailable")
	store := &fakeJobs{job: Job{ID: "mark-sent"}, markSentErr: want}
	err := NewWorker(store, fakeSender{}, time.Nanosecond).RunOnce(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("RunOnce error = %v, want %v", err, want)
	}
	if !store.failed || store.failure != want.Error() {
		t.Fatalf("failed=%v failure=%q", store.failed, store.failure)
	}
}

func TestWorkerCancelsJobWhenMatchMovedToAnotherRoom(t *testing.T) {
	store := &fakeJobs{job: Job{ID: "old-room", Channel: "#mp_42"}}
	sent := false
	worker := NewWorker(store, SenderFunc(func(context.Context, Job) (Delivery, error) {
		sent = true
		return Delivery{Status: JobAcknowledged}, nil
	}), time.Nanosecond).WithValidator(func(context.Context, Job) error {
		return fmt.Errorf("%w: channel changed", ErrJobObsolete)
	})
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sent || !store.failed || !strings.Contains(store.failure, "channel changed") {
		t.Fatalf("sent=%v cancelled=%v reason=%q", sent, store.failed, store.failure)
	}
}

func TestBanchoReplyConfirmsOnlyOutstandingDelivery(t *testing.T) {
	client := NewClient(nil, "", "", "", "")
	client.pending = []pendingDelivery{
		{jobID: "map-1", kind: "MAP", channel: "#mp_42", target: "123"},
	}
	receipts := make(chan DeliveryReceipt, 1)
	client.SetReceiptHandler(func(receipt DeliveryReceipt) { receipts <- receipt })
	observation, ok := parseObservation(":BanchoBot!bot PRIVMSG #mp_42 :Beatmap changed to: test https://osu.ppy.sh/b/123")
	if !ok {
		t.Fatal("parse acknowledgment")
	}
	client.handleReceipt(observation)
	select {
	case receipt := <-receipts:
		if receipt.JobID != "map-1" || !receipt.Acknowledged {
			t.Fatalf("receipt=%+v", receipt)
		}
	case <-time.After(time.Second):
		t.Fatal("outstanding response was not acknowledged")
	}
	if len(client.pending) != 0 {
		t.Fatalf("acknowledged response left a pending job: %+v", client.pending)
	}
}

func TestDeliveryReceiptCarriesLeaseToken(t *testing.T) {
	client := NewClient(nil, "", "", "", "")
	client.pending = []pendingDelivery{{jobID: "job-1", token: "lease-1", kind: "MAP", channel: "#mp_42", target: "123", attempts: 2}}
	var got DeliveryReceipt
	client.SetReceiptHandler(func(receipt DeliveryReceipt) { got = receipt })
	observation, ok := parseObservation(":BanchoBot!bot PRIVMSG #mp_42 :Beatmap changed to: test https://osu.ppy.sh/b/123")
	if !ok {
		t.Fatal("parse acknowledgment")
	}
	client.handleReceipt(observation)
	if got.JobID != "job-1" || got.LeaseToken != "lease-1" || got.Attempts != 2 || !got.Acknowledged {
		t.Fatalf("receipt=%+v", got)
	}
}

func TestBanchoReplyMustMatchOutstandingCommandTarget(t *testing.T) {
	client := NewClient(nil, "", "", "", "")
	client.pending = []pendingDelivery{{jobID: "map-456", token: "lease-new", kind: "MAP", channel: "#mp_42", target: "456"}}
	receipts := make(chan DeliveryReceipt, 1)
	client.SetReceiptHandler(func(receipt DeliveryReceipt) { receipts <- receipt })

	for _, line := range []string{
		":BanchoBot!bot PRIVMSG #mp_42 :Beatmap changed to: old https://osu.ppy.sh/b/123",
		":BanchoBot!bot PRIVMSG #mp_42 :Permission denied",
		":someone!user PRIVMSG #mp_42 :Beatmap changed to: new https://osu.ppy.sh/b/456",
	} {
		observation, ok := parseObservation(line)
		if !ok {
			t.Fatalf("parse %q", line)
		}
		client.handleReceipt(observation)
	}
	select {
	case receipt := <-receipts:
		t.Fatalf("unrelated reply produced receipt %+v", receipt)
	default:
	}
	if len(client.pending) != 1 || client.pending[0].jobID != "map-456" {
		t.Fatalf("pending delivery changed: %+v", client.pending)
	}

	observation, _ := parseObservation(":BanchoBot!bot PRIVMSG #mp_42 :Beatmap changed to: new https://osu.ppy.sh/b/456")
	client.handleReceipt(observation)
	select {
	case receipt := <-receipts:
		if receipt.JobID != "map-456" || !receipt.Acknowledged {
			t.Fatalf("receipt=%+v", receipt)
		}
	case <-time.After(time.Second):
		t.Fatal("matching reply was not acknowledged")
	}
}

func TestOutboundCommandTargetIsRequiredForCorrelation(t *testing.T) {
	for _, test := range []struct {
		kind, payload, want string
	}{
		{kind: "MAP", payload: "PRIVMSG #mp_42 :!mp map 123", want: "123"},
		{kind: "TB_MAP", payload: "PRIVMSG #mp_42 :!mp map 999", want: "999"},
		{kind: "INVITE", payload: "PRIVMSG #mp_42 :!mp invite #42", want: "#42"},
	} {
		got, err := commandTarget(test.kind, []byte(test.payload))
		if err != nil || got != test.want {
			t.Fatalf("kind=%s target=%q err=%v", test.kind, got, err)
		}
	}
	if _, err := commandTarget("MAP", []byte("PRIVMSG #mp_42 :!mp map")); err == nil {
		t.Fatal("targetless map command was accepted")
	}
}

func TestInviteReceiptUsesExpectedUsernameInsteadOfNumericCommandTarget(t *testing.T) {
	client := NewClient(nil, "", "", "", "")
	client.pending = []pendingDelivery{{
		jobID: "invite-42", kind: "INVITE", channel: "#mp_42", target: "Player Name",
	}}
	receipts := make(chan DeliveryReceipt, 1)
	client.SetReceiptHandler(func(receipt DeliveryReceipt) { receipts <- receipt })

	old, _ := parseObservation(":BanchoBot!bot PRIVMSG #mp_42 :Invited Other_Player to the room.")
	client.handleReceipt(old)
	select {
	case receipt := <-receipts:
		t.Fatalf("unrelated invite reply produced receipt %+v", receipt)
	default:
	}

	matching, _ := parseObservation(":BanchoBot!bot PRIVMSG #mp_42 :Invited Player_Name to the room.")
	client.handleReceipt(matching)
	select {
	case receipt := <-receipts:
		if receipt.JobID != "invite-42" || !receipt.Acknowledged {
			t.Fatalf("receipt=%+v", receipt)
		}
	case <-time.After(time.Second):
		t.Fatal("username invite reply was not acknowledged")
	}
}

func TestSendRejectsConcurrentDeliveryInOneChannel(t *testing.T) {
	client := NewClient(nil, "", "", "", "")
	client.pending = []pendingDelivery{
		{jobID: "map-1", kind: "MAP", channel: "#mp_42"},
	}
	client.channel = "#mp_42"
	client.joined["#mp_42"] = true
	if _, err := client.Send(context.Background(), Job{ID: "map-2", Kind: "MAP", Channel: "#mp_42", Payload: []byte("PRIVMSG #mp_42 :!mp map 456")}); !errors.Is(err, ErrChannelBusy) {
		t.Fatalf("Send error = %v, want ErrChannelBusy", err)
	}
}
