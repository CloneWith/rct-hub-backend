package irc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClientAgainstFakeIRCServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverLines := make(chan string, 8)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		for i := 0; i < 4 && scanner.Scan(); i++ {
			serverLines <- scanner.Text()
		}
		_, _ = conn.Write([]byte("PING :bancho\r\n:ref!u PRIVMSG #mp_42 :!result RED piece-1\r\n"))
		if scanner.Scan() {
			serverLines <- scanner.Text()
		}
	}()
	client := NewClient(&net.Dialer{}, listener.Addr().String(), "bot", "secret", "#mp_42")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	observed := make(chan Observation, 1)
	go func() { _ = client.Read(ctx, func(o Observation) { observed <- o }) }()
	for _, want := range []string{"PASS secret", "NICK bot", "USER bot 0 * :bot", "JOIN #mp_42"} {
		select {
		case got := <-serverLines:
			if got != want {
				t.Fatalf("line=%q want=%q", got, want)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	select {
	case got := <-serverLines:
		if !strings.HasPrefix(got, "PONG") {
			t.Fatalf("line=%q", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case got := <-observed:
		if got.Command != ":!result RED piece-1" || got.ID == "" || got.SuggestedResult == nil || got.SuggestedResult.WinningTeam != "RED" {
			t.Fatalf("observation=%+v", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestClientReconnectsAndDeduplicatesRepeatedEvidence(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connections := make(chan struct{}, 2)
	line := ":ref!u PRIVMSG #mp_42 :!result BLUE piece-2\r\n"
	go func() {
		first, err := listener.Accept()
		if err != nil {
			return
		}
		connections <- struct{}{}
		readHandshake(first, 3)
		_ = first.Close()

		second, err := listener.Accept()
		if err != nil {
			return
		}
		defer second.Close()
		connections <- struct{}{}
		readHandshake(second, 3)
		_, _ = second.Write([]byte(line + line))
	}()

	client := NewClient(&net.Dialer{}, listener.Addr().String(), "bot", "", "#mp_42")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var mu sync.Mutex
	unique := map[string]Observation{}
	seen := make(chan struct{}, 2)
	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx, func(observation Observation) {
			mu.Lock()
			unique[observation.ID] = observation
			mu.Unlock()
			seen <- struct{}{}
		})
	}()
	for range 2 {
		select {
		case <-connections:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	for range 2 {
		select {
		case <-seen:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	mu.Lock()
	count := len(unique)
	mu.Unlock()
	if count != 1 {
		t.Fatalf("unique observations=%d, want 1", count)
	}
	cancel()
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("IRC client did not stop after cancellation")
	}
}

func TestAckLossTimesOutAndServiceRestartRecoversJob(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	firstCommand := make(chan string, 1)
	secondCommand := make(chan string, 1)
	go func() {
		first, err := listener.Accept()
		if err != nil {
			return
		}
		firstReader := bufio.NewReader(first)
		readHandshakeReader(firstReader, 3)
		firstCommand <- readReaderLine(firstReader)
		_ = first.Close() // The command was read, but its acknowledgement is lost.

		second, err := listener.Accept()
		if err != nil {
			return
		}
		defer second.Close()
		secondReader := bufio.NewReader(second)
		readHandshakeReader(secondReader, 3)
		secondCommand <- readReaderLine(secondReader)
		_, _ = second.Write([]byte(":BanchoBot!bot PRIVMSG #mp_42 :Beatmap changed to: test https://osu.ppy.sh/b/123\r\n"))
	}()

	store := newMemoryJobStore(Job{
		ID: "event-map", MatchID: "507f1f77bcf86cd799439011", Channel: "#mp_42", Kind: "MAP",
		Payload: []byte("PRIVMSG #mp_42 :!mp map 123"), Status: JobPending,
	})
	client1 := NewClient(&net.Dialer{}, listener.Addr().String(), "bot", "", "#mp_42")
	ctx1, cancel1 := context.WithCancel(context.Background())
	go func() { _ = client1.Run(ctx1, nil) }()
	waitConnected(t, client1)
	worker1 := NewWorker(store, client1, time.Millisecond).WithTimeouts(30*time.Millisecond, time.Second)
	if err := worker1.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-firstCommand:
		if got != "PRIVMSG #mp_42 :!mp map 123" {
			t.Fatalf("first command=%q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("first service did not send the job")
	}
	cancel1()
	_ = client1.Close()
	time.Sleep(40 * time.Millisecond)

	// A new worker sees the same durable SENT job. It first records the lost
	// acknowledgement as a retryable failure, then retries once it is due.
	client2 := NewClient(&net.Dialer{}, listener.Addr().String(), "bot", "", "#mp_42")
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	client2.SetReceiptHandler(func(receipt DeliveryReceipt) {
		if receipt.Acknowledged {
			_ = store.Ack(context.Background(), receipt.JobID, receipt.LeaseToken, receipt.ReceivedAt)
		}
	})
	go func() { _ = client2.Run(ctx2, nil) }()
	waitConnected(t, client2)
	worker2 := NewWorker(store, client2, time.Millisecond).WithTimeouts(30*time.Millisecond, time.Second)
	if err := worker2.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := store.snapshot(); got.Status != JobFailed || !strings.Contains(got.LastError, "timed out") {
		t.Fatalf("after ack loss job=%+v", got)
	}
	store.makeDue()
	if err := worker2.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-secondCommand:
		if got != "PRIVMSG #mp_42 :!mp map 123" {
			t.Fatalf("retried command=%q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("restarted service did not retry the job")
	}
	waitUntil(t, time.Second, func() bool { return store.snapshot().Status == JobAcknowledged })
}

type memoryJobStore struct {
	mu   sync.Mutex
	jobs []Job
}

func newMemoryJobStore(jobs ...Job) *memoryJobStore {
	return &memoryJobStore{jobs: append([]Job(nil), jobs...)}
}

func (s *memoryJobStore) Claim(_ context.Context, now, leaseUntil time.Time) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.jobs {
		job := &s.jobs[index]
		eligible := (job.Status == JobPending || job.Status == JobFailed) && !job.NextTryAt.After(now)
		eligible = eligible || job.Status == JobSending && !job.NextTryAt.After(now)
		eligible = eligible || job.Status == JobSent && !job.AckDeadline.After(now)
		if !eligible {
			continue
		}
		before := *job
		job.Status = JobSending
		job.LeaseToken = fmt.Sprintf("lease-%d", time.Now().UnixNano())
		job.NextTryAt = leaseUntil
		before.LeaseToken = job.LeaseToken
		return &before, nil
	}
	return nil, nil
}

func (s *memoryJobStore) MarkSent(_ context.Context, id, token string, sentAt, deadline time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.byID(id)
	if job.LeaseToken != token {
		return errors.New("lease lost")
	}
	job.Status, job.SentAt, job.AckDeadline = JobSent, sentAt, deadline
	return nil
}

func (s *memoryJobStore) Ack(_ context.Context, id, token string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.byID(id)
	if job.LeaseToken != token {
		return errors.New("lease lost")
	}
	job.Status, job.AcknowledgedAt, job.LastError = JobAcknowledged, at, ""
	return nil
}

func (s *memoryJobStore) Fail(_ context.Context, id, token, message string, retryAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.byID(id)
	if job.LeaseToken != token {
		return errors.New("lease lost")
	}
	job.Status, job.LastError, job.NextTryAt = JobFailed, message, retryAt
	job.Attempts++
	return nil
}

func (s *memoryJobStore) Reject(_ context.Context, id, token, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.byID(id)
	if job.LeaseToken != token {
		return errors.New("lease lost")
	}
	job.Status, job.LastError, job.LeaseToken = JobFailed, message, ""
	job.Attempts++
	return nil
}

func (s *memoryJobStore) Cancel(_ context.Context, id, token, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.byID(id)
	if job.LeaseToken != token {
		return errors.New("lease lost")
	}
	job.Status, job.LastError, job.LeaseToken = JobCancelled, message, ""
	return nil
}

func (s *memoryJobStore) byID(id string) *Job {
	for index := range s.jobs {
		if s.jobs[index].ID == id {
			return &s.jobs[index]
		}
	}
	panic("unknown test job " + id)
}

func (s *memoryJobStore) snapshot() Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs[0]
}

func (s *memoryJobStore) makeDue() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[0].NextTryAt = time.Now().Add(-time.Second)
}

func readHandshake(conn net.Conn, lines int) {
	scanner := bufio.NewScanner(conn)
	for range lines {
		if !scanner.Scan() {
			return
		}
	}
}

func readHandshakeReader(reader *bufio.Reader, lines int) {
	for range lines {
		if _, err := reader.ReadString('\n'); err != nil {
			return
		}
	}
}

func readReaderLine(reader *bufio.Reader) string {
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line)
}

func waitConnected(t *testing.T, client *Client) {
	t.Helper()
	waitUntil(t, time.Second, func() bool { return client.Status().Connected })
}

func waitUntil(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
