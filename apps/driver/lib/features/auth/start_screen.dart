import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/models.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import '../onboarding/onboarding_common.dart';
import 'welcome_screen.dart';

/// First screen: with a saved session still valid, wherever the driver's
/// status says (sign-up, documents, verification, Requests); Welcome
/// otherwise.
class StartScreen extends StatefulWidget {
  const StartScreen({super.key});

  @override
  State<StartScreen> createState() => _StartScreenState();
}

class _StartScreenState extends State<StartScreen> {
  Future<Widget?>? _session;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _session ??= _home();
  }

  Future<Widget?> _home() async {
    final scope = AppScope.of(context);
    final profile = await scope.api.restoreSession();
    if (profile == null) return null;
    return driverHome(scope.api, scope.clock.now(), signedIn: profile);
  }

  void _retry() => setState(() => _session = _home());

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<Widget?>(
      future: _session,
      builder: (context, snap) {
        if (snap.connectionState != ConnectionState.done) {
          return const Scaffold(body: Center(child: CircularProgressIndicator(color: AppColors.yellow)));
        }
        if (snap.hasError) {
          final message = snap.error is ApiException ? (snap.error as ApiException).message : 'Something went wrong.';
          return Scaffold(
            body: SafeArea(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Column(
                  key: const Key('start.offline'),
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    Text(message, textAlign: TextAlign.center, style: AppText.body(15, color: AppColors.muted, height: 1.45)),
                    const SizedBox(height: 16),
                    PrimaryButton(key: const Key('start.retry'), label: 'Try again', onPressed: _retry),
                  ],
                ),
              ),
            ),
          );
        }
        return snap.data ?? const WelcomeScreen();
      },
    );
  }
}
