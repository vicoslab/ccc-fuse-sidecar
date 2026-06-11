package main

import "testing"

func TestParseRequiredPrefixesDoesNotUseLegacyDefault(t *testing.T) {
	if _, err := parseRequiredPrefixes(nil, "", "allowed host prefixes"); err == nil {
		t.Fatal("expected missing prefixes to fail")
	}
	got, err := parseRequiredPrefixes([]string{"/storage"}, "", "allowed host prefixes")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "/storage" {
		t.Fatalf("prefixes = %#v", got)
	}
	if _, err := parseRequiredPrefixes([]string{"/"}, "", "allowed host prefixes"); err == nil {
		t.Fatal("expected root prefix to be rejected")
	}
}

func TestParseOptionalPrefixesDoesNotUseLegacyDefault(t *testing.T) {
	got, err := parseOptionalPrefixes(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("prefixes = %#v, want nil", got)
	}
	got, err = parseOptionalPrefixes([]string{"/storage"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "/storage" {
		t.Fatalf("prefixes = %#v", got)
	}
	if _, err := parseOptionalPrefixes([]string{"/"}, ""); err == nil {
		t.Fatal("expected root prefix to be rejected")
	}
}

func TestLabelListMap(t *testing.T) {
	var labels labelList
	if err := labels.Set("ccc.fuse=enabled"); err != nil {
		t.Fatal(err)
	}
	got, err := labels.Map()
	if err != nil {
		t.Fatal(err)
	}
	if got["ccc.fuse"] != "enabled" {
		t.Fatalf("labels = %#v", got)
	}
	if err := labels.Set("missing-equals"); err == nil {
		t.Fatal("expected missing equals to fail")
	}
	if err := labels.Set("=empty-key"); err == nil {
		t.Fatal("expected empty key to fail")
	}
	if err := labels.Set("a=b=c"); err == nil {
		t.Fatal("expected multiple equals to fail")
	}
}
