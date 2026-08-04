package main

import (
	"bufio"
	"strings"
	"testing"

	"github.com/jkthorne/saltare/cli/internal/api"
)

var pickChoices = []api.WorkspaceChoice{
	{ID: 1, Slug: "acme-corp", Name: "Acme Corp"},
	{ID: 2, Slug: "beta-co", Name: "Beta Co"},
}

func pick(t *testing.T, input, defaultSlug string) (string, string, error) {
	t.Helper()
	var out strings.Builder
	slug, err := pickWorkspace(bufio.NewReader(strings.NewReader(input)), &out, pickChoices, defaultSlug)
	return slug, out.String(), err
}

func TestPickWorkspaceByNumber(t *testing.T) {
	slug, out, err := pick(t, "2\n", "")
	if err != nil || slug != "beta-co" {
		t.Fatalf("slug=%q err=%v", slug, err)
	}
	if !strings.Contains(out, "1. Acme Corp (acme-corp)") || !strings.Contains(out, "2. Beta Co (beta-co)") {
		t.Fatalf("choices not listed:\n%s", out)
	}
}

func TestPickWorkspaceBySlugCaseInsensitive(t *testing.T) {
	slug, _, err := pick(t, "Beta-Co\n", "")
	if err != nil || slug != "beta-co" {
		t.Fatalf("slug=%q err=%v", slug, err)
	}
}

func TestPickWorkspaceRepromptsOnGarbage(t *testing.T) {
	slug, out, err := pick(t, "7\nnope\n1\n", "")
	if err != nil || slug != "acme-corp" {
		t.Fatalf("slug=%q err=%v", slug, err)
	}
	if !strings.Contains(out, "between 1 and 2") || !strings.Contains(out, `"nope" is not`) {
		t.Fatalf("expected reprompt guidance:\n%s", out)
	}
}

func TestPickWorkspaceDefaultOnEnter(t *testing.T) {
	slug, out, err := pick(t, "\n", "beta-co")
	if err != nil || slug != "beta-co" {
		t.Fatalf("slug=%q err=%v", slug, err)
	}
	if !strings.Contains(out, "[beta-co]") {
		t.Fatalf("default not offered:\n%s", out)
	}
}

func TestPickWorkspaceEmptyEnterWithoutDefaultReprompts(t *testing.T) {
	slug, _, err := pick(t, "\n\n2\n", "")
	if err != nil || slug != "beta-co" {
		t.Fatalf("blank lines should reprompt, then accept: slug=%q err=%v", slug, err)
	}
}

func TestPickWorkspaceEOF(t *testing.T) {
	_, _, err := pick(t, "garbage", "") // no newline, then EOF
	if err == nil {
		t.Fatal("EOF must surface an error, not loop")
	}
}
