package biographyvoice

import (
	"strings"
	"testing"
	"time"
)

func TestResumeTokenRoundTripRejectsWrongClientAndTampering(t *testing.T) {
	codec, err := newResumeTokenCodec("0123456789abcdef0123456789abcdef", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	codec.now = func() time.Time { return time.Unix(1000, 0) }
	token, err := codec.Encode("session-sensitive-id", "device-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(token, "session-sensitive-id") {
		t.Fatal("resume token exposed the TMA session ID")
	}
	claims, err := codec.Decode(token, "device-1", "")
	if err != nil || claims.TMASessionID != "session-sensitive-id" {
		t.Fatalf("unexpected decoded claims: %+v err=%v", claims, err)
	}
	if _, err := codec.Decode(token, "device-2", ""); err == nil {
		t.Fatal("expected token to be bound to the client instance")
	}
	replacement := "A"
	if strings.HasSuffix(token, replacement) {
		replacement = "B"
	}
	tampered := token[:len(token)-1] + replacement
	if _, err := codec.Decode(tampered, "device-1", ""); err == nil {
		t.Fatal("expected tampered token rejection")
	}
}

func TestResumeTokenExpires(t *testing.T) {
	codec, err := newResumeTokenCodec("0123456789abcdef0123456789abcdef", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	codec.now = func() time.Time { return now }
	token, err := codec.Encode("session-1", "device-1")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	if _, err := codec.Decode(token, "device-1", ""); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired token error, got %v", err)
	}
}

func TestResumeTokenRoundTripsProjectState(t *testing.T) {
	codec, err := newResumeTokenCodec("0123456789abcdef0123456789abcdef", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	project := sampleBiographyProject()
	project.OverallProgress = 68
	token, err := codec.EncodeState("session-1", "device-1", "user-1", &project)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := codec.Decode(token, "device-1", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if claims.Project == nil || claims.Project.OverallProgress != 68 {
		t.Fatalf("project state was not preserved: %+v", claims.Project)
	}
	if _, err := codec.Decode(token, "device-1", "user-2"); err == nil {
		t.Fatal("expected token to be bound to the authenticated user")
	}
}
