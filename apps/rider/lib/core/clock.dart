abstract class Clock {
  const Clock();
  DateTime now();
}

class SystemClock extends Clock {
  const SystemClock();
  @override
  DateTime now() => DateTime.now();
}

class FixedClock extends Clock {
  const FixedClock(this.value);
  final DateTime value;
  @override
  DateTime now() => value;
}

/// A clock tests move by hand, e.g. 15 minutes on so a round of offers
/// closes in the demo backend.
class ManualClock extends Clock {
  ManualClock(this.value);
  DateTime value;

  @override
  DateTime now() => value;

  void advance(Duration by) => value = value.add(by);
}
