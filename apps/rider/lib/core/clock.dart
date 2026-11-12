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
