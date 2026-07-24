package jwtutil

import (
	"testing"
	"time"

	"rctHubBackend/internal/domain"
)

func TestSignerRoundTrip(t *testing.T) {
	signer := NewSigner("a-32-byte-secret-key-for-test-purpose", "rcthub-test")
	roles := []domain.UserRole{domain.RolePlayer, domain.RoleAdmin}
	token, err := signer.Generate("user-123", 42, "tester", roles, time.Hour)
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
	if len(claims.Roles) != 2 || claims.Roles[0] != domain.RolePlayer || claims.Roles[1] != domain.RoleAdmin {
		t.Errorf("expected roles [player admin], got %v", claims.Roles)
	}
}

func TestSignerExpiredToken(t *testing.T) {
	signer := NewSigner("a-32-byte-secret-key-for-test-purpose", "rcthub-test")
	token, err := signer.Generate("user-123", 42, "tester", nil, -time.Hour)
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
	token, err := signer.Generate("user-123", 42, "tester", nil, time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	other := NewSigner("different-32-byte-secret-key-for-tests", "rcthub-test")
	_, err = other.Parse(token)
	if err == nil {
		t.Fatal("expected error for invalid signature")
	}
}
