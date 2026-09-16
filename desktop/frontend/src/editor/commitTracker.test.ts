import { describe, expect, it } from "vitest";

import { CommitTracker } from "./commitTracker";

describe("CommitTracker", () => {
  it("issues strictly increasing generations", () => {
    const tracker = new CommitTracker();
    let previous = 0;
    for (let i = 0; i < 5; i++) {
      const current = tracker.dirty();
      expect(current).toBeGreaterThan(previous);
      previous = current;
    }
  });

  // A real bug found while wiring this into the editor commit controller:
  // a commit with no prior dirty() call (a title-only rename before any
  // body edit) targets generation 0, a legitimate first attempt, not a
  // stale or already-resolved duplicate.
  it("does not discard a failure at generation 0 with no prior dirty() call", () => {
    const tracker = new CommitTracker();
    expect(tracker.accept(0, false)).toBe("could_not_save");
  });

  it("does not discard a success at generation 0 with no prior dirty() call", () => {
    const tracker = new CommitTracker();
    expect(tracker.accept(0, true)).toBe("saved");
  });

  it("current() does not itself mark the session dirty", () => {
    const tracker = new CommitTracker();
    const generation = tracker.dirty();
    expect(tracker.current()).toBe(generation);
    expect(tracker.current()).toBe(generation);
  });

  it("accepts a success for the latest generation as saved", () => {
    const tracker = new CommitTracker();
    const generation = tracker.dirty();
    expect(tracker.accept(generation, true)).toBe("saved");
  });

  it("accepts a failure for the latest generation as could_not_save", () => {
    const tracker = new CommitTracker();
    const generation = tracker.dirty();
    expect(tracker.accept(generation, false)).toBe("could_not_save");
  });

  // The scenario design.md decision 2 requires: a slow commit for an
  // older generation must not report "saved" once newer, still-unsaved
  // input has arrived, even though that older commit itself succeeded.
  it("discards a stale success instead of marking newer dirty content as saved", () => {
    const tracker = new CommitTracker();
    const stale = tracker.dirty();
    tracker.dirty(); // newer input arrives before the stale commit resolves

    expect(tracker.accept(stale, true)).toBeNull();
  });

  it("discards a stale failure instead of flagging newer dirty content as failed", () => {
    const tracker = new CommitTracker();
    const stale = tracker.dirty();
    tracker.dirty();

    expect(tracker.accept(stale, false)).toBeNull();
  });

  it("still reports the latest generation correctly after a stale completion was discarded", () => {
    const tracker = new CommitTracker();
    const stale = tracker.dirty();
    const latest = tracker.dirty();

    expect(tracker.accept(stale, true)).toBeNull();
    expect(tracker.accept(latest, true)).toBe("saved");
  });

  it("re-affirms saved on a duplicate success for the same generation", () => {
    const tracker = new CommitTracker();
    const generation = tracker.dirty();

    expect(tracker.accept(generation, true)).toBe("saved");
    expect(tracker.accept(generation, true)).toBe("saved");
  });

  it("does not let a failure regress a generation an earlier attempt already saved", () => {
    const tracker = new CommitTracker();
    const generation = tracker.dirty();

    expect(tracker.accept(generation, true)).toBe("saved");
    expect(tracker.accept(generation, false)).toBeNull();
  });

  it("accepts a retry's success for the same generation after an earlier failure", () => {
    // The common retry path: a commit fails, its payload is requeued for
    // the next attempt, and since nothing new was typed in between, the
    // retry captures the exact same generation as the failed attempt.
    const tracker = new CommitTracker();
    const generation = tracker.dirty();

    expect(tracker.accept(generation, false)).toBe("could_not_save");
    expect(tracker.accept(generation, true)).toBe("saved");
  });

  it("discards a late out-of-order completion of an earlier in-flight commit", () => {
    // Two commits can be in flight if a debounce commits generation 1,
    // more input arrives (generation 2), and generation 2's commit
    // completes before generation 1's slower one settles.
    const tracker = new CommitTracker();
    const first = tracker.dirty();
    const second = tracker.dirty();

    expect(tracker.accept(second, true)).toBe("saved");
    expect(tracker.accept(first, true)).toBeNull();
  });
});
