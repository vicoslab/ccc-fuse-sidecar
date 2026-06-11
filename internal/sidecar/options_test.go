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

func TestBuildMountPlanAllowsCommonLibfuseOptions(t *testing.T) {
	plan, err := BuildMountPlan(3, []string{
		"allow_root",
		"atomic_o_trunc",
		"direct_io",
		"hard_remove",
		"large_read",
		"no_remote_lock",
		"nonseekable",
		"readdir_ino",
		"use_ino",
		"max_background=12",
		"max_readahead=131072",
		"remember=60",
	}, 1000, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"allow_root", "direct_io", "max_background=12", "remember=60"} {
		if !strings.Contains(plan.Data, want) {
			t.Fatalf("data %q missing %q", plan.Data, want)
		}
	}
}

func TestBuildMountPlanAllowsUnknownSafeFuseDataOptions(t *testing.T) {
	plan, err := BuildMountPlan(1, []string{"new_kernel_option", "new_kernel_value=42"}, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"new_kernel_option", "new_kernel_value=42"} {
		if !strings.Contains(plan.Data, want) {
			t.Fatalf("data %q missing %q", plan.Data, want)
		}
	}
}

func TestBuildMountPlanRejectsUnsafeOption(t *testing.T) {
	if _, err := BuildMountPlan(1, []string{"dev"}, 1, 1); err == nil {
		t.Fatal("expected dev option to be rejected")
	}
	if _, err := BuildMountPlan(1, []string{"unsafe,value"}, 1, 1); err == nil {
		t.Fatal("expected unsafe option syntax to be rejected")
	}
}
