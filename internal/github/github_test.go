package github

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestFindOpen(t *testing.T) {
	c := Client{run: func(args ...string) ([]byte, error) {
		return []byte(`[{"number":12,"url":"https://example/pr/12","title":"title","body":"body","state":"OPEN"}]`), nil
	}}
	pr, err := c.FindOpen("feature")
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 12 || pr.Body != "body" {
		t.Fatalf("unexpected PR: %#v", pr)
	}
}

func TestFindOpenDistinguishesMissingAndFailure(t *testing.T) {
	missing := Client{run: func(args ...string) ([]byte, error) { return []byte("[]"), nil }}
	if _, err := missing.FindOpen("feature"); !errors.Is(err, ErrPRNotFound) {
		t.Fatalf("expected ErrPRNotFound, got %v", err)
	}

	failed := Client{run: func(args ...string) ([]byte, error) { return []byte("network down"), errors.New("exit 1") }}
	if _, err := failed.FindOpen("feature"); err == nil || errors.Is(err, ErrPRNotFound) || !strings.Contains(err.Error(), "network down") {
		t.Fatalf("expected operational error, got %v", err)
	}
}

func TestUpdateArgumentsAndError(t *testing.T) {
	var got []string
	c := Client{run: func(args ...string) ([]byte, error) {
		got = append([]string(nil), args...)
		return nil, nil
	}}
	if err := c.Update("feature", "title", "/tmp/body"); err != nil {
		t.Fatal(err)
	}
	want := []string{"pr", "edit", "feature", "--title", "title", "--body-file", "/tmp/body"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%v", got)
	}

	failed := Client{run: func(args ...string) ([]byte, error) { return []byte("denied"), errors.New("exit 1") }}
	if err := failed.Update("feature", "title", "/tmp/body"); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected contextual update error, got %v", err)
	}
}

func TestCreateArguments(t *testing.T) {
	var got []string
	c := Client{run: func(args ...string) ([]byte, error) {
		got = append([]string(nil), args...)
		return []byte("https://example/pr/1\n"), nil
	}}
	url, err := c.Create("main", "feature", "title", "/tmp/body", true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pr", "create", "--base", "main", "--head", "feature", "--title", "title", "--body-file", "/tmp/body", "--draft"}
	if !reflect.DeepEqual(got, want) || url != "https://example/pr/1" {
		t.Fatalf("args=%v url=%q", got, url)
	}
}
