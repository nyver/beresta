import "package:flutter/material.dart";

import "app.dart";
import "error_reporting.dart";

export "app.dart";
export "core_gateway.dart";

void main() {
  configureErrorReporting();
  runApp(const BerestaApp());
}
