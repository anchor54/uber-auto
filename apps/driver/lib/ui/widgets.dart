import 'package:flutter/material.dart';

import 'tokens.dart';

/// The key every button also carries, from its label in snake_case without
/// articles: "I'm interested" -> `im_interested`. Tests find buttons by it.
Key labelKey(String label) {
  final words = label
      .toLowerCase()
      .replaceAll(RegExp(r"['’]"), '')
      .split(RegExp(r'[^a-z0-9]+'))
      .where((w) => w.isNotEmpty && w != 'a' && w != 'an' && w != 'the');
  return Key(words.join('_'));
}

/// Back button plus a quiet label, as at the top of every pushed screen.
class FlowHeader extends StatelessWidget {
  const FlowHeader({super.key, this.label, this.onBack, this.trailing});

  final String? label;
  final VoidCallback? onBack;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 8, 16, 4),
      child: Row(
        children: [
          Semantics(
            button: true,
            label: 'Back',
            child: InkResponse(
              key: const Key('back'),
              onTap: onBack ?? () => Navigator.of(context).maybePop(),
              radius: 22,
              child: const SizedBox(width: 44, height: 44, child: Icon(Icons.arrow_back, color: AppColors.ink)),
            ),
          ),
          const SizedBox(width: 4),
          if (label != null) Expanded(child: Text(label!, style: AppText.body(14, weight: FontWeight.w600, color: AppColors.muted))),
          if (label == null) const Spacer(),
          if (trailing != null) trailing!,
        ],
      ),
    );
  }
}

class CardBox extends StatelessWidget {
  const CardBox({super.key, required this.child, this.padding = const EdgeInsets.all(16), this.color = AppColors.card, this.radius = 16, this.border});

  final Widget child;
  final EdgeInsetsGeometry padding;
  final Color color;
  final double radius;
  final BoxBorder? border;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: padding,
      decoration: BoxDecoration(color: color, borderRadius: BorderRadius.circular(radius), border: border),
      child: child,
    );
  }
}

/// Full-width action button: yellow, 54 pt tall at the bottom of a screen.
class PrimaryButton extends StatelessWidget {
  const PrimaryButton({
    super.key,
    required this.label,
    required this.onPressed,
    this.loading = false,
    this.height = 54,
    this.color = AppColors.yellow,
    this.textColor = AppColors.onYellow,
    this.icon,
  });

  final String label;
  final VoidCallback? onPressed;
  final bool loading;
  final double height;
  final Color color;
  final Color textColor;
  final IconData? icon;

  @override
  Widget build(BuildContext context) {
    final enabled = onPressed != null && !loading;
    return Semantics(
      button: true,
      enabled: enabled,
      label: label,
      excludeSemantics: true,
      child: KeyedSubtree(
        key: labelKey(label),
        child: Material(
          color: enabled ? color : color.withValues(alpha: 0.35),
          borderRadius: BorderRadius.circular(14),
          child: InkWell(
            borderRadius: BorderRadius.circular(14),
            onTap: enabled ? onPressed : null,
            child: SizedBox(
              height: height,
              width: double.infinity,
              child: Center(
                child: loading
                    ? SizedBox(width: 22, height: 22, child: CircularProgressIndicator(strokeWidth: 2.4, color: textColor))
                    : Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          if (icon != null) ...[Icon(icon, size: 18, color: textColor), const SizedBox(width: 6)],
                          Text(label, style: AppText.body(16, weight: FontWeight.w600, color: textColor)),
                        ],
                      ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class OutlineButtonBox extends StatelessWidget {
  const OutlineButtonBox({super.key, required this.label, required this.onPressed, this.height = 48, this.expand = true});

  final String label;
  final VoidCallback? onPressed;
  final double height;
  final bool expand;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      button: true,
      label: label,
      excludeSemantics: true,
      child: KeyedSubtree(
        key: labelKey(label),
        child: Material(
          color: Colors.transparent,
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12), side: const BorderSide(color: AppColors.line)),
          child: InkWell(
            borderRadius: BorderRadius.circular(12),
            onTap: onPressed,
            child: SizedBox(
              height: height,
              width: expand ? double.infinity : null,
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 16),
                child: Center(widthFactor: 1, child: Text(label, style: AppText.body(15, weight: FontWeight.w600))),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class LinkButton extends StatelessWidget {
  const LinkButton({super.key, required this.label, required this.onPressed, this.height = 44, this.color = AppColors.yellow, this.size = 14});

  final String label;
  final VoidCallback? onPressed;
  final double height;
  final Color color;
  final double size;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      button: true,
      label: label,
      excludeSemantics: true,
      child: InkWell(
        key: labelKey(label),
        onTap: onPressed,
        borderRadius: BorderRadius.circular(10),
        child: SizedBox(
          height: height,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 6),
            child: Center(widthFactor: 1, child: Text(label, style: AppText.body(size, weight: FontWeight.w600, color: color))),
          ),
        ),
      ),
    );
  }
}

class Avatar extends StatelessWidget {
  const Avatar(this.initials, {super.key, this.size = 44, this.color = AppColors.yellow});

  final String initials;
  final double size;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(color: color, shape: BoxShape.circle),
      alignment: Alignment.center,
      child: Text(initials, style: AppText.body(size * 0.34, weight: FontWeight.w700, color: AppColors.onYellow)),
    );
  }
}

/// Small rounded label, e.g. the countdown "Closes in 12 min".
class Chip2 extends StatelessWidget {
  const Chip2(this.text, {super.key, this.background = AppColors.cardRaised, this.foreground = AppColors.yellow});

  final String text;
  final Color background;
  final Color foreground;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
      decoration: BoxDecoration(color: background, borderRadius: BorderRadius.circular(999)),
      child: Text(text, style: AppText.body(12, weight: FontWeight.w600, color: foreground)),
    );
  }
}

/// "Label ........ value" row.
class SummaryRow extends StatelessWidget {
  const SummaryRow(this.label, this.value, {super.key, this.valueStyle});

  final String label;
  final String value;
  final TextStyle? valueStyle;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.baseline,
        textBaseline: TextBaseline.alphabetic,
        children: [
          Expanded(child: Text(label, style: AppText.body(14, color: AppColors.muted))),
          const SizedBox(width: 16),
          Text(value, textAlign: TextAlign.right, style: valueStyle ?? AppText.body(14, weight: FontWeight.w600)),
        ],
      ),
    );
  }
}

class Hairline extends StatelessWidget {
  const Hairline({super.key});
  @override
  Widget build(BuildContext context) => Container(height: 1, color: AppColors.divider);
}

void showInfo(BuildContext context, String message) {
  ScaffoldMessenger.of(context)
    ..hideCurrentSnackBar()
    ..showSnackBar(SnackBar(content: Text(message), duration: const Duration(seconds: 3)));
}

/// "Closes in 12 min" from [seconds] left (rounded up).
String closesIn(int seconds) => 'Closes in ${(seconds / 60).ceil()} min';

/// "4 min left" from [seconds] left (rounded up).
String minutesLeft(int seconds) => '${(seconds / 60).ceil()} min left';

InputDecoration driverInputDecoration(String hint, {bool error = false}) => InputDecoration(
      hintText: hint,
      hintStyle: AppText.body(16, color: AppColors.muted),
      filled: true,
      fillColor: AppColors.card,
      contentPadding: const EdgeInsets.symmetric(horizontal: 14, vertical: 14),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(12),
        borderSide: BorderSide(color: error ? AppColors.red : AppColors.line),
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(12),
        borderSide: BorderSide(color: error ? AppColors.red : AppColors.yellow, width: 1.5),
      ),
    );
