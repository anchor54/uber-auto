import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app_scope.dart';
import '../../core/api.dart';
import '../../core/image_source.dart';
import '../../core/models.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import '../shell/driver_shell.dart';
import 'documents_screen.dart';
import 'sign_up_screen.dart';
import 'verification_screen.dart';

/// How soon the team reviews documents and confirms payments ("[N] working
/// hours" in the spec).
const reviewHours = 24;

const supportLine = 'Call support on 080 4718 2000, 8 AM to 8 PM.';

/// Where a signed-in driver belongs, by status (driver.get):
/// no profile → D1.1, signing_up → D1.2, submitted / changes_needed /
/// approved / rejected → D1.5, live (or paused) → Requests, with the
/// "You're live" banner within a day of going live.
Future<Widget> driverHome(DriverApi api, DateTime now, {DriverProfile? signedIn}) async {
  if (signedIn != null && !signedIn.driverExists) return SignUpScreen(verifiedPhone: signedIn.phone);
  final Driver d;
  try {
    d = await api.me();
  } on ApiException catch (e) {
    if (e.code == 'not_registered') return SignUpScreen(verifiedPhone: signedIn?.phone);
    rethrow;
  }
  return screenForDriver(d, now);
}

Widget screenForDriver(Driver d, DateTime now) => switch (d.status) {
      DriverStatus.signingUp => const DocumentsScreen(),
      DriverStatus.live || DriverStatus.paused => DriverShell(profile: d.profile, showLiveBanner: d.wentLiveRecently(now)),
      _ => const VerificationScreen(),
    };

/// Replaces the whole stack with [screen].
void openAsRoot(BuildContext context, Widget screen) {
  Navigator.of(context).pushAndRemoveUntil(MaterialPageRoute<void>(builder: (_) => screen), (_) => false);
}

/// "₹1,199".
String rupees(int amount) {
  final s = amount.abs().toString();
  final b = StringBuffer();
  for (var i = 0; i < s.length; i++) {
    if (i > 0 && (s.length - i) % 3 == 0) b.write(',');
    b.write(s[i]);
  }
  return '${amount < 0 ? '-' : ''}₹$b';
}

/// Reads the driver again when a push arrives, when the app comes back to
/// the foreground and, with [AppScope.refreshEvery], on a timer while
/// [keepRefreshing] says so. Call [startRefreshing] once a context is ready.
mixin DriverRefresh<T extends StatefulWidget> on State<T> {
  StreamSubscription<String>? _pushes;
  Timer? _timer;
  _ResumeObserver? _observer;
  bool _started = false;

  /// Reads the screen's data again.
  void refresh();

  /// Whether the timer should still read (e.g. while waiting on the admin).
  bool get keepRefreshing => true;

  void startRefreshing() {
    if (_started) return;
    _started = true;
    final scope = AppScope.of(context);
    _pushes = scope.api.pushes.listen((_) {
      if (mounted) refresh();
    });
    _observer = _ResumeObserver(() {
      if (mounted) refresh();
    });
    WidgetsBinding.instance.addObserver(_observer!);
    final every = scope.refreshEvery;
    if (every != null) {
      _timer = Timer.periodic(every, (_) {
        if (mounted && keepRefreshing) refresh();
      });
    }
  }

  @override
  void dispose() {
    _pushes?.cancel();
    _timer?.cancel();
    final o = _observer;
    if (o != null) WidgetsBinding.instance.removeObserver(o);
    super.dispose();
  }
}

class _ResumeObserver with WidgetsBindingObserver {
  _ResumeObserver(this.onResume);
  final VoidCallback onResume;

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) onResume();
  }
}

/// Camera or gallery, then a preview with "Use this photo". Null when the
/// driver backs out at any point.
Future<PickedImage?> choosePhoto(BuildContext context) async {
  final photos = AppScope.of(context).photos;
  final origin = await showModalBottomSheet<ImageOrigin>(
    context: context,
    backgroundColor: AppColors.card,
    showDragHandle: true,
    builder: (sheet) => SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Add a photo', style: AppText.display(20)),
            const SizedBox(height: 12),
            _SheetOption(
              icon: Icons.photo_camera_outlined,
              label: 'Take a photo',
              onTap: () => Navigator.of(sheet).pop(ImageOrigin.camera),
            ),
            _SheetOption(
              icon: Icons.photo_library_outlined,
              label: 'Choose from gallery',
              onTap: () => Navigator.of(sheet).pop(ImageOrigin.gallery),
            ),
          ],
        ),
      ),
    ),
  );
  if (origin == null || !context.mounted) return null;
  PickedImage? image;
  try {
    image = await photos.pick(origin);
  } catch (_) {
    if (context.mounted) {
      showInfo(context, origin == ImageOrigin.camera ? "Couldn't open the camera." : "Couldn't open your photos.");
    }
    return null;
  }
  if (image == null || !context.mounted) return null;
  final picked = image;
  final use = await Navigator.of(context).push<bool>(
    MaterialPageRoute<bool>(fullscreenDialog: true, builder: (_) => PhotoPreviewScreen(image: picked)),
  );
  return use == true ? picked : null;
}

class _SheetOption extends StatelessWidget {
  const _SheetOption({required this.icon, required this.label, required this.onTap});

  final IconData icon;
  final String label;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      button: true,
      label: label,
      excludeSemantics: true,
      child: InkWell(
        key: labelKey(label),
        onTap: onTap,
        borderRadius: BorderRadius.circular(12),
        child: SizedBox(
          height: 52,
          child: Row(
            children: [
              Icon(icon, color: AppColors.yellow),
              const SizedBox(width: 14),
              Text(label, style: AppText.body(16, weight: FontWeight.w500)),
            ],
          ),
        ),
      ),
    );
  }
}

/// The picked photo before it goes up: "Use this photo" or pick again.
class PhotoPreviewScreen extends StatelessWidget {
  const PhotoPreviewScreen({super.key, required this.image});

  final PickedImage image;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const FlowHeader(label: 'Preview'),
            Expanded(
              child: Padding(
                padding: const EdgeInsets.all(20),
                child: ClipRRect(
                  borderRadius: BorderRadius.circular(16),
                  child: Container(
                    color: AppColors.card,
                    alignment: Alignment.center,
                    child: Image.memory(
                      image.bytes,
                      key: const Key('photo.preview'),
                      fit: BoxFit.contain,
                      gaplessPlayback: true,
                      errorBuilder: (context, error, stack) => const Icon(Icons.image_outlined, size: 64, color: AppColors.muted),
                    ),
                  ),
                ),
              ),
            ),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 20),
              child: Text('Check that every word is readable and nothing is cut off.',
                  style: AppText.body(14, color: AppColors.muted, height: 1.45)),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 12, 20, 16),
              child: Column(
                children: [
                  PrimaryButton(label: 'Use this photo', height: 52, onPressed: () => Navigator.of(context).pop(true)),
                  const SizedBox(height: 8),
                  OutlineButtonBox(label: 'Choose another', onPressed: () => Navigator.of(context).pop(false)),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// A compact action on a row: "Upload", "Add", "Fix".
class SmallButton extends StatelessWidget {
  const SmallButton({super.key, required this.label, required this.onPressed, this.color = AppColors.yellow});

  final String label;
  final VoidCallback? onPressed;
  final Color color;

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
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(10), side: BorderSide(color: color)),
          child: InkWell(
            borderRadius: BorderRadius.circular(10),
            onTap: onPressed,
            child: ConstrainedBox(
              constraints: const BoxConstraints(minHeight: 44, minWidth: 72),
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 14),
                child: Center(widthFactor: 1, child: Text(label, style: AppText.body(14, weight: FontWeight.w600, color: color))),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// A note with an icon, e.g. the lock note on D1.2.
class NoteBox extends StatelessWidget {
  const NoteBox({super.key, required this.text, this.icon = Icons.info_outline, this.color = AppColors.muted});

  final String text;
  final IconData icon;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(color: AppColors.cardRaised, borderRadius: BorderRadius.circular(14)),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(icon, size: 18, color: color),
          const SizedBox(width: 10),
          Expanded(child: Text(text, style: AppText.body(13, color: AppColors.muted, height: 1.45))),
        ],
      ),
    );
  }
}

/// A field with its label above (the label is also the field's key).
class LabelledField extends StatelessWidget {
  const LabelledField({
    super.key,
    required this.label,
    required this.controller,
    this.hint = '',
    this.keyboardType,
    this.inputFormatters,
    this.enabled = true,
    this.error,
    this.prefixText,
    this.textCapitalization = TextCapitalization.none,
    this.onSubmitted,
  });

  final String label;
  final TextEditingController controller;
  final String hint;
  final TextInputType? keyboardType;
  final List<TextInputFormatter>? inputFormatters;
  final bool enabled;
  final String? error;
  final String? prefixText;
  final TextCapitalization textCapitalization;
  final ValueChanged<String>? onSubmitted;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(label, style: AppText.body(13, weight: FontWeight.w600, color: AppColors.muted)),
        const SizedBox(height: 6),
        TextField(
          key: labelKey(label),
          controller: controller,
          enabled: enabled,
          keyboardType: keyboardType,
          inputFormatters: inputFormatters,
          textCapitalization: textCapitalization,
          onSubmitted: onSubmitted,
          style: AppText.body(16, weight: FontWeight.w500, color: enabled ? AppColors.ink : AppColors.muted),
          decoration: driverInputDecoration(hint, error: error != null).copyWith(
            prefixText: prefixText,
            disabledBorder: OutlineInputBorder(
              borderRadius: BorderRadius.circular(12),
              borderSide: const BorderSide(color: AppColors.divider),
            ),
          ),
        ),
        if (error != null) ...[
          const SizedBox(height: 6),
          Text(error!, style: AppText.body(13, color: AppColors.orange, height: 1.4)),
        ],
      ],
    );
  }
}

/// Full-screen spinner or error while a screen's first read is in flight.
class LoadingBody extends StatelessWidget {
  const LoadingBody({super.key, this.error, required this.onRetry});

  final Object? error;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    final e = error;
    if (e == null) return const Center(child: CircularProgressIndicator(color: AppColors.yellow));
    return Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Text(e is ApiException ? e.message : 'Something went wrong.',
              textAlign: TextAlign.center, style: AppText.body(15, color: AppColors.muted, height: 1.45)),
          const SizedBox(height: 16),
          PrimaryButton(label: 'Try again', onPressed: onRetry),
        ],
      ),
    );
  }
}

/// The right-hand state of an item row: "Uploaded", "Verified", …
class ItemStateLabel extends StatelessWidget {
  const ItemStateLabel(this.text, {super.key, this.color = AppColors.muted, this.icon});

  final String text;
  final Color color;
  final IconData? icon;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        if (icon != null) ...[Icon(icon, size: 16, color: color), const SizedBox(width: 4)],
        Text(text, style: AppText.body(13, weight: FontWeight.w600, color: color)),
      ],
    );
  }
}
