package sidecar

import (
	"strings"
	"syscall"
	"testing"
)

func TestBuildMountPlanFiltersAndOverrides(t *testing.T) {
	plan, err := BuildMountPlan(7, []string{
		"fsname=myfs",
		"allow_other",
		"ro",
		"nodev",
		"nosuid",
		"user_id=1234",
		"group_id=1234",
		"fd=99",
		"subtype=demo",
	}, 1000, 1001)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Source != "myfs" {
		t.Fatalf("source = %q, want myfs", plan.Source)
	}
	if plan.FSType != "fuse" {
		t.Fatalf("fstype = %q, want fuse", plan.FSType)
	}
	if plan.Flags&syscall.MS_RDONLY == 0 {
		t.Fatal("expected MS_RDONLY")
	}
	for _, want := range []string{"fd=7", "user_id=1000", "group_id=1001", "rootmode=40000", "allow_other", "subtype=demo"} {
		if !strings.Contains(plan.Data, want) {
			t.Fatalf("data %q missing %q", plan.Data, want)
		}
	}
	if strings.Contains(plan.Data, "fd=99") || strings.Contains(plan.Data, "user_id=1234") {
		t.Fatalf("client-controlled privileged options leaked into data: %q", plan.Data)
	}
}

func TestBuildMountPlanRejectsUnsafeOption(t *testing.T) {
	if _, err := BuildMountPlan(1, []string{"dev"}, 1, 1); err == nil {
		t.Fatal("expected dev option to be rejected")
	}
	if _, err := BuildMountPlan(1, []string{"unknown_option"}, 1, 1); err == nil {
		t.Fatal("expected unknown option to be rejected")
	}
}
