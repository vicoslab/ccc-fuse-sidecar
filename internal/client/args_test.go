package client

import "testing"

func TestParseArgsMount(t *testing.T) {
	got, err := ParseArgs([]string{"-oallow_other", "-zq", "--", "/mnt/fuse"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Options != "allow_other" || !got.Lazy || !got.Quiet || got.Mountpoint != "/mnt/fuse" {
		t.Fatalf("unexpected args: %+v", got)
	}
}

func TestParseArgsUmountLazyAlias(t *testing.T) {
	got, err := ParseArgs([]string{"-l", "/mnt/fuse"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Lazy || got.Mountpoint != "/mnt/fuse" {
		t.Fatalf("unexpected args: %+v", got)
	}
}

func TestParseArgsRejectsUnknown(t *testing.T) {
	if _, err := ParseArgs([]string{"--definitely-unknown", "/mnt"}); err == nil {
		t.Fatal("expected unknown option error")
	}
}

func TestSplitMountOptions(t *testing.T) {
	got := SplitMountOptions(" rw, allow_other ,,fsname=test ")
	want := []string{"rw", "allow_other", "fsname=test"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
