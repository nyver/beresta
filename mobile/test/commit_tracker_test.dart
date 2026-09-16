import "package:beresta/commit_tracker.dart";
import "package:flutter_test/flutter_test.dart";

void main() {
  group("CommitTracker", () {
    test("issues strictly increasing generations", () {
      final tracker = CommitTracker();
      var previous = 0;
      for (var i = 0; i < 5; i++) {
        final current = tracker.dirty();
        expect(current, greaterThan(previous));
        previous = current;
      }
    });

    test("current() does not itself mark the session dirty", () {
      final tracker = CommitTracker();
      final generation = tracker.dirty();
      expect(tracker.current(), generation);
      expect(tracker.current(), generation);
    });

    test("accepts a success for the latest generation as saved", () {
      final tracker = CommitTracker();
      final generation = tracker.dirty();
      expect(tracker.accept(generation, true), LocalSaveState.saved);
    });

    test("accepts a failure for the latest generation as couldNotSave", () {
      final tracker = CommitTracker();
      final generation = tracker.dirty();
      expect(tracker.accept(generation, false), LocalSaveState.couldNotSave);
    });

    // The scenario design.md decision 2 requires: a slow commit for an
    // older generation must not report saved once newer, still-unsaved
    // input has arrived, even though that older commit itself succeeded.
    test(
      "discards a stale success instead of marking newer dirty content as saved",
      () {
        final tracker = CommitTracker();
        final stale = tracker.dirty();
        tracker.dirty(); // newer input arrives before the stale commit resolves

        expect(tracker.accept(stale, true), isNull);
      },
    );

    test(
      "discards a stale failure instead of flagging newer dirty content as failed",
      () {
        final tracker = CommitTracker();
        final stale = tracker.dirty();
        tracker.dirty();

        expect(tracker.accept(stale, false), isNull);
      },
    );

    test(
      "still reports the latest generation correctly after a stale completion was discarded",
      () {
        final tracker = CommitTracker();
        final stale = tracker.dirty();
        final latest = tracker.dirty();

        expect(tracker.accept(stale, true), isNull);
        expect(tracker.accept(latest, true), LocalSaveState.saved);
      },
    );

    test("re-affirms saved on a duplicate success for the same generation", () {
      final tracker = CommitTracker();
      final generation = tracker.dirty();

      expect(tracker.accept(generation, true), LocalSaveState.saved);
      expect(tracker.accept(generation, true), LocalSaveState.saved);
    });

    test(
      "does not let a failure regress a generation an earlier attempt already saved",
      () {
        final tracker = CommitTracker();
        final generation = tracker.dirty();

        expect(tracker.accept(generation, true), LocalSaveState.saved);
        expect(tracker.accept(generation, false), isNull);
      },
    );

    test(
      "accepts a retry's success for the same generation after an earlier failure",
      () {
        // The common retry path: a commit fails, and since nothing new was
        // typed in between, the retry captures the exact same generation
        // as the failed attempt.
        final tracker = CommitTracker();
        final generation = tracker.dirty();

        expect(tracker.accept(generation, false), LocalSaveState.couldNotSave);
        expect(tracker.accept(generation, true), LocalSaveState.saved);
      },
    );

    test(
      "discards a late out-of-order completion of an earlier in-flight commit",
      () {
        // Two commits can be in flight if a debounce commits generation 1,
        // more input arrives (generation 2), and generation 2's commit
        // completes before generation 1's slower one settles.
        final tracker = CommitTracker();
        final first = tracker.dirty();
        final second = tracker.dirty();

        expect(tracker.accept(second, true), LocalSaveState.saved);
        expect(tracker.accept(first, true), isNull);
      },
    );

    // A real bug found while wiring this into the editor commit
    // controller: a commit with no prior dirty() call targets generation
    // 0, a legitimate first attempt, not a stale or already-resolved
    // duplicate.
    test(
      "does not discard a failure at generation 0 with no prior dirty() call",
      () {
        final tracker = CommitTracker();
        expect(tracker.accept(0, false), LocalSaveState.couldNotSave);
      },
    );

    test(
      "does not discard a success at generation 0 with no prior dirty() call",
      () {
        final tracker = CommitTracker();
        expect(tracker.accept(0, true), LocalSaveState.saved);
      },
    );
  });
}
