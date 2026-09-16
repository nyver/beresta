/// The closed three-value note-persistence model from
/// specs/product-experience ("Note persistence SHALL be represented only
/// as `Saving`, `Saved`, or `Could not save`"), matching Go's
/// core/presentation.LocalSaveState and desktop's identically-named Dart
/// port's TypeScript counterpart one-for-one.
enum LocalSaveState { saving, saved, couldNotSave }

/// CommitTracker is a Dart port of core/editorcommit.Tracker (Go) and
/// desktop/frontend/src/editor/commitTracker.ts: input marks this editor
/// session dirty and advances a monotonic generation, and a commit
/// completion may only update the reported [LocalSaveState] when it is
/// for the latest generation. A completion for a generation superseded by
/// newer dirty input is discarded - whether it succeeded or failed - so a
/// slow, stale commit can never mark newer unsaved edits as saved, or
/// wrongly flag them as failed. See core/editorcommit/tracker.go and its
/// tests for the authoritative semantics this mirrors.
///
/// Unlike the Go original, this runs in one single-threaded Dart isolate
/// (no goroutines), so no locking is needed - but async completions can
/// still resolve out of order, which is exactly the race this guards
/// against.
class CommitTracker {
  int _latest = 0;
  // The highest generation accept() has confirmed saved, meaningful only
  // once _hasSaved is true (generation 0 is a legitimate commit target -
  // see current()'s doc comment - so it cannot double as its own
  // "nothing saved yet" sentinel). _saved only ever advances on a
  // success, never on a failure: a retry of the same (unchanged)
  // generation - the common case, since a retry resends identical
  // content and so never calls dirty() again - must still be able to
  // move that generation from couldNotSave to saved.
  int _saved = 0;
  bool _hasSaved = false;

  /// Marks the session dirty with new local input and returns the new
  /// current generation.
  int dirty() {
    _latest += 1;
    return _latest;
  }

  /// Returns the session's current generation without marking it dirty:
  /// the generation a commit starting right now would be committing. Is
  /// 0 when dirty() has never been called, which is still a legitimate
  /// generation to pass to accept(), not a sentinel to special-case.
  int current() => _latest;

  /// Reports the outcome of a commit attempt for [generation], which must
  /// be a value previously returned by dirty(). Returns the
  /// [LocalSaveState] the UI should now report, or null when the
  /// completion must be discarded: generation is stale (superseded by
  /// newer dirty input), or it is a failure for a generation an earlier,
  /// different commit attempt has already confirmed saved - so a late
  /// duplicate or superseded failure can never regress a confirmed save.
  /// The caller must then leave the currently displayed save state
  /// untouched.
  LocalSaveState? accept(int generation, bool succeeded) {
    if (generation < _latest) {
      return null;
    }
    if (!succeeded && _hasSaved && generation <= _saved) {
      return null;
    }
    if (succeeded) {
      _saved = generation;
      _hasSaved = true;
      return LocalSaveState.saved;
    }
    return LocalSaveState.couldNotSave;
  }
}
