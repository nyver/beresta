import "package:flutter/material.dart";

/// A two-way EN/RU language toggle, shared by onboarding and the notes
/// shell's navigation drawer.
class LanguageControl extends StatelessWidget {
  const LanguageControl({
    required this.language,
    required this.onChanged,
    super.key,
  });

  final String language;
  final ValueChanged<String> onChanged;

  @override
  Widget build(BuildContext context) {
    return Align(
      child: SegmentedButton<String>(
        segments: const [
          ButtonSegment(value: "en", label: Text("EN")),
          ButtonSegment(value: "ru", label: Text("RU")),
        ],
        selected: {language},
        onSelectionChanged: (value) => onChanged(value.first),
      ),
    );
  }
}
