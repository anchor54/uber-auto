import 'package:flutter/material.dart';

import 'icons.dart';
import 'tokens.dart';

enum RiderTab { home, schedule, account }

class RiderTabBar extends StatelessWidget {
  const RiderTabBar({super.key, required this.current, required this.onSelect});

  final RiderTab current;
  final ValueChanged<RiderTab> onSelect;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: const BoxDecoration(
        color: AppColors.surface,
        border: Border(top: BorderSide(color: AppColors.cardBorder)),
      ),
      child: SafeArea(
        top: false,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(8, 8, 8, 8),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.spaceAround,
            children: [
              _item(RiderTab.home, 'Home', AppIcons.home),
              _item(RiderTab.schedule, 'Schedule', AppIcons.calendar),
              _item(RiderTab.account, 'Account', AppIcons.user),
            ],
          ),
        ),
      ),
    );
  }

  Widget _item(RiderTab tab, String label, AppIconData icon) {
    final active = tab == current;
    final color = active ? AppColors.green : AppColors.muted;
    return Semantics(
      button: true,
      selected: active,
      label: label,
      excludeSemantics: true,
      child: InkResponse(
        key: Key('tab.${tab.name}'),
        onTap: () => onSelect(tab),
        child: SizedBox(
          width: 72,
          height: 48,
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              AppIcon(icon, color: color),
              const SizedBox(height: 4),
              Text(label, style: AppText.body(11, weight: active ? FontWeight.w600 : FontWeight.w500, color: color)),
            ],
          ),
        ),
      ),
    );
  }
}
