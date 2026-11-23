import 'package:flutter/material.dart';

import 'tokens.dart';

enum DriverTab { today, requests, earnings, account }

/// The driver app's bottom bar: Today, Requests, Earnings, Account.
class DriverTabBar extends StatelessWidget {
  const DriverTabBar({super.key, required this.current, required this.onSelect});

  final DriverTab current;
  final ValueChanged<DriverTab> onSelect;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: const BoxDecoration(
        color: AppColors.card,
        border: Border(top: BorderSide(color: AppColors.divider)),
      ),
      child: SafeArea(
        top: false,
        child: Padding(
          padding: const EdgeInsets.all(8),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.spaceAround,
            children: [
              _item(DriverTab.today, 'Today', Icons.today_outlined),
              _item(DriverTab.requests, 'Requests', Icons.inbox_outlined),
              _item(DriverTab.earnings, 'Earnings', Icons.currency_rupee),
              _item(DriverTab.account, 'Account', Icons.person_outline),
            ],
          ),
        ),
      ),
    );
  }

  Widget _item(DriverTab tab, String label, IconData icon) {
    final active = tab == current;
    final color = active ? AppColors.yellow : AppColors.muted;
    return Semantics(
      button: true,
      selected: active,
      label: label,
      excludeSemantics: true,
      child: InkResponse(
        key: Key('tab.${tab.name}'),
        onTap: () => onSelect(tab),
        child: SizedBox(
          width: 76,
          height: 48,
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Icon(icon, color: color, size: 22),
              const SizedBox(height: 3),
              Text(label, style: AppText.body(11, weight: active ? FontWeight.w600 : FontWeight.w500, color: color)),
            ],
          ),
        ),
      ),
    );
  }
}
