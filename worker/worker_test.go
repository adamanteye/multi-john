package worker

import (
	"reflect"
	"testing"
)

func TestParseFlagsKeepsCommaSeparatedOrder(t *testing.T) {
	got := parseFlags(" --format=crypt, --wordlist=/work/chinese-common.txt, --rules ")
	want := []string{"--format=crypt", "--wordlist=/work/chinese-common.txt", "--rules"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseFlags = %#v, want %#v", got, want)
	}
}

func TestWithoutNodeFlagRemovesNodeAndKeepsFork(t *testing.T) {
	got := withoutNodeFlag([]string{
		"--format=crypt",
		"--fork=99",
		"--node=9/9",
		"--rules",
		"--node",
	})
	want := []string{"--format=crypt", "--fork=99", "--rules"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("withoutNodeFlag = %#v, want %#v", got, want)
	}
}

func TestParseCPUSet(t *testing.T) {
	got := parseCPUSet("0-2,5,8-9")
	want := []int{0, 1, 2, 5, 8, 9}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseCPUSet = %#v, want %#v", got, want)
	}
}

func TestParseCPUQuota(t *testing.T) {
	tests := []struct {
		value string
		want  int
	}{
		{value: "max 100000", want: 0},
		{value: "25000 100000", want: 1},
		{value: "200000 100000", want: 2},
		{value: "250000 100000", want: 3},
	}
	for _, tt := range tests {
		if got := parseCPUQuota(tt.value); got != tt.want {
			t.Fatalf("parseCPUQuota(%q) = %d, want %d", tt.value, got, tt.want)
		}
	}
}
