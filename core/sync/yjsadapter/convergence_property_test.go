package yjsadapter

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestReplicaUpdatesConvergeAcrossPermutationDuplicationSnapshotAndReplay(t *testing.T) {
	left := New()
	defer left.Close()
	right := New()
	defer right.Close()
	if err := left.Insert("body", 0, "left paragraph\n", nil); err != nil {
		t.Fatal(err)
	}
	if err := right.Insert("body", 0, "right paragraph\n", nil); err != nil {
		t.Fatal(err)
	}
	leftUpdate, err := left.EncodeStateAsUpdate(FormatV2)
	if err != nil {
		t.Fatal(err)
	}
	rightUpdate, err := right.EncodeStateAsUpdate(FormatV2)
	if err != nil {
		t.Fatal(err)
	}
	updates := [][]byte{leftUpdate, rightUpdate, leftUpdate, rightUpdate}
	rng := rand.New(rand.NewSource(7))
	var canonical []byte
	for iteration := 0; iteration < 64; iteration++ {
		order := rng.Perm(len(updates))
		replica := New()
		for _, index := range order {
			if err := replica.ApplyUpdate(FormatV2, updates[index]); err != nil {
				replica.Close()
				t.Fatal(err)
			}
		}
		snapshot, err := replica.EncodeStateAsUpdate(FormatV2)
		replica.Close()
		if err != nil {
			t.Fatal(err)
		}
		replayed, err := Restore(FormatV2, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if err := replayed.ApplyUpdate(FormatV2, leftUpdate); err != nil {
			replayed.Close()
			t.Fatal(err)
		}
		state, err := replayed.EncodeStateAsUpdate(FormatV2)
		replayed.Close()
		if err != nil {
			t.Fatal(err)
		}
		if canonical == nil {
			canonical = state
		} else if !bytes.Equal(canonical, state) {
			t.Fatalf("replica %d did not converge", iteration)
		}
	}
}
