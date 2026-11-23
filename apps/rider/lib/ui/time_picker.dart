import 'package:flutter/material.dart';

import '../core/schedule.dart';
import 'tokens.dart';

/// A bottom sheet listing the day's times in 5-minute steps. Each option has
/// the key `time.HH:MM` (24 h); tapping one picks it and closes the sheet.
/// Returns minutes after midnight, or null when dismissed.
Future<int?> showFiveMinuteTimePicker(BuildContext context, {required int initial, required String title}) {
  return showModalBottomSheet<int>(
    context: context,
    isScrollControlled: true,
    backgroundColor: AppColors.surface,
    showDragHandle: true,
    builder: (context) => _TimeSheet(initial: initial - initial % 5, title: title),
  );
}

class _TimeSheet extends StatefulWidget {
  const _TimeSheet({required this.initial, required this.title});

  final int initial;
  final String title;

  @override
  State<_TimeSheet> createState() => _TimeSheetState();
}

class _TimeSheetState extends State<_TimeSheet> {
  static const _row = 48.0;
  late final ScrollController _scroll = ScrollController(
    // The current time a couple of rows from the top.
    initialScrollOffset: ((widget.initial ~/ 5) - 2).clamp(0, 288).toDouble() * _row,
  );

  @override
  void dispose() {
    _scroll.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final height = MediaQuery.sizeOf(context).height * 0.7;
    return SizedBox(
      height: height,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(20, 0, 20, 8),
            child: Text(widget.title, style: AppText.display(20)),
          ),
          Expanded(
            child: SingleChildScrollView(
              key: const Key('time.list'),
              controller: _scroll,
              child: Column(
                children: [
                  for (var m = 0; m < 1440; m += 5) _option(m),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _option(int m) {
    final selected = m == widget.initial;
    return Semantics(
      button: true,
      selected: selected,
      label: formatTime(m),
      excludeSemantics: true,
      child: InkWell(
        key: Key('time.${clockKey(m)}'),
        onTap: () => Navigator.of(context).pop(m),
        child: Container(
          height: _row,
          padding: const EdgeInsets.symmetric(horizontal: 20),
          color: selected ? AppColors.greenTint : null,
          alignment: Alignment.centerLeft,
          child: Text(
            formatTime(m),
            style: AppText.body(16, weight: selected ? FontWeight.w600 : FontWeight.w400, color: selected ? AppColors.green : AppColors.ink),
          ),
        ),
      ),
    );
  }
}
