import 'package:flutter/material.dart';

import '../../core/models.dart';
import '../../ui/tab_bar.dart';
import '../../ui/tokens.dart';
import '../requests/requests_screen.dart';
import '../week/week_screen.dart';

/// The signed-in app: the bottom bar (Today, Requests, Earnings, Account)
/// and the tab's screen. Each tab is read again whenever it opens.
///
/// Until the daily run (D3) exists, Today shows "Your week" (D2.3).
/// Earnings and Account come with their own flows.
class DriverShell extends StatefulWidget {
  const DriverShell({super.key, this.profile, this.initialTab = DriverTab.requests});

  final DriverProfile? profile;
  final DriverTab initialTab;

  @override
  State<DriverShell> createState() => _DriverShellState();
}

class _DriverShellState extends State<DriverShell> {
  late DriverTab _tab = widget.initialTab;

  /// Bumped on every tab tap, so the tab's screen is built afresh and reloads.
  int _opened = 0;

  /// The week Today shows; null: this week.
  DateTime? _weekStart;

  void _select(DriverTab tab) => setState(() {
        _tab = tab;
        _weekStart = null;
        _opened++;
      });

  /// The win banner on Requests: Your week, at the week the new rides start.
  void _openWeek(DateTime? start) => setState(() {
        _tab = DriverTab.today;
        _weekStart = start;
        _opened++;
      });

  @override
  Widget build(BuildContext context) {
    final Widget body = switch (_tab) {
      DriverTab.today => WeekScreen(
          key: ValueKey('today.$_opened'),
          start: _weekStart,
          onOpenRequests: () => _select(DriverTab.requests),
        ),
      DriverTab.requests => RequestsScreen(
          key: ValueKey('requests.$_opened'),
          profile: widget.profile,
          onOpenWeek: _openWeek,
        ),
      DriverTab.earnings => const _ComingSoon(key: ValueKey('earnings'), title: 'Earnings', flow: 'D4'),
      DriverTab.account => _ComingSoon(
          key: const ValueKey('account'),
          title: 'Account',
          flow: 'D5',
          line: widget.profile?.name,
        ),
    };
    return Scaffold(
      body: body,
      bottomNavigationBar: DriverTabBar(current: _tab, onSelect: _select),
    );
  }
}

class _ComingSoon extends StatelessWidget {
  const _ComingSoon({super.key, required this.title, required this.flow, this.line});

  final String title;
  final String flow;
  final String? line;

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(20, 24, 20, 20),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Semantics(header: true, child: Text(title, style: AppText.display(28))),
            if (line != null) ...[
              const SizedBox(height: 8),
              Text(line!, style: AppText.body(15)),
            ],
            const SizedBox(height: 12),
            Text('This comes with flow $flow.', style: AppText.body(14, color: AppColors.muted)),
          ],
        ),
      ),
    );
  }
}
