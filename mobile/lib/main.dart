import "package:flutter/material.dart";

void main() {
  runApp(const BerestaApp());
}

class BerestaApp extends StatelessWidget {
  const BerestaApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: "Beresta",
      theme: ThemeData(colorSchemeSeed: const Color(0xFF754C29)),
      home: const Scaffold(
        body: Center(
          child: Text("Beresta — encrypted notes, available offline."),
        ),
      ),
    );
  }
}
