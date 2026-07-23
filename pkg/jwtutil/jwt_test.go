package jwtutil

import (
	"testing"
	"time"
)

func TestSignerRoundTrip(t *testing.T) {
	signer := NewSigner("a-32-byte-secret-key-for-test-purpose", "rcthub-test")
	token, err := signer.Generate("user-123", 42, "tester", time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	claims, err := signer.Parse(token)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}

	if claims.UserID != "user-123" {
		t.Errorf("expected user_id user-123, got %s", claims.UserID)
	}
	if claims.OsuID != 42 {
		t.Errorf("expected osu_id 42, got %d", claims.OsuID)
	}
	if claims.Username != "tester" {
		t.Errorf("expected username tester, got %s", claims.Username)
	}
}

func TestSignerExpiredToken(t *testing.T) {
	signer := NewSigner("a-32-byte-secret-key-for-test-purpose", "rcthub-test")
	token, err := signer.Generate("user-123", 42, "tester", -time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	_, err = signer.Parse(token)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestSignerInvalidSignature(t *testing.T) {
	signer := NewSigner("a-32-byte-secret-key-for-test-purpose", "rcthub-test")
	token, err := signer.Generate("user-123", 42, "tester", time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	other := NewSigner("different-32-byte-secret-key-for-tests", "rcthub-test")
	_, err = other.Parse(token)
	if err == nil {
		t.Fatal("expected error for invalid signature")
	}
}
