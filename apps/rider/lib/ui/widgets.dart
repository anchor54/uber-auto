import 'package:flutter/material.dart';

import 'icons.dart';
import 'tokens.dart';

/// Back button plus a quiet step label, as at the top of every flow screen.
class FlowHeader extends StatelessWidget {
  const FlowHeader({super.key, this.label, this.onBack});

  final String? label;
  final VoidCallback? onBack;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
      child: Row(
        children: [
          Semantics(
            button: true,
            label: 'Back',
            child: InkResponse(
              key: const Key('back'),
              onTap: onBack ?? () => Navigator.of(context).maybePop(),
              radius: 22,
              child: const SizedBox(
                width: 44,
                height: 44,
                child: Center(child: AppIcon(AppIcons.back)),
              ),
            ),
          ),
          const SizedBox(width: 8),
          if (label != null)
            Expanded(child: Text(label!, style: AppText.body(13, weight: FontWeight.w600, color: AppColors.muted))),
        ],
      ),
    );
  }
}

class ScreenTitle extends StatelessWidget {
  const ScreenTitle(this.title, {super.key, this.subtitle, this.padding = const EdgeInsets.fromLTRB(20, 0, 20, 16)});

  final String title;
  final String? subtitle;
  final EdgeInsets padding;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: padding,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Semantics(header: true, child: Text(title, style: AppText.display(28))),
          if (subtitle != null) ...[
            const SizedBox(height: 6),
            Text(subtitle!, style: AppText.body(14, color: AppColors.muted, height: 1.45)),
          ],
        ],
      ),
    );
  }
}

class CardBox extends StatelessWidget {
  const CardBox({
    super.key,
    required this.child,
    this.padding = const EdgeInsets.all(16),
    this.color = AppColors.surface,
    this.radius = 16,
    this.border,
  });

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

class PrimaryButton extends StatelessWidget {
  const PrimaryButton({
    super.key,
    required this.label,
    required this.onPressed,
    this.loading = false,
    this.height = 52,
    this.color = AppColors.green,
    this.textColor = Colors.white,
  });

  final String label;
  final VoidCallback? onPressed;
  final bool loading;
  final double height;
  final Color color;
  final Color textColor;

  @override
  Widget build(BuildContext context) {
    final enabled = onPressed != null && !loading;
    return Semantics(
      button: true,
      enabled: enabled,
      label: label,
      excludeSemantics: true,
      child: Material(
        color: enabled ? color : color.withValues(alpha: 0.45),
        borderRadius: BorderRadius.circular(14),
        child: InkWell(
          borderRadius: BorderRadius.circular(14),
          onTap: enabled ? onPressed : null,
          child: SizedBox(
            height: height,
            width: double.infinity,
            child: Center(
              child: loading
                  ? SizedBox(
                      width: 22,
                      height: 22,
                      child: CircularProgressIndicator(strokeWidth: 2.4, color: textColor),
                    )
                  : Text(label, style: AppText.body(16, weight: FontWeight.w600, color: textColor)),
            ),
          ),
        ),
      ),
    );
  }
}

class OutlineButtonBox extends StatelessWidget {
  const OutlineButtonBox({super.key, required this.label, required this.onPressed, this.height = 44, this.expand = true});

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
      child: Material(
        color: AppColors.surface,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(12),
          side: const BorderSide(color: AppColors.line),
        ),
        child: InkWell(
          borderRadius: BorderRadius.circular(12),
          onTap: onPressed,
          child: SizedBox(
            height: height,
            width: expand ? double.infinity : null,
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              child: Center(
                widthFactor: 1,
                child: Text(label, style: AppText.body(14, weight: FontWeight.w600)),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class LinkButton extends StatelessWidget {
  const LinkButton({super.key, required this.label, required this.onPressed, this.height = 44, this.color = AppColors.green, this.size = 14});

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
        onTap: onPressed,
        borderRadius: BorderRadius.circular(10),
        child: SizedBox(
          height: height,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 6),
            child: Center(
              widthFactor: 1,
              child: Text(label, style: AppText.body(size, weight: FontWeight.w600, color: color)),
            ),
          ),
        ),
      ),
    );
  }
}

/// Two-option pill switcher ("Every weekday" / "Chosen days each week").
class PillSegments<T> extends StatelessWidget {
  const PillSegments({super.key, required this.options, required this.value, required this.onChanged, required this.label});

  final List<(T, String)> options;
  final T value;
  final ValueChanged<T> onChanged;
  final String label;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      label: label,
      child: Container(
        padding: const EdgeInsets.all(3),
        decoration: BoxDecoration(color: AppColors.chip, borderRadius: BorderRadius.circular(10)),
        child: Row(
          children: [
            for (final (v, text) in options)
              Expanded(
                child: Semantics(
                  selected: v == value,
                  button: true,
                  label: text,
                  excludeSemantics: true,
                  child: GestureDetector(
                    key: ValueKey('segment.$text'),
                    behavior: HitTestBehavior.opaque,
                    onTap: () => onChanged(v),
                    child: AnimatedContainer(
                      duration: const Duration(milliseconds: 150),
                      height: 38,
                      decoration: BoxDecoration(
                        color: v == value ? AppColors.surface : Colors.transparent,
                        borderRadius: BorderRadius.circular(8),
                        boxShadow: v == value
                            ? const [BoxShadow(color: Color(0x1A12161A), blurRadius: 2, offset: Offset(0, 1))]
                            : null,
                      ),
                      alignment: Alignment.center,
                      child: Text(
                        text,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: AppText.body(
                          13,
                          weight: v == value ? FontWeight.w600 : FontWeight.w500,
                          color: v == value ? AppColors.ink : AppColors.muted,
                        ),
                      ),
                    ),
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }
}

class Avatar extends StatelessWidget {
  const Avatar(this.initials, {super.key, this.size = 52, this.color = AppColors.yellow});

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
      child: Text(initials, style: AppText.body(size * 0.33, weight: FontWeight.w700)),
    );
  }
}

class CheckLine extends StatelessWidget {
  const CheckLine(this.text, {super.key, this.color = AppColors.green, this.textColor = AppColors.ink});

  final String text;
  final Color color;
  final Color textColor;

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(padding: const EdgeInsets.only(top: 1), child: AppIcon(AppIcons.check, size: 18, color: color)),
        const SizedBox(width: 10),
        Expanded(child: Text(text, style: AppText.body(14, color: textColor, height: 1.4))),
      ],
    );
  }
}

/// "Label ........ value" row used in summaries.
class SummaryRow extends StatelessWidget {
  const SummaryRow(this.label, this.value, {super.key, this.valueKey});

  final String label;
  final String value;
  final Key? valueKey;

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(label, style: AppText.body(14, color: AppColors.muted)),
        const SizedBox(width: 16),
        Expanded(
          child: Text(
            value,
            key: valueKey,
            textAlign: TextAlign.right,
            style: AppText.body(14, weight: FontWeight.w600),
          ),
        ),
      ],
    );
  }
}

/// "Fare per ride ....... ₹235" with the price in the display face.
class PriceRow extends StatelessWidget {
  const PriceRow({super.key, required this.label, required this.price, this.priceKey, this.labelStyle});

  final String label;
  final String price;
  final Key? priceKey;
  final TextStyle? labelStyle;

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.baseline,
      textBaseline: TextBaseline.alphabetic,
      children: [
        Expanded(child: Text(label, style: labelStyle ?? AppText.body(14, color: AppColors.muted))),
        Text(price, key: priceKey, style: AppText.display(22, letterSpacing: 0)),
      ],
    );
  }
}

class Hairline extends StatelessWidget {
  const Hairline({super.key, this.color = AppColors.divider});
  final Color color;
  @override
  Widget build(BuildContext context) => Container(height: 1, color: color);
}

/// Small rounded label, e.g. "Driver confirmed".
class StatusChip extends StatelessWidget {
  const StatusChip(this.text, {super.key, this.background = AppColors.greenTint, this.foreground = AppColors.green});

  final String text;
  final Color background;
  final Color foreground;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
      decoration: BoxDecoration(color: background, borderRadius: BorderRadius.circular(999)),
      child: Text(text, style: AppText.body(12, weight: FontWeight.w600, color: foreground)),
    );
  }
}

void showInfo(BuildContext context, String message) {
  ScaffoldMessenger.of(context)
    ..hideCurrentSnackBar()
    ..showSnackBar(SnackBar(content: Text(message), duration: const Duration(seconds: 3)));
}

/// Bottom action area pinned below scrolling content.
class BottomActions extends StatelessWidget {
  const BottomActions({super.key, required this.children, this.padding = const EdgeInsets.fromLTRB(16, 12, 16, 16)});

  final List<Widget> children;
  final EdgeInsets padding;

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      top: false,
      child: Padding(
        padding: padding,
        child: Column(mainAxisSize: MainAxisSize.min, children: children),
      ),
    );
  }
}
