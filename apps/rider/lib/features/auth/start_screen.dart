import 'package:flutter/material.dart';

import '../../app_scope.dart';
import '../../core/models.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import '../home/home_screen.dart';
import 'welcome_screen.dart';

/// First screen: Home if a saved session is still valid, Welcome otherwise.
class StartScreen extends StatefulWidget {
  const StartScreen({super.key});

  @override
  State<StartScreen> createState() => _StartScreenState();
}

class _StartScreenState extends State<StartScreen> {
  Future<RiderProfile?>? _session;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _session ??= AppScope.of(context).api.restoreSession();
  }

  void _retry() => setState(() => _session = AppScope.of(context).api.restoreSession());

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<RiderProfile?>(
      future: _session,
      builder: (context, snap) {
        if (snap.connectionState != ConnectionState.done) {
          return const Scaffold(body: Center(child: CircularProgressIndicator(color: AppColors.green)));
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
        return snap.data == null ? const WelcomeScreen() : const HomeScreen();
      },
    );
  }
}
