import "package:beresta/main.dart";
import "package:flutter_test/flutter_test.dart";

void main() {
  testWidgets("renders the offline-first application shell", (
    WidgetTester tester,
  ) async {
    await tester.pumpWidget(const BerestaApp());

    expect(find.textContaining("encrypted notes"), findsOneWidget);
  });
}
